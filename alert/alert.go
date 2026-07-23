package alert

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/urfan03/semeion/core"
)

type Alert struct {
	Job         string            `json:"job"`
	Time        time.Time         `json:"time"`
	Detector    string            `json:"detector"`
	Series      string            `json:"series,omitempty"`
	Score       float64           `json:"score"`
	Actual      float64           `json:"actual"`
	Typical     float64           `json:"typical"`
	Probability float64           `json:"probability"`
	Direction   string            `json:"direction,omitempty"`
	Kind        string            `json:"kind,omitempty"`
	Template    string            `json:"template,omitempty"`
	Note        string            `json:"note,omitempty"`
	Influencers []core.Influencer `json:"influencers,omitempty"`
}

func (a Alert) Severity() string {
	switch {
	case a.Score >= 75:
		return "critical"
	case a.Score >= 50:
		return "warning"
	default:
		return "info"
	}
}

func (a Alert) Title() string {
	s := fmt.Sprintf("%s: %s", a.Job, a.Detector)
	if a.Series != "" {
		s += " [" + a.Series + "]"
	}
	return fmt.Sprintf("%s — score %.0f", s, a.Score)
}

func (a Alert) Description() string {
	if a.Note != "" && a.Kind == "digest" {
		return a.Note
	}
	var b strings.Builder
	fmt.Fprintf(&b, "actual %.4g, typical %.4g", a.Actual, a.Typical)
	if a.Direction != "" {
		fmt.Fprintf(&b, " (%s)", a.Direction)
	}
	if a.Probability > 0 {
		fmt.Fprintf(&b, ", p=%.3g", a.Probability)
	}
	if a.Kind != "" {
		fmt.Fprintf(&b, ", kind=%s", a.Kind)
	}
	if a.Template != "" {
		fmt.Fprintf(&b, "\ntemplate: %s", a.Template)
	}
	if len(a.Influencers) > 0 {
		parts := make([]string, 0, len(a.Influencers))
		for _, in := range a.Influencers {
			parts = append(parts, in.Field+"="+in.Value)
		}
		fmt.Fprintf(&b, "\ninfluencers: %s", strings.Join(parts, ", "))
	}
	fmt.Fprintf(&b, "\nbucket: %s", a.Time.UTC().Format(time.RFC3339))
	return b.String()
}

type Sink interface {
	Name() string
	Send(ctx context.Context, a Alert) error
}

type Notifier struct {
	Sinks    []Sink
	MinScore float64
	Dedup    time.Duration
	OnError  func(sink string, a Alert, err error)

	FlapThreshold int
	FlapWindow    time.Duration
	Flapped       int64

	mu      sync.Mutex
	last    map[string]time.Time
	history map[string][]time.Time
}

func NewNotifier(sinks ...Sink) *Notifier {
	return &Notifier{
		Sinks: sinks, MinScore: 50, Dedup: 30 * time.Minute,
		FlapThreshold: 5, FlapWindow: 2 * time.Hour,
		last: map[string]time.Time{}, history: map[string][]time.Time{},
	}
}

func (n *Notifier) Notify(ctx context.Context, job string, results []core.BucketResult) (int, error) {
	var (
		sent int
		errs []error
	)
	for _, br := range results {
		for _, r := range br.Records {
			a := FromRecord(job, r)
			if !n.admit(a) {
				continue
			}
			delivered := false
			for _, s := range n.Sinks {
				if err := s.Send(ctx, a); err != nil {
					if n.OnError != nil {
						n.OnError(s.Name(), a, err)
					}
					errs = append(errs, fmt.Errorf("%s: %w", s.Name(), err))
					continue
				}
				delivered = true
			}
			if delivered {
				sent++
			}
		}
	}
	return sent, errors.Join(errs...)
}

func (n *Notifier) Deliver(ctx context.Context, a Alert) (bool, error) {
	delivered := false
	var errs []error
	for _, s := range n.Sinks {
		if err := s.Send(ctx, a); err != nil {
			if n.OnError != nil {
				n.OnError(s.Name(), a, err)
			}
			errs = append(errs, fmt.Errorf("%s: %w", s.Name(), err))
			continue
		}
		delivered = true
	}
	return delivered, errors.Join(errs...)
}

func (n *Notifier) admit(a Alert) bool {
	if a.Score < n.MinScore {
		return false
	}
	if n.Dedup <= 0 {
		return true
	}
	key := a.Job + "\x00" + a.Detector + "\x00" + a.Series
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.last == nil {
		n.last = map[string]time.Time{}
	}

	if prev, ok := n.last[key]; ok && a.Time.Sub(prev) < n.Dedup {
		return false
	}

	if n.FlapThreshold > 0 && n.FlapWindow > 0 {
		if n.history == nil {
			n.history = map[string][]time.Time{}
		}
		cutoff := a.Time.Add(-n.FlapWindow)
		h := n.history[key][:0:0]
		for _, t := range n.history[key] {
			if t.After(cutoff) {
				h = append(h, t)
			}
		}
		h = append(h, a.Time)
		n.history[key] = h
		if len(h) > n.FlapThreshold {
			n.Flapped++
			n.last[key] = a.Time
			return false
		}
	}
	n.last[key] = a.Time
	return true
}

func FromRecord(job string, r core.Record) Alert {
	return Alert{
		Job:         job,
		Time:        r.Time,
		Detector:    r.Detector,
		Series:      r.Series,
		Score:       r.Score,
		Actual:      r.Actual,
		Typical:     r.Typical,
		Probability: r.Probability,
		Direction:   string(r.Direction),
		Kind:        r.Kind,
		Template:    r.Template,
		Influencers: r.Influencers,
	}
}

type Multi []Sink

func (m Multi) Name() string { return "multi" }

func (m Multi) Send(ctx context.Context, a Alert) error {
	var errs []error
	for _, s := range m {
		if err := s.Send(ctx, a); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.Name(), err))
		}
	}
	return errors.Join(errs...)
}

func sortedInfluencers(in []core.Influencer) []core.Influencer {
	out := append([]core.Influencer(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Field != out[j].Field {
			return out[i].Field < out[j].Field
		}
		return out[i].Value < out[j].Value
	})
	return out
}
