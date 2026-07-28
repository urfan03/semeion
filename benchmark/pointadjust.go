package benchmark

import (
	"math"
	"sort"
)

func Segments(labels []bool) [][2]int {
	var out [][2]int
	start := -1
	for i, v := range labels {
		if v && start < 0 {
			start = i
		} else if !v && start >= 0 {
			out = append(out, [2]int{start, i - 1})
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, [2]int{start, len(labels) - 1})
	}
	return out
}

func PointAdjust(pred, labels []bool) []bool {
	out := make([]bool, len(labels))
	copy(out, pred)
	for _, seg := range Segments(labels) {
		hit := false
		for i := seg[0]; i <= seg[1] && i < len(pred); i++ {
			if pred[i] {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		for i := seg[0]; i <= seg[1] && i < len(out); i++ {
			out[i] = true
		}
	}
	return out
}

func Confusion(pred, labels []bool) ScoreResult {
	var res ScoreResult
	for i := range labels {
		p := i < len(pred) && pred[i]
		switch {
		case labels[i] && p:
			res.TP++
		case labels[i]:
			res.FN++
		case p:
			res.FP++
		}
	}
	if res.TP+res.FP > 0 {
		res.Precision = float64(res.TP) / float64(res.TP+res.FP)
	}
	if res.TP+res.FN > 0 {
		res.Recall = float64(res.TP) / float64(res.TP+res.FN)
	}
	if res.Precision+res.Recall > 0 {
		res.F1 = 2 * res.Precision * res.Recall / (res.Precision + res.Recall)
	}
	return res
}

func PointAdjustedScore(pred, labels []bool) ScoreResult {
	return Confusion(PointAdjust(pred, labels), labels)
}

const maxSweep = 512

func sweepThresholds(scores []float64) []float64 {
	vals := make([]float64, 0, len(scores))
	for _, s := range scores {
		if !math.IsNaN(s) && !math.IsInf(s, 0) {
			vals = append(vals, s)
		}
	}
	if len(vals) == 0 {
		return nil
	}
	sort.Float64s(vals)
	uniq := vals[:1]
	for _, v := range vals[1:] {
		if v != uniq[len(uniq)-1] {
			uniq = append(uniq, v)
		}
	}
	if len(uniq) <= maxSweep {
		return uniq
	}
	out := make([]float64, 0, maxSweep)
	for i := 0; i < maxSweep; i++ {
		out = append(out, uniq[i*(len(uniq)-1)/(maxSweep-1)])
	}
	return out
}

func BestPointAdjustedF1(scores []float64, labels []bool) (ScoreResult, float64) {
	best, bestThr := ScoreResult{}, math.Inf(1)
	pred := make([]bool, len(labels))
	for _, thr := range sweepThresholds(scores) {
		for i := range pred {
			pred[i] = i < len(scores) && scores[i] >= thr
		}
		r := PointAdjustedScore(pred, labels)
		if r.F1 > best.F1 {
			best, bestThr = r, thr
		}
	}
	if best.F1 == 0 {
		bestThr = math.Inf(1)
	}
	return best, bestThr
}

func BestF1(scores []float64, labels []bool) (ScoreResult, float64) {
	best, bestThr := ScoreResult{}, math.Inf(1)
	pred := make([]bool, len(labels))
	for _, thr := range sweepThresholds(scores) {
		for i := range pred {
			pred[i] = i < len(scores) && scores[i] >= thr
		}
		r := Confusion(pred, labels)
		if r.F1 > best.F1 {
			best, bestThr = r, thr
		}
	}
	if best.F1 == 0 {
		bestThr = math.Inf(1)
	}
	return best, bestThr
}

func AUCPR(scores []float64, labels []bool) float64 {
	n := len(labels)
	if n == 0 || len(scores) < n {
		return 0
	}
	pos := 0
	for _, l := range labels {
		if l {
			pos++
		}
	}
	if pos == 0 || pos == n {
		return 0
	}
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return scores[idx[a]] > scores[idx[b]] })

	var tp, fp int
	var ap, prevRecall float64
	for i := 0; i < n; {
		j := i
		for j < n && scores[idx[j]] == scores[idx[i]] {
			if labels[idx[j]] {
				tp++
			} else {
				fp++
			}
			j++
		}
		recall := float64(tp) / float64(pos)
		precision := float64(tp) / float64(tp+fp)
		ap += (recall - prevRecall) * precision
		prevRecall = recall
		i = j
	}
	return ap
}

func PointAdjustedAUCPR(scores []float64, labels []bool) float64 {
	n := len(labels)
	if n == 0 || len(scores) < n {
		return 0
	}
	pos := 0
	for _, l := range labels {
		if l {
			pos++
		}
	}
	if pos == 0 || pos == n {
		return 0
	}
	thrs := sweepThresholds(scores)
	pred := make([]bool, n)
	type pt struct{ r, p float64 }
	pts := make([]pt, 0, len(thrs)+1)
	for i := len(thrs) - 1; i >= 0; i-- {
		for k := range pred {
			pred[k] = scores[k] >= thrs[i]
		}
		r := PointAdjustedScore(pred, labels)
		pts = append(pts, pt{r.Recall, r.Precision})
	}
	var ap, prevRecall float64
	for _, p := range pts {
		if p.r > prevRecall {
			ap += (p.r - prevRecall) * p.p
			prevRecall = p.r
		}
	}
	return ap
}
