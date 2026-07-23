package slo

import (
	"math"
	"sort"
	"time"
)

type Target struct {
	Objective float64
	Window    time.Duration
}

type Sample struct {
	Time  time.Time
	Good  float64
	Total float64
}

type BurnWindow struct {
	Window   time.Duration `json:"window"`
	BurnRate float64       `json:"burn_rate"`
	ErrRate  float64       `json:"error_rate"`
	Samples  int           `json:"samples"`
}

type Report struct {
	Objective       float64      `json:"objective"`
	SLI             float64      `json:"sli"`
	ErrorBudget     float64      `json:"error_budget"`
	BudgetConsumed  float64      `json:"budget_consumed"`
	BudgetRemaining float64      `json:"budget_remaining"`
	BurnRate        float64      `json:"burn_rate"`
	Windows         []BurnWindow `json:"windows"`
	Exhaustion      *time.Time   `json:"exhaustion,omitempty"`
	Severity        string       `json:"severity"`
	NoData          bool         `json:"no_data,omitempty"`
	Now             time.Time    `json:"now"`
}

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

	if total == 0 {
		r.NoData = true
		r.Severity = "no_data"
		return r
	}

	for _, w := range displayWindows(win) {
		if g, tot, n := windowStats(sorted, now.Add(-w), now); n > 0 {
			bw := BurnWindow{Window: w, Samples: n, ErrRate: 1 - g/tot}
			if budget > 0 {
				bw.BurnRate = bw.ErrRate / budget
			}
			r.Windows = append(r.Windows, bw)
		}
	}

	r.BurnRate = burnOver(sorted, budget, now, win/720)

	r.Severity = severity(sorted, budget, now, win)
	r.Exhaustion = projectExhaustion(r, win, now)
	return r
}

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

func severity(sorted []Sample, budget float64, now time.Time, slo time.Duration) string {
	fastLong := burnOver(sorted, budget, now, slo/720)
	fastShort := burnOver(sorted, budget, now, slo/8640)
	slowLong := burnOver(sorted, budget, now, slo/120)
	slowShort := burnOver(sorted, budget, now, slo/1440)

	if fastLong >= 14.4 && fastShort >= 14.4 {
		return "critical"
	}
	if slowLong >= 6 && slowShort >= 6 {
		return "warning"
	}
	return "ok"
}

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

func projectExhaustion(r Report, slo time.Duration, now time.Time) *time.Time {
	if r.BurnRate <= 0 || r.BudgetRemaining <= 0 {
		return nil
	}

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
