package types

// FrameReader is the minimal read interface needed by lazy OperatorInput.
// Both RowFrame and ColumnFrame satisfy this interface.
type FrameReader interface {
	Common(field string) any
	Item(index int, field string) any
	ItemCount() int
}

// ColumnReader is an optional upgrade interface for FrameReader
// implementations that can serve a whole column in one call. ColumnFrame
// implements it to expose a zero-copy view; frames that do not implement
// it fall back to per-element gathering in ItemColumn.
//
// The returned slice is a read-only view valid for the current operator
// Execute only. Safety of escaping the frame lock relies on the DAG
// scheduler: writers of this field and row-set mutating operators
// (removals / reorder / additions — including ColumnFrame's in-place
// reorder) are hazard-ordered relative to the reader, so no concurrent
// operator can touch the returned backing array.
type ColumnReader interface {
	// ItemColumnView returns the [offset, offset+count) window of the
	// field's column. ok=false means the frame cannot serve a contiguous
	// view for this field (e.g. the column does not exist) and the caller
	// should fall back to per-element access.
	ItemColumnView(field string, offset, count int) (col []any, ok bool)
}

// OperatorInput provides read-only access to DataFrame data for one operator invocation.
// It operates in two modes:
//   - Lazy mode (frame != nil): reads from Frame on demand, avoiding O(N×M) materialization
//   - Materialized mode (items != nil): legacy path, used when lazy proxy is split for data_parallel
type OperatorInput struct {
	common map[string]any

	// Lazy mode fields
	frame        FrameReader
	itemDefaults map[string]any
	itemFields   []string
	offset       int
	count        int

	// Materialized mode field (nil in lazy mode)
	items []map[string]any

	// Resolved {{field}} interpolation map for this request (issue #74).
	// Populated by the scheduler before Execute when the operator declared
	// templated params; nil otherwise. Shards inherit the parent map by
	// reference — safe because the map is treated read-only past resolution.
	templated map[string]any
}

// NewOperatorInput creates a materialized OperatorInput. Intended for engine-internal use.
func NewOperatorInput(common map[string]any, items []map[string]any) *OperatorInput {
	return &OperatorInput{common: common, items: items}
}

// NewLazyOperatorInput creates a lazy OperatorInput backed by a Frame reference.
// Item reads are deferred until Item() is called, avoiding O(N×M) upfront materialization.
func NewLazyOperatorInput(common map[string]any, frame FrameReader, itemDefaults map[string]any, itemFields []string, offset, count int) *OperatorInput {
	return &OperatorInput{
		common:       common,
		frame:        frame,
		itemDefaults: itemDefaults,
		itemFields:   itemFields,
		offset:       offset,
		count:        count,
	}
}

// Common returns a common-side field value, or nil if not present.
func (in *OperatorInput) Common(field string) any {
	if in.common == nil {
		return nil
	}
	return in.common[field]
}

// ItemCount returns the number of items.
func (in *OperatorInput) ItemCount() int {
	if in.items != nil {
		return len(in.items)
	}
	return in.count
}

// Item returns a field value for the item at the given index, or nil if not present.
func (in *OperatorInput) Item(index int, field string) any {
	if in.items != nil {
		if index < 0 || index >= len(in.items) {
			return nil
		}
		return in.items[index][field]
	}
	if index < 0 || index >= in.count {
		return nil
	}
	v := in.frame.Item(in.offset+index, field)
	if v == nil && in.itemDefaults != nil {
		if d, ok := in.itemDefaults[field]; ok {
			return d
		}
	}
	return v
}

