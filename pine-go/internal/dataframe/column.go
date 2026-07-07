package dataframe

// Typed column storage for ColumnFrame, mirroring pine-cpp's Column
// hierarchy (pine-cpp/include/pine/column.hpp): fixed-width typed columns
// with an internal validity bitmap, plus a heterogeneous JSON fallback.
//
// A position can be ABSENT (validity false — the row never wrote the
// field). jsonColumn additionally allows PRESENT-NULL (validity true,
// value nil). Typed columns cannot represent present-null and must be
// promoted to jsonColumn first.
//
// Divergence from pine-cpp, on purpose: pine-cpp picks DoubleColumn for
// any numeric first value because its Variant stores all numbers as
// double natively. Go's `any` preserves the concrete numeric type
// (int vs float64), and downstream contracts observe it (merge_dedup's
// type-prefixed keys, %T-based error messages), so typed dispatch here
// is by EXACT runtime type: float64 → float64Column, everything else
// numeric falls to jsonColumn. JSON-sourced data (requests, recall
// items) is always float64 in Go, so the common case still gets the
// typed path.

type columnKind uint8

const (
	kindJSON columnKind = iota
	kindFloat64
	kindString
	kindBool
)

// column is the type-erased storage for a single field's item values.
// All implementations keep data and validity the same length (rowCount).
type column interface {
	kind() columnKind
	len() int

	// isNull is the user-facing "value at i is nil" predicate: true when
	// the slot is absent, or (jsonColumn only) present with a nil value.
	isNull(i int) bool
	// present reflects the raw validity bit.
	present(i int) bool
	// get returns the boxed value at i, or nil when isNull(i).
	get(i int) any

	// set writes v at i marking it present. Returns false when v's type
	// cannot be stored (type mismatch, or nil into a typed column) — the
	// caller promotes to jsonColumn and retries.
	set(i int, v any) bool
	// appendVal appends v as a present slot; false → promote first.
	appendVal(v any) bool
	// appendAbsent appends one absent slot.
	appendAbsent()
	// grow ensures capacity for newCap rows (len unchanged).
	grow(newCap int)

	// removeByBitmap compacts in place: drop rows where bitmap[i] is
	// true; kept is the resulting length (computed once by the caller
	// and shared across all columns).
	removeByBitmap(bitmap []bool, kept int)
	// reorder applies a validated permutation in place via cycle
	// following. visited is caller-owned scratch shared across columns;
	// the column resets it before use.
	reorder(order []int, visited []bool)

	// view returns the [offset, offset+count) window as boxed values
	// with nil in null slots — element i must equal get(offset + i).
	// jsonColumn returns a zero-copy subslice; typed columns box a copy.
	view(offset, count int) []any

	// toJSON materializes an equivalent jsonColumn (promotion path).
	toJSON() *jsonColumn
}

// --- jsonColumn: heterogeneous fallback ---

// jsonColumn stores boxed values. Invariant: data[i] == nil whenever
// !validity[i], so view() can hand out the backing slice zero-copy with
// Item()-identical nil semantics.
type jsonColumn struct {
	data     []any
	validity []bool
}

func newJSONColumn(n int) *jsonColumn {
	return &jsonColumn{data: make([]any, n), validity: make([]bool, n)}
}

func (c *jsonColumn) kind() columnKind { return kindJSON }
func (c *jsonColumn) len() int         { return len(c.data) }

func (c *jsonColumn) isNull(i int) bool  { return !c.validity[i] || c.data[i] == nil }
func (c *jsonColumn) present(i int) bool { return c.validity[i] }
func (c *jsonColumn) get(i int) any      { return c.data[i] }

func (c *jsonColumn) set(i int, v any) bool {
	c.data[i] = v
	c.validity[i] = true
	return true
}

func (c *jsonColumn) appendVal(v any) bool {
	c.data = append(c.data, v)
	c.validity = append(c.validity, true)
	return true
}

func (c *jsonColumn) appendAbsent() {
	c.data = append(c.data, nil)
	c.validity = append(c.validity, false)
}

