package benchmark

import (
	"math"
	"sort"
)

type Bias string

const (
	BiasFlat   Bias = "flat"
	BiasFront  Bias = "front"
	BiasBack   Bias = "back"
	BiasMiddle Bias = "middle"
)

type RangeOptions struct {
	Alpha float64
	Bias  Bias
}

func (o RangeOptions) withDefaults() RangeOptions {
	if o.Alpha < 0 || o.Alpha > 1 {
		o.Alpha = 0
	}
	if o.Bias == "" {
		o.Bias = BiasFlat
	}
	return o
}

func biasWeight(b Bias, i, n int) float64 {
	switch b {
	case BiasFront:
		return float64(n - i)
	case BiasBack:
		return float64(i + 1)
	case BiasMiddle:
		mid := float64(n-1) / 2
		return float64(n)/2 - math.Abs(float64(i)-mid)
	default:
		return 1
	}
}

func overlapSize(a, b [2]int) int {
	lo := a[0]
	if b[0] > lo {
		lo = b[0]
	}
	hi := a[1]
	if b[1] < hi {
		hi = b[1]
	}
	if hi < lo {
		return 0
	}
	return hi - lo + 1
}

func omega(r [2]int, overlaps [][2]int, b Bias) float64 {
	n := r[1] - r[0] + 1
	if n <= 0 {
		return 0
	}
	var total, hit float64
	for i := 0; i < n; i++ {
		w := biasWeight(b, i, n)
		total += w
		pos := r[0] + i
		for _, o := range overlaps {
			if pos >= o[0] && pos <= o[1] {
				hit += w
				break
			}
		}
	}
	if total == 0 {
		return 0
	}
	return hit / total
}

func rangeSide(subject, other [][2]int, alpha float64, b Bias) float64 {
	if len(subject) == 0 {
		return 0
	}
	var total float64
	for _, r := range subject {
		var hits [][2]int
		for _, o := range other {
			if overlapSize(r, o) > 0 {
				hits = append(hits, o)
			}
		}
		existence := 0.0
		if len(hits) > 0 {
			existence = 1
		}
		cardinality := 1.0
		if len(hits) > 1 {
			cardinality = 1 / float64(len(hits))
		}
		total += alpha*existence + (1-alpha)*cardinality*omega(r, hits, b)
	}
	return total / float64(len(subject))
}

func RangeRecall(pred, labels []bool, opt RangeOptions) float64 {
	opt = opt.withDefaults()
	return rangeSide(Segments(labels), Segments(pred), opt.Alpha, opt.Bias)
}

func RangePrecision(pred, labels []bool, opt RangeOptions) float64 {
	opt = opt.withDefaults()
	return rangeSide(Segments(pred), Segments(labels), 0, opt.Bias)
}

func RangeF1(pred, labels []bool, opt RangeOptions) (precision, recall, f1 float64) {
	precision = RangePrecision(pred, labels, opt)
	recall = RangeRecall(pred, labels, opt)
	if precision+recall > 0 {
		f1 = 2 * precision * recall / (precision + recall)
	}
	return precision, recall, f1
}

func BestRangeF1(scores []float64, labels []bool, opt RangeOptions) (precision, recall, f1, threshold float64) {
	threshold = math.Inf(1)
	pred := make([]bool, len(labels))
	for _, thr := range sweepThresholds(scores) {
		for i := range pred {
			pred[i] = i < len(scores) && scores[i] >= thr
		}
		p, r, v := RangeF1(pred, labels, opt)
		if v > f1 {
			precision, recall, f1, threshold = p, r, v, thr
		}
	}
	if f1 == 0 {
		threshold = math.Inf(1)
	}
	return precision, recall, f1, threshold
}

func PointAdjustK(pred, labels []bool, k float64) []bool {
	if k <= 0 {
		return PointAdjust(pred, labels)
	}
	out := make([]bool, len(labels))
	copy(out, pred)
	for _, seg := range Segments(labels) {
		n := seg[1] - seg[0] + 1
		hits := 0
		for i := seg[0]; i <= seg[1] && i < len(pred); i++ {
			if pred[i] {
				hits++
			}
		}
		if float64(hits) < k*float64(n) {
			continue
		}
		for i := seg[0]; i <= seg[1] && i < len(out); i++ {
			out[i] = true
		}
	}
	return out
}

