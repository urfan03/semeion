// Package engine runs a Job: it buckets incoming points by the job's bucket
// span, aggregates each bucket per detector and series (by/partition split),
// scores each bucket value against a per-series streaming Model, and emits the
// anomalous records.
//
// Two entry points share one scoring path:
//   - Run    — batch: score a whole slice of points (backtesting / CSV files).
//   - Push/Flush — streaming: feed points as they arrive; a bucket is scored
//     (closed) once a strictly newer bucket is seen, so scores stay causal.
//
// Snapshot/Restore persist the learned per-series baselines so a long-running
// detector survives restarts.
package engine

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/detect"
	"github.com/urfan03/semeion/jobspec"
	"github.com/urfan03/semeion/model"
)

const (
	defaultThreshold = 50
	rareWarmup       = 20 // buckets before rare-value detection starts
	rareMaxBuckets   = 2  // a value seen in <= this many buckets is "rare"
	rareValueScore   = 70 // fixed score for a rare value
)

// rareTracker counts, per distinct by_field value, how many buckets it has
// appeared in — the state behind the `rare` function.
type rareTracker struct {
	buckets int
	seen    map[string]int
}

// Engine holds a job, the live per-(detector,series) models + rare trackers,
// and — for the streaming path — the buffer of not-yet-closed buckets.
type Engine struct {
	job        jobspec.Job
	models     map[string]*detect.Model             // temporal per-series + population pooled
	seasonal   map[string]*detect.SeasonalModel     // seasonality-aware per-series
	distrib    map[string]*detect.DistributionModel // distribution-based per-series
	multivar   map[string]*detect.MultivariateModel // multivariate per-series
	slotModels map[string]*detect.Model             // per wall-clock slot (time_of_day/week)
	rare       map[string]*rareTracker              // per rare-detector value frequencies
	provider   model.Provider                       // heavy-model math (seasonality, …)
	threshold  float64

	pending   map[time.Time][]core.DataPoint // streaming buffer (open buckets)
	watermark time.Time
	hasMark   bool
}

// New validates the job and returns a ready engine (with the default Go model
// provider). Use NewWithProvider to inject a Python sidecar.
func New(job jobspec.Job) (*Engine, error) {
	return NewWithProvider(job, model.NewGoProvider())
}

// NewWithProvider is New with an explicit heavy-model provider.
func NewWithProvider(job jobspec.Job, prov model.Provider) (*Engine, error) {
	if err := job.Validate(); err != nil {
		return nil, err
	}
	if prov == nil {
		prov = model.NewGoProvider()
	}
	return &Engine{
		job:        job,
		models:     make(map[string]*detect.Model),
		seasonal:   make(map[string]*detect.SeasonalModel),
		distrib:    make(map[string]*detect.DistributionModel),
		multivar:   make(map[string]*detect.MultivariateModel),
		slotModels: make(map[string]*detect.Model),
		rare:       make(map[string]*rareTracker),
		provider:   prov,
		threshold:  defaultThreshold,
		pending:    make(map[time.Time][]core.DataPoint),
	}, nil
}

// SetThreshold sets the minimum score (0..100) a record must reach to be
// reported. It applies to the streaming path; Run also accepts it per call.
func (e *Engine) SetThreshold(t float64) { e.threshold = t }

// seriesKey builds a series identity from a detector's partition/by fields.
func seriesKey(d jobspec.Detector, fields map[string]string) string {
	key := ""
	if d.PartitionField != "" {
		key += d.PartitionField + "=" + fields[d.PartitionField] + ";"
	}
	if d.ByField != "" {
		key += d.ByField + "=" + fields[d.ByField]
	}
	return key
}

// scoreBucket scores one bucket's points across every detector, dispatching by
// detector kind (temporal metric / population / rare). Shared by Run + streaming.
func (e *Engine) scoreBucket(bt time.Time, pts []core.DataPoint) core.BucketResult {
	br := core.BucketResult{Time: bt}
	for _, d := range e.job.Detectors {
		switch {
		case d.IsMultivariate():
			e.scoreMultivariate(&br, d, bt, pts)
		case d.Function == jobspec.FuncRare:
			e.scoreRare(&br, d, bt, pts)
		case d.Function == jobspec.FuncInfoContent:
			e.scoreInfoContent(&br, d, bt, pts)
		case d.Function == jobspec.FuncTimeOfDay || d.Function == jobspec.FuncTimeOfWeek:
			e.scoreTimeOf(&br, d, bt, pts)
		case d.IsPopulation():
			e.scorePopulation(&br, d, bt, pts)
		default:
			e.scoreTemporal(&br, d, bt, pts)
		}
	}
	return br
}

