// Package alert delivers anomaly records to the outside world.
//
// Detection stays deterministic and side-effect free; this package is the only
// place that talks to Slack, a webhook, or Alertmanager. A Notifier applies the
// two things every practical alerting path needs — a score floor and per-series
// deduplication — so a sustained anomaly pages once, not once per bucket.
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

// Alert is one anomaly record, tagged with the job it came from.
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
	Influencers []core.Influencer `json:"influencers,omitempty"`
}

// Severity buckets the score the way an on-call rotation reads it.
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

// Title is the one-line summary shared by every sink.
func (a Alert) Title() string {
	s := fmt.Sprintf("%s: %s", a.Job, a.Detector)
	if a.Series != "" {
		s += " [" + a.Series + "]"
	}
	return fmt.Sprintf("%s — score %.0f", s, a.Score)
}

// Description explains the record in words: what was seen, what was expected.
func (a Alert) Description() string {
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

// Sink delivers one alert somewhere.
type Sink interface {
	Name() string
	Send(ctx context.Context, a Alert) error
}

// Notifier filters, deduplicates and fans records out to every sink.
//
// The zero value is unusable — build it with NewNotifier.
type Notifier struct {
	Sinks    []Sink
	MinScore float64       // records below this are dropped (default 50)
	Dedup    time.Duration // re-alert window per (job, detector, series); 0 disables
	OnError  func(sink string, a Alert, err error)

	mu   sync.Mutex
	last map[string]time.Time
}

// NewNotifier builds a notifier with sensible on-call defaults: only warning and
// above, and at most one page per series per 30 minutes.
func NewNotifier(sinks ...Sink) *Notifier {
	return &Notifier{Sinks: sinks, MinScore: 50, Dedup: 30 * time.Minute, last: map[string]time.Time{}}
}

// Notify sends every qualifying record in results. It returns how many alerts
// were delivered and joins any sink errors — one broken sink never stops the
// others, and never stops detection.
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

// Deliver sends one already-decided alert to every sink, bypassing the score
// floor and dedup window. It is for callers that do their own deduplication —
// the incident tracker fires a lifecycle event exactly once per transition, so
// re-applying the per-series dedup here would be wrong.
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

// admit applies the score floor and the dedup window. It records the send time
// only when the alert passes, so a suppressed alert never extends the window.
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
	// Dedup is measured in *bucket* time, not wall clock: replaying history must
	// suppress exactly the same alerts a live run would.
	if prev, ok := n.last[key]; ok && a.Time.Sub(prev) < n.Dedup {
		return false
	}
	n.last[key] = a.Time
	return true
}

// FromRecord converts an engine record into an alert.
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

// Multi fans one alert out to several sinks as if they were one.
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

// sortedInfluencers keeps sink payloads stable for tests and diffs.
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
