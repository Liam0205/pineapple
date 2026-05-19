package runtime

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Liam0205/pineapple/pine-go/internal/config"
	"github.com/Liam0205/pineapple/pine-go/internal/dataframe"
)

func TestStatsRecordExec(t *testing.T) {
	s := NewStats()
	s.RecordExec("op_a", 10*time.Millisecond)
	s.RecordExec("op_a", 20*time.Millisecond)
	s.RecordExec("op_b", 5*time.Millisecond)

	snap := s.Snapshot()
	if snap["op_a"].ExecCount != 2 {
		t.Errorf("op_a exec_count = %d, want 2", snap["op_a"].ExecCount)
	}
	if snap["op_a"].MaxDurationNs != (20 * time.Millisecond).Nanoseconds() {
		t.Errorf("op_a max = %d", snap["op_a"].MaxDurationNs)
	}
	if snap["op_a"].AvgDurationNs != (15 * time.Millisecond).Nanoseconds() {
		t.Errorf("op_a avg = %d, want %d", snap["op_a"].AvgDurationNs, (15 * time.Millisecond).Nanoseconds())
	}
	if snap["op_b"].ExecCount != 1 {
		t.Errorf("op_b exec_count = %d, want 1", snap["op_b"].ExecCount)
	}
}

func TestStatsRecordSkip(t *testing.T) {
	s := NewStats()
	s.RecordSkip("op_a")
	s.RecordSkip("op_a")

	snap := s.Snapshot()
	if snap["op_a"].SkipCount != 2 {
		t.Errorf("skip_count = %d, want 2", snap["op_a"].SkipCount)
	}
	if snap["op_a"].ExecCount != 0 {
		t.Errorf("exec_count = %d, want 0", snap["op_a"].ExecCount)
	}
}

func TestStatsRecordError(t *testing.T) {
	s := NewStats()
	s.RecordError("op_a", 5*time.Millisecond)

	snap := s.Snapshot()
	if snap["op_a"].ErrorCount != 1 {
		t.Errorf("error_count = %d, want 1", snap["op_a"].ErrorCount)
	}
	if snap["op_a"].TotalDurationNs != (5 * time.Millisecond).Nanoseconds() {
		t.Errorf("total = %d", snap["op_a"].TotalDurationNs)
	}
}

func TestStatsConcurrentSafety(t *testing.T) {
	s := NewStats()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			name := "op_a"
			if idx%3 == 0 {
				name = "op_b"
			}
			s.RecordExec(name, time.Duration(idx)*time.Microsecond)
			if idx%5 == 0 {
				s.RecordSkip(name)
			}
			if idx%7 == 0 {
				s.RecordError(name, time.Duration(idx)*time.Microsecond)
			}
			_ = s.Snapshot()
		}(i)
	}
	wg.Wait()

	snap := s.Snapshot()
	if _, ok := snap["op_a"]; !ok {
		t.Error("missing op_a stats")
	}
	if _, ok := snap["op_b"]; !ok {
		t.Error("missing op_b stats")
	}
}

func TestStatsIntegrationWithRun(t *testing.T) {
	// Test that stats are recorded during a real Run
	s := NewStats()

	opA := &CompiledOperator{
		Name:     "op_a",
		Instance: &setCommonOp{field: "x", value: 10.0},
		Config: config.OperatorConfig{
			TypeName:  "set",
			Meta:      config.Metadata{CommonOutput: []string{"x"}},
			InputSpec: &config.InputFieldSpec{},
		},
	}
	opB := &CompiledOperator{
		Name:     "op_b",
		Instance: &readAndSetOp{readField: "x", writeField: "y"},
		Config: config.OperatorConfig{
			TypeName:  "rw",
			Meta:      config.Metadata{CommonInput: []string{"x"}, CommonOutput: []string{"y"}},
			InputSpec: config.ComputeInputFieldSpec(config.Metadata{CommonInput: []string{"x"}, CommonOutput: []string{"y"}}, nil, nil, nil),
		},
	}

	plan := buildPlan(t, []string{"op_a", "op_b"}, map[string]*CompiledOperator{
		"op_a": opA, "op_b": opB,
	})

	// Run twice
	for i := 0; i < 2; i++ {
		frame := dataframe.New(map[string]any{}, nil)
		_, _, err := Run(context.Background(), plan, frame, s, nil)
		if err != nil {
			t.Fatal(err)
		}
	}

	snap := s.Snapshot()
	if snap["op_a"].ExecCount != 2 {
		t.Errorf("op_a exec = %d, want 2", snap["op_a"].ExecCount)
	}
	if snap["op_b"].ExecCount != 2 {
		t.Errorf("op_b exec = %d, want 2", snap["op_b"].ExecCount)
	}
}