// scoreTemporal is the standard per-series temporal detector: each by/partition
// series is scored against its own learned baseline.
func (e *Engine) scoreTemporal(br *core.BucketResult, d jobspec.Detector, bt time.Time, pts []core.DataPoint) {
	bySeries := make(map[string][]core.DataPoint)
	for _, p := range pts {
		sk := seriesKey(d, p.Fields)
		bySeries[sk] = append(bySeries[sk], p)
	}
	for sk, sp := range bySeries {
		val, ok := detect.Aggregate(d.Function, sp)
		if !ok {
			continue
		}
		mk := d.ID() + "|" + sk
		var prob, score, typical float64
		var dir core.Direction
		switch {
		case d.Distribution:
			dm := e.distrib[mk]
			if dm == nil {
				dm = detect.NewDistributionModel(d.EffectiveSide(), e.provider)
				e.distrib[mk] = dm
			}
			prob, score, typical, dir = dm.Observe(val)
		case d.Seasonal:
			sm := e.seasonal[mk]
			if sm == nil {
				sm = detect.NewSeasonalModel(d.EffectiveSide(), e.provider)
				e.seasonal[mk] = sm
			}
			prob, score, typical, dir = sm.Observe(val)
		default:
			prob, score, typical, dir = e.model(mk, d.EffectiveSide()).Observe(val)
		}
		if score >= e.threshold {
			e.emit(br, d, core.Record{
				Time: bt, Detector: d.ID(), Series: sk,
				Actual: val, Typical: typical, Probability: prob,
				Score: score, Direction: dir, Kind: "metric",
				Influencers: e.influencers(d, sp),
			})
		}
	}
}

// scorePopulation scores each member (over_field value) against a shared, pooled
// baseline — an entity behaving unlike its peers is flagged. All members are
// scored against the past baseline first, then folded in.
func (e *Engine) scorePopulation(br *core.BucketResult, d jobspec.Detector, bt time.Time, pts []core.DataPoint) {
	byEntity := make(map[string][]core.DataPoint)
	for _, p := range pts {
		if ent := p.Fields[d.OverField]; ent != "" {
			byEntity[ent] = append(byEntity[ent], p)
		}
	}
	if len(byEntity) == 0 {
		return
	}
	m := e.model(d.ID()+"|__pool__", d.EffectiveSide())

	type ev struct {
		entity              string
		val, prob, score, t float64
		dir                 core.Direction
	}
	ents := make([]string, 0, len(byEntity))
	for ent := range byEntity {
		ents = append(ents, ent)
	}
	sort.Strings(ents)

	evs := make([]ev, 0, len(ents))
	for _, ent := range ents {
		val, ok := detect.Aggregate(d.Function, byEntity[ent])
		if !ok {
			continue
		}
		prob, score, typical, dir := m.Score(val) // peek against pooled baseline
		evs = append(evs, ev{ent, val, prob, score, typical, dir})
	}
	for _, x := range evs { // fold every member into the pooled baseline
		m.Learn(x.val)
	}
	for _, x := range evs {
		if x.score >= e.threshold {
			e.emit(br, d, core.Record{
				Time: bt, Detector: d.ID(), Series: x.entity,
				Actual: x.val, Typical: x.t, Probability: x.prob,
				Score: x.score, Direction: x.dir, Kind: "population",
				Influencers: []core.Influencer{{Field: d.OverField, Value: x.entity}},
			})
		}
	}
}

// scoreMultivariate scores the joint metric vector [agg(f1), agg(f2), …] per
// series against its learned covariance (relationship-break detection), and
// attributes the anomaly across metrics.
func (e *Engine) scoreMultivariate(br *core.BucketResult, d jobspec.Detector, bt time.Time, pts []core.DataPoint) {
	bySeries := make(map[string][]core.DataPoint)
	for _, p := range pts {
		bySeries[seriesKey(d, p.Fields)] = append(bySeries[seriesKey(d, p.Fields)], p)
	}
	for sk, sp := range bySeries {
		vec := make([]float64, len(d.Fields))
		ok := true
		for i, f := range d.Fields {
			v, has := meanValues(sp, f)
			if !has {
				ok = false
				break
			}
			vec[i] = v
		}
		if !ok {
			continue
		}
		mk := d.ID() + "|" + sk
		mm := e.multivar[mk]
		if mm == nil {
			mm = detect.NewMultivariateModel(len(d.Fields))
			e.multivar[mk] = mm
		}
		prob, score, dist, contrib := mm.Observe(vec)
		if score >= e.threshold {
			e.emit(br, d, core.Record{
				Time: bt, Detector: d.ID(), Series: sk,
				Actual: dist, Typical: math.Sqrt(float64(len(d.Fields))),
				Probability: prob, Score: score, Direction: core.DirUp, Kind: "multivariate",
				Influencers: multivarInfluencers(d.Fields, contrib),
			})
		}
	}
}