// ItemColumn returns all values of the given item field for this input's
// window as a slice indexed by item position. Element i is identical to
// Item(i, field), including item-default substitution for nil slots.
//
// The returned slice is READ-ONLY and valid only for the duration of the
// current Execute call: when backed by a ColumnFrame without defaults for
// this field it is a zero-copy view of the frame's column. Operators must
// not mutate it or retain it past Execute.
//
// Compared to an Item() loop this collapses the per-element lock + map
// lookup to once per column, which is where column storage's contiguous
// layout actually pays off.
func (in *OperatorInput) ItemColumn(field string) []any {
	// Materialized mode: gather from row maps.
	if in.items != nil {
		col := make([]any, len(in.items))
		for i, item := range in.items {
			col[i] = item[field]
		}
		return col
	}

	var defaultVal any
	var hasDefault bool
	if in.itemDefaults != nil {
		defaultVal, hasDefault = in.itemDefaults[field]
	}

	if cr, ok := in.frame.(ColumnReader); ok {
		if view, ok := cr.ItemColumnView(field, in.offset, in.count); ok {
			if !hasDefault {
				return view
			}
			// Defaults force a copy: nil slots substitute the default
			// value, matching Item()'s per-element semantics.
			col := make([]any, len(view))
			for i, v := range view {
				if v == nil {
					col[i] = defaultVal
				} else {
					col[i] = v
				}
			}
			return col
		}
	}

	// Fallback: per-element gather through the FrameReader interface.
	col := make([]any, in.count)
	for i := 0; i < in.count; i++ {
		v := in.frame.Item(in.offset+i, field)
		if v == nil && hasDefault {
			v = defaultVal
		}
		col[i] = v
	}
	return col
}

// CommonKeys returns the list of common field names available in this input.
func (in *OperatorInput) CommonKeys() []string {
	keys := make([]string, 0, len(in.common))
	for k := range in.common {
		keys = append(keys, k)
	}
	return keys
}

// ItemKeys returns the list of item field names available for the given item index.
func (in *OperatorInput) ItemKeys(index int) []string {
	if in.items != nil {
		if index < 0 || index >= len(in.items) {
			return nil
		}
		keys := make([]string, 0, len(in.items[index]))
		for k := range in.items[index] {
			keys = append(keys, k)
		}
		return keys
	}
	if index < 0 || index >= in.count {
		return nil
	}
	return in.itemFields
}

// ItemWrite represents a single field write to an item at a given index.
type ItemWrite struct {
	Index int
	Field string
	Value any
}

// OperatorOutput collects writes from an operator, applied to the DataFrame by the engine.
type OperatorOutput struct {
	commonWrites map[string]any
	itemWrites   []ItemWrite
	addedItems   []map[string]any
	removedItems map[int]struct{}
	itemOrder    []int
	warning      error
}

// NewOperatorOutput creates an OperatorOutput. Intended for engine-internal use.
func NewOperatorOutput() *OperatorOutput {
	return &OperatorOutput{}
}

// SetCommon writes a common-side field.
func (out *OperatorOutput) SetCommon(field string, value any) {
	if out.commonWrites == nil {
		out.commonWrites = make(map[string]any)
	}
	out.commonWrites[field] = value
}

// SetItem writes a field for the item at the given index.
func (out *OperatorOutput) SetItem(index int, field string, value any) {
	out.itemWrites = append(out.itemWrites, ItemWrite{Index: index, Field: field, Value: value})
}

// AddItem appends a new item row.
func (out *OperatorOutput) AddItem(fields map[string]any) {
	out.addedItems = append(out.addedItems, fields)
}

// RemoveItem marks the item at the given index for removal.
func (out *OperatorOutput) RemoveItem(index int) {
	if out.removedItems == nil {
		out.removedItems = make(map[int]struct{})
	}
	out.removedItems[index] = struct{}{}
}

// SetItemOrder sets the new ordering of items.
func (out *OperatorOutput) SetItemOrder(newOrder []int) {
	out.itemOrder = newOrder
}

// SetWarning records a recoverable error. First warning wins.
func (out *OperatorOutput) SetWarning(err error) {
	if out.warning == nil {
		out.warning = err
	}
}

