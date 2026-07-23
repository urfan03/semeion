package jobspec

import (
	"fmt"
	"strings"
	"time"
)

type Function string

const (
	FuncCount         Function = "count"
	FuncSum           Function = "sum"
	FuncMean          Function = "mean"
	FuncMin           Function = "min"
	FuncMax           Function = "max"
	FuncMedian        Function = "median"
	FuncDistinctCount Function = "distinct_count"
	FuncNonZeroCount  Function = "non_zero_count"
	FuncNonNullSum    Function = "non_null_sum"
	FuncVarp          Function = "varp"
	FuncMetric        Function = "metric"
	FuncRate          Function = "rate"
	FuncRare          Function = "rare"
	FuncFreqRare      Function = "freq_rare"
	FuncInfoContent   Function = "info_content"
	FuncTimeOfDay     Function = "time_of_day"
	FuncTimeOfWeek    Function = "time_of_week"
	FuncLatLong       Function = "lat_long"
	FuncRatio         Function = "ratio"
)

type Side string

const (
	SideBoth Side = "both"
	SideHigh Side = "high"
	SideLow  Side = "low"
)

type Detector struct {
	Function          Function `json:"function"                  yaml:"function"`
	Field             string   `json:"field,omitempty"           yaml:"field,omitempty"`
	DenomField        string   `json:"denom_field,omitempty"     yaml:"denom_field,omitempty"`
	SummaryCountField string   `json:"summary_count_field,omitempty" yaml:"summary_count_field,omitempty"`
	Side              Side     `json:"side,omitempty"            yaml:"side,omitempty"`
	ByField           string   `json:"by_field,omitempty"        yaml:"by_field,omitempty"`
	PartitionField    string   `json:"partition_field,omitempty" yaml:"partition_field,omitempty"`

	Fields []string `json:"fields,omitempty" yaml:"fields,omitempty"`

	OverField string `json:"over_field,omitempty" yaml:"over_field,omitempty"`

	Seasonal bool `json:"seasonal,omitempty" yaml:"seasonal,omitempty"`

	Distribution bool `json:"distribution,omitempty" yaml:"distribution,omitempty"`

	Rules []Rule `json:"rules,omitempty" yaml:"rules,omitempty"`
}

type Calendar struct {
	Name  string    `json:"name,omitempty" yaml:"name,omitempty"`
	Start time.Time `json:"start"          yaml:"start"`
	End   time.Time `json:"end"            yaml:"end"`
	// RecurWeekly makes this a recurring weekly event: only the weekday-of-week
	// and time-of-day of [Start,End) are matched, on every week (a maintenance
	// window every Sunday 02:00-03:00). RecurDaily matches the time-of-day every
	// day. Both compare in UTC. When neither is set the window is one-shot.
	RecurWeekly bool `json:"recur_weekly,omitempty" yaml:"recur_weekly,omitempty"`
	RecurDaily  bool `json:"recur_daily,omitempty"  yaml:"recur_daily,omitempty"`
}

// Covers reports whether t falls inside this calendar window, honouring weekly or
// daily recurrence.
func (c Calendar) Covers(t time.Time) bool {
	if c.End.Before(c.Start) || c.End.Equal(c.Start) {
		return false
	}
	t = t.UTC()
	switch {
	case c.RecurDaily:
		return coversDaily(c.Start.UTC(), c.End.UTC(), t)
	case c.RecurWeekly:
		return coversWeekly(c.Start.UTC(), c.End.UTC(), t)
	default:
		return !t.Before(c.Start) && t.Before(c.End)
	}
}

func secondsOfDay(t time.Time) int { return t.Hour()*3600 + t.Minute()*60 + t.Second() }

func coversDaily(start, end, t time.Time) bool {
	s := secondsOfDay(start)
	dur := int(end.Sub(start).Seconds())
	if dur >= 86400 {
		return true
	}
	off := (secondsOfDay(t) - s + 86400) % 86400
	return off < dur
}

func coversWeekly(start, end, t time.Time) bool {
	s := int(start.Weekday())*86400 + secondsOfDay(start)
	dur := int(end.Sub(start).Seconds())
	if dur >= 7*86400 {
		return true
	}
	cur := int(t.Weekday())*86400 + secondsOfDay(t)
	off := (cur - s + 7*86400) % (7 * 86400)
	return off < dur
}

