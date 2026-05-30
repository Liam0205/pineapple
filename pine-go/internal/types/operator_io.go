package types

// FrameReader is the minimal read interface needed by lazy OperatorInput.
// Both RowFrame and ColumnFrame satisfy this interface.
type FrameReader interface {
	Common(field string) any
	Item(index int, field string) any
	ItemCount() int
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

// --- Accessors for engine-internal use ---

func (in *OperatorInput) RawCommon() map[string]any { return in.common }
func (in *OperatorInput) RawItems() []map[string]any { return in.items }

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

func (out *OperatorOutput) GetCommonWrites() map[string]any  { return out.commonWrites }
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
