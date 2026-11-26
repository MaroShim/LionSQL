package execution_test

import (
	"fmt"
	"testing"

	"lionsql/pkg/execution"
)

func TestFastBloomFilter_IntKeys(t *testing.T) {
	bf := execution.NewFastBloomFilter(1000)

	// Insert 500 even numbers
	for i := 0; i < 1000; i += 2 {
		bf.AddInt(int64(i))
	}

	// Verify all inserted numbers return true (Zero false negatives)
	for i := 0; i < 1000; i += 2 {
		if !bf.MayContainInt(int64(i)) {
			t.Fatalf("expected key %d to be present in bloom filter", i)
		}
	}

	// Check odd numbers for false positives (should be very low, < 3%)
	falsePositives := 0
	totalTested := 0
	for i := 1; i < 1000; i += 2 {
		totalTested++
		if bf.MayContainInt(int64(i)) {
			falsePositives++
		}
	}

	fpRate := float64(falsePositives) / float64(totalTested)
	if fpRate > 0.05 {
		t.Fatalf("false positive rate too high: got %f (%d/%d), expected <= 0.05", fpRate, falsePositives, totalTested)
	}
}

func TestFastBloomFilter_StringKeys(t *testing.T) {
	bf := execution.NewFastBloomFilter(500)

	for i := 0; i < 200; i++ {
		bf.AddString(fmt.Sprintf("key_user_%d", i))
	}

	for i := 0; i < 200; i++ {
		if !bf.MayContainString(fmt.Sprintf("key_user_%d", i)) {
			t.Fatalf("expected string key_user_%d to be present", i)
		}
	}

	falsePositives := 0
	for i := 200; i < 1200; i++ {
		if bf.MayContainString(fmt.Sprintf("key_user_%d", i)) {
			falsePositives++
		}
	}

	fpRate := float64(falsePositives) / 1000.0
	if fpRate > 0.05 {
		t.Fatalf("string false positive rate too high: %f", fpRate)
	}
}
