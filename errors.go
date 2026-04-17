package pine

import "github.com/Liam0205/pineapple/internal/types"

// Re-export error types from internal/types.

type ConfigError = types.ConfigError
type RegistryError = types.RegistryError
type ValidationError = types.ValidationError
type ExecutionError = types.ExecutionError
type PanicError = types.PanicError