func (c *jsonColumn) grow(newCap int) {
	if cap(c.data) < newCap {
		data := make([]any, len(c.data), newCap)
		copy(data, c.data)
		c.data = data
		validity := make([]bool, len(c.validity), newCap)
		copy(validity, c.validity)
		c.validity = validity
	}
}

func (c *jsonColumn) removeByBitmap(bitmap []bool, kept int) {
	c.data = compactAny(c.data, bitmap, kept)
	c.validity = compactBool(c.validity, bitmap, kept)
}

func (c *jsonColumn) reorder(order []int, visited []bool) {
	reorderInPlace(c.data, c.validity, order, visited)
}

func (c *jsonColumn) view(offset, count int) []any {
	return c.data[offset : offset+count : offset+count]
}

func (c *jsonColumn) toJSON() *jsonColumn { return c }

// --- typedColumn[T]: fixed-width storage + validity bitmap ---

type typedColumn[T comparable] struct {
	data     []T
	validity []bool
	k        columnKind
	// box turns a stored T back into the boxed value; accept reports
	// whether a boxed value belongs in this column and converts it.
	accept func(v any) (T, bool)
}

func newFloat64Column(n int) *typedColumn[float64] {
	return &typedColumn[float64]{
		data: make([]float64, n), validity: make([]bool, n), k: kindFloat64,
		accept: func(v any) (float64, bool) { f, ok := v.(float64); return f, ok },
	}
}

func newStringColumn(n int) *typedColumn[string] {
	return &typedColumn[string]{
		data: make([]string, n), validity: make([]bool, n), k: kindString,
		accept: func(v any) (string, bool) { s, ok := v.(string); return s, ok },
	}
}

func newBoolColumn(n int) *typedColumn[bool] {
	return &typedColumn[bool]{
		data: make([]bool, n), validity: make([]bool, n), k: kindBool,
		accept: func(v any) (bool, bool) { b, ok := v.(bool); return b, ok },
	}
}

func (c *typedColumn[T]) kind() columnKind { return c.k }
func (c *typedColumn[T]) len() int         { return len(c.data) }

func (c *typedColumn[T]) isNull(i int) bool  { return !c.validity[i] }
func (c *typedColumn[T]) present(i int) bool { return c.validity[i] }

func (c *typedColumn[T]) get(i int) any {
	if !c.validity[i] {
		return nil
	}
	return c.data[i]
}

func (c *typedColumn[T]) set(i int, v any) bool {
	t, ok := c.accept(v)
	if !ok {
		return false // nil or type mismatch → caller promotes
	}
	c.data[i] = t
	c.validity[i] = true
	return true
}

func (c *typedColumn[T]) appendVal(v any) bool {
	t, ok := c.accept(v)
	if !ok {
		return false
	}
	c.data = append(c.data, t)
	c.validity = append(c.validity, true)
	return true
}

func (c *typedColumn[T]) appendAbsent() {
	var zero T
	c.data = append(c.data, zero)
	c.validity = append(c.validity, false)
}

func (c *typedColumn[T]) grow(newCap int) {
	if cap(c.data) < newCap {
		data := make([]T, len(c.data), newCap)
		copy(data, c.data)
		c.data = data
		validity := make([]bool, len(c.validity), newCap)
		copy(validity, c.validity)
		c.validity = validity
	}
}

func (c *typedColumn[T]) removeByBitmap(bitmap []bool, kept int) {
	c.data = compactTyped(c.data, bitmap, kept)
	c.validity = compactBool(c.validity, bitmap, kept)
}

func (c *typedColumn[T]) reorder(order []int, visited []bool) {
	reorderInPlace(c.data, c.validity, order, visited)
}

func (c *typedColumn[T]) view(offset, count int) []any {
	out := make([]any, count)
	for i := 0; i < count; i++ {
		if c.validity[offset+i] {
			out[i] = c.data[offset+i]
		}
	}
	return out
}

func (c *typedColumn[T]) toJSON() *jsonColumn {
	j := &jsonColumn{data: make([]any, len(c.data)), validity: make([]bool, len(c.validity))}
	for i := range c.data {
		if c.validity[i] {
			j.data[i] = c.data[i]
			j.validity[i] = true
		}
	}
	return j
}

