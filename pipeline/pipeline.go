package pipeline

import (
	"fmt"
	"math"

	"github.com/urfan03/semeion/evt"
	"github.com/urfan03/semeion/fuse"
	"github.com/urfan03/semeion/guard"
	"github.com/urfan03/semeion/hst"
	"github.com/urfan03/semeion/mp"
	"github.com/urfan03/semeion/prep"
	"github.com/urfan03/semeion/shape"
)

type Sensitivity string

const (
	Sensitive Sensitivity = "sensitive"
	Balanced  Sensitivity = "balanced"
	Precise   Sensitivity = "precise"
	Paranoid  Sensitivity = "paranoid"
)

type Options struct {
	Sensitivity Sensitivity
	History     int
	Calibration int
	Refresh     int
	Period      int
	Deseasonal  bool
	Budget      guard.Budget
	MinEffect   float64
	MinDuration int
	Q           float64
	Seed        uint64
}

const (
	defaultHistory  = 4000
	minHistory      = 400
	maxHistory      = 50_000
	defaultRefresh  = 32
	defaultQ        = 1e-3
	dampWindow      = 16
	refractoryScale = 60
)

func (o Options) resolve() (Options, error) {
	if o.Sensitivity == "" {
		o.Sensitivity = Balanced
	}
	if _, ok := guard.Presets()[string(o.Sensitivity)]; !ok {
		return o, fmt.Errorf("pipeline: unknown sensitivity %q", o.Sensitivity)
	}
	if o.History <= 0 {
		o.History = defaultHistory
	}
	if o.History < minHistory {
		return o, fmt.Errorf("pipeline: history %d is below the %d needed to calibrate", o.History, minHistory)
	}
	if o.History > maxHistory {
		return o, fmt.Errorf("pipeline: history %d exceeds the %d cap", o.History, maxHistory)
	}
	if o.Calibration <= 0 {
		o.Calibration = o.History / 4
	}
	if o.Calibration < 200 {
		o.Calibration = 200
	}
	if o.Calibration >= o.History {
		return o, fmt.Errorf("pipeline: calibration %d must be shorter than history %d", o.Calibration, o.History)
	}
	if o.Refresh <= 0 {
		o.Refresh = defaultRefresh
	}
	if o.Q <= 0 || o.Q >= 1 {
		o.Q = defaultQ
	}
	if o.MinDuration < 0 {
		o.MinDuration = 0
	}
	if o.Seed == 0 {
		o.Seed = 0x5e1
	}
	return o, nil
}

type Alarm struct {
	Start    int        `json:"start"`
	End      int        `json:"end"`
	Score    float64    `json:"score"`
	P        float64    `json:"p_value"`
	Level    float64    `json:"level"`
	Baseline float64    `json:"baseline"`
	Effect   float64    `json:"effect"`
	Shape    shape.Kind `json:"shape"`
	Duration int        `json:"duration"`
	Reason   string     `json:"reason"`
}

type Detector struct {
	opt      Options
	policy   guard.Options
	hist     []float64
	base     int
	seen     int
	sinceRun int
	emitted  int
	period   int
}

func New(opt Options) (*Detector, error) {
	opt, err := opt.resolve()
	if err != nil {
		return nil, err
	}
	policy := guard.Presets()[string(opt.Sensitivity)]
	if opt.MinDuration > 0 && policy.Persist < 2 {
		policy.Persist, policy.Of = 2, 10
	}
	return &Detector{
		opt: opt, policy: policy,
		hist:    make([]float64, 0, opt.History),
		emitted: -1,
	}, nil
}

func (d *Detector) Options() Options { return d.opt }

func (d *Detector) Seen() int { return d.seen }

func (d *Detector) Ready() bool { return len(d.hist) > d.opt.Calibration }

func (d *Detector) Period() int { return d.period }

func (d *Detector) push(v float64) {
	if len(d.hist) == d.opt.History {
		copy(d.hist, d.hist[1:])
		d.hist = d.hist[:len(d.hist)-1]
		d.base++
	}
	d.hist = append(d.hist, v)
	d.seen++
	d.sinceRun++
}

// Push appends one observation and returns any alarms this point newly confirms.
// Confirmation is inherently late — a persistence policy cannot rule on a point
// until it has seen the points after it — so an alarm's Start may sit behind the
// point just pushed. Indices are absolute across the whole stream. The pipeline
// re-scores its window every Refresh points rather than on every point, which
// bounds the per-point cost; alarms are therefore reported within Refresh points
// of confirmation.
func (d *Detector) Push(v float64) []Alarm {
	d.push(v)
	if !d.Ready() || d.sinceRun < d.opt.Refresh {
		return nil
	}
	d.sinceRun = 0

	var fresh []Alarm
	for _, a := range d.evaluate() {
		if a.Start <= d.emitted {
			continue
		}
		fresh = append(fresh, a)
	}
	for _, a := range fresh {
		if a.Start > d.emitted {
			d.emitted = a.Start
		}
	}
	return fresh
}

// Scan scores a whole window in one pass and returns every alarm in it. Use it
// for backfill and evaluation; Push is the streaming path.
func (d *Detector) Scan(values []float64) []Alarm {
	d.hist = d.hist[:0]
	d.base, d.seen, d.sinceRun, d.emitted = 0, 0, 0, -1
	for _, v := range values {
		if len(d.hist) == d.opt.History {
			copy(d.hist, d.hist[1:])
			d.hist = d.hist[:len(d.hist)-1]
			d.base++
		}
		d.hist = append(d.hist, v)
		d.seen++
	}
	if !d.Ready() {
		return nil
	}
	return d.evaluate()
}

