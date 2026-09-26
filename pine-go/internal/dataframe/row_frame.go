package dataframe

import (
	"fmt"
	"math"
	"reflect"
	"sync"
	"unsafe"

	"github.com/Liam0205/pineapple/pine-go/internal/config"
	"github.com/Liam0205/pineapple/pine-go/internal/types"
)

// RowFrame is a request-local row-store DataFrame.
// It is concurrency-safe: concurrent reads (BuildInput, Common, Item) are allowed,
// while mutations (ApplyOutput, SetCommon) are exclusive.
type RowFrame struct {
	mu     sync.RWMutex
	common map[string]any
	items  []map[string]any
}

func newRowFrame(common map[string]any, items []map[string]any) *RowFrame {
	c := make(map[string]any, len(common))
	for k, v := range common {
		c[k] = v
	}
	its := make([]map[string]any, len(items))
	for i, item := range items {
		row := make(map[string]any, len(item))
		for k, v := range item {
			row[k] = v
		}
		its[i] = row
	}
	return &RowFrame{common: c, items: its}
}

func (f *RowFrame) Common(field string) any {
	f.mu.RLock()
	v := f.common[field]
	f.mu.RUnlock()
	return v
}

func (f *RowFrame) SetCommon(field string, value any) {
	f.mu.Lock()
	f.common[field] = value
	f.mu.Unlock()
}

func (f *RowFrame) ItemCount() int {
	f.mu.RLock()
	n := len(f.items)
	f.mu.RUnlock()
	return n
}

func (f *RowFrame) Item(index int, field string) any {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if index < 0 || index >= len(f.items) {
		return nil
	}
	return f.items[index][field]
}

// ItemColumnView implements types.ColumnReader by gathering the field from
// each row map under a single lock acquisition. Unlike ColumnFrame this
// cannot be zero-copy (values live scattered in per-item maps), but it
// still collapses the per-element lock tax of an Item() loop to one lock.
func (f *RowFrame) ItemColumnView(field string, offset, count int) ([]any, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if offset < 0 || count < 0 || offset+count > len(f.items) {
		return nil, false
	}
	col := make([]any, count)
	for i := 0; i < count; i++ {
		col[i] = f.items[offset+i][field]
	}
	return col, true
}

func (f *RowFrame) BuildInput(
	opName string,
	spec *config.InputFieldSpec,
) (*types.OperatorInput, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	// Eagerly build common (few fields, cheap)
	totalCommon := len(spec.StrictCommon) + len(spec.DefaultedCommon) + len(spec.NullableCommon)
	cs := make(map[string]any, totalCommon)

	for _, field := range spec.StrictCommon {
		v, exists := f.common[field]
		if !exists || v == nil {
			return nil, fmt.Errorf("required field %q is nil in common", field)
		}
		cs[field] = v
	}
	for _, df := range spec.DefaultedCommon {
		v, exists := f.common[df.Name]
		if !exists || v == nil {
			cs[df.Name] = df.Default
		} else {
			cs[df.Name] = v
		}
	}
	for _, field := range spec.NullableCommon {
		v, exists := f.common[field]
		if !exists {
			return nil, fmt.Errorf("required field %q is missing in common", field)
		}
		cs[field] = v
	}

	// Validate strict item fields upfront (fail fast)
	for i, item := range f.items {
		for _, field := range spec.StrictItem {
			v, exists := item[field]
			if !exists || v == nil {
				return nil, fmt.Errorf("required field %q is nil on item[%d]", field, i)
			}
		}
		for _, field := range spec.NullableItem {
			_, exists := item[field]
			if !exists {
				return nil, fmt.Errorf("required field %q is missing on item[%d]", field, i)
			}
		}
	}

	// Build item defaults map and field list for lazy access
	var itemDefaults map[string]any
	if len(spec.DefaultedItem) > 0 {
		itemDefaults = make(map[string]any, len(spec.DefaultedItem))
		for _, df := range spec.DefaultedItem {
			itemDefaults[df.Name] = df.Default
		}
	}

	totalItem := len(spec.StrictItem) + len(spec.DefaultedItem) + len(spec.NullableItem)
	itemFields := make([]string, 0, totalItem)
	itemFields = append(itemFields, spec.StrictItem...)
	for _, df := range spec.DefaultedItem {
		itemFields = append(itemFields, df.Name)
	}
	itemFields = append(itemFields, spec.NullableItem...)

	return types.NewLazyOperatorInput(cs, f, itemDefaults, itemFields, 0, len(f.items)), nil
}

