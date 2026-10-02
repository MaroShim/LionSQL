package execution

import (
	"math"
	"sort"
)

// Centroid represents a cluster mean and weight in a T-Digest.
type Centroid struct {
	Mean   float64
	Weight float64
}

// TDigest is a streaming quantile approximation data structure with high tail accuracy.
type TDigest struct {
	compression float64
	centroids   []Centroid
	count       float64
	minVal      float64
	maxVal      float64
	unmerged    []float64
	maxUnmerged int
}

// NewTDigest creates a new TDigest instance with the specified compression factor.
func NewTDigest(compression float64) *TDigest {
	if compression <= 0 {
		compression = 100
	}
	return &TDigest{
		compression: compression,
		centroids:   make([]Centroid, 0, int(compression*2)),
		minVal:      math.MaxFloat64,
		maxVal:      -math.MaxFloat64,
		maxUnmerged: int(compression * 5),
		unmerged:    make([]float64, 0, int(compression*5)),
	}
}

// Add appends a new numeric sample into the T-Digest.
func (t *TDigest) Add(val float64) {
	if math.IsNaN(val) {
		return
	}
	if val < t.minVal {
		t.minVal = val
	}
	if val > t.maxVal {
		t.maxVal = val
	}
	t.unmerged = append(t.unmerged, val)
	t.count++
	if len(t.unmerged) >= t.maxUnmerged {
		t.compress()
	}
}

// compress merges unmerged points into the centroid list using the scale function k1.
func (t *TDigest) compress() {
	if len(t.unmerged) == 0 {
		return
	}
	all := make([]Centroid, 0, len(t.centroids)+len(t.unmerged))
	all = append(all, t.centroids...)
	for _, v := range t.unmerged {
		all = append(all, Centroid{Mean: v, Weight: 1.0})
	}
	t.unmerged = t.unmerged[:0]

	sort.Slice(all, func(i, j int) bool {
		return all[i].Mean < all[j].Mean
	})

	totalWeight := t.count
	var newCentroids []Centroid
	cur := all[0]
	weightSoFar := 0.0

	for i := 1; i < len(all); i++ {
		next := all[i]
		proposedWeight := cur.Weight + next.Weight
		q := (weightSoFar + cur.Weight/2.0) / totalWeight
		maxW := 4.0 * totalWeight * q * (1.0 - q) / t.compression
		if maxW < 1.0 {
			maxW = 1.0
		}

		if proposedWeight <= maxW {
			cur.Mean = (cur.Mean*cur.Weight + next.Mean*next.Weight) / proposedWeight
			cur.Weight = proposedWeight
		} else {
			weightSoFar += cur.Weight
			newCentroids = append(newCentroids, cur)
			cur = next
		}
	}
	newCentroids = append(newCentroids, cur)
	t.centroids = newCentroids
}

// Quantile estimates the value at quantile q (0.0 <= q <= 1.0).
func (t *TDigest) Quantile(q float64) float64 {
	if t.count == 0 {
		return math.NaN()
	}
	t.compress()
	if len(t.centroids) == 0 {
		return math.NaN()
	}
	if len(t.centroids) == 1 || q <= 0 {
		return t.centroids[0].Mean
	}
	if q >= 1.0 {
		return t.centroids[len(t.centroids)-1].Mean
	}

	targetWeight := q * t.count
	var weightSoFar float64

	for i, c := range t.centroids {
		midWeight := weightSoFar + c.Weight/2.0
		if midWeight >= targetWeight {
			if i == 0 {
				return c.Mean
			}
			prev := t.centroids[i-1]
			prevMid := weightSoFar - prev.Weight/2.0
			delta := midWeight - prevMid
			if delta <= 0 {
				return c.Mean
			}
			f := (targetWeight - prevMid) / delta
			return prev.Mean + f*(c.Mean-prev.Mean)
		}
		weightSoFar += c.Weight
	}

	return t.centroids[len(t.centroids)-1].Mean
}

// ComputePercentileCont computes exact continuous percentile with linear interpolation.
// Assumes values slice is sorted in ascending order.
func ComputePercentileCont(values []float64, p float64) float64 {
	n := len(values)
	if n == 0 {
		return math.NaN()
	}
	if n == 1 || p <= 0 {
		return values[0]
	}
	if p >= 1.0 {
		return values[n-1]
	}

	rn := p * float64(n-1)
	k := int(rn)
	d := rn - float64(k)

	if d == 0 || k >= n-1 {
		return values[k]
	}
	return values[k] + d*(values[k+1]-values[k])
}

// ComputePercentileDisc computes discrete percentile (nearest value whose cumulative distribution is >= p).
// Assumes values slice is sorted in ascending order.
func ComputePercentileDisc(values []float64, p float64) float64 {
	n := len(values)
	if n == 0 {
		return math.NaN()
	}
	if n == 1 || p <= 0 {
		return values[0]
	}
	if p >= 1.0 {
		return values[n-1]
	}

	idx := int(math.Ceil(p*float64(n))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= n {
		idx = n - 1
	}
	return values[idx]
}