func (d *Detector) prepared() []float64 {
	if !d.opt.Deseasonal {
		return d.hist
	}
	resid, period := prep.Deseasonalize(d.hist, prep.Options{Period: d.opt.Period})
	d.period = period
	if period == 0 {
		return d.hist
	}
	return resid
}

func floorP(p []float64, n int) []float64 {
	if n < 2 {
		return p
	}
	floor := 1 / (10 * float64(n))
	for i, v := range p {
		if math.IsNaN(v) || v < floor {
			p[i] = floor
			continue
		}
		if v > 1 {
			p[i] = 1
		}
	}
	return p
}

func (d *Detector) pvalues(work []float64) []float64 {
	warm := d.opt.Calibration
	n := len(work)
	streams := [][]float64{
		floorP(evt.TwoSidedProbabilities(work, evt.StreamOptions{Calibration: warm, Drift: true}), n),
		floorP(fuse.TrimmedPValues(mp.DAMP(work, mp.DAMPOptions{Window: dampWindow}), warm, 0.02), n),
		floorP(fuse.TrimmedPValues(hst.Series(work, hst.SeriesOptions{Options: hst.Options{Seed: d.opt.Seed}}), warm, 0.02), n),
	}
	return floorP(fuse.CauchyStreams(streams, nil), n)
}

func (d *Detector) evaluate() []Alarm {
	work := d.prepared()
	p := d.pvalues(work)
	if len(p) != len(d.hist) {
		return nil
	}
	scores := fuse.NegLog10(p)

	policy := d.policy
	policy.Warmup = d.opt.Calibration
	if policy.Refractory == 0 && d.opt.Sensitivity != Sensitive {
		policy.Refractory = refractoryScale
	}
	if d.opt.Budget.Alarms > 0 && d.opt.Budget.Per > 0 {
		policy.Threshold = guard.SolveThreshold(scores, d.opt.Budget, policy)
	} else {
		z, _, ok := evt.POT(scores, evt.Options{Q: d.opt.Q, Level: 0.98})
		if !ok {
			return nil
		}
		policy.Threshold = z
	}

	fired := guard.Apply(scores, policy)
	base, scaleOf := guard.RollingBaseline(d.hist, d.opt.Calibration/2)
	if d.opt.MinEffect > 0 {
		fired = guard.GateByEffect(fired, guard.Effect{
			Values: d.hist, Baseline: base, Scale: scaleOf, MinRel: d.opt.MinEffect,
		})
	}

	marks := make([]float64, len(fired))
	for i, f := range fired {
		if f {
			marks[i] = 1
		}
	}
	dev := make([]float64, len(d.hist))
	for i := range dev {
		if scaleOf[i] > 0 {
			dev[i] = math.Abs(d.hist[i]-base[i]) / scaleOf[i]
		}
	}

	ctx := d.opt.Calibration / 2
	var out []Alarm
	for _, c := range guard.Candidates(marks, 1, 10) {
		start, end := locate(dev, c.Start, c.End, ctx)
		length := end - start + 1
		if length < d.opt.MinDuration {
			continue
		}
		cls := shape.Classify(d.hist, start, end, shape.Options{Context: ctx})
		if d.opt.MinDuration > 0 && cls.Kind == shape.Unknown {
			continue
		}
		scale := scaleOf[start]
		effect := 0.0
		if scale > 0 {
			effect = math.Abs(cls.During-cls.Before) / scale
		}
		peak := 0.0
		for i := c.Start; i <= c.End && i < len(scores); i++ {
			if scores[i] > peak {
				peak = scores[i]
			}
		}
		out = append(out, Alarm{
			Start: d.base + start, End: d.base + end,
			Score: peak, P: math.Pow(10, -peak),
			Level: cls.During, Baseline: cls.Before, Effect: effect,
			Shape: cls.Kind, Duration: length,
			Reason: reason(cls.Kind, length, effect),
		})
	}
	return out
}

// locate moves an alarm from the point the score crossed to the region a person
// would look at. A level shift makes both of its edges look like changes, so the
// detector can fire on the recovery, where the value is already back to normal.
// Anchoring on the largest deviation near the crossing and growing while the
// deviation stays comparable reports the elevated stretch instead.
func locate(dev []float64, start, end, search int) (int, int) {
	lo := start - search
	if lo < 0 {
		lo = 0
	}
	hi := end + search
	if hi > len(dev)-1 {
		hi = len(dev) - 1
	}
	anchor, best := start, 0.0
	for i := lo; i <= hi; i++ {
		if dev[i] > best {
			best, anchor = dev[i], i
		}
	}
	if best <= 0 {
		return start, end
	}
	cut := best / 2
	a, b := anchor, anchor
	for a > lo && dev[a-1] >= cut {
		a--
	}
	for b < hi && dev[b+1] >= cut {
		b++
	}
	if a > start {
		a = start
	}
	if b < end {
		b = end
	}
	return a, b
}

func reason(k shape.Kind, duration int, effect float64) string {
	return fmt.Sprintf("%s lasting %d samples, %.1f robust deviations from baseline", k, duration, effect)
}
