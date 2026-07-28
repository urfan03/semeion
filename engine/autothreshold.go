package engine

import (
	"github.com/urfan03/semeion/evt"
	"github.com/urfan03/semeion/jobspec"
)

type autoThreshold struct {
	cfg  jobspec.AutoThreshold
	spot *evt.SPOT
	warm []float64
}

func newAutoThreshold(cfg *jobspec.AutoThreshold) *autoThreshold {
	if cfg == nil {
		return nil
	}
	return &autoThreshold{cfg: cfg.Normalized()}
}

func (a *autoThreshold) clamp(v float64) float64 {
	if v < a.cfg.Min {
		return a.cfg.Min
	}
	if v > a.cfg.Max {
		return a.cfg.Max
	}
	return v
}

func (a *autoThreshold) observe(score float64) (float64, bool) {
	if a.spot == nil {
		a.warm = append(a.warm, score)
		if len(a.warm) < a.cfg.Calibration {
			return 0, false
		}
		s := evt.NewSPOT(evt.Options{Q: a.cfg.Q, Level: a.cfg.Level})
		if !s.Calibrate(a.warm) {
			if len(a.warm) >= 8*a.cfg.Calibration {
				a.warm = a.warm[len(a.warm)-4*a.cfg.Calibration:]
			}
			return 0, false
		}
		a.spot = s
		a.warm = nil
		return a.clamp(s.Threshold()), true
	}
	a.spot.Step(score)
	return a.clamp(a.spot.Threshold()), true
}

func (e *Engine) admits(score float64) bool {
	if e.autoThresh != nil && score > e.rawMax {
		e.rawMax = score
	}
	return score >= e.threshold
}

func (e *Engine) Threshold() float64 { return e.threshold }

func (e *Engine) observeThreshold() {
	if e.autoThresh == nil {
		return
	}
	raw := e.rawMax
	e.rawMax = 0
	if t, ok := e.autoThresh.observe(raw); ok {
		e.threshold = t
	}
}

func (e *Engine) AutoThresholdActive() bool {
	return e.autoThresh != nil && e.autoThresh.spot != nil
}

func (a *autoThreshold) snapshot() []byte {
	if a == nil || a.spot == nil {
		return nil
	}
	b, err := a.spot.Snapshot()
	if err != nil {
		return nil
	}
	return b
}

func (a *autoThreshold) restore(b []byte) {
	if a == nil || len(b) == 0 {
		return
	}
	if s, err := evt.RestoreSPOT(b); err == nil {
		a.spot = s
		a.warm = nil
	}
}
