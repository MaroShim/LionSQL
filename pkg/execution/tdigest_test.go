package execution_test

import (
	"math"
	"math/rand"
	"sort"
	"testing"

	"lionsql/pkg/execution"
)

func TestPercentile_ExactCalculations(t *testing.T) {
	// Sample dataset: [10, 20, 30, 40]
	data := []float64{10, 20, 30, 40}

	// 1. Continuous percentile (PERCENTILE_CONT)
	// p=0.0 -> 10
	if p0 := execution.ComputePercentileCont(data, 0.0); p0 != 10 {
		t.Errorf("expected p0=10, got %v", p0)
	}
	// p=0.5 -> 25 (linear interpolation between 20 and 30)
	if p50 := execution.ComputePercentileCont(data, 0.5); p50 != 25 {
		t.Errorf("expected p50=25, got %v", p50)
	}
	// p=0.75 -> 32.5
	if p75 := execution.ComputePercentileCont(data, 0.75); p75 != 32.5 {
		t.Errorf("expected p75=32.5, got %v", p75)
	}
	// p=1.0 -> 40
	if p100 := execution.ComputePercentileCont(data, 1.0); p100 != 40 {
		t.Errorf("expected p100=40, got %v", p100)
	}

	// 2. Discrete percentile (PERCENTILE_DISC)
	// p=0.5 -> index ceil(0.5*4)-1 = 1 -> 20
	if d50 := execution.ComputePercentileDisc(data, 0.5); d50 != 20 {
		t.Errorf("expected disc50=20, got %v", d50)
	}
	// p=0.75 -> index ceil(0.75*4)-1 = 2 -> 30
	if d75 := execution.ComputePercentileDisc(data, 0.75); d75 != 30 {
		t.Errorf("expected disc75=30, got %v", d75)
	}
	// p=1.0 -> 40
	if d100 := execution.ComputePercentileDisc(data, 1.0); d100 != 40 {
		t.Errorf("expected disc100=40, got %v", d100)
	}
}

func TestTDigest_Accuracy(t *testing.T) {
	td := execution.NewTDigest(100)

	// Generate 10,000 uniform samples from 0 to 1,000
	r := rand.New(rand.NewSource(42))
	var samples []float64
	for i := 0; i < 10000; i++ {
		v := r.Float64() * 1000.0
		samples = append(samples, v)
		td.Add(v)
	}

	sort.Float64s(samples)

	// Test P50, P90, P95, P99
	for _, q := range []float64{0.5, 0.9, 0.95, 0.99} {
		exact := execution.ComputePercentileCont(samples, q)
		approx := td.Quantile(q)
		relErr := math.Abs(approx-exact) / exact
		// TDigest relative error on tail and median should be well within 1.5%
		if relErr > 0.015 {
			t.Errorf("Quantile(%.2f): exact=%.2f, approx=%.2f, relErr=%.4f (exceeds threshold)", q, exact, approx, relErr)
		}
	}
}
