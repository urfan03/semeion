// Package jobspec defines the anomaly-detection job configuration — the
// user-facing contract that mirrors Elastic ML's mental model (detectors,
// bucket span, by/over/partition fields, influencers) so it feels familiar,
// while staying independent of any backend.
package jobspec

import (
	"fmt"
	"strings"
	"time"
)

// Function is a detector analysis function. This is a growing subset of Elastic
// ML's function set; more (distinct_count, rare, population, info_content, …)
// arrive over the roadmap.
type Function string

const (
	FuncCount         Function = "count"
	FuncSum           Function = "sum"
	FuncMean          Function = "mean"
	FuncMin           Function = "min"
	FuncMax           Function = "max"
	FuncMedian        Function = "median"
	FuncDistinctCount Function = "distinct_count" // cardinality of field's values in the bucket
	FuncNonZeroCount  Function = "non_zero_count" // count of non-zero field values
	FuncNonNullSum    Function = "non_null_sum"   // sum of the field's present (non-null) values
	FuncVarp          Function = "varp"           // population variance of the field
	FuncMetric        Function = "metric"         // metric summary (scored on the mean)
	FuncRate          Function = "rate"           // per-second rate: sum(field)/sec, or count/sec
	FuncRare          Function = "rare"           // rare values of by_field (over time)
	FuncFreqRare      Function = "freq_rare"      // rare values weighted by frequency (routed to rare)
	FuncInfoContent   Function = "info_content"   // entropy of by_field's value distribution
	FuncTimeOfDay     Function = "time_of_day"    // events at an unusual hour-of-day
	FuncTimeOfWeek    Function = "time_of_week"   // events at an unusual hour-of-week
	FuncLatLong       Function = "lat_long"       // events at an unusual location (Values lat/lon)
)

// Side controls which deviations count as anomalous — the equivalent of
// Elastic's high_/low_ function variants.
type Side string

const (
	SideBoth Side = "both" // spikes and dips (default)
	SideHigh Side = "high" // only unusually high
	SideLow  Side = "low"  // only unusually low
)

// Detector is one analysis within a Job.
type Detector struct {
	Function       Function `json:"function"                  yaml:"function"`
	Field          string   `json:"field,omitempty"           yaml:"field,omitempty"` // metric field (empty for count/rare)
	Side           Side     `json:"side,omitempty"            yaml:"side,omitempty"`
	ByField        string   `json:"by_field,omitempty"        yaml:"by_field,omitempty"`
	PartitionField string   `json:"partition_field,omitempty" yaml:"partition_field,omitempty"`
	// Fields (≥2) make the detector MULTIVARIATE: the metrics are scored jointly
	// (Mahalanobis distance) so a broken correlation between them is caught even
	// when each metric alone is in range. Each field is aggregated by Function.
	Fields []string `json:"fields,omitempty" yaml:"fields,omitempty"`
	// OverField turns the detector into a POPULATION analysis: members (distinct
	// over_field values) are scored against a shared, pooled baseline, so an
	// entity behaving unlike its peers is flagged.
	OverField string `json:"over_field,omitempty" yaml:"over_field,omitempty"`
	// Seasonal makes the detector seasonality-aware: the period is auto-detected
	// and each value is scored against its phase (time-of-cycle) baseline.
	Seasonal bool `json:"seasonal,omitempty" yaml:"seasonal,omitempty"`
	// Distribution scores against a best-fit distribution (normal / lognormal /
	// exponential / poisson) instead of the default robust z-score — better for
	// skewed or count data.
	Distribution bool `json:"distribution,omitempty" yaml:"distribution,omitempty"`
	// Rules filter this detector's results (suppress trivial / safelisted hits).
	Rules []Rule `json:"rules,omitempty" yaml:"rules,omitempty"`
}