// meanValues is the mean of a named numeric metric across a bucket's points.
func meanValues(pts []core.DataPoint, field string) (float64, bool) {
	var s float64
	n := 0
	for _, p := range pts {
		if p.Values != nil {
			if v, ok := p.Values[field]; ok {
				s += v
				n++
			}
		}
	}
	if n == 0 {
		return 0, false
	}
	return s / float64(n), true
}

// multivarInfluencers ranks metrics by their contribution share to the anomaly.
func multivarInfluencers(fields []string, contrib []float64) []core.Influencer {
	type fc struct {
		f string
		c float64
	}
	arr := make([]fc, 0, len(fields))
	for i, f := range fields {
		c := 0.0
		if i < len(contrib) {
			c = contrib[i]
		}
		arr = append(arr, fc{f, c})
	}
	sort.Slice(arr, func(i, j int) bool { return arr[i].c > arr[j].c })
	var out []core.Influencer
	for _, x := range arr {
		if x.c < 0.1 {
			continue
		}
		out = append(out, core.Influencer{Field: x.f, Value: fmt.Sprintf("%.0f%%", x.c*100)})
	}
	if len(out) == 0 && len(arr) > 0 {
		out = append(out, core.Influencer{Field: arr[0].f, Value: fmt.Sprintf("%.0f%%", arr[0].c*100)})
	}
	return out
}

// scoreInfoContent scores the Shannon entropy (bits) of a by_field's value
// distribution per bucket — a spike in diversity (e.g. DGA-like domains, a fan-
// out of error codes) shows up as an entropy anomaly. Split by partition only.
func (e *Engine) scoreInfoContent(br *core.BucketResult, d jobspec.Detector, bt time.Time, pts []core.DataPoint) {
	byPart := make(map[string][]core.DataPoint)
	for _, p := range pts {
		byPart[p.Fields[d.PartitionField]] = append(byPart[p.Fields[d.PartitionField]], p)
	}
	for part, pp := range byPart {
		val := shannonEntropy(pp, d.ByField)
		m := e.model(d.ID()+"|"+part, jobspec.SideBoth)
		prob, score, typical, dir := m.Observe(val)
		if score >= e.threshold {
			infl := []core.Influencer{{Field: d.ByField, Value: dominant(pp, d.ByField)}}
			if d.PartitionField != "" {
				infl = append(infl, core.Influencer{Field: d.PartitionField, Value: part})
			}
			e.emit(br, d, core.Record{
				Time: bt, Detector: d.ID(), Series: part, Actual: val, Typical: typical,
				Probability: prob, Score: score, Direction: dir, Kind: "info_content", Influencers: infl,
			})
		}
	}
}

// scoreTimeOf flags event bursts at an unusual wall-clock slot: the bucket's
// event count is scored against the baseline of counts AT THAT hour-of-day
// (time_of_day) or hour-of-week (time_of_week). A burst at a normally-quiet
// 3am fires; the same burst at a busy hour does not.
func (e *Engine) scoreTimeOf(br *core.BucketResult, d jobspec.Detector, bt time.Time, pts []core.DataPoint) {
	slot := bt.UTC().Hour()
	if d.Function == jobspec.FuncTimeOfWeek {
		slot = int(bt.UTC().Weekday())*24 + bt.UTC().Hour()
	}
	bySeries := make(map[string][]core.DataPoint)
	for _, p := range pts {
		bySeries[seriesKey(d, p.Fields)] = append(bySeries[seriesKey(d, p.Fields)], p)
	}
	for sk, sp := range bySeries {
		val := float64(len(sp))
		// One slot baseline per (detector, series, slot); slots see 1 sample per
		// day/week, so use a small warm-up.
		key := fmt.Sprintf("%s|%s|slot%d", d.ID(), sk, slot)
		m := e.slotModels[key]
		if m == nil {
			m = detect.NewModelWarmup(jobspec.SideHigh, 512, 3)
			e.slotModels[key] = m
		}
		prob, score, typical, dir := m.Observe(val)
		if score >= e.threshold {
			e.emit(br, d, core.Record{
				Time: bt, Detector: d.ID(), Series: sk, Actual: val, Typical: typical,
				Probability: prob, Score: score, Direction: dir, Kind: string(d.Function),
				Influencers: e.influencers(d, sp),
			})
		}
	}
}

