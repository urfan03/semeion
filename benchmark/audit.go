package benchmark

import (
	"encoding/json"
	"io"
	"sort"

	"github.com/urfan03/semeion/guard"
	"github.com/urfan03/semeion/shape"
)

type AuditEntry struct {
	Key      string     `json:"key"`
	Start    int        `json:"start"`
	End      int        `json:"end"`
	Peak     int        `json:"peak"`
	Score    float64    `json:"score"`
	Labelled bool       `json:"labelled"`
	Shape    shape.Kind `json:"shape"`
	Z        float64    `json:"z"`
	Duration int        `json:"duration"`
	Before   float64    `json:"before"`
	During   float64    `json:"during"`
	After    float64    `json:"after"`
	Context  []float64  `json:"context,omitempty"`
	Offset   int        `json:"context_offset,omitempty"`
	Nearest  int        `json:"nearest_labelled_gap"`
}

type AuditOptions struct {
	Threshold ThresholdFunc
	Policy    guard.Options
	Gap       int
	Context   int
	Limit     int
	FalseOnly bool
}

func (o AuditOptions) resolve() AuditOptions {
	if o.Gap < 0 {
		o.Gap = 0
	}
	if o.Context <= 0 {
		o.Context = 120
	}
	if o.Limit <= 0 {
		o.Limit = 50
	}
	return o
}

func nearestLabelledGap(labels []bool, start, end int) int {
	segs := Segments(labels)
	if len(segs) == 0 {
		return -1
	}
	best := -1
	for _, g := range segs {
		d := 0
		switch {
		case end < g[0]:
			d = g[0] - end
		case start > g[1]:
			d = start - g[1]
		}
		if best < 0 || d < best {
			best = d
		}
	}
	return best
}

func Audit(series []CorpusSeries, fn ScoreFunc, opt AuditOptions) []AuditEntry {
	opt = opt.resolve()
	var out []AuditEntry
	for _, s := range series {
		if s.Anomalies == 0 {
			continue
		}
		scores := fn(s)
		if len(scores) != len(s.Points) {
			continue
		}
		thr := 0.0
		if opt.Threshold != nil {
			thr = opt.Threshold(s, scores)
		}
		policy := opt.Policy
		policy.Threshold = thr
		fired := guard.Apply(scores, policy)

		marks := make([]float64, len(scores))
		for i, f := range fired {
			if f {
				marks[i] = 1
			}
		}
		values := s.Values()
		for _, c := range guard.Candidates(marks, 1, opt.Gap) {
			labelled := false
			for k := c.Start; k <= c.End && k < len(s.Labels); k++ {
				if s.Labels[k] {
					labelled = true
					break
				}
			}
			if opt.FalseOnly && labelled {
				continue
			}
			cls := shape.Classify(values, c.Start, c.End, shape.Options{Context: opt.Context})
			lo := c.Start - opt.Context
			if lo < 0 {
				lo = 0
			}
			hi := c.End + opt.Context
			if hi > len(values)-1 {
				hi = len(values) - 1
			}
			out = append(out, AuditEntry{
				Key: s.Key, Start: c.Start, End: c.End, Peak: c.Peak,
				Score: scores[c.Peak], Labelled: labelled,
				Shape: cls.Kind, Z: cls.Z, Duration: cls.Duration,
				Before: cls.Before, During: cls.During, After: cls.After,
				Context: append([]float64(nil), values[lo:hi+1]...), Offset: lo,
				Nearest: nearestLabelledGap(s.Labels, c.Start, c.End),
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > opt.Limit {
		out = out[:opt.Limit]
	}
	return out
}

func WriteAudit(w io.Writer, entries []AuditEntry) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(entries)
}