// Calendar is a time window during which anomalies are suppressed (a known
// event: a release, a sale, maintenance). Elastic ML calls these calendars /
// scheduled events.
type Calendar struct {
	Name  string    `json:"name,omitempty" yaml:"name,omitempty"`
	Start time.Time `json:"start"          yaml:"start"`
	End   time.Time `json:"end"            yaml:"end"`
}

// Rule filters a detector's results (Elastic ML job rules / filters): suppress
// anomalies whose actual value is trivially small/large, whose deviation from the
// typical value is negligible, whose series value is safelisted, that fall in a
// muted wall-clock window, or that are attributed to a safelisted dimension value.
type Rule struct {
	SkipActualBelow *float64 `json:"skip_actual_below,omitempty" yaml:"skip_actual_below,omitempty"`
	SkipActualAbove *float64 `json:"skip_actual_above,omitempty" yaml:"skip_actual_above,omitempty"`
	SkipValues      []string `json:"skip_values,omitempty"       yaml:"skip_values,omitempty"` // safelist of series values
	// SkipDiffBelow suppresses when |actual − typical| is below an ABSOLUTE floor
	// (a spike that is statistically unusual but operationally tiny).
	SkipDiffBelow *float64 `json:"skip_diff_below,omitempty" yaml:"skip_diff_below,omitempty"`
	// SkipDiffRatioBelow suppresses when |actual − typical| / |typical| is below a
	// RELATIVE floor (e.g. 0.1 = ignore deviations under 10% of the baseline).
	SkipDiffRatioBelow *float64 `json:"skip_diff_ratio_below,omitempty" yaml:"skip_diff_ratio_below,omitempty"`
	// SkipHoursUTC mutes results whose bucket falls in these hours-of-day (UTC) —
	// a known nightly batch window, say. SkipWeekdaysUTC does the same per weekday
	// (0 = Sunday).
	SkipHoursUTC    []int `json:"skip_hours_utc,omitempty"    yaml:"skip_hours_utc,omitempty"`
	SkipWeekdaysUTC []int `json:"skip_weekdays_utc,omitempty" yaml:"skip_weekdays_utc,omitempty"`
	// SkipInfluencer safelists dimension values: if the record is attributed to
	// any listed field=value (e.g. env=staging), it is suppressed — a filter-ref
	// scoped to an influencer, not just the by/partition series key.
	SkipInfluencer map[string][]string `json:"skip_influencer,omitempty" yaml:"skip_influencer,omitempty"`
}

// Job is a complete anomaly-detection configuration.
type Job struct {
	Name        string        `json:"name"                  yaml:"name"`
	BucketSpan  time.Duration `json:"bucket_span"           yaml:"bucket_span"`
	Detectors   []Detector    `json:"detectors"             yaml:"detectors"`
	Influencers []string      `json:"influencers,omitempty" yaml:"influencers,omitempty"`
	Calendars   []Calendar    `json:"calendars,omitempty"   yaml:"calendars,omitempty"`
	// Sensitivity (0..1) enables per-series adaptive gating: a metric record is
	// reported only if its score also exceeds this quantile of the series' own
	// recent scores. A chronically noisy series must clear its own high-water
	// mark (fewer false positives); a quiet series still alerts on a modest bump.
	// 0 (default) disables it — the fixed threshold alone applies.
	Sensitivity float64 `json:"sensitivity,omitempty" yaml:"sensitivity,omitempty"`
}

// NeedsField reports whether the function operates on a metric field. Only the
// value functions do; count / rare / info_content / time_of_* work off events
// and dimensions.
func (d Detector) NeedsField() bool {
	if d.IsMultivariate() {
		return false // uses Fields (a metric vector), not a single Field
	}
	switch d.Function {
	case FuncMean, FuncSum, FuncMin, FuncMax, FuncMedian, FuncDistinctCount, FuncNonZeroCount,
		FuncNonNullSum, FuncVarp, FuncMetric:
		return true
	}
	return false
}

