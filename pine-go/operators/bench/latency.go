package bench

import (
	"math"
	"math/rand"
	"time"
)

type LatencyProfile struct {
	P50Mean float64
	P50Max  float64
	P99Mean float64
	P99Max  float64
	IsIO    bool
}

type LatencySampler struct {
	profile LatencyProfile
	rng     *rand.Rand
}

func NewLatencySampler(profile LatencyProfile) *LatencySampler {
	return &LatencySampler{
		profile: profile,
		rng:     rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (s *LatencySampler) Sample() time.Duration {
	if s == nil || s.profile.P50Mean <= 0 {
		return 0
	}

	jitterFactor := s.rng.Float64()
	p50 := s.profile.P50Mean + jitterFactor*(s.profile.P50Max-s.profile.P50Mean)
	p99 := s.profile.P99Mean + jitterFactor*(s.profile.P99Max-s.profile.P99Mean)

	if p50 <= 0 {
		p50 = 0.001
	}
	if p99 <= p50 {
		p99 = p50 * 2
	}

	mu := math.Log(p50)
	sigma := (math.Log(p99) - mu) / 2.326

	if sigma <= 0 {
		sigma = 0.1
	}

	sample := math.Exp(mu + sigma*s.rng.NormFloat64())

	cap := p99 * 1.5
	if sample > cap {
		sample = cap
	}
	if sample < 0 {
		sample = 0
	}

	return time.Duration(sample * float64(time.Millisecond))
}

func (s *LatencySampler) Apply() float64 {
	d := s.Sample()
	if d <= 0 {
		return 0
	}
	if s.profile.IsIO {
		time.Sleep(d)
		return 0
	}
	// CPU-intensive: sustained FP division until timeout
	deadline := time.Now().Add(d)
	acc := 1.0
	for time.Now().Before(deadline) {
		a := s.rng.Float64()*1000 + 1
		b := s.rng.Float64()*1000 + 1
		acc += a / b
		a = s.rng.Float64()*1000 + 1
		b = s.rng.Float64()*1000 + 1
		acc -= a / b
	}
	return acc
}

func ParseBenchProfile(params map[string]any) *LatencySampler {
	raw, ok := params["bench_profile"]
	if !ok || raw == nil {
		return nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}

	profile := LatencyProfile{}

	if p50, ok := m["p50"].([]any); ok && len(p50) >= 2 {
		profile.P50Mean = toFloat(p50[0])
		profile.P50Max = toFloat(p50[1])
	}
	if p99, ok := m["p99"].([]any); ok && len(p99) >= 2 {
		profile.P99Mean = toFloat(p99[0])
		profile.P99Max = toFloat(p99[1])
	}
	if t, ok := m["type"].(string); ok {
		profile.IsIO = (t == "io")
	}

	if profile.P50Mean <= 0 && profile.P99Mean <= 0 {
		return nil
	}

	return NewLatencySampler(profile)
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	default:
		return 0
	}
}
