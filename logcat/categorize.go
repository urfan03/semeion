package logcat

import (
	"fmt"
	"sort"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/detect"
	"github.com/urfan03/semeion/jobspec"
)

// Tuning (conservative, to keep false positives low).
const (
	catWarmup      = 20 // buckets before scoring begins
	catMinNew      = 3  // a new template must occur >= this in its first bucket to fire
	catRareMax     = 2  // a template seen in <= this many buckets total is "rare"
	scoreNew       = 90 // fixed score for a brand-new template
	scoreRare      = 65 // fixed score for a rare template
	catMaxExamples = 4  // distinct example messages kept per category (Elastic ML keeps ~4)
)

// Categorizer detects log anomalies: NEW templates ("an error type never seen
// before"), RARE templates, and SPIKING templates ("this category is N× its
// norm"). It groups messages with Drain and scores each template's per-bucket
// count series with the shared robust model. Batch (Run) and streaming
// (Push/Flush) share one scoring path; Snapshot/Restore persist all state.
type Categorizer struct {
	span       time.Duration
	drain      *Drain
	warmup     int
	influencer string // optional dimension field for attribution (e.g. "service")

	models   map[int]*detect.Model // per-template spike model (keyed by cluster ID)
	firstBkt map[int]time.Time     // first bucket a template appeared in
	buckets  map[int]int           // distinct buckets a template appeared in (rarity)
	sample   map[int]string        // the representative (first) example message per template
	examples map[int][]string      // up to catMaxExamples distinct example messages per template
	matches  map[int]int           // cumulative lines matched per template (num_matches)
	suppress map[int]bool          // feedback: templates the operator muted
	seen     int                   // buckets processed

	pending   map[time.Time][]core.LogLine // streaming buffer (open buckets)
	watermark time.Time
	hasMark   bool
}

// NewCategorizer builds a categorizer over the given bucket span.
func NewCategorizer(span time.Duration) *Categorizer {
	return &Categorizer{
		span: span, drain: NewDrain(), warmup: catWarmup,
		models:   make(map[int]*detect.Model),
		firstBkt: make(map[int]time.Time),
		buckets:  make(map[int]int),
		sample:   make(map[int]string),
		examples: make(map[int][]string),
		matches:  make(map[int]int),
		suppress: make(map[int]bool),
		pending:  make(map[time.Time][]core.LogLine),
	}
}

// WithInfluencer sets a dimension field (e.g. "service") to attribute anomalies
// to; the dominant value among a template's lines is reported as an influencer.
func (c *Categorizer) WithInfluencer(field string) *Categorizer { c.influencer = field; return c }

// Drain exposes the underlying template store (for inspection / persistence).
func (c *Categorizer) Drain() *Drain { return c.drain }

// Suppress mutes a template — the feedback / active-suppression hook. A muted
// template is never reported again (until un-suppressed).
func (c *Categorizer) Suppress(templateID int) { c.suppress[templateID] = true }

// Unsuppress re-enables reporting for a template.
func (c *Categorizer) Unsuppress(templateID int) { delete(c.suppress, templateID) }

// Run categorizes log lines (batch) and returns per-bucket anomaly records in
// time order.
func (c *Categorizer) Run(lines []core.LogLine, threshold float64) []core.BucketResult {
	buckets := make(map[time.Time][]core.LogLine)
	for _, l := range lines {
		bt := l.Time.Truncate(c.span)
		buckets[bt] = append(buckets[bt], l)
	}
	times := make([]time.Time, 0, len(buckets))
	for t := range buckets {
		times = append(times, t)
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })

	out := make([]core.BucketResult, 0, len(times))
	for _, bt := range times {
		out = append(out, c.closeBucket(bt, buckets[bt], threshold))
	}
	return out
}

// Push buffers one log line (streaming). Buckets strictly older than the newest
// bucket seen are closed and scored; their results are returned.
func (c *Categorizer) Push(l core.LogLine, threshold float64) []core.BucketResult {
	bt := l.Time.Truncate(c.span)
	if !c.hasMark {
		c.watermark, c.hasMark = bt, true
	}
	var out []core.BucketResult
	if bt.After(c.watermark) {
		out = c.closeBefore(bt, threshold)
		c.watermark = bt
	}
	c.pending[bt] = append(c.pending[bt], l)
	return out
}

// Flush closes and scores every remaining open bucket (end of stream).
func (c *Categorizer) Flush(threshold float64) []core.BucketResult {
	return c.closeBefore(time.Time{}, threshold)
}

