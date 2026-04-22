package types

// OperatorInput provides read-only access to DataFrame data for one operator invocation.
type OperatorInput struct {
	common map[string]any
	items  []map[string]any
}

// NewOperatorInput creates an OperatorInput. Intended for engine-internal use.
func NewOperatorInput(common map[string]any, items []map[string]any) *OperatorInput {
	return &OperatorInput{common: common, items: items}
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
	return len(in.items)
}

// Item returns a field value for the item at the given index, or nil if not present.
func (in *OperatorInput) Item(index int, field string) any {
	if index < 0 || index >= len(in.items) {
		return nil
	}
	return in.items[index][field]
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
	if index < 0 || index >= len(in.items) {
		return nil
	}
	keys := make([]string, 0, len(in.items[index]))
	for k := range in.items[index] {
		keys = append(keys, k)
	}
	return keys
}

// OperatorOutput collects writes from an operator, applied to the DataFrame by the engine.
type OperatorOutput struct {
	commonWrites map[string]any
	itemWrites   map[int]map[string]any
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
	if out.itemWrites == nil {
		out.itemWrites = make(map[int]map[string]any)
	}
	if out.itemWrites[index] == nil {
		out.itemWrites[index] = make(map[string]any)
	}
	out.itemWrites[index][field] = value
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

func (in *OperatorInput) RawCommon() map[string]any  { return in.common }
func (in *OperatorInput) RawItems() []map[string]any  { return in.items }

func (out *OperatorOutput) GetCommonWrites() map[string]any       { return out.commonWrites }
func (out *OperatorOutput) GetItemWrites() map[int]map[string]any  { return out.itemWrites }
func (out *OperatorOutput) GetAddedItems() []map[string]any        { return out.addedItems }
func (out *OperatorOutput) GetRemovedItems() map[int]struct{}      { return out.removedItems }
func (out *OperatorOutput) GetItemOrder() []int                    { return out.itemOrder }
func (out *OperatorOutput) GetWarning() error                      { return out.warning }
