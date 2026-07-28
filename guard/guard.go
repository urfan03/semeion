package guard

import "math"

type Window struct {
	Start int
	End   int
}

func (w Window) covers(i int) bool { return i >= w.Start && i <= w.End }

type Options struct {
	Threshold  float64
	Persist    int
	Of         int
	Refractory int
	Warmup     int
	Cooldown   float64
	Suppress   []Window
}

func (o Options) resolve() Options {
	if o.Persist < 1 {
		o.Persist = 1
	}
	if o.Of < o.Persist {
		o.Of = o.Persist
	}
	if o.Refractory < 0 {
		o.Refractory = 0
	}
	if o.Warmup < 0 {
		o.Warmup = 0
	}
	if o.Threshold == 0 {
		o.Threshold = math.Inf(1)
	}
	return o
}

type Guard struct {
	opt      Options
	ring     []bool
	at       int
	filled   int
	hot      int
	quiet    int
	seen     int
	penalty  float64
	lastFire int
}

func New(opt Options) *Guard {
	opt = opt.resolve()
	return &Guard{opt: opt, ring: make([]bool, opt.Of), lastFire: -1}
}

func (g *Guard) Threshold() float64 { return g.opt.Threshold + g.penalty }

func (g *Guard) Penalize(step, max float64) {
	g.penalty += step
	if max > 0 && g.penalty > max {
		g.penalty = max
	}
}

func (g *Guard) ClearPenalty() { g.penalty = 0 }

func (g *Guard) Seen() int { return g.seen }

func (g *Guard) push(over bool) {
	if g.filled == g.opt.Of && g.ring[g.at] {
		g.hot--
	}
	g.ring[g.at] = over
	if over {
		g.hot++
	}
	g.at = (g.at + 1) % g.opt.Of
	if g.filled < g.opt.Of {
		g.filled++
	}
}

func (g *Guard) Step(score float64) bool {
	i := g.seen
	g.seen++

	suppressed := false
	for _, w := range g.opt.Suppress {
		if w.covers(i) {
			suppressed = true
			break
		}
	}

	over := !suppressed && !math.IsNaN(score) && score >= g.Threshold()
	g.push(over)

	if g.quiet > 0 {
		g.quiet--
		return false
	}
	if suppressed || i < g.opt.Warmup || g.filled < g.opt.Persist {
		return false
	}
	if g.hot < g.opt.Persist {
		return false
	}
	if g.opt.Cooldown > 0 && g.lastFire >= 0 && score < g.opt.Cooldown*g.Threshold() {
		return false
	}
	g.lastFire = i
	g.quiet = g.opt.Refractory
	return true
}

func Apply(scores []float64, opt Options) []bool {
	g := New(opt)
	out := make([]bool, len(scores))
	for i, s := range scores {
		out[i] = g.Step(s)
	}
	return out
}

type Alarm struct {
	Index int     `json:"index"`
	Score float64 `json:"score"`
}

func Alarms(scores []float64, opt Options) []Alarm {
	fired := Apply(scores, opt)
	var out []Alarm
	for i, f := range fired {
		if f {
			out = append(out, Alarm{Index: i, Score: scores[i]})
		}
	}
	return out
}

func Sensitive() Options { return Options{} }

func Balanced() Options { return Options{Refractory: 60} }

func Precise() Options { return Options{Persist: 2, Of: 10} }

func Paranoid() Options { return Options{Persist: 2, Of: 10, Refractory: 60} }

func Presets() map[string]Options {
	return map[string]Options{
		"sensitive": Sensitive(),
		"balanced":  Balanced(),
		"precise":   Precise(),
		"paranoid":  Paranoid(),
	}
}

func SuppressAround(centres []int, before, after int) []Window {
	out := make([]Window, 0, len(centres))
	for _, c := range centres {
		lo := c - before
		if lo < 0 {
			lo = 0
		}
		out = append(out, Window{Start: lo, End: c + after})
	}
	return out
}
