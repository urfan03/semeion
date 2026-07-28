package evt

import (
	"math"
	"sort"
)

type SPOT struct {
	opt   Options
	init  float64
	zq    float64
	peaks []float64
	fit   GPD
	n     int
	nt    int
	since int
	ready bool
}

func NewSPOT(opt Options) *SPOT {
	return &SPOT{opt: opt.withDefaults()}
}

func (s *SPOT) Calibrate(initial []float64) bool {
	clean := make([]float64, 0, len(initial))
	for _, v := range initial {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			clean = append(clean, v)
		}
	}
	if len(clean) < s.opt.MinPeaks*2 {
		return false
	}
	sorted := append([]float64(nil), clean...)
	sort.Float64s(sorted)
	s.init = quantileOf(sorted, s.opt.Level)
	s.peaks = s.peaks[:0]
	for _, v := range clean {
		if v > s.init {
			s.peaks = append(s.peaks, v-s.init)
		}
	}
	s.n = len(clean)
	s.nt = len(s.peaks)
	if s.nt < s.opt.MinPeaks {
		return false
	}
	if s.opt.MaxPeaks > 0 && len(s.peaks) > s.opt.MaxPeaks {
		s.peaks = append(s.peaks[:0], s.peaks[len(s.peaks)-s.opt.MaxPeaks:]...)
	}
	g, ok := FitGPD(s.peaks)
	if !ok {
		return false
	}
	s.fit = g
	s.zq = Quantile(g, s.init, s.opt.Q, s.n, s.nt)
	s.ready = true
	return true
}

func (s *SPOT) Ready() bool { return s.ready }

func (s *SPOT) Threshold() float64 { return s.zq }

func (s *SPOT) Initial() float64 { return s.init }

func (s *SPOT) Fit() GPD { return s.fit }

func (s *SPOT) Peaks() int { return s.nt }

func (s *SPOT) Step(x float64) bool {
	if !s.ready || math.IsNaN(x) || math.IsInf(x, 0) {
		return false
	}
	if x > s.zq {
		return true
	}
	s.n++
	if x > s.init {
		s.peaks = append(s.peaks, x-s.init)
		s.nt++
		s.since++
		if s.opt.MaxPeaks > 0 && len(s.peaks) > s.opt.MaxPeaks {
			s.peaks = append(s.peaks[:0], s.peaks[len(s.peaks)-s.opt.MaxPeaks:]...)
		}
		if s.since >= s.opt.RefitEvery {
			s.since = 0
			if g, ok := FitGPD(s.peaks); ok {
				s.fit = g
			}
		}
		s.zq = Quantile(s.fit, s.init, s.opt.Q, s.n, s.nt)
	}
	return false
}

type DSPOT struct {
	spot  *SPOT
	depth int
	win   []float64
	idx   int
	full  bool
	sum   float64
}

func NewDSPOT(opt Options, depth int) *DSPOT {
	if depth < 1 {
		depth = 10
	}
	return &DSPOT{spot: NewSPOT(opt), depth: depth, win: make([]float64, depth)}
}

func (d *DSPOT) push(x float64) {
	if d.full {
		d.sum -= d.win[d.idx]
	}
	d.win[d.idx] = x
	d.sum += x
	d.idx++
	if d.idx >= d.depth {
		d.idx = 0
		d.full = true
	}
}

func (d *DSPOT) local() float64 {
	n := d.depth
	if !d.full {
		n = d.idx
	}
	if n == 0 {
		return 0
	}
	return d.sum / float64(n)
}

func (d *DSPOT) Calibrate(initial []float64) bool {
	if len(initial) <= d.depth {
		return false
	}
	d.idx, d.full, d.sum = 0, false, 0
	for i := 0; i < d.depth; i++ {
		d.push(initial[i])
	}
	res := make([]float64, 0, len(initial)-d.depth)
	for i := d.depth; i < len(initial); i++ {
		res = append(res, initial[i]-d.local())
		d.push(initial[i])
	}
	return d.spot.Calibrate(res)
}

func (d *DSPOT) Ready() bool { return d.spot.Ready() }

func (d *DSPOT) Threshold() float64 { return d.local() + d.spot.Threshold() }

func (d *DSPOT) Step(x float64) bool {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return false
	}
	m := d.local()
	if d.spot.Step(x - m) {
		return true
	}
	d.push(x)
	return false
}

type StreamOptions struct {
	Options
	Calibration int
	Depth       int
	Drift       bool
}

func (o StreamOptions) withDefaults() StreamOptions {
	o.Options = o.Options.withDefaults()
	if o.Calibration <= 0 {
		o.Calibration = 300
	}
	if o.Depth <= 0 {
		o.Depth = 24
	}
	return o
}

func Stream(values []float64, opt StreamOptions) ([]bool, []float64) {
	opt = opt.withDefaults()
	alarms := make([]bool, len(values))
	thresholds := make([]float64, len(values))
	if len(values) <= opt.Calibration {
		return alarms, thresholds
	}
	calib := values[:opt.Calibration]

	if opt.Drift {
		d := NewDSPOT(opt.Options, opt.Depth)
		if !d.Calibrate(calib) {
			return alarms, thresholds
		}
		for i := opt.Calibration; i < len(values); i++ {
			thresholds[i] = d.Threshold()
			alarms[i] = d.Step(values[i])
		}
		return alarms, thresholds
	}

	s := NewSPOT(opt.Options)
	if !s.Calibrate(calib) {
		return alarms, thresholds
	}
	for i := opt.Calibration; i < len(values); i++ {
		thresholds[i] = s.Threshold()
		alarms[i] = s.Step(values[i])
	}
	return alarms, thresholds
}

func Excess(values []float64, opt StreamOptions) []float64 {
	opt = opt.withDefaults()
	out := make([]float64, len(values))
	_, thresholds := Stream(values, opt)
	for i, v := range values {
		if thresholds[i] == 0 {
			continue
		}
		scale := math.Abs(thresholds[i])
		if scale < 1e-9 {
			scale = 1e-9
		}
		out[i] = (v - thresholds[i]) / scale
	}
	return out
}