func (f *RowFrame) ApplyOutput(out *types.OperatorOutput, opName string, recall bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	// 1. Common writes
	for field, value := range out.GetCommonWrites() {
		if err := validateValue(field, value); err != nil {
			return fmt.Errorf("common write: %w", err)
		}
		f.common[field] = value
	}

	// 2. Item field writes
	for _, w := range out.GetItemWrites() {
		if w.Index < 0 || w.Index >= len(f.items) {
			return fmt.Errorf("SetItem index %d out of range [0, %d)", w.Index, len(f.items))
		}
		if err := validateValue(w.Field, w.Value); err != nil {
			return fmt.Errorf("item[%d] write: %w", w.Index, err)
		}
		f.items[w.Index][w.Field] = w.Value
	}

	// 2b. Whole-column typed writes: scatter into the per-item maps in
	// one lock window. Applied after per-element writes so a column
	// write to the same field deterministically wins. Boxing here is
	// unavoidable (values live in maps); the column-store frame gets the
	// zero-copy adopt path instead.
	for _, cw := range out.GetColumnWrites() {
		if len(cw.Vals) != len(f.items) {
			return fmt.Errorf("SetItemColumnFloat64 %q length %d does not match item count %d",
				cw.Field, len(cw.Vals), len(f.items))
		}
		// Inline the float64 NaN/Inf check instead of calling
		// validateValue(field, any(v)) — the any conversion boxes every
		// element (one heap alloc each), which would hand back a third of
		// the batch-write win. Same first-error message as validateValue.
		for i, v := range cw.Vals {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return fmt.Errorf("item[%d] write: field %q: NaN/Inf is not a valid JSON value", i, cw.Field)
			}
		}
		for i, v := range cw.Vals {
			f.items[i][cw.Field] = v
		}
	}

	// 3. Removals
	removed := out.GetRemovedItems()
	if len(removed) > 0 {
		for idx := range removed {
			if idx < 0 || idx >= len(f.items) {
				return fmt.Errorf("RemoveItem index %d out of range [0, %d)", idx, len(f.items))
			}
		}
		bitmap := make([]bool, len(f.items))
		for idx := range removed {
			bitmap[idx] = true
		}
		surviving := make([]map[string]any, 0, len(f.items)-len(removed))
		for i, item := range f.items {
			if !bitmap[i] {
				surviving = append(surviving, item)
			}
		}
		f.items = surviving
	}

	// 4. Reorder
	if order := out.GetItemOrder(); order != nil {
		if len(order) != len(f.items) {
			return fmt.Errorf("SetItemOrder length %d does not match item count %d", len(order), len(f.items))
		}
		// Permutation check — without this, set_item_order([0,0,0]) would
		// silently duplicate item 0 across the frame.
		seen := make([]bool, len(f.items))
		reordered := make([]map[string]any, len(order))
		for newIdx, origIdx := range order {
			if origIdx < 0 || origIdx >= len(f.items) {
				return fmt.Errorf("SetItemOrder index %d out of range [0, %d)", origIdx, len(f.items))
			}
			if seen[origIdx] {
				return fmt.Errorf("SetItemOrder duplicate index %d (order must be a permutation)", origIdx)
			}
			seen[origIdx] = true
			reordered[newIdx] = f.items[origIdx]
		}
		f.items = reordered
	}

	// 5. Additions (zero-copy: take ownership of the caller's map)
	if addedItems := out.GetAddedItems(); len(addedItems) > 0 {
		if cap(f.items)-len(f.items) < len(addedItems) {
			grown := make([]map[string]any, len(f.items), len(f.items)+len(addedItems))
			copy(grown, f.items)
			f.items = grown
		}
		for _, added := range addedItems {
			for k, v := range added {
				if err := validateValue(k, v); err != nil {
					return fmt.Errorf("added item write: %w", err)
				}
			}
			if recall {
				added["_source"] = opName
			}
			f.items = append(f.items, added)
		}
	}

	return nil
}