// Reset prepares an OperatorOutput for reuse by a sync.Pool. It must be called
// only after ApplyOutput has consumed every write — the Frame copies field
// values by value (item / common writes) and takes ownership of added-item map
// references, so no aliasing of OperatorOutput's slices survives that point.
//
// Each slice element is nil'd before the slice header is truncated so the
// backing array stops pinning the previous request's values (notably
// ItemWrite.Value which can hold large Lua-side payloads or composite maps).
// Maps are cleared in place via delete to retain bucket allocations across
// borrows; itemOrder and warning are dropped wholesale.
func (out *OperatorOutput) Reset() {
	for i := range out.itemWrites {
		out.itemWrites[i] = ItemWrite{}
	}
	out.itemWrites = out.itemWrites[:0]

	for i := range out.addedItems {
		out.addedItems[i] = nil
	}
	out.addedItems = out.addedItems[:0]

	for k := range out.commonWrites {
		delete(out.commonWrites, k)
	}
	for k := range out.removedItems {
		delete(out.removedItems, k)
	}

	out.itemOrder = nil
	out.warning = nil
}

// --- Accessors for engine-internal use ---

func (in *OperatorInput) RawCommon() map[string]any  { return in.common }
func (in *OperatorInput) RawItems() []map[string]any { return in.items }

// TemplatedParam returns the resolved + coerced value for a templated
// param declared on this operator (issue #74). Returns (nil, false) when
// the param was not templated. Read-only: the map is shared across
// data_parallel shards.
func (in *OperatorInput) TemplatedParam(name string) (any, bool) {
	if in.templated == nil {
		return nil, false
	}
	v, ok := in.templated[name]
	return v, ok
}

// SetTemplatedParams installs the per-request resolved {{field}} map.
// Engine-internal: invoked once by the scheduler after BuildInput and
// before Execute (or before splitting for data_parallel). Shards reuse
// this map by reference via splitInput.
func (in *OperatorInput) SetTemplatedParams(resolved map[string]any) {
	in.templated = resolved
}

// RawTemplated returns the underlying resolved map (engine-internal,
// used by splitInput to propagate it to shards).
func (in *OperatorInput) RawTemplated() map[string]any { return in.templated }

// IsLazy returns true if this OperatorInput is in lazy (frame-backed) mode.
func (in *OperatorInput) IsLazy() bool { return in.frame != nil }

// LazyOffset returns the item offset for lazy mode (used by splitInput).
func (in *OperatorInput) LazyOffset() int { return in.offset }

// LazyFrame returns the underlying FrameReader (nil if materialized).
func (in *OperatorInput) LazyFrame() FrameReader { return in.frame }

// LazyItemDefaults returns the item defaults map (nil if materialized).
func (in *OperatorInput) LazyItemDefaults() map[string]any { return in.itemDefaults }

// LazyItemFields returns the item field names (nil if materialized).
func (in *OperatorInput) LazyItemFields() []string { return in.itemFields }

func (out *OperatorOutput) GetCommonWrites() map[string]any   { return out.commonWrites }
func (out *OperatorOutput) GetItemWrites() []ItemWrite        { return out.itemWrites }
func (out *OperatorOutput) GetAddedItems() []map[string]any   { return out.addedItems }
func (out *OperatorOutput) GetRemovedItems() map[int]struct{} { return out.removedItems }
func (out *OperatorOutput) GetItemOrder() []int               { return out.itemOrder }
func (out *OperatorOutput) GetWarning() error                 { return out.warning }

// ItemWriteMap reconstructs the legacy map[int]map[string]any view from the
// flat slice. Intended for tests and debug snapshots only.
func (out *OperatorOutput) ItemWriteMap() map[int]map[string]any {
	m := make(map[int]map[string]any)
	for _, w := range out.itemWrites {
		if m[w.Index] == nil {
			m[w.Index] = make(map[string]any)
		}
		m[w.Index][w.Field] = w.Value
	}
	return m
}
