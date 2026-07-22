package slo

import (
	"testing"
	"time"
)

var now = time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)

const day = 24 * time.Hour

// steady builds `n` minutely buckets ending at `now`, each with `total` events
// and a fixed error ratio. Minutely resolution keeps every burn window (down to
// the ~1-minute shortest for a 24h SLO) populated.
func steady(n int, total, errRatio float64) []Sample {
	s := make([]Sample, n)
	for i := 0; i < n; i++ {
		ts := now.Add(-time.Duration(n-i) * time.Minute)
		s[i] = Sample{Time: ts, Total: total, Good: total * (1 - errRatio)}
	}
	return s
}

func TestHealthyBudget(t *testing.T) {
	// 99.9% target, observed 99.95% → half the budget spent, nothing burning fast.
	r := Evaluate(Target{Objective: 0.999, Window: day}, steady(1440, 1000, 0.0005), now)

	if r.Severity != "ok" {
		t.Errorf("a healthy service should be ok, got %q (burn %.2f)", r.Severity, r.BurnRate)
	}
	if r.BudgetConsumed <= 0.4 || r.BudgetConsumed >= 0.6 {
		t.Errorf("expected ~half the budget consumed, got %.2f", r.BudgetConsumed)
	}
	if r.SLI < 0.999 {
		t.Errorf("SLI should be above the objective, got %.5f", r.SLI)
	}
}

func TestBlownBudgetIsCritical(t *testing.T) {
	// 5% errors against a 0.1% budget → burning ~50×.
	r := Evaluate(Target{Objective: 0.999, Window: day}, steady(1440, 1000, 0.05), now)

	if r.Severity != "critical" {
		t.Fatalf("a heavy sustained burn should be critical, got %q (windows %+v)", r.Severity, r.Windows)
	}
	if r.BudgetConsumed <= 1 {
		t.Errorf("budget should be blown, consumed=%.2f", r.BudgetConsumed)
	}
	if r.BudgetRemaining >= 0 {
		t.Errorf("remaining budget should be negative when blown, got %.2f", r.BudgetRemaining)
	}
	if r.Exhaustion != nil {
		t.Errorf("a blown budget has no future exhaustion, got %v", r.Exhaustion)
	}
}

func TestProjectedExhaustion(t *testing.T) {
	// 0.5% errors against a 1% budget → burning 0.5×, budget half-spent and
	// projected to run out in the future.
	r := Evaluate(Target{Objective: 0.99, Window: day}, steady(1440, 1000, 0.005), now)

	if r.BurnRate <= 0 || r.BurnRate >= 1 {
		t.Fatalf("expected a sub-1× steady burn, got %.2f", r.BurnRate)
	}
	if r.Exhaustion == nil {
		t.Fatal("a burning-but-intact budget should project an exhaustion time")
	}
	if !r.Exhaustion.After(now) {
		t.Errorf("exhaustion must be in the future, got %v (now %v)", r.Exhaustion, now)
	}
}

// A spike confined to the last half hour must raise burn on the short window
// without blowing the day's budget — the whole point of multi-window.
func TestRecentSpikeRaisesShortWindowBurn(t *testing.T) {
	s := steady(1440, 1000, 0.0001) // healthy day
	for i := len(s) - 30; i < len(s); i++ {
		s[i] = Sample{Time: s[i].Time, Total: 1000, Good: 800} // last 30m at 20% errors
	}
	// 1% budget over the day: 30m at 20% burns fast but does not exhaust it.
	r := Evaluate(Target{Objective: 0.99, Window: day}, s, now)

	if len(r.Windows) < 1 || r.Windows[0].BurnRate < 14.4 {
		t.Fatalf("the short window should show a fast burn, got %+v", r.Windows)
	}
	if r.BudgetConsumed >= 1 {
		t.Errorf("a 30-minute spike should not blow the day's 1%% budget, consumed=%.2f", r.BudgetConsumed)
	}
	if r.Severity == "ok" {
		t.Errorf("a fast short-window burn should not read as ok")
	}
}

func TestNoTrafficIsPerfect(t *testing.T) {
	r := Evaluate(Target{Objective: 0.999, Window: day}, nil, now)
	if r.SLI != 1.0 || r.Severity != "ok" {
		t.Errorf("no traffic should read as perfect, got SLI=%.3f sev=%q", r.SLI, r.Severity)
	}
	if r.Exhaustion != nil {
		t.Error("nothing burning → no exhaustion")
	}
}

func TestDefaultsAppliedForBadTarget(t *testing.T) {
	r := Evaluate(Target{Objective: 1.5, Window: -time.Hour}, steady(2880, 100, 0.001), now)
	if r.Objective != 0.999 {
		t.Errorf("an out-of-range objective should default to 0.999, got %v", r.Objective)
	}
}