func (c *Categorizer) closeBefore(limit time.Time, threshold float64) []core.BucketResult {
	var times []time.Time
	for t := range c.pending {
		if limit.IsZero() || t.Before(limit) {
			times = append(times, t)
		}
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	out := make([]core.BucketResult, 0, len(times))
	for _, t := range times {
		out = append(out, c.closeBucket(t, c.pending[t], threshold))
		delete(c.pending, t)
	}
	return out
}

// closeBucket scores one bucket and advances the bucket counter.
func (c *Categorizer) closeBucket(bt time.Time, lines []core.LogLine, threshold float64) core.BucketResult {
	br := c.scoreBucket(bt, lines, threshold)
	c.seen++
	return br
}

func (c *Categorizer) scoreBucket(bt time.Time, lines []core.LogLine, threshold float64) core.BucketResult {
	counts := make(map[int]int)
	cluster := make(map[int]*Cluster)
	infl := make(map[int]map[string]int) // template → influencer value → count
	for _, l := range lines {
		cl := c.drain.Match(l.Message)
		if cl == nil {
			continue
		}
		counts[cl.ID]++
		cluster[cl.ID] = cl
		c.matches[cl.ID]++
		if _, ok := c.sample[cl.ID]; !ok {
			c.sample[cl.ID] = l.Message
		}
		c.addExample(cl.ID, l.Message)
		if c.influencer != "" {
			if v := l.Fields[c.influencer]; v != "" {
				if infl[cl.ID] == nil {
					infl[cl.ID] = make(map[string]int)
				}
				infl[cl.ID][v]++
			}
		}
	}

	ids := make([]int, 0, len(counts))
	for id := range counts {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	br := core.BucketResult{Time: bt}
	for _, id := range ids {
		cnt := counts[id]

		_, isKnown := c.firstBkt[id]
		isNew := !isKnown
		if isNew {
			c.firstBkt[id] = bt
		}
		c.buckets[id]++

		m := c.models[id]
		if m == nil {
			m = detect.NewModel(jobspec.SideHigh)
			c.models[id] = m
		}
		_, spikeScore, typical, _ := m.Observe(float64(cnt))

		if c.seen < c.warmup || c.suppress[id] {
			continue
		}

		rec := core.Record{
			Time: bt, Detector: "categorization", Series: fmt.Sprintf("T%d", id),
			Actual: float64(cnt), Direction: core.DirUp,
			Template: cluster[id].Template(), Sample: c.sample[id],
			CategoryID: id, Examples: append([]string(nil), c.examples[id]...), MatchCount: c.matches[id],
			Influencers: []core.Influencer{{Field: "template", Value: cluster[id].Template()}},
		}
		if c.influencer != "" {
			if v := topValue(infl[id]); v != "" {
				rec.Influencers = append(rec.Influencers, core.Influencer{Field: c.influencer, Value: v})
			}
		}
		switch {
		case isNew && cnt >= catMinNew:
			rec.Kind, rec.Score = "new_category", scoreNew
		case spikeScore >= threshold:
			rec.Kind, rec.Score, rec.Typical = "category_spike", spikeScore, typical
		case c.buckets[id] <= catRareMax:
			rec.Kind, rec.Score = "rare_category", scoreRare
		default:
			continue
		}
		if rec.Score >= threshold {
			br.Records = append(br.Records, rec)
			if rec.Score > br.Score {
				br.Score = rec.Score
			}
		}
	}
	return br
}

func topValue(m map[string]int) string {
	best, bestN := "", 0
	for v, n := range m {
		if n > bestN {
			best, bestN = v, n
		}
	}
	return best
}

// addExample keeps up to catMaxExamples DISTINCT example messages for a category
// (Elastic ML surfaces a handful of examples per category so an operator can see
// the range of real messages behind a template, not just one).
func (c *Categorizer) addExample(id int, msg string) {
	ex := c.examples[id]
	if len(ex) >= catMaxExamples {
		return
	}
	for _, e := range ex {
		if e == msg {
			return // already have this exact example
		}
	}
	c.examples[id] = append(ex, msg)
}

// CategoryDefinition describes one learned log category (Elastic ML's
// category_definition): a stable id, the template, several example messages, and
// how much traffic it has carried. Returned by Categories for inspection / a
// category catalogue in the UI.
type CategoryDefinition struct {
	ID         int       `json:"id"`
	Template   string    `json:"template"`
	Examples   []string  `json:"examples,omitempty"`
	NumMatches int       `json:"num_matches"`  // cumulative lines matched
	Buckets    int       `json:"buckets_seen"` // distinct buckets it appeared in
	FirstSeen  time.Time `json:"first_seen"`
	Suppressed bool      `json:"suppressed,omitempty"`
}

// Categories returns every learned category with its definition, ordered by id.
// It is a read-only view of the categorizer's template catalogue.
func (c *Categorizer) Categories() []CategoryDefinition {
	byID := make(map[int]*Cluster)
	for _, cl := range c.drain.Clusters() {
		byID[cl.ID] = cl
	}
	ids := make([]int, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := make([]CategoryDefinition, 0, len(ids))
	for _, id := range ids {
		out = append(out, CategoryDefinition{
			ID:         id,
			Template:   byID[id].Template(),
			Examples:   append([]string(nil), c.examples[id]...),
			NumMatches: c.matches[id],
			Buckets:    c.buckets[id],
			FirstSeen:  c.firstBkt[id],
			Suppressed: c.suppress[id],
		})
	}
	return out
}

// ── Persistence ──────────────────────────────────────────────────────────────

// Snapshot is the serialisable state of a Categorizer (templates, per-template
// baselines, and detection bookkeeping) so log-anomaly detection resumes across
// restarts. Pending (in-flight) lines are not persisted — they are re-fed.
type Snapshot struct {
	Span       time.Duration             `json:"span"`
	Warmup     int                       `json:"warmup"`
	Influencer string                    `json:"influencer,omitempty"`
	Seen       int                       `json:"seen"`
	Watermark  time.Time                 `json:"watermark"`
	HasMark    bool                      `json:"has_mark"`
	Drain      State                     `json:"drain"`
	Models     map[int]detect.ModelState `json:"models"`
	FirstBkt   map[int]time.Time         `json:"first_bkt"`
	Buckets    map[int]int               `json:"buckets"`
	Sample     map[int]string            `json:"sample"`
	Examples   map[int][]string          `json:"examples,omitempty"`
	Matches    map[int]int               `json:"matches,omitempty"`
	Suppress   map[int]bool              `json:"suppress"`
}

// Snapshot captures the categorizer's current learned state.
func (c *Categorizer) Snapshot() Snapshot {
	ms := make(map[int]detect.ModelState, len(c.models))
	for id, m := range c.models {
		ms[id] = m.State()
	}
	return Snapshot{
		Span: c.span, Warmup: c.warmup, Influencer: c.influencer, Seen: c.seen,
		Watermark: c.watermark, HasMark: c.hasMark,
		Drain:    c.drain.Export(),
		Models:   ms,
		FirstBkt: copyTimeMap(c.firstBkt),
		Buckets:  copyIntMap(c.buckets),
		Sample:   copyStrMap(c.sample),
		Examples: copyStrSliceMap(c.examples),
		Matches:  copyIntMap(c.matches),
		Suppress: copyBoolMap(c.suppress),
	}
}

// RestoreCategorizer rebuilds a Categorizer from a snapshot.
func RestoreCategorizer(s Snapshot) *Categorizer {
	span := s.Span
	if span <= 0 {
		span = time.Minute
	}
	c := NewCategorizer(span)
	if s.Warmup > 0 {
		c.warmup = s.Warmup
	}
	c.influencer = s.Influencer
	c.seen = s.Seen
	c.watermark = s.Watermark
	c.hasMark = s.HasMark
	c.drain = LoadState(s.Drain)
	c.models = make(map[int]*detect.Model, len(s.Models))
	for id, st := range s.Models {
		c.models[id] = detect.ModelFromState(st)
	}
	c.firstBkt = copyTimeMap(s.FirstBkt)
	c.buckets = copyIntMap(s.Buckets)
	c.sample = copyStrMap(s.Sample)
	c.examples = copyStrSliceMap(s.Examples)
	c.matches = copyIntMap(s.Matches)
	// Back-compat: an older snapshot has no examples map — seed each category's
	// example ring from its representative sample so the catalogue isn't empty.
	if len(c.examples) == 0 && len(c.sample) > 0 {
		c.examples = make(map[int][]string, len(c.sample))
		for id, msg := range c.sample {
			c.examples[id] = []string{msg}
		}
	}
	c.suppress = copyBoolMap(s.Suppress)
	return c
}

func copyTimeMap(m map[int]time.Time) map[int]time.Time {
	out := make(map[int]time.Time, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
func copyIntMap(m map[int]int) map[int]int {
	out := make(map[int]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
func copyStrMap(m map[int]string) map[int]string {
	out := make(map[int]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
func copyStrSliceMap(m map[int][]string) map[int][]string {
	out := make(map[int][]string, len(m))
	for k, v := range m {
		out[k] = append([]string(nil), v...)
	}
	return out
}
func copyBoolMap(m map[int]bool) map[int]bool {
	out := make(map[int]bool, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
