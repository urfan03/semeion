package benchmark

import "sort"

type Operating struct {
	Label          string  `json:"label"`
	Events         int     `json:"events"`
	EventsHit      int     `json:"events_hit"`
	Alarms         int     `json:"alarms"`
	FalseAlarms    int     `json:"false_alarms"`
	EventRecall    float64 `json:"event_recall"`
	AlarmPrecision float64 `json:"alarm_precision"`
	F1             float64 `json:"f1"`
	AlarmsPerSerie float64 `json:"alarms_per_series"`
	Series         int     `json:"series"`
}

func (o *Operating) finish() {
	if o.Events > 0 {
		o.EventRecall = float64(o.EventsHit) / float64(o.Events)
	}
	if o.Alarms > 0 {
		o.AlarmPrecision = float64(o.Alarms-o.FalseAlarms) / float64(o.Alarms)
	}
	if o.EventRecall+o.AlarmPrecision > 0 {
		o.F1 = 2 * o.EventRecall * o.AlarmPrecision / (o.EventRecall + o.AlarmPrecision)
	}
	if o.Series > 0 {
		o.AlarmsPerSerie = float64(o.Alarms) / float64(o.Series)
	}
}

type AlarmFunc func(s CorpusSeries, scores []float64) []bool

func EventScore(series []CorpusSeries, fn ScoreFunc, alarm AlarmFunc, label string) Operating {
	op := Operating{Label: label}
	for _, s := range series {
		if s.Anomalies == 0 {
			continue
		}
		scores := fn(s)
		if len(scores) != len(s.Points) {
			continue
		}
		pred := alarm(s, scores)
		if len(pred) != len(s.Points) {
			continue
		}
		op.Series++

		segs := Segments(s.Labels)
		op.Events += len(segs)
		for _, g := range segs {
			for k := g[0]; k <= g[1] && k < len(pred); k++ {
				if pred[k] {
					op.EventsHit++
					break
				}
			}
		}
		for i, p := range pred {
			if !p {
				continue
			}
			op.Alarms++
			if !s.Labels[i] {
				op.FalseAlarms++
			}
		}
	}
	op.finish()
	return op
}

func Curve(series []CorpusSeries, fn ScoreFunc, alarms []AlarmFunc, labels []string) []Operating {
	out := make([]Operating, 0, len(alarms))
	for i, a := range alarms {
		label := ""
		if i < len(labels) {
			label = labels[i]
		}
		out = append(out, EventScore(series, fn, a, label))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].EventRecall < out[j].EventRecall })
	return out
}
