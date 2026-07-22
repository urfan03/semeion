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
	Severity        string       `json:"severity"`             // ok | warning | critical | no_data
	NoData          bool         `json:"no_data,omitempty"`    // no events in the window (SLI unknown)
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

	// No data at all over the window: the SLI is unknown, not perfect. A service
	// that has stopped emitting (an outage that also killed its exporter) would
	// otherwise read as a healthy 100% — the most dangerous false-negative.
	if total == 0 {
		r.NoData = true
		r.Severity = "no_data"
		return r
	}

	// Burn over each evaluated window, for display. A window with no samples is
	// "no data", not "zero burn", so it is dropped rather than counted as calm.
	for _, w := range displayWindows(win) {
		if g, tot, n := windowStats(sorted, now.Add(-w), now); n > 0 {
			bw := BurnWindow{Window: w, Samples: n, ErrRate: 1 - g/tot}
			if budget > 0 {
				bw.BurnRate = bw.ErrRate / budget
			}
			r.Windows = append(r.Windows, bw)
		}
	}
	// Headline burn = the fast long-window rate (the primary paging signal).
	r.BurnRate = burnOver(sorted, budget, now, win/720) // ≈1h for a 30d SLO

	r.Severity = severity(sorted, budget, now, win)
	r.Exhaustion = projectExhaustion(r, win, now)
	return r
}

// burnOver returns the error-budget burn rate over the trailing window w (error
// rate ÷ budget). A window with no samples returns 0 burn but is only used where
// the caller has already established that data exists.
func burnOver(sorted []Sample, budget float64, now time.Time, w time.Duration) float64 {
	if w < time.Minute {
		w = time.Minute
	}
	g, tot, n := windowStats(sorted, now.Add(-w), now)
	if n == 0 || tot == 0 || budget <= 0 {
		return 0
	}
	return (1 - g/tot) / budget
}

// displayWindows are representative trailing windows shown in the report,
// scaled to the SLO window (≈1h / 6h / 3d for a 30d objective).
func displayWindows(slo time.Duration) []time.Duration {
	fracs := []float64{1.0 / 720, 1.0 / 120, 1.0 / 10}
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

// severity applies Google SRE's multi-window, multi-burn-rate policy. Each alert
// is a LONG window (the burn is big enough) confirmed by a SHORT window (the
// burn is still happening right now, not a past blip that has since stopped):
//
//	critical: 14.4× over ≈1h, confirmed over ≈5m  (budget gone in ~2 days)
//	warning:   6×  over ≈6h, confirmed over ≈30m  (budget gone in ~5 days)
//
// Pairing a long window with a short confirmation is what lets it page FAST
// (on the short window) without pinning on a single noisy bucket.
func severity(sorted []Sample, budget float64, now time.Time, slo time.Duration) string {
	fastLong := burnOver(sorted, budget, now, slo/720)   // ≈1h
	fastShort := burnOver(sorted, budget, now, slo/8640) // ≈5m
	slowLong := burnOver(sorted, budget, now, slo/120)   // ≈6h
	slowShort := burnOver(sorted, budget, now, slo/1440) // ≈30m

	if fastLong >= 14.4 && fastShort >= 14.4 {
		return "critical"
	}
	if slowLong >= 6 && slowShort >= 6 {
		return "warning"
	}
	return "ok"
}

// windowStats sums good/total and counts samples in [from,to].
func windowStats(s []Sample, from, to time.Time) (good, total float64, n int) {
	for _, x := range s {
		if !x.Time.Before(from) && !x.Time.After(to) {
			good += x.Good
			total += x.Total
			n++
		}
	}
	return good, total, n
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
