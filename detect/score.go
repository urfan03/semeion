// Package detect turns bucketed values into scored anomalies: a per-series
// streaming baseline model + the probability→score normalisation.
package detect

import "math"

// maxSeverity caps the score curve: a tail probability of 1e-12 maps to 100.
const maxSeverity = 12.0 // = -log10(1e-12)

// scoreFromProbability maps a tail probability p ∈ [0,1] to a 0..100 anomaly
// score using severity = -log10(p), scaled by maxSeverity. This is monotone
// (smaller p → higher score), bounded, and gives an intuitive curve:
//
//	p=0.01 → ~17    p=1e-4 → ~33    p=1e-6 → ~50    p=1e-9 → ~75    p≤1e-12 → 100
func scoreFromProbability(p float64) float64 {
	if p <= 0 {
		return 100
	}
	if p >= 1 {
		return 0
	}
	score := (-math.Log10(p)) / maxSeverity * 100
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}
