package guard

import "math"

type Candidate struct {
	Start  int     `json:"start"`
	End    int     `json:"end"`
	Peak   int     `json:"peak"`
	Score  float64 `json:"score"`
	MinP   float64 `json:"min_p"`
	Length int     `json:"length"`
}

func Candidates(scores []float64, threshold float64, gap int) []Candidate {
	if gap < 0 {
		gap = 0
	}
	var out []Candidate
	i := 0
	for i < len(scores) {
		if math.IsNaN(scores[i]) || scores[i] < threshold {
			i++
			continue
		}
		start, end := i, i
		peak, best := i, scores[i]
		j := i
		for j < len(scores) {
			if !math.IsNaN(scores[j]) && scores[j] >= threshold {
				end = j
				if scores[j] > best {
					best, peak = scores[j], j
				}
				j++
				continue
			}
			quiet := 0
			k := j
			for k < len(scores) && quiet <= gap {
				if !math.IsNaN(scores[k]) && scores[k] >= threshold {
					break
				}
				quiet++
				k++
			}
			if k < len(scores) && quiet <= gap && !math.IsNaN(scores[k]) && scores[k] >= threshold {
				j = k
				continue
			}
			break
		}
		out = append(out, Candidate{Start: start, End: end, Peak: peak, Score: best, Length: end - start + 1})
		i = j + 1
	}
	return out
}

func CandidatesFromP(pvals []float64, alpha float64, gap int) []Candidate {
	scores := make([]float64, len(pvals))
	for i, p := range pvals {
		if math.IsNaN(p) {
			scores[i] = math.NaN()
			continue
		}
		if p <= 0 {
			p = 1e-300
		}
		scores[i] = -math.Log10(p)
	}
	out := Candidates(scores, -math.Log10(alpha), gap)
	for i := range out {
		out[i].MinP = math.Pow(10, -out[i].Score)
	}
	return out
}

func SidakP(minP float64, length int) float64 {
	if length < 1 {
		length = 1
	}
	if minP <= 0 {
		return 0
	}
	if minP >= 1 {
		return 1
	}
	p := 1 - math.Pow(1-minP, float64(length))
	if p > 1 {
		return 1
	}
	return p
}

func Mask(n int, cands []Candidate, accept []bool, peakOnly bool) []bool {
	out := make([]bool, n)
	for i, c := range cands {
		if i < len(accept) && !accept[i] {
			continue
		}
		if peakOnly {
			if c.Peak >= 0 && c.Peak < n {
				out[c.Peak] = true
			}
			continue
		}
		for k := c.Start; k <= c.End && k < n; k++ {
			out[k] = true
		}
	}
	return out
}
