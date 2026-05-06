package pine

import "sync"

// ResetLogOnce resets the log setup guard for testing purposes.
func ResetLogOnce() {
	logOnce = sync.Once{}
}
