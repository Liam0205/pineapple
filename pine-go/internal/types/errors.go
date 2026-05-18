package types

import "fmt"

// ConfigError indicates a problem with the JSON configuration.
type ConfigError struct {
	Message string
}

func (e *ConfigError) Error() string {
	return fmt.Sprintf("pine: config error: %s", e.Message)
}

// RegistryError indicates a problem with operator registration or lookup.
type RegistryError struct {
	Operator string
	Message  string
}

func (e *RegistryError) Error() string {
	return fmt.Sprintf("pine: registry error [%s]: %s", e.Operator, e.Message)
}

// ValidationError indicates a request validation failure.
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("pine: validation error: %s", e.Message)
}

// ExecutionError indicates a fatal error during DAG execution.
type ExecutionError struct {
	Operator string
	Err      error
}

func (e *ExecutionError) Error() string {
	return fmt.Sprintf("pine: execution error in operator %q: %v", e.Operator, e.Err)
}

func (e *ExecutionError) Unwrap() error {
	return e.Err
}

// PanicError wraps a recovered panic from an operator.
type PanicError struct {
	Operator string
	Value    any
	Stack    string
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("pine: panic in operator %q: %v", e.Operator, e.Value)
}

// DetailedError returns the full error including stack trace, for logging purposes.
func (e *PanicError) DetailedError() string {
	return fmt.Sprintf("pine: panic in operator %q: %v\n%s", e.Operator, e.Value, e.Stack)
}
