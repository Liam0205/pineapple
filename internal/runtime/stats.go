package runtime

import (
	"sync"
	"sync/atomic"
	"time"
)

// OpStats holds per-operator cumulative execution statistics.
// All fields are updated atomically and are safe for concurrent access.
type OpStats struct {
	ExecCount      int64 // number of successful executions
	SkipCount      int64 // number of times the operator was skipped
	ErrorCount     int64 // number of errors (fatal)
	TotalDurationNs int64 // cumulative execution time in nanoseconds
	MaxDurationNs   int64 // maximum single-execution time in nanoseconds
}

// Stats accumulates per-operator execution statistics across requests.
// Thread-safe for concurrent updates from multiple Execute calls.
type Stats struct {
	mu    sync.RWMutex
	ops   map[string]*OpStats
}

// NewStats creates a new Stats accumulator.
func NewStats() *Stats {
	return &Stats{
		ops: make(map[string]*OpStats),
	}
}

// getOrCreate returns the OpStats for a given operator, creating it if needed.
func (s *Stats) getOrCreate(name string) *OpStats {
	s.mu.RLock()
	st, ok := s.ops[name]
	s.mu.RUnlock()
	if ok {
		return st
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// Double-check after acquiring write lock
	if st, ok = s.ops[name]; ok {
		return st
	}
	st = &OpStats{}
	s.ops[name] = st
	return st
}

// RecordExec records a successful operator execution.
func (s *Stats) RecordExec(name string, duration time.Duration) {
	st := s.getOrCreate(name)
	atomic.AddInt64(&st.ExecCount, 1)
	ns := duration.Nanoseconds()
	atomic.AddInt64(&st.TotalDurationNs, ns)

	// CAS loop for max
	for {
		cur := atomic.LoadInt64(&st.MaxDurationNs)
		if ns <= cur {
			break
		}
		if atomic.CompareAndSwapInt64(&st.MaxDurationNs, cur, ns) {
			break
		}
	}
}

// RecordSkip records a skipped operator execution.
func (s *Stats) RecordSkip(name string) {
	st := s.getOrCreate(name)
	atomic.AddInt64(&st.SkipCount, 1)
}

// RecordError records a failed operator execution.
func (s *Stats) RecordError(name string, duration time.Duration) {
	st := s.getOrCreate(name)
	atomic.AddInt64(&st.ErrorCount, 1)
	ns := duration.Nanoseconds()
	atomic.AddInt64(&st.TotalDurationNs, ns)

	for {
		cur := atomic.LoadInt64(&st.MaxDurationNs)
		if ns <= cur {
			break
		}
		if atomic.CompareAndSwapInt64(&st.MaxDurationNs, cur, ns) {
			break
		}
	}
}

// Snapshot returns a point-in-time copy of all operator statistics.
type OpStatsSnapshot struct {
	ExecCount      int64   `json:"exec_count"`
	SkipCount      int64   `json:"skip_count"`
	ErrorCount     int64   `json:"error_count"`
	TotalDurationNs int64  `json:"total_duration_ns"`
	MaxDurationNs   int64  `json:"max_duration_ns"`
	AvgDurationNs   int64  `json:"avg_duration_ns"`
}

// Snapshot returns a point-in-time copy of all operator statistics.
func (s *Stats) Snapshot() map[string]OpStatsSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]OpStatsSnapshot, len(s.ops))
	for name, st := range s.ops {
		exec := atomic.LoadInt64(&st.ExecCount)
		total := atomic.LoadInt64(&st.TotalDurationNs)
		var avg int64
		if exec > 0 {
			avg = total / exec
		}
		result[name] = OpStatsSnapshot{
			ExecCount:       exec,
			SkipCount:       atomic.LoadInt64(&st.SkipCount),
			ErrorCount:      atomic.LoadInt64(&st.ErrorCount),
			TotalDurationNs: total,
			MaxDurationNs:   atomic.LoadInt64(&st.MaxDurationNs),
			AvgDurationNs:   avg,
		}
	}
	return result
}
