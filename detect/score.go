package detect

import "math"

const maxSeverity = 12.0

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
