// Package slo computes service-level-objective attainment and error-budget burn.
//
// This is the forward-looking half of the platform: detection and correlation
// say what is wrong now, SLO says how much of your reliability budget it is
// costing and when — at the current rate — you will run out. The maths is the
// standard Google SRE multi-window, multi-burn-rate model, done deterministically
// with no dependencies.
package slo

import (
	"math"
	"sort"
	"time"
)

// Target is the objective and the window it is measured over.
type Target struct {
	Objective float64       // e.g. 0.999 (three nines)
	Window    time.Duration // e.g. 30 * 24h
}

// Sample is the good/total event count in one bucket. Use Total=1, Good=0|1 for
// a boolean success stream, or real counts for aggregated buckets.
type Sample struct {
	Time  time.Time
	Good  float64
	Total float64
}

// BurnWindow is the error-budget burn rate over one lookback window.
type BurnWindow struct {
	Window   time.Duration `json:"window"`
	BurnRate float64       `json:"burn_rate"` // ×; 1.0 = exactly on budget for the SLO window
	ErrRate  float64       `json:"error_rate"`
	Samples  int           `json:"samples"`
}

// Report is the SLO state at evaluation time.
type Report struct {
	Objective       float64      `json:"objective"`
	SLI             float64      `json:"sli"`              // observed success ratio over the window
	ErrorBudget     float64      `json:"error_budget"`     // 1 - objective
	BudgetConsumed  float64      `json:"budget_consumed"`  // 0..1 (>1 = blown)
	BudgetRemaining float64      `json:"budget_remaining"` // 1 - consumed, floored at 0 semantics kept signed
	BurnRate        float64      `json:"burn_rate"`        // over the shortest window
	Windows         []BurnWindow `json:"windows"`
	Exhaustion      *time.Time   `json:"exhaustion,omitempty"` // projected budget-exhaustion time
	Severity        string       `json:"severity"`             // ok | warning | critical
	Now             time.Time    `json:"now"`
}

// Evaluate computes the report for a target from the samples, as of `now`.
//
// The full-window SLI drives budget consumption; short trailing windows drive
// burn-rate alerting, so a budget that is fine on the month can still page when
// it starts draining fast in the last hour.
func Evaluate(t Target, samples []Sample, now time.Time) Report {
	obj := t.Objective
	if obj <= 0 || obj >= 1 {
		obj = 0.999
	}
	win := t.Window
	if win <= 0 {
		win = 30 * 24 * time.Hour
	}
	budget := 1 - obj

	sorted := append([]Sample(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Time.Before(sorted[j].Time) })

	good, total := sumWindow(sorted, now.Add(-win), now)
	sli := 1.0
	if total > 0 {
		sli = good / total
	}
	errRate := 1 - sli
	consumed := 0.0
	if budget > 0 {
		consumed = errRate / budget
	}

	r := Report{
		Objective:       obj,
		SLI:             sli,
		ErrorBudget:     budget,
		BudgetConsumed:  consumed,
		BudgetRemaining: 1 - consumed,
		Now:             now,
	}

	// Burn-rate windows. Each rate is the window's error rate divided by the
	// budget: 1.0 means "burning exactly fast enough to spend the whole budget
	// over the SLO window", >1 means faster. A window that caught no samples
	// carries no signal and is dropped — an empty window is "no data", not
	// "zero burn", and treating it as the latter would silence real alerts.
	for _, w := range burnWindows(win) {
		n := countWindow(sorted, now.Add(-w), now)
		if n == 0 {
			continue
		}
		g, tot := sumWindow(sorted, now.Add(-w), now)
		bw := BurnWindow{Window: w, Samples: n}
		if tot > 0 {
			bw.ErrRate = 1 - g/tot
			if budget > 0 {
				bw.BurnRate = bw.ErrRate / budget
			}
		}
		r.Windows = append(r.Windows, bw)
	}
	if len(r.Windows) > 0 {
		r.BurnRate = r.Windows[0].BurnRate // shortest window with data
	}

	r.Severity = severity(r.Windows, win)
	r.Exhaustion = projectExhaustion(r, win, now)
	return r
}

// burnWindows returns the trailing windows to evaluate, shortest first, scaled
// to the SLO window so the model works for a 1h or a 30d objective alike.
func burnWindows(slo time.Duration) []time.Duration {
	// Fractions of the SLO window: ~0.07% (≈30m of 30d), ~0.8% (≈6h), ~14% (≈4d).
	fracs := []float64{1.0 / 1440, 1.0 / 120, 1.0 / 7}
	out := make([]time.Duration, 0, len(fracs))
	for _, f := range fracs {
		w := time.Duration(float64(slo) * f)
		if w < time.Minute {
			w = time.Minute
		}
		out = append(out, w)
	}
	return out
}

// severity applies the two-window burn-rate policy from the SRE workbook, over
// the windows that actually had data (shortest first): a fast burn sustained
// across the two shortest windows is critical; a slower sustained burn, or a
// sharp burn on the shortest window alone, is a warning.
func severity(windows []BurnWindow, slo time.Duration) string {
	if len(windows) == 0 {
		return "ok"
	}
	// Fast/critical: budget would be gone in ~2 days at 30d. Require corroboration
	// from a second window when one exists, so a single noisy bucket can't page.
	if windows[0].BurnRate >= 14.4 {
		if len(windows) == 1 || windows[1].BurnRate >= 14.4 {
			return "critical"
		}
	}
	// Slow/warning: a sustained ≥6× burn on the longer windows.
	if windows[len(windows)-1].BurnRate >= 6 {
		return "warning"
	}
	// A sharp burn on the shortest window alone is still worth a warning.
	if windows[0].BurnRate >= 3 {
		return "warning"
	}
	return "ok"
}

// projectExhaustion estimates when the budget hits zero at the current burn.
// It uses the shortest window's rate (the most responsive), and returns nil when
// nothing is burning or the budget is already spent.
func projectExhaustion(r Report, slo time.Duration, now time.Time) *time.Time {
	if r.BurnRate <= 0 || r.BudgetRemaining <= 0 {
		return nil
	}
	// At burn rate B, the entire window's budget is consumed in Window/B. The
	// remaining fraction takes remaining × Window / B.
	ttl := time.Duration(r.BudgetRemaining * float64(slo) / r.BurnRate)
	if ttl <= 0 || math.IsInf(float64(ttl), 0) {
		return nil
	}
	t := now.Add(ttl)
	return &t
}

func sumWindow(s []Sample, from, to time.Time) (good, total float64) {
	for _, x := range s {
		if !x.Time.Before(from) && !x.Time.After(to) {
			good += x.Good
			total += x.Total
		}
	}
	return good, total
}

func countWindow(s []Sample, from, to time.Time) int {
	n := 0
	for _, x := range s {
		if !x.Time.Before(from) && !x.Time.After(to) {
			n++
		}
	}
	return n
}
