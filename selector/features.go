package selector

import (
	"math"
	"sort"
)

type Features struct {
	LogLength   float64 `json:"log_length"`
	Seasonality float64 `json:"seasonality"`
	LogPeriod   float64 `json:"log_period"`
	Trend       float64 `json:"trend"`
	Noise       float64 `json:"noise"`
	Skewness    float64 `json:"skewness"`
	Kurtosis    float64 `json:"kurtosis"`
	Spikiness   float64 `json:"spikiness"`
	Flatness    float64 `json:"flatness"`
}

func (f Features) Vector() []float64 {
	return []float64{f.LogLength, f.Seasonality, f.LogPeriod, f.Trend,
		f.Noise, f.Skewness, f.Kurtosis, f.Spikiness, f.Flatness}
}

func FeatureNames() []string {
	return []string{"log_length", "seasonality", "log_period", "trend",
		"noise", "skewness", "kurtosis", "spikiness", "flatness"}
}

func Extract(values []float64) Features {
	n := len(values)
	f := Features{LogLength: math.Log1p(float64(n))}
	if n < 8 {
		return f
	}

	var sum float64
	for _, v := range values {
		sum += v
	}
	mean := sum / float64(n)
	var m2, m3, m4 float64
	for _, v := range values {
		d := v - mean
		d2 := d * d
		m2 += d2
		m3 += d2 * d
		m4 += d2 * d2
	}
	m2 /= float64(n)
	m3 /= float64(n)
	m4 /= float64(n)
	sd := math.Sqrt(m2)
	if sd > 1e-12 {
		f.Skewness = m3 / (sd * sd * sd)
		f.Kurtosis = m4/(m2*m2) - 3
	}

	var diffSS float64
	flat := 0
	eps := 1e-9 * (math.Abs(mean) + 1)
	for i := 1; i < n; i++ {
		d := values[i] - values[i-1]
		diffSS += d * d
		if math.Abs(d) <= eps {
			flat++
		}
	}
	diffSD := math.Sqrt(diffSS / float64(n-1))
	f.Flatness = float64(flat) / float64(n-1)
	if sd > 1e-12 {
		f.Noise = diffSD / sd
	}

	var sx, sy, sxx, sxy float64
	for i, v := range values {
		x := float64(i)
		sx += x
		sy += v
		sxx += x * x
		sxy += x * v
	}
	fn := float64(n)
	den := fn*sxx - sx*sx
	if den != 0 && sd > 1e-12 {
		slope := (fn*sxy - sx*sy) / den
		f.Trend = math.Abs(slope) * fn / sd
	}

	med := median(values)
	dev := make([]float64, n)
	for i, v := range values {
		dev[i] = math.Abs(v - med)
	}
	mad := median(dev)
	if mad > 1e-12 {
		spikes := 0
		for _, d := range dev {
			if d > 3*1.4826*mad {
				spikes++
			}
		}
		f.Spikiness = float64(spikes) / fn
	}

	best, lag := acfPeak(values, mean, m2, n)
	f.Seasonality = best
	f.LogPeriod = math.Log1p(float64(lag))
	return f
}

func acfPeak(values []float64, mean, variance float64, n int) (float64, int) {
	if variance <= 1e-18 {
		return 0, 0
	}
	maxLag := n / 3
	if maxLag > 600 {
		maxLag = 600
	}
	if maxLag < 3 {
		return 0, 0
	}
	best, bestLag := 0.0, 0
	prev, prevPrev := 0.0, 0.0
	for lag := 1; lag <= maxLag; lag++ {
		var s float64
		for i := lag; i < n; i++ {
			s += (values[i] - mean) * (values[i-lag] - mean)
		}
		r := s / (float64(n) * variance)
		if lag >= 3 && prev > prevPrev && prev >= r && prev > best {
			best, bestLag = prev, lag-1
		}
		prevPrev, prev = prev, r
	}
	if best < 0 {
		best = 0
	}
	if best > 1 {
		best = 1
	}
	return best, bestLag
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	c := append([]float64(nil), xs...)
	sort.Float64s(c)
	m := len(c) / 2
	if len(c)%2 == 1 {
		return c[m]
	}
	return (c[m-1] + c[m]) / 2
}