func (f *RowFrame) ToResult(commonOut, itemOut []string) *types.Result {
	f.mu.RLock()
	defer f.mu.RUnlock()

	common := projectMap(f.common, commonOut)
	items := make([]map[string]any, len(f.items))
	for i, item := range f.items {
		items[i] = projectMap(item, itemOut)
	}
	return &types.Result{Common: common, Items: items}
}

func projectMap(src map[string]any, fields []string) map[string]any {
	out := make(map[string]any, len(fields))
	for _, k := range fields {
		if v, ok := src[k]; ok {
			out[k] = v
		}
	}
	return out
}

func validateValue(field string, value any) error {
	if value == nil {
		return nil
	}
	switch v := value.(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("field %q: NaN/Inf is not a valid JSON value", field)
		}
		return nil
	case float32:
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return fmt.Errorf("field %q: NaN/Inf is not a valid JSON value", field)
		}
		return nil
	case bool, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		string:
		return nil
	case []any, map[string]any:
		// Fast path for the shapes Lua and JSON produce; the reflect
		// branch below covers other slice/map types.
		if containsNonFinite(value) {
			return fmt.Errorf("field %q: NaN/Inf is not a valid JSON value", field)
		}
		return nil
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Slice, reflect.Map:
		// A composite can still carry NaN/±Inf (e.g. a Lua table
		// `{x * 2}` whose element overflowed), which json.Marshal rejects
		// at response time. Reject it here with the scalar message so
		// every runtime fails at the same point with the same bytes
		// (issue #210). Element types are not checked — only
		// non-finite numbers.
		if containsNonFinite(value) {
			return fmt.Errorf("field %q: NaN/Inf is not a valid JSON value", field)
		}
		return nil
	}
	return fmt.Errorf("field %q: unsupported type %T", field, value)
}

// maxCompositeScanDepth bounds the descent into nested composites: a
// composite at this depth or deeper is not inspected (scalars are checked at
// any depth their parent reaches). The rule depends only on the value, never
// on map iteration order, so a given value is always either rejected here or
// left to the encoder. Same bound as pine-java MAX_COMPOSITE_SCAN_DEPTH and
// pine-cpp kMaxCompositeScanDepth, and as encoding/json's
// startDetectingCyclesAfter.
const maxCompositeScanDepth = 1000

// containsNonFinite reports whether v holds a NaN or ±Inf in any composite
// within maxCompositeScanDepth.
//
// A custom operator can hand over a value whose composites are shared or
// form a cycle (a Lua- or JSON-built value is always a tree). Each composite
// is identified the way encoding/json identifies it for its cycle check (map
// pointer; slice data pointer + length) and remembered with the shallowest
// depth it was scanned at. Reaching it again at the same or a greater depth
// cannot find anything new — every descendant was already inspected with at
// least as much depth budget — so it is skipped. That keeps the scan linear in
// the number of distinct composites for any sharing pattern or cycle, and the
// verdict still depends only on the value, not on iteration order. Pointers
// and interfaces are unwrapped through the same bookkeeping, so a pointer
// that refers to itself terminates too.
func containsNonFinite(v any) bool {
	var s nonFiniteScanner
	return s.scan(v, 0)
}

type compositeID struct {
	ptr  uintptr
	len  int
	kind reflect.Kind
}

// nonFiniteScanner records each composite with the shallowest depth it was
// scanned at: inline for the first few (the common case — a Lua table of a
// handful of values — never allocates), then in a map.
type nonFiniteScanner struct {
	small [16]seenComposite
	n     int
	more  map[compositeID]int
}

type seenComposite struct {
	id    compositeID
	depth int
}

