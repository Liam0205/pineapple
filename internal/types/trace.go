package types

import "time"

// OpTrace records execution details for a single operator in a DAG run.
type OpTrace struct {
	// Name is the unique operator name from the JSON config.
	Name string

	// StartTime is when the operator began executing (or was evaluated for skip).
	StartTime time.Time

	// Duration is the wall-clock execution time. Zero for skipped operators.
	Duration time.Duration

	// Skipped is true if the operator was skipped due to a control-flow skip field.
	Skipped bool

	// InputSnapshot captures the operator's input data (only when debug=true).
	InputSnapshot map[string]any

	// OutputSnapshot captures the operator's output data (only when debug=true).
	OutputSnapshot map[string]any
}
