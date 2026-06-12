package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"time"
)

func main() {
	// CPU-type operators and their P50 mean targets (ms)
	targets := []struct {
		Name  string
		P50Ms float64
	}{
		{"filter_impression", 1.1},
		{"filter_blocked_creator", 0.1},
		{"reorder_topn_boost", 0.5},
		{"transform_generate_request_id", 0.05},
	}

	results := make(map[string]int64)

	for _, t := range targets {
		n := calibrate(t.P50Ms)
		results[t.Name] = n
		fmt.Fprintf(os.Stderr, "  %-40s target=%6.2fms  N=%d\n", t.Name, t.P50Ms, n)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(results)
}

func calibrate(targetMs float64) int64 {
	target := time.Duration(targetMs * float64(time.Millisecond))

	// Binary search for N that makes cpuWork(N) ≈ target
	lo, hi := int64(1), int64(1000000)

	// First find upper bound
	for measure(hi) < target {
		hi *= 2
	}

	// Binary search
	for lo < hi-1 {
		mid := (lo + hi) / 2
		elapsed := measure(mid)
		if elapsed < target {
			lo = mid
		} else {
			hi = mid
		}
	}

	// Verify with multiple runs
	var total time.Duration
	const runs = 20
	for i := 0; i < runs; i++ {
		total += measure(hi)
	}
	avg := total / runs
	fmt.Fprintf(os.Stderr, "    verify: N=%d avg=%v target=%v\n", hi, avg, target)

	return hi
}

func measure(n int64) time.Duration {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	start := time.Now()
	cpuWork(rng, n)
	return time.Since(start)
}

func cpuWork(rng *rand.Rand, n int64) float64 {
	acc := 1.0
	for i := int64(0); i < n; i++ {
		a := rng.Float64()*1000 + 1
		b := rng.Float64()*1000 + 1
		acc += a / b
		a = rng.Float64()*1000 + 1
		b = rng.Float64()*1000 + 1
		acc -= a / b
	}
	return acc
}