func isNonFiniteFloat(f float64) bool { return math.IsNaN(f) || math.IsInf(f, 0) }

// enter reports whether the composite id at depth still needs scanning and
// records it.
func (s *nonFiniteScanner) enter(id compositeID, depth int) bool {
	for i := 0; i < s.n; i++ {
		if s.small[i].id == id {
			if s.small[i].depth <= depth {
				return false
			}
			s.small[i].depth = depth
			return true
		}
	}
	if s.more != nil {
		if d, ok := s.more[id]; ok && d <= depth {
			return false
		}
		s.more[id] = depth
		return true
	}
	if s.n < len(s.small) {
		s.small[s.n] = seenComposite{id, depth}
		s.n++
		return true
	}
	s.more = map[compositeID]int{id: depth}
	return true
}

// scan classifies v found at composite depth `depth`.
func (s *nonFiniteScanner) scan(v any, depth int) bool {
	switch x := v.(type) {
	case nil, bool, string, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return false
	case float64:
		return isNonFiniteFloat(x)
	case float32:
		return isNonFiniteFloat(float64(x))
	case []any:
		if depth >= maxCompositeScanDepth || len(x) == 0 {
			return false
		}
		if !s.enter(compositeID{uintptr(unsafe.Pointer(unsafe.SliceData(x))), len(x), reflect.Slice}, depth) {
			return false
		}
		for _, e := range x {
			if s.scan(e, depth+1) {
				return true
			}
		}
		return false
	case map[string]any:
		if depth >= maxCompositeScanDepth || len(x) == 0 {
			return false
		}
		if !s.enter(compositeID{ptr: reflect.ValueOf(x).Pointer(), kind: reflect.Map}, depth) {
			return false
		}
		for _, e := range x {
			if s.scan(e, depth+1) {
				return true
			}
		}
		return false
	}
	return s.scanReflect(reflect.ValueOf(v), depth)
}

// scanReflect covers the value types a custom Go operator can build beyond
// []any / map[string]any: named float types (`type score float64`),
// arrays, and pointers / interfaces, unwrapped the way encoding/json
// unwraps them. Each pointer hop counts as a level, like a composite, so a
// chain of pointers is bounded by the same depth and a self-referencing
// pointer is caught by the seen set.
func (s *nonFiniteScanner) scanReflect(rv reflect.Value, depth int) bool {
	switch rv.Kind() {
	case reflect.Float32, reflect.Float64:
		return isNonFiniteFloat(rv.Float())
	case reflect.Interface:
		if rv.IsNil() {
			return false
		}
		return s.scanElem(rv.Elem(), depth)
	case reflect.Pointer:
		if rv.IsNil() || depth >= maxCompositeScanDepth {
			return false
		}
		if !s.enter(compositeID{ptr: rv.Pointer(), kind: reflect.Pointer}, depth) {
			return false
		}
		return s.scanElem(rv.Elem(), depth+1)
	case reflect.Slice, reflect.Array, reflect.Map:
	default:
		return false
	}
	if depth >= maxCompositeScanDepth || rv.Len() == 0 {
		return false
	}
	switch rv.Kind() {
	case reflect.Map:
		if !s.enter(compositeID{ptr: rv.Pointer(), kind: reflect.Map}, depth) {
			return false
		}
		iter := rv.MapRange()
		for iter.Next() {
			if s.scanElem(iter.Value(), depth+1) {
				return true
			}
		}
		return false
	case reflect.Slice:
		if !s.enter(compositeID{ptr: rv.Pointer(), len: rv.Len(), kind: reflect.Slice}, depth) {
			return false
		}
	}
	// An array is a value, not a reference: it cannot be shared or cyclic
	// on its own, so it needs no identity.
	for i := 0; i < rv.Len(); i++ {
		if s.scanElem(rv.Index(i), depth+1) {
			return true
		}
	}
	return false
}

func (s *nonFiniteScanner) scanElem(e reflect.Value, depth int) bool {
	if e.CanInterface() {
		return s.scan(e.Interface(), depth)
	}
	return s.scanReflect(e, depth)
}