type Rule struct {
	SkipActualBelow *float64 `json:"skip_actual_below,omitempty" yaml:"skip_actual_below,omitempty"`
	SkipActualAbove *float64 `json:"skip_actual_above,omitempty" yaml:"skip_actual_above,omitempty"`
	SkipValues      []string `json:"skip_values,omitempty"       yaml:"skip_values,omitempty"`

	SkipDiffBelow *float64 `json:"skip_diff_below,omitempty" yaml:"skip_diff_below,omitempty"`

	SkipDiffRatioBelow *float64 `json:"skip_diff_ratio_below,omitempty" yaml:"skip_diff_ratio_below,omitempty"`

	SkipHoursUTC    []int `json:"skip_hours_utc,omitempty"    yaml:"skip_hours_utc,omitempty"`
	SkipWeekdaysUTC []int `json:"skip_weekdays_utc,omitempty" yaml:"skip_weekdays_utc,omitempty"`

	SkipInfluencer map[string][]string `json:"skip_influencer,omitempty" yaml:"skip_influencer,omitempty"`
	// SkipModelUpdate stops a matching bucket from being LEARNED into the baseline
	// (Elastic ML's skip_model_update action) — the anomaly is still reported, but
	// the outlier value doesn't pollute the model. Applies to the plain temporal
	// path.
	SkipModelUpdate bool `json:"skip_model_update,omitempty" yaml:"skip_model_update,omitempty"`
}

type Job struct {
	Name        string        `json:"name"                  yaml:"name"`
	BucketSpan  time.Duration `json:"bucket_span"           yaml:"bucket_span"`
	Detectors   []Detector    `json:"detectors"             yaml:"detectors"`
	Influencers []string      `json:"influencers,omitempty" yaml:"influencers,omitempty"`
	Calendars   []Calendar    `json:"calendars,omitempty"   yaml:"calendars,omitempty"`
	Groups      []string      `json:"groups,omitempty"      yaml:"groups,omitempty"`

	Sensitivity float64 `json:"sensitivity,omitempty" yaml:"sensitivity,omitempty"`
}

func (j Job) InGroup(group string) bool {
	for _, g := range j.Groups {
		if g == group {
			return true
		}
	}
	return false
}

func (d Detector) NeedsField() bool {
	if d.IsMultivariate() {
		return false
	}
	switch d.Function {
	case FuncMean, FuncSum, FuncMin, FuncMax, FuncMedian, FuncDistinctCount, FuncNonZeroCount,
		FuncNonNullSum, FuncVarp, FuncMetric:
		return true
	}
	return false
}

func (d Detector) IsPopulation() bool { return d.OverField != "" }

func (d Detector) CountsEmptyAsZero() bool {
	if d.ByField != "" || d.PartitionField != "" || d.OverField != "" || d.IsMultivariate() {
		return false
	}
	switch d.Function {
	case FuncCount, FuncNonZeroCount, FuncDistinctCount:
		return true
	case FuncRate:
		return d.Field == ""
	}
	return false
}

// CountFamilySplit reports a count-family detector split by a by/partition field:
// each distinct split value is zero-filled when it produces no events in a bucket
// (a per-partition drop to zero is a real signal), matching Elastic ML.
func (d Detector) CountFamilySplit() bool {
	if d.OverField != "" || d.IsMultivariate() {
		return false
	}
	if d.ByField == "" && d.PartitionField == "" {
		return false
	}
	switch d.Function {
	case FuncCount, FuncNonZeroCount, FuncDistinctCount:
		return true
	}
	return false
}

func (d Detector) EffectiveSide() Side {
	if d.Side == "" {
		return SideBoth
	}
	return d.Side
}

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
	case d.Function == FuncRatio:
		return fmt.Sprintf("ratio(%s/%s)", d.Field, d.DenomField)
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
		if d.Function == FuncRatio && (d.Field == "" || d.DenomField == "") {
			return fmt.Errorf("job %q: detector %d (ratio): field and denom_field are required", j.Name, i)
		}
	}
	return nil
}