// shannonEntropy returns the entropy (bits) of the distribution of field values.
func shannonEntropy(pts []core.DataPoint, field string) float64 {
	counts := make(map[string]int)
	total := 0
	for _, p := range pts {
		if v := p.Fields[field]; v != "" {
			counts[v]++
			total++
		}
	}
	if total == 0 {
		return 0
	}
	var h float64
	for _, c := range counts {
		p := float64(c) / float64(total)
		h -= p * math.Log2(p)
	}
	return h
}

// scoreRare flags by_field values that are rare across the analysed window.
func (e *Engine) scoreRare(br *core.BucketResult, d jobspec.Detector, bt time.Time, pts []core.DataPoint) {
	if rareValueScore < e.threshold {
		return
	}
	tr := e.rare[d.ID()]
	if tr == nil {
		tr = &rareTracker{seen: make(map[string]int)}
		e.rare[d.ID()] = tr
	}
	present := make(map[string]int)
	for _, p := range pts {
		if v := p.Fields[d.ByField]; v != "" {
			present[v]++
		}
	}
	tr.buckets++
	vals := make([]string, 0, len(present))
	for v := range present {
		vals = append(vals, v)
	}
	sort.Strings(vals)
	for _, v := range vals {
		tr.seen[v]++
		if tr.buckets < rareWarmup {
			continue
		}
		if tr.seen[v] <= rareMaxBuckets {
			e.emit(br, d, core.Record{
				Time: bt, Detector: d.ID(), Series: v,
				Actual: float64(present[v]), Score: rareValueScore, Direction: core.DirUp,
				Kind: "rare", Influencers: []core.Influencer{{Field: d.ByField, Value: v}},
			})
		}
	}
}

// model fetches (or lazily creates) a model by key.
func (e *Engine) model(key string, side jobspec.Side) *detect.Model {
	m := e.models[key]
	if m == nil {
		m = detect.NewModel(side)
		e.models[key] = m
	}
	return m
}

// influencers builds the dimension attributions for a temporal record: the
// by/partition split values plus the dominant value of each job influencer.
func (e *Engine) influencers(d jobspec.Detector, sp []core.DataPoint) []core.Influencer {
	if len(sp) == 0 {
		return nil
	}
	var infl []core.Influencer
	if d.PartitionField != "" {
		infl = append(infl, core.Influencer{Field: d.PartitionField, Value: sp[0].Fields[d.PartitionField]})
	}
	if d.ByField != "" {
		infl = append(infl, core.Influencer{Field: d.ByField, Value: sp[0].Fields[d.ByField]})
	}
	for _, f := range e.job.Influencers {
		if v := dominant(sp, f); v != "" {
			infl = append(infl, core.Influencer{Field: f, Value: v})
		}
	}
	return infl
}

func dominant(pts []core.DataPoint, field string) string {
	counts := make(map[string]int)
	for _, p := range pts {
		if v := p.Fields[field]; v != "" {
			counts[v]++
		}
	}
	best, bestN := "", 0
	for v, n := range counts {
		if n > bestN {
			best, bestN = v, n
		}
	}
	return best
}

func addRec(br *core.BucketResult, r core.Record) {
	br.Records = append(br.Records, r)
	if r.Score > br.Score {
		br.Score = r.Score
	}
}

// emit records an anomaly unless a calendar window or a detector rule suppresses
// it.
func (e *Engine) emit(br *core.BucketResult, d jobspec.Detector, r core.Record) {
	if e.suppressed(d, r) {
		return
	}
	addRec(br, r)
}

// suppressed applies job calendars + detector rules to one candidate record.
func (e *Engine) suppressed(d jobspec.Detector, r core.Record) bool {
	for _, c := range e.job.Calendars {
		if !r.Time.Before(c.Start) && r.Time.Before(c.End) {
			return true // inside a known event window
		}
	}
	for _, rule := range d.Rules {
		if rule.SkipActualBelow != nil && r.Actual < *rule.SkipActualBelow {
			return true
		}
		if rule.SkipActualAbove != nil && r.Actual > *rule.SkipActualAbove {
			return true
		}
		for _, v := range rule.SkipValues {
			if r.Series == v {
				return true
			}
		}
	}
	return false
}

