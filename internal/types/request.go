package types

// Request is the input to Engine.Execute.
type Request struct {
	// Common features (e.g. user_id, user_age). Must not be nil.
	Common map[string]any

	// Item list. Optional — recall-only flows don't need external items.
	Items []map[string]any
}

// Result is the output of Engine.Execute.
type Result struct {
	// Common output fields after pipeline execution.
	Common map[string]any

	// Items after pipeline execution (filtered, sorted, enriched).
	Items []map[string]any

	// Warnings collects recoverable errors from operators.
	Warnings []error
}
