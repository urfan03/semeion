package conformal

import (
	"math"
	"sort"
)

type BlockCalibrator struct {
	windows []int
	byWin   map[int]*Calibrator
	alpha   float64
}

func blockWindows(maxWindow int) []int {
	if maxWindow < 1 {
		maxWindow = 1
	}
	var out []int
	for w := 1; w <= maxWindow; w *= 2 {
		out = append(out, w)
	}
	if out[len(out)-1] != maxWindow {
		out = append(out, maxWindow)
	}
	return out
}

func slidingMax(scores []float64, window int) []float64 {
	if window < 1 {
		window = 1
	}
	if len(scores) < window {
		return nil
	}
	out := make([]float64, 0, len(scores)-window+1)
	for i := 0; i+window <= len(scores); i++ {
		best := math.Inf(-1)
		for k := i; k < i+window; k++ {
			v := scores[k]
			if math.IsNaN(v) || math.IsInf(v, 0) {
				continue
			}
			if v > best {
				best = v
			}
		}
		if math.IsInf(best, -1) {
			continue
		}
		out = append(out, best)
	}
	return out
}

func NewBlock(calibration []float64, maxWindow int, alpha, trim float64) *BlockCalibrator {
	if alpha <= 0 || alpha >= 1 {
		alpha = 0.01
	}
	b := &BlockCalibrator{byWin: map[int]*Calibrator{}, alpha: alpha}
	for _, w := range blockWindows(maxWindow) {
		maxes := slidingMax(calibration, w)
		if len(maxes) == 0 {
			continue
		}
		b.windows = append(b.windows, w)
		b.byWin[w] = NewTrimmed(maxes, alpha, trim)
	}
	sort.Ints(b.windows)
	return b
}

func (b *BlockCalibrator) Windows() []int { return b.windows }

func (b *BlockCalibrator) pick(runLen int) *Calibrator {
	if len(b.windows) == 0 {
		return nil
	}
	if runLen < 1 {
		runLen = 1
	}
	best := b.windows[0]
	for _, w := range b.windows {
		if w <= runLen {
			best = w
			continue
		}
		break
	}
	return b.byWin[best]
}

func (b *BlockCalibrator) P(runMax float64, runLen int) float64 {
	c := b.pick(runLen)
	if c == nil {
		return 1
	}
	return c.P(runMax)
}

func (b *BlockCalibrator) Alarm(runMax float64, runLen int) bool {
	return b.P(runMax, runLen) <= b.alpha
}

func (b *BlockCalibrator) Threshold(runLen int) float64 {
	c := b.pick(runLen)
	if c == nil {
		return math.Inf(1)
	}
	return c.Threshold()
}

func (b *BlockCalibrator) Size(runLen int) int {
	c := b.pick(runLen)
	if c == nil {
		return 0
	}
	return c.Size()
}