// Run processes a batch of points (any order) and returns one BucketResult per
// occupied bucket, in time order, each carrying the records that scored at or
// above threshold. Models update in bucket order, so a bucket is scored only
// against its past.
func (e *Engine) Run(points []core.DataPoint, threshold float64) []core.BucketResult {
	e.threshold = threshold
	span := e.job.BucketSpan

	buckets := make(map[time.Time][]core.DataPoint)
	for _, p := range points {
		bt := p.Time.Truncate(span)
		buckets[bt] = append(buckets[bt], p)
	}
	times := sortedTimes(buckets)

	out := make([]core.BucketResult, 0, len(times))
	for _, bt := range times {
		out = append(out, e.scoreBucket(bt, buckets[bt]))
	}
	return out
}

// Push adds one point to the streaming buffer. When the point belongs to a
// bucket newer than any seen, every older open bucket is closed and scored;
// their results are returned (usually empty, occasionally one).
func (e *Engine) Push(p core.DataPoint) []core.BucketResult {
	bt := p.Time.Truncate(e.job.BucketSpan)
	if !e.hasMark {
		e.watermark, e.hasMark = bt, true
	}
	var out []core.BucketResult
	if bt.After(e.watermark) {
		out = e.closeBefore(bt)
		e.watermark = bt
	}
	e.pending[bt] = append(e.pending[bt], p)
	return out
}

// Flush closes and scores every remaining open bucket (end of stream).
func (e *Engine) Flush() []core.BucketResult {
	return e.closeBefore(time.Time{}) // zero limit → close all
}

func (e *Engine) closeBefore(limit time.Time) []core.BucketResult {
	var times []time.Time
	for t := range e.pending {
		if limit.IsZero() || t.Before(limit) {
			times = append(times, t)
		}
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	out := make([]core.BucketResult, 0, len(times))
	for _, t := range times {
		out = append(out, e.scoreBucket(t, e.pending[t]))
		delete(e.pending, t)
	}
	return out
}

func sortedTimes(m map[time.Time][]core.DataPoint) []time.Time {
	times := make([]time.Time, 0, len(m))
	for t := range m {
		times = append(times, t)
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	return times
}

// ── Persistence ──────────────────────────────────────────────────────────────

// Snapshot is the serialisable state of an engine: its learned per-series
// models plus streaming position. In-flight (pending) points are not persisted
// — they are re-fed by the datafeed after a restart.
type Snapshot struct {
	JobName   string                       `json:"job"`
	Threshold float64                      `json:"threshold"`
	Watermark time.Time                    `json:"watermark"`
	HasMark   bool                         `json:"has_mark"`
	Models    map[string]detect.ModelState `json:"models"`
	Rare      map[string]RareState         `json:"rare,omitempty"`
}

// RareState persists a rare detector's value frequencies.
type RareState struct {
	Buckets int            `json:"buckets"`
	Seen    map[string]int `json:"seen"`
}

// Snapshot captures the engine's current learned state.
func (e *Engine) Snapshot() Snapshot {
	ms := make(map[string]detect.ModelState, len(e.models))
	for k, m := range e.models {
		ms[k] = m.State()
	}
	rs := make(map[string]RareState, len(e.rare))
	for k, tr := range e.rare {
		seen := make(map[string]int, len(tr.seen))
		for v, n := range tr.seen {
			seen[v] = n
		}
		rs[k] = RareState{Buckets: tr.buckets, Seen: seen}
	}
	return Snapshot{
		JobName:   e.job.Name,
		Threshold: e.threshold,
		Watermark: e.watermark,
		HasMark:   e.hasMark,
		Models:    ms,
		Rare:      rs,
	}
}

// Restore replaces the engine's learned state with a snapshot's. The job is
// unchanged (the caller reconstructs the engine from the same job definition).
func (e *Engine) Restore(s Snapshot) {
	if s.Threshold > 0 {
		e.threshold = s.Threshold
	}
	e.watermark = s.Watermark
	e.hasMark = s.HasMark
	e.models = make(map[string]*detect.Model, len(s.Models))
	for k, st := range s.Models {
		e.models[k] = detect.ModelFromState(st)
	}
	e.rare = make(map[string]*rareTracker, len(s.Rare))
	for k, st := range s.Rare {
		seen := make(map[string]int, len(st.Seen))
		for v, n := range st.Seen {
			seen[v] = n
		}
		e.rare[k] = &rareTracker{buckets: st.Buckets, seen: seen}
	}
}