// float64Window returns the [offset, offset+count) window of a
// float64Column's raw data zero-copy, ok only when every slot in the
// window is present (no nil anywhere — so item_defaults can never fire
// and element i is exactly what Item(offset+i) would box).
func float64Window(c column, offset, count int) ([]float64, bool) {
	fc, ok := c.(*typedColumn[float64])
	if !ok {
		return nil, false
	}
	for i := offset; i < offset+count; i++ {
		if !fc.validity[i] {
			return nil, false
		}
	}
	return fc.data[offset : offset+count : offset+count], true
}

// --- construction-time type inference (mirrors pine-cpp make_column) ---

// makeColumn scans the field's values across all items and returns the
// best-fitting column. Present-null (explicit nil value) disqualifies
// typed storage. Fields absent everywhere get a jsonColumn (type cannot
// be inferred).
func makeColumn(items []map[string]any, field string) column {
	kind := columnKind(255) // unknown
	for _, item := range items {
		v, exists := item[field]
		if !exists {
			continue
		}
		var k columnKind
		switch v.(type) {
		case float64:
			k = kindFloat64
		case string:
			k = kindString
		case bool:
			k = kindBool
		default: // nil (present-null), ints, composites, everything else
			k = kindJSON
		}
		if kind == 255 {
			kind = k
		} else if kind != k {
			kind = kindJSON
		}
		if kind == kindJSON {
			break
		}
	}

	n := len(items)
	var col column
	switch kind {
	case kindFloat64:
		col = newFloat64Column(0)
	case kindString:
		col = newStringColumn(0)
	case kindBool:
		col = newBoolColumn(0)
	default:
		col = newJSONColumn(0)
	}
	col.grow(n)
	for _, item := range items {
		if v, exists := item[field]; exists {
			// Cannot fail: the probe above guarantees acceptance.
			col.appendVal(v)
		} else {
			col.appendAbsent()
		}
	}
	return col
}

// newColumnForValue picks a fresh column for a first-seen field written
// at runtime, sized to n absent rows. Exact-type dispatch (see the
// header comment for why ints do not get a typed column in Go).
func newColumnForValue(v any, n int) column {
	switch v.(type) {
	case float64:
		return newFloat64Column(n)
	case string:
		return newStringColumn(n)
	case bool:
		return newBoolColumn(n)
	default:
		return newJSONColumn(n)
	}
}

// --- shared compaction / permutation helpers ---

func compactAny(data []any, bitmap []bool, kept int) []any {
	write := 0
	for i, v := range data {
		if !bitmap[i] {
			data[write] = v
			write++
		}
	}
	// Clear the tail so dropped boxed values stop pinning their referents.
	for i := kept; i < len(data); i++ {
		data[i] = nil
	}
	return data[:kept]
}

func compactTyped[T any](data []T, bitmap []bool, kept int) []T {
	write := 0
	for i := range data {
		if !bitmap[i] {
			data[write] = data[i]
			write++
		}
	}
	return data[:kept]
}

func compactBool(data []bool, bitmap []bool, kept int) []bool {
	write := 0
	for i, v := range data {
		if !bitmap[i] {
			data[write] = v
			write++
		}
	}
	return data[:kept]
}

// reorderInPlace applies a validated permutation to data+validity via
// cycle following: ≤ n moves, zero allocation (visited is caller scratch,
// reset here — the cycle structure depends only on order, so the frame
// shares one buffer across all columns).
func reorderInPlace[T any](data []T, validity []bool, order []int, visited []bool) {
	n := len(order)
	for i := 0; i < n; i++ {
		visited[i] = false
	}
	for i := 0; i < n; i++ {
		if visited[i] {
			continue
		}
		if order[i] == i {
			visited[i] = true
			continue
		}
		tmpVal := data[i]
		tmpPres := validity[i]
		j := i
		for {
			src := order[j]
			if src == i {
				data[j] = tmpVal
				validity[j] = tmpPres
				visited[j] = true
				break
			}
			data[j] = data[src]
			validity[j] = validity[src]
			visited[j] = true
			j = src
		}
	}
}