func BestPointAdjustedKF1(scores []float64, labels []bool, k float64) (ScoreResult, float64) {
	best, bestThr := ScoreResult{}, math.Inf(1)
	pred := make([]bool, len(labels))
	for _, thr := range sweepThresholds(scores) {
		for i := range pred {
			pred[i] = i < len(scores) && scores[i] >= thr
		}
		r := Confusion(PointAdjustK(pred, labels, k), labels)
		if r.F1 > best.F1 {
			best, bestThr = r, thr
		}
	}
	if best.F1 == 0 {
		bestThr = math.Inf(1)
	}
	return best, bestThr
}

func bufferedLabels(labels []bool, buffer int) []float64 {
	y := make([]float64, len(labels))
	for i, v := range labels {
		if v {
			y[i] = 1
		}
	}
	if buffer <= 0 {
		return y
	}
	half := buffer
	for _, seg := range Segments(labels) {
		for d := 1; d <= half; d++ {
			w := math.Sin(math.Pi/2*(1-float64(d)/float64(half+1))) * 1
			if before := seg[0] - d; before >= 0 && w > y[before] {
				y[before] = w
			}
			if after := seg[1] + d; after < len(y) && w > y[after] {
				y[after] = w
			}
		}
	}
	return y
}

type curvePoint struct {
	tpr, fpr, precision, recall float64
}

func rangeCurve(scores []float64, labels []bool, buffer int) []curvePoint {
	n := len(labels)
	y := bufferedLabels(labels, buffer)
	segs := Segments(labels)

	var weightedP, weightedN float64
	for _, v := range y {
		weightedP += v
		weightedN += 1 - v
	}
	origP := 0.0
	for _, v := range labels {
		if v {
			origP++
		}
	}
	if weightedP == 0 || weightedN == 0 || origP == 0 || len(segs) == 0 {
		return nil
	}

	owner := make([]int, n)
	for i := range owner {
		owner[i] = -1
	}
	for si, s := range segs {
		for k := s[0]; k <= s[1] && k < n; k++ {
			owner[k] = si
		}
	}

	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return scores[idx[a]] > scores[idx[b]] })

	segHits := make([]int, len(segs))
	touched := 0
	out := make([]curvePoint, 0, len(idx)+1)
	out = append(out, curvePoint{0, 0, 1, 0})
	var tp, flagged float64
	for i := 0; i < n; {
		j := i
		for j < n && scores[idx[j]] == scores[idx[i]] {
			p := idx[j]
			tp += y[p]
			flagged++
			if s := owner[p]; s >= 0 {
				if segHits[s] == 0 {
					touched++
				}
				segHits[s]++
			}
			j++
		}
		i = j

		existence := float64(touched) / float64(len(segs))
		adjTP := tp + existence*origP
		adjP := weightedP + origP
		fp := flagged - tp

		tpr := adjTP / adjP
		fpr := fp / weightedN
		precision := 1.0
		if adjTP+fp > 0 {
			precision = adjTP / (adjTP + fp)
		}
		out = append(out, curvePoint{tpr, fpr, precision, tpr})
	}
	return out
}

func RangeAUC(scores []float64, labels []bool, buffer int) (roc, pr float64) {
	pts := rangeCurve(scores, labels, buffer)
	if len(pts) < 2 {
		return 0, 0
	}
	for i := 1; i < len(pts); i++ {
		dFPR := pts[i].fpr - pts[i-1].fpr
		roc += dFPR * (pts[i].tpr + pts[i-1].tpr) / 2
		dRecall := pts[i].recall - pts[i-1].recall
		pr += dRecall * pts[i].precision
	}
	return clamp01(roc), clamp01(pr)
}

func VUS(scores []float64, labels []bool, maxBuffer int) (vusROC, vusPR float64) {
	if maxBuffer < 0 {
		maxBuffer = 0
	}
	var rocSum, prSum float64
	count := 0
	for b := 0; b <= maxBuffer; b++ {
		roc, pr := RangeAUC(scores, labels, b)
		rocSum += roc
		prSum += pr
		count++
	}
	if count == 0 {
		return 0, 0
	}
	return rocSum / float64(count), prSum / float64(count)
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
