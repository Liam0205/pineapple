package registry

import (
	"fmt"
	"sync"

	"github.com/Liam0205/pineapple/pine-go/internal/types"
)

// reservedKeys are operator config keys handled by the engine, not passed to Init.
var reservedKeys = map[string]struct{}{
	"type_name":               {},
	"$metadata":               {},
	"$code_info":              {},
	"skip":                    {},
	"recall":                  {},
	"sources":                 {},
	"debug":                   {},
	"consumes_row_set":        {},
	"mutates_row_set":         {},
	"additive_writes_row_set": {},
	"common_defaults":         {},
	"item_defaults":           {},
	"strict_common":           {},
	"strict_item":             {},
	"for_branch_control":      {},
	"data_parallel":           {},
}

// IsReservedKey returns true if the key is engine-reserved.
func IsReservedKey(key string) bool {
	_, ok := reservedKeys[key]
	return ok
}

type entry struct {
	Schema  types.OperatorSchema
	Factory func() types.Operator
}

var (
	mu       sync.RWMutex
	registry = make(map[string]entry)
)

// Register registers an operator type. Panics on duplicate name or missing doc fields.
func Register(schema types.OperatorSchema, factory func() types.Operator) {
	if schema.Name == "" {
		panic("pine: Register called with empty Name")
	}
	if !types.IsValidOperatorType(schema.Type) {
		panic(fmt.Sprintf("pine: operator %q: Type must be one of Recall/Transform/Filter/Merge/Reorder/Observe, got %q", schema.Name, schema.Type))
	}
	if schema.Description == "" {
		panic(fmt.Sprintf("pine: operator %q: Description is required", schema.Name))
	}
	for pname, pspec := range schema.Params {
		if pspec.Description == "" {
			panic(fmt.Sprintf("pine: operator %q param %q: Description is required", schema.Name, pname))
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if _, exists := registry[schema.Name]; exists {
		panic(fmt.Sprintf("pine: duplicate operator registration: %q", schema.Name))
	}
	registry[schema.Name] = entry{Schema: schema, Factory: factory}
}

// Lookup returns the schema and factory for a registered operator type.
func Lookup(name string) (types.OperatorSchema, func() types.Operator, bool) {
	mu.RLock()
	defer mu.RUnlock()
	e, ok := registry[name]
	if !ok {
		return types.OperatorSchema{}, nil, false
	}
	return e.Schema, e.Factory, true
}

// Reset clears the registry. For testing only.
func Reset() {
	mu.Lock()
	defer mu.Unlock()
	registry = make(map[string]entry)
}

// ValidateAndExtractParams validates raw config params against the schema,
// filters out reserved keys, applies defaults, and returns the business params for Init.
func ValidateAndExtractParams(schema types.OperatorSchema, rawParams map[string]any) (map[string]any, error) {
	params := make(map[string]any)

	// Copy non-reserved keys
	for k, v := range rawParams {
		if IsReservedKey(k) {
			continue
		}
		params[k] = v
	}

	// Check required params and apply defaults
	for name, spec := range schema.Params {
		if _, present := params[name]; !present {
			if spec.Required {
				return nil, fmt.Errorf("required parameter %q missing for operator %q", name, schema.Name)
			}
			if spec.Default != nil {
				params[name] = spec.Default
			}
		}
	}

	// Reject undeclared parameters
	for k := range params {
		if _, declared := schema.Params[k]; !declared {
			return nil, fmt.Errorf("unknown parameter %q for operator %q", k, schema.Name)
		}
	}

	return params, nil
}

// BuildOperator looks up, validates params, creates an instance, and calls Init.
func BuildOperator(typeName string, rawParams map[string]any) (types.Operator, types.OperatorSchema, error) {
	schema, factory, ok := Lookup(typeName)
	if !ok {
		return nil, types.OperatorSchema{}, &types.RegistryError{
			Operator: typeName,
			Message:  "operator type not registered",
		}
	}

	params, err := ValidateAndExtractParams(schema, rawParams)
	if err != nil {
		return nil, schema, &types.RegistryError{
			Operator: typeName,
			Message:  err.Error(),
		}
	}

	op := factory()
	if err := op.Init(params); err != nil {
		return nil, schema, &types.RegistryError{
			Operator: typeName,
			Message:  fmt.Sprintf("Init failed: %v", err),
		}
	}

	return op, schema, nil
}