// IsPopulation reports whether this is a population (over_field) analysis.
func (d Detector) IsPopulation() bool { return d.OverField != "" }

// CountsEmptyAsZero reports whether an empty (missing) bucket is a meaningful
// zero for this detector — a count-family metric where "no events" is itself the
// signal (a traffic drop to zero) — rather than a data gap. Only single-series
// detectors (no by/partition/over split, not multivariate) qualify: an empty
// bucket carries no field values, so there is no way to know WHICH by-series
// went silent. Metric functions (mean/sum/min/max/median/…) treat an empty
// bucket as no-data and are not scored on it. This drives gap-filling: the engine
// synthesises the missing buckets so a drop to zero is caught.
func (d Detector) CountsEmptyAsZero() bool {
	if d.ByField != "" || d.PartitionField != "" || d.OverField != "" || d.IsMultivariate() {
		return false
	}
	switch d.Function {
	case FuncCount, FuncNonZeroCount, FuncDistinctCount:
		return true
	case FuncRate:
		return d.Field == "" // count-rate → 0/s; a field-rate over an empty bucket is a gap
	}
	return false
}

// EffectiveSide resolves the default (both) when unset.
func (d Detector) EffectiveSide() Side {
	if d.Side == "" {
		return SideBoth
	}
	return d.Side
}

// ID is a stable identifier for a detector within a job (e.g. "mean(latency)",
// "rare(status)", "mean(latency) over host").
// IsMultivariate reports whether this detector scores a metric vector jointly.
func (d Detector) IsMultivariate() bool { return len(d.Fields) >= 2 }

func (d Detector) ID() string {
	switch {
	case d.IsMultivariate():
		return fmt.Sprintf("multivariate(%s)", strings.Join(d.Fields, ","))
	case d.Function == FuncRare || d.Function == FuncFreqRare:
		return fmt.Sprintf("%s(%s)", d.Function, d.ByField)
	case d.Function == FuncInfoContent:
		return fmt.Sprintf("info_content(%s)", d.ByField)
	case d.Function == FuncTimeOfDay || d.Function == FuncTimeOfWeek:
		return string(d.Function)
	case d.Function == FuncLatLong:
		if d.OverField != "" {
			return "lat_long over " + d.OverField
		}
		if d.ByField != "" {
			return "lat_long by " + d.ByField
		}
		return "lat_long"
	case d.OverField != "" && d.Field != "":
		return fmt.Sprintf("%s(%s) over %s", d.Function, d.Field, d.OverField)
	case d.OverField != "":
		return fmt.Sprintf("%s over %s", d.Function, d.OverField)
	case d.Field != "":
		return fmt.Sprintf("%s(%s)", d.Function, d.Field)
	default:
		return string(d.Function)
	}
}

// Validate checks the job is well-formed.
func (j *Job) Validate() error {
	if j.Name == "" {
		return fmt.Errorf("job: name is required")
	}
	if j.BucketSpan <= 0 {
		return fmt.Errorf("job %q: bucket_span must be > 0", j.Name)
	}
	if len(j.Detectors) == 0 {
		return fmt.Errorf("job %q: at least one detector is required", j.Name)
	}
	for i, d := range j.Detectors {
		if d.Function == "" && !d.IsMultivariate() {
			return fmt.Errorf("job %q: detector %d: function is required", j.Name, i)
		}
		if d.NeedsField() && d.Field == "" {
			return fmt.Errorf("job %q: detector %d (%s): field is required", j.Name, i, d.Function)
		}
		if (d.Function == FuncRare || d.Function == FuncFreqRare) && d.ByField == "" {
			return fmt.Errorf("job %q: detector %d (%s): by_field is required", j.Name, i, d.Function)
		}
		if d.Function == FuncInfoContent && d.ByField == "" {
			return fmt.Errorf("job %q: detector %d (info_content): by_field is required", j.Name, i)
		}
	}
	return nil
}
