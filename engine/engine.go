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
	"github.com/urfan03/semeion/stats"
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

// maxRareValues bounds a rare detector's per-value frequency map against a
// high-cardinality by_field.
const maxRareValues = 100000

// evictCommon trims seen down to `keep` entries, dropping the highest-count
// (most common) values first — they are the least likely to be flagged as rare.
func (t *rareTracker) evictCommon(keep int) {
	type kv struct {
		v string
		n int
	}
	all := make([]kv, 0, len(t.seen))
	for v, n := range t.seen {
		all = append(all, kv{v, n})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].n > all[j].n }) // most common first
	for _, e := range all[:len(all)-keep] {
		delete(t.seen, e.v)
	}
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
	geo        map[string]*detect.GeoModel          // per-series location baseline (lat_long)
	rare       map[string]*rareTracker              // per rare-detector value frequencies
	provider   model.Provider                       // heavy-model math (seasonality, …)
	threshold  float64

	pending   map[time.Time][]core.DataPoint // streaming buffer (open buckets)
	watermark time.Time
	hasMark   bool

	// Series-model memory bound (the Elastic ML model_memory_limit equivalent).
	// A high-cardinality by/partition/over field would otherwise allocate one
	// permanent model per distinct value and grow without limit. seriesLRU stamps
	// each series' last use; when the count exceeds MaxSeries the least-recently-
	// used are evicted from every model map.
	seriesLRU    map[string]int64
	seriesScores map[string][]float64 // recent scores per series, for adaptive sensitivity
	lruTick      int64
	MaxSeries    int // 0 → defaultMaxSeries
	Evicted      int64

	// LateDropped counts streaming points rejected because their bucket had
	// already closed (out-of-order / delayed arrivals).
	LateDropped int64

	// GapsFilled counts synthesised empty buckets scored as zeros for count-family
	// detectors (a traffic drop to zero shows up as a low anomaly). GapsSkipped
	// counts gap-fill ranges too large to materialise (see maxGapFill) — reported
	// so a bounded run never silently looks like full coverage.
	GapsFilled  int64
	GapsSkipped int64
}

// maxGapFill bounds how many missing buckets a single gap-fill will synthesise,
// so a sparse series over a long horizon can't allocate unbounded buckets.
const maxGapFill = 1_000_000

// boundsZ is the z used for a record's typical range (model_lower/upper): ~95%.
const boundsZ = 1.96

// defaultMaxSeries caps the number of distinct per-series models an engine keeps
// resident. At ~10 KB/model this is ~500 MB worst case — a backstop, not a
// tuning knob; set Engine.MaxSeries to change it.
const defaultMaxSeries = 50000

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
		geo:        make(map[string]*detect.GeoModel),
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
	// A bucket inside a calendar window (a known event: release, sale,
	// maintenance) is excluded from analysis ENTIRELY — not scored, and not fed
	// into any baseline. Suppressing only the alert while still training would
	// poison the model (the event's level becomes "normal"), so the whole bucket
	// is skipped, matching Elastic ML's calendar/scheduled-event semantics.
	if e.inCalendar(bt) {
		return br
	}
	for _, d := range e.job.Detectors {
		switch {
		case d.IsMultivariate():
			e.scoreMultivariate(&br, d, bt, pts)
		case d.Function == jobspec.FuncRare || d.Function == jobspec.FuncFreqRare:
			e.scoreRare(&br, d, bt, pts)
		case d.Function == jobspec.FuncInfoContent:
			e.scoreInfoContent(&br, d, bt, pts)
		case d.Function == jobspec.FuncTimeOfDay || d.Function == jobspec.FuncTimeOfWeek:
			e.scoreTimeOf(&br, d, bt, pts)
		case d.Function == jobspec.FuncLatLong:
			e.scoreGeo(&br, d, bt, pts)
		case d.IsPopulation():
			e.scorePopulation(&br, d, bt, pts)
		default:
			e.scoreTemporal(&br, d, bt, pts)
		}
	}
	return br
}

// Interim scores the currently-open (pending) buckets provisionally — WITHOUT
// closing them, learning from them, or mutating any engine state — and marks
// each record is_interim. It lets a caller alert on the in-progress bucket
// mid-span; the definitive record (which may change or disappear as the rest of
// the bucket's data arrives) is emitted later when the bucket closes.
//
// Only the standard temporal detectors (count / value functions, per by-series)
// produce interim results, and only against a baseline that has already warmed:
// they peek via the model's non-mutating Score. Seasonal / distribution /
// multivariate / rare / info_content / time_of_* detectors are evaluated only on
// closed buckets (their scorers update learned state, so there is no safe peek).
func (e *Engine) Interim() []core.BucketResult {
	times := make([]time.Time, 0, len(e.pending))
	for t := range e.pending {
		times = append(times, t)
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	out := make([]core.BucketResult, 0, len(times))
	for _, bt := range times {
		br := core.BucketResult{Time: bt}
		if e.inCalendar(bt) {
			out = append(out, br)
			continue
		}
		for _, d := range e.job.Detectors {
			if !interimEligible(d) {
				continue
			}
			e.scoreTemporalPeek(&br, d, bt, e.pending[bt])
		}
		out = append(out, br)
	}
	return out
}

// interimEligible reports whether a detector supports provisional (open-bucket)
// scoring — only the plain temporal Model path, which has a non-mutating peek.
func interimEligible(d jobspec.Detector) bool {
	if d.IsMultivariate() || d.IsPopulation() || d.Seasonal || d.Distribution {
		return false
	}
	switch d.Function {
	case jobspec.FuncRare, jobspec.FuncFreqRare, jobspec.FuncInfoContent,
		jobspec.FuncTimeOfDay, jobspec.FuncTimeOfWeek:
		return false
	}
	return true
}

// scoreTemporalPeek mirrors scoreTemporal but PEEKS: it scores against the
// existing per-series baseline via Model.Score (no learning), never creates or
// touches series bookkeeping, and marks each emitted record interim. A series
// with no baseline yet is skipped (nothing to compare against).
func (e *Engine) scoreTemporalPeek(br *core.BucketResult, d jobspec.Detector, bt time.Time, pts []core.DataPoint) {
	bySeries := make(map[string][]core.DataPoint)
	for _, p := range pts {
		bySeries[seriesKey(d, p.Fields)] = append(bySeries[seriesKey(d, p.Fields)], p)
	}
	if d.CountsEmptyAsZero() {
		if _, ok := bySeries[""]; !ok {
			bySeries[""] = nil
		}
	}
	for sk, sp := range bySeries {
		var val float64
		var ok bool
		if d.Function == jobspec.FuncRate {
			val, ok = e.rateValue(d, sp)
		} else {
			val, ok = detect.Aggregate(d.Function, d.Field, sp)
		}
		if !ok {
			continue
		}
		mdl := e.models[d.ID()+"|"+sk]
		if mdl == nil {
			continue // no baseline learned yet — nothing to peek against
		}
		prob, score, typical, dir := mdl.Score(val)
		if score < e.threshold {
			continue
		}
		lower, upper := mdl.Bounds(boundsZ)
		e.emit(br, d, core.Record{
			Time: bt, Detector: d.ID(), Series: sk,
			Actual: val, Typical: typical, Lower: lower, Upper: upper, Probability: prob,
			Score: score, Direction: dir, Kind: "metric", Interim: true,
			Influencers: e.influencers(d, sp),
		})
	}
}

// scoreTemporal is the standard per-series temporal detector: each by/partition
// series is scored against its own learned baseline.
func (e *Engine) scoreTemporal(br *core.BucketResult, d jobspec.Detector, bt time.Time, pts []core.DataPoint) {
	bySeries := make(map[string][]core.DataPoint)
	for _, p := range pts {
		sk := seriesKey(d, p.Fields)
		bySeries[sk] = append(bySeries[sk], p)
	}
	// For a count-family single-series detector, an empty bucket is a real zero
	// (a drop in traffic), so ensure the lone series is scored even with no points.
	if d.CountsEmptyAsZero() {
		if _, ok := bySeries[""]; !ok {
			bySeries[""] = nil
		}
	}
	for sk, sp := range bySeries {
		var val float64
		var ok bool
		if d.Function == jobspec.FuncRate {
			val, ok = e.rateValue(d, sp)
		} else {
			val, ok = detect.Aggregate(d.Function, d.Field, sp)
		}
		if !ok {
			continue
		}
		mk := d.ID() + "|" + sk
		e.touchSeries(mk)
		var prob, score, typical float64
		var dir core.Direction
		var lower, upper float64
		kind := "metric"
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
				sm = detect.NewSeasonalModel(d.EffectiveSide(), e.provider, e.job.BucketSpan)
				e.seasonal[mk] = sm
			}
			prob, score, typical, dir = sm.Observe(bt, val)
			lower, upper = sm.Bounds(boundsZ)
		default:
			mdl := e.model(mk, d.EffectiveSide())
			prob, score, typical, dir = mdl.Observe(val)
			if mdl.LastMulti() {
				kind = "multi_bucket"
			}
			lower, upper = mdl.Bounds(boundsZ)
		}
		admit := score >= e.threshold
		if admit && e.job.Sensitivity > 0 {
			admit = e.adaptiveAdmit(mk, score) // must also clear the series' own recent quantile
		}
		if admit {
			e.emit(br, d, core.Record{
				Time: bt, Detector: d.ID(), Series: sk,
				Actual: val, Typical: typical, Lower: lower, Upper: upper, Probability: prob,
				Score: score, Direction: dir, Kind: kind,
				Influencers: e.influencers(d, sp),
			})
		}
	}
}

// rateValue computes the bucket's per-second rate: when a field is set it is the
// sum of that field's values over the bucket span (a counter/throughput rate);
// with no field it is the event count per second. Returns false for an empty
// bucket only when there's nothing to rate — but a zero-count bucket is a
// legitimate rate of 0, so count-rate always yields a value.
func (e *Engine) rateValue(d jobspec.Detector, pts []core.DataPoint) (float64, bool) {
	secs := e.job.BucketSpan.Seconds()
	if secs <= 0 {
		secs = 1
	}
	if d.Field == "" {
		return float64(len(pts)) / secs, true // events per second
	}
	if len(pts) == 0 {
		return 0, false
	}
	var s float64
	for _, p := range pts {
		s += valueRate(p, d.Field)
	}
	return s / secs, true
}

// valueRate reads a point's rate field (Values[field], falling back to Value).
func valueRate(p core.DataPoint, field string) float64 {
	if field != "" && p.Values != nil {
		if v, ok := p.Values[field]; ok {
			return v
		}
	}
	return p.Value
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
		val, ok := detect.Aggregate(d.Function, d.Field, byEntity[ent])
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
		e.touchSeries(mk)
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

// scoreGeo scores each series' bucket LOCATION (the mean of the bucket's points'
// lat/lon) against the series' learned geographic centroid — an unusually distant
// location fires. Points carry their coordinates in Values["lat"]/["lon"].
func (e *Engine) scoreGeo(br *core.BucketResult, d jobspec.Detector, bt time.Time, pts []core.DataPoint) {
	bySeries := make(map[string][]core.DataPoint)
	for _, p := range pts {
		sk := seriesKey(d, p.Fields)
		bySeries[sk] = append(bySeries[sk], p)
	}
	for sk, sp := range bySeries {
		lat, lon, ok := meanLatLon(sp)
		if !ok {
			continue
		}
		mk := d.ID() + "|" + sk
		e.touchSeries(mk)
		gm := e.geo[mk]
		if gm == nil {
			gm = detect.NewGeoModel()
			e.geo[mk] = gm
		}
		actualKm, _ := gm.DistanceKm(lat, lon) // distance from the centroid learned SO FAR
		prob, score, typicalKm, dir := gm.Observe(lat, lon)
		if score >= e.threshold {
			e.emit(br, d, core.Record{
				Time: bt, Detector: d.ID(), Series: sk,
				Actual: actualKm, Typical: typicalKm, Probability: prob,
				Score: score, Direction: dir, Kind: "lat_long",
				Influencers: e.influencers(d, sp),
			})
		}
	}
}

// meanLatLon returns the mean location of a bucket's points (via unit-vector
// averaging, correct across the antimeridian/poles). ok is false when no point
// carries both coordinates.
func meanLatLon(pts []core.DataPoint) (lat, lon float64, ok bool) {
	var x, y, z float64
	n := 0
	for _, p := range pts {
		if p.Values == nil {
			continue
		}
		la, laok := p.Values["lat"]
		lo, look := p.Values["lon"]
		if !laok || !look {
			continue
		}
		phi, lam := la*math.Pi/180, lo*math.Pi/180
		x += math.Cos(phi) * math.Cos(lam)
		y += math.Cos(phi) * math.Sin(lam)
		z += math.Sin(phi)
		n++
	}
	if n == 0 {
		return 0, 0, false
	}
	lon = math.Atan2(y, x) * 180 / math.Pi
	lat = math.Atan2(z, math.Hypot(x, y)) * 180 / math.Pi
	return lat, lon, true
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
			bv, bscore := dominant(pp, d.ByField)
			infl := []core.Influencer{{Field: d.ByField, Value: bv, Score: bscore}}
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
		e.touchSeries(key)
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

// scoreRare flags by_field values that are rare across the analysed window. For
// freq_rare the score is additionally weighted by the value's in-bucket
// frequency — a rare value that recurs many times in one bucket is more
// anomalous than a lone occurrence (Elastic ML's freq_rare intuition).
func (e *Engine) scoreRare(br *core.BucketResult, d jobspec.Detector, bt time.Time, pts []core.DataPoint) {
	freq := d.Function == jobspec.FuncFreqRare
	// A plain rare hit is a fixed score; freq_rare can climb to 100 with frequency,
	// so only the plain case can be short-circuited below threshold.
	if !freq && rareValueScore < e.threshold {
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
	// Bound the per-value frequency map: a high-cardinality by_field would
	// otherwise grow it without limit. Drop the most-common values (the least
	// "rare", least likely to be flagged) when over the cap.
	if len(tr.seen) > maxRareValues {
		tr.evictCommon(maxRareValues * 9 / 10)
	}
	for _, v := range vals {
		tr.seen[v]++
		if tr.buckets < rareWarmup {
			continue
		}
		if tr.seen[v] <= rareMaxBuckets {
			score, kind := float64(rareValueScore), "rare"
			if freq {
				// +10 bits per doubling of in-bucket frequency, capped at 100.
				score = math.Min(100, rareValueScore+10*math.Log2(float64(present[v]+1)))
				kind = "freq_rare"
			}
			if score < e.threshold {
				continue
			}
			e.emit(br, d, core.Record{
				Time: bt, Detector: d.ID(), Series: v,
				Actual: float64(present[v]), Score: score, Direction: core.DirUp,
				Kind: kind, Influencers: []core.Influencer{{Field: d.ByField, Value: v}},
			})
		}
	}
}

// adaptiveScoreWindow bounds the per-series recent-score ring.
const adaptiveScoreWindow = 512

// adaptiveAdmit records a score in the series' rolling window and reports whether
// it clears the sensitivity quantile of that window — the per-series adaptive
// gate. It records every score (so the quantile reflects the true distribution),
// and admits while the window is still warming (too few samples to estimate a
// quantile), so it never blocks a genuinely new series.
func (e *Engine) adaptiveAdmit(mk string, score float64) bool {
	if e.seriesScores == nil {
		e.seriesScores = make(map[string][]float64)
	}
	buf := append(e.seriesScores[mk], score)
	if len(buf) > adaptiveScoreWindow {
		buf = buf[len(buf)-adaptiveScoreWindow:]
	}
	e.seriesScores[mk] = buf
	if len(buf) < 20 {
		return true // not enough history to gate on yet
	}
	return score >= stats.Quantile(buf, e.job.Sensitivity)
}

// gapFillActive reports whether the job has any detector for which a missing
// bucket is a meaningful zero — if so, the engine materialises empty buckets so
// a drop to zero is detected rather than silently skipped.
func (e *Engine) gapFillActive() bool {
	for _, d := range e.job.Detectors {
		if d.CountsEmptyAsZero() {
			return true
		}
	}
	return false
}

// fillGaps extends occupied bucket times with the empty buckets between them (at
// bucket-span steps) and ensures the buckets map has a (nil) entry for each, so
// scoreBucket scores them as zeros. Bounded by maxGapFill; over the bound it
// leaves the occupied times untouched and records the skip. Returns the full
// ordered time list.
func (e *Engine) fillGaps(times []time.Time, span time.Duration, buckets map[time.Time][]core.DataPoint) []time.Time {
	if len(times) < 2 {
		return times
	}
	first, last := times[0], times[len(times)-1]
	total := int(last.Sub(first)/span) + 1
	if total > maxGapFill {
		e.GapsSkipped += int64(total - len(times))
		return times
	}
	full := make([]time.Time, 0, total)
	for t := first; !t.After(last); t = t.Add(span) {
		full = append(full, t)
		if _, ok := buckets[t]; !ok {
			buckets[t] = nil
			e.GapsFilled++
		}
	}
	return full
}

// model fetches (or lazily creates) a model by key.
func (e *Engine) model(key string, side jobspec.Side) *detect.Model {
	e.touchSeries(key)
	m := e.models[key]
	if m == nil {
		m = detect.NewModel(side)
		e.models[key] = m
	}
	return m
}

// touchSeries records a series key's use for LRU accounting and evicts the
// least-recently-used models when the resident count exceeds MaxSeries.
func (e *Engine) touchSeries(key string) {
	if e.seriesLRU == nil {
		e.seriesLRU = make(map[string]int64)
	}
	e.lruTick++
	_, existed := e.seriesLRU[key]
	e.seriesLRU[key] = e.lruTick
	if !existed {
		e.evictSeries()
	}
}

// evictSeries drops the oldest ~10% of series when over the cap (batched so the
// O(n log n) sort amortises across many inserts).
func (e *Engine) evictSeries() {
	max := e.MaxSeries
	if max <= 0 {
		max = defaultMaxSeries
	}
	if len(e.seriesLRU) <= max {
		return
	}
	type kv struct {
		k string
		t int64
	}
	all := make([]kv, 0, len(e.seriesLRU))
	for k, t := range e.seriesLRU {
		all = append(all, kv{k, t})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].t < all[j].t })
	keep := max * 9 / 10
	for _, e2 := range all[:len(all)-keep] {
		e.dropSeries(e2.k)
	}
}

// dropSeries removes a series key from every model map (it lives in exactly one).
func (e *Engine) dropSeries(key string) {
	delete(e.seriesLRU, key)
	delete(e.models, key)
	delete(e.seasonal, key)
	delete(e.distrib, key)
	delete(e.multivar, key)
	delete(e.slotModels, key)
	delete(e.geo, key)
	delete(e.seriesScores, key)
	e.Evicted++
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
		if v, score := dominant(sp, f); v != "" {
			infl = append(infl, core.Influencer{Field: f, Value: v, Score: score})
		}
	}
	return infl
}

// dominant returns the most-frequent value of field in the bucket and its
// INFLUENCE SCORE: the share of the bucket's points that value carries (0..1) —
// a deterministic proxy for Elastic ML's influencer score. A value present in
// most of the anomalous points is a strong influencer; one in a handful is weak.
func dominant(pts []core.DataPoint, field string) (string, float64) {
	counts := make(map[string]int)
	total := 0
	for _, p := range pts {
		if v := p.Fields[field]; v != "" {
			counts[v]++
			total++
		}
	}
	best, bestN := "", 0
	for v, n := range counts {
		if n > bestN {
			best, bestN = v, n
		}
	}
	if total == 0 {
		return "", 0
	}
	return best, float64(bestN) / float64(total)
}

func addRec(br *core.BucketResult, r core.Record) {
	br.Records = append(br.Records, r)
	if r.Score > br.Score {
		br.Score = r.Score
	}
}

// RenormalizeResults rescales record scores relative to the most anomalous
// bucket in the set: severity = -log10(probability), the score becomes
// severity / anchor · 100 where anchor = max(observed severity, absolute
// full-scale). So a later, larger anomaly pulls earlier moderate ones down —
// the scores reflect how unusual each bucket is relative to what's been seen
// (Elastic ML calls this renormalization). No-op below full scale.
func RenormalizeResults(results []core.BucketResult) {
	const fullScale = 12.0 // severity that maps to 100 in absolute terms
	sevOf := func(p float64) float64 {
		if p <= 0 {
			// A genuine underflow (truly extreme) anchors at full scale, never
			// beyond it — so it can't unfairly deflate every other record.
			return fullScale
		}
		return -math.Log10(p)
	}
	anchor := fullScale
	for i := range results {
		for _, r := range results[i].Records {
			if s := sevOf(r.Probability); s > anchor {
				anchor = s
			}
		}
	}
	for i := range results {
		br := &results[i]
		br.Score = 0
		for j := range br.Records {
			r := &br.Records[j]
			sc := sevOf(r.Probability) / anchor * 100
			if sc > 100 {
				sc = 100
			}
			r.Score = sc
			if sc > br.Score {
				br.Score = sc
			}
		}
	}
}

// emit records an anomaly unless a calendar window or a detector rule suppresses
// it.
func (e *Engine) emit(br *core.BucketResult, d jobspec.Detector, r core.Record) {
	if e.suppressed(d, r) {
		return
	}
	// Backfill the probability from the assigned score for detectors that don't
	// compute a tail probability (rare / info_content / time_of_day). Without
	// this a probability-less record reads as p=0 (maximally extreme) and would
	// dominate RenormalizeResults, pinning benign one-offs to score 100.
	if r.Probability <= 0 && r.Score > 0 {
		r.Probability = probFromScore(r.Score)
	}
	addRec(br, r)
}

// probFromScore inverts scoreFromProbability (score = -log10(p)/12·100).
func probFromScore(score float64) float64 {
	if score <= 0 {
		return 1
	}
	if score >= 100 {
		return 1e-12
	}
	return math.Pow(10, -score/100*12)
}

// suppressed applies job calendars + detector rules to one candidate record.
// inCalendar reports whether a bucket time falls in any calendar window.
func (e *Engine) inCalendar(bt time.Time) bool {
	for _, c := range e.job.Calendars {
		if !bt.Before(c.Start) && bt.Before(c.End) {
			return true
		}
	}
	return false
}

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
		diff := math.Abs(r.Actual - r.Typical)
		if rule.SkipDiffBelow != nil && diff < *rule.SkipDiffBelow {
			return true
		}
		if rule.SkipDiffRatioBelow != nil && r.Typical != 0 && diff/math.Abs(r.Typical) < *rule.SkipDiffRatioBelow {
			return true
		}
		if len(rule.SkipHoursUTC) > 0 && containsInt(rule.SkipHoursUTC, r.Time.UTC().Hour()) {
			return true
		}
		if len(rule.SkipWeekdaysUTC) > 0 && containsInt(rule.SkipWeekdaysUTC, int(r.Time.UTC().Weekday())) {
			return true
		}
		for _, v := range rule.SkipValues {
			if r.Series == v {
				return true
			}
		}
		if len(rule.SkipInfluencer) > 0 {
			for _, infl := range r.Influencers {
				for _, v := range rule.SkipInfluencer[infl.Field] {
					if infl.Value == v {
						return true
					}
				}
			}
		}
	}
	return false
}

// containsInt reports whether xs contains v.
func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
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
	if e.gapFillActive() {
		times = e.fillGaps(times, span, buckets)
	}

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
	// A point whose bucket has already been closed and scored must be dropped:
	// re-opening it would emit a duplicate, out-of-order BucketResult and pollute
	// the baseline (which already learned that bucket). A late point whose bucket
	// is still open (in the pending buffer) is fine to fold in.
	if _, open := e.pending[bt]; !open && bt.Before(e.watermark) {
		e.LateDropped++
		return nil
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
	// Gap-fill the range being closed: for count-family jobs, buckets that saw no
	// points between the earliest open bucket and the close boundary are scored as
	// zeros (a silent series is a low anomaly), matching the batch Run path. The
	// upper edge is the last bucket strictly before the limit (a fresh newer bucket
	// arriving) or the last pending bucket on a Flush (zero limit).
	if e.gapFillActive() && len(times) > 0 {
		span := e.job.BucketSpan
		first := times[0]
		last := times[len(times)-1]
		if !limit.IsZero() {
			if b := limit.Add(-span); b.After(last) {
				last = b
			}
		}
		times = e.fillGaps([]time.Time{first, last}, span, e.pending)
	}
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
	// The non-plain model families — persisted so seasonal/distribution/
	// multivariate/time-of-day jobs survive a restart with their learned state
	// instead of silently cold-starting.
	Seasonal map[string]detect.SeasonalState     `json:"seasonal,omitempty"`
	Distrib  map[string]detect.DistributionState `json:"distrib,omitempty"`
	Multivar map[string]detect.MultivariateState `json:"multivar,omitempty"`
	Slots    map[string]detect.ModelState        `json:"slots,omitempty"`
	Geo      map[string]detect.GeoState          `json:"geo,omitempty"`
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
	seas := make(map[string]detect.SeasonalState, len(e.seasonal))
	for k, m := range e.seasonal {
		seas[k] = m.State()
	}
	dist := make(map[string]detect.DistributionState, len(e.distrib))
	for k, m := range e.distrib {
		dist[k] = m.State()
	}
	mv := make(map[string]detect.MultivariateState, len(e.multivar))
	for k, m := range e.multivar {
		mv[k] = m.State()
	}
	slots := make(map[string]detect.ModelState, len(e.slotModels))
	for k, m := range e.slotModels {
		slots[k] = m.State()
	}
	geo := make(map[string]detect.GeoState, len(e.geo))
	for k, m := range e.geo {
		geo[k] = m.State()
	}
	return Snapshot{
		JobName:   e.job.Name,
		Threshold: e.threshold,
		Watermark: e.watermark,
		HasMark:   e.hasMark,
		Models:    ms,
		Rare:      rs,
		Seasonal:  seas,
		Distrib:   dist,
		Multivar:  mv,
		Slots:     slots,
		Geo:       geo,
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
	e.seasonal = make(map[string]*detect.SeasonalModel, len(s.Seasonal))
	for k, st := range s.Seasonal {
		e.seasonal[k] = detect.SeasonalFromState(st, e.provider)
	}
	e.distrib = make(map[string]*detect.DistributionModel, len(s.Distrib))
	for k, st := range s.Distrib {
		e.distrib[k] = detect.DistributionFromState(st, e.provider)
	}
	e.multivar = make(map[string]*detect.MultivariateModel, len(s.Multivar))
	for k, st := range s.Multivar {
		e.multivar[k] = detect.MultivariateFromState(st)
	}
	e.slotModels = make(map[string]*detect.Model, len(s.Slots))
	for k, st := range s.Slots {
		e.slotModels[k] = detect.ModelFromState(st)
	}
	e.geo = make(map[string]*detect.GeoModel, len(s.Geo))
	for k, st := range s.Geo {
		e.geo[k] = detect.GeoFromState(st)
	}
	// Re-seed the LRU so restored models are subject to the same memory bound.
	e.seriesLRU = make(map[string]int64)
	e.lruTick = 0
	for k := range e.models {
		e.lruTick++
		e.seriesLRU[k] = e.lruTick
	}
	for k := range e.seasonal {
		e.lruTick++
		e.seriesLRU[k] = e.lruTick
	}
	for k := range e.distrib {
		e.lruTick++
		e.seriesLRU[k] = e.lruTick
	}
	for k := range e.multivar {
		e.lruTick++
		e.seriesLRU[k] = e.lruTick
	}
	for k := range e.slotModels {
		e.lruTick++
		e.seriesLRU[k] = e.lruTick
	}
	for k := range e.geo {
		e.lruTick++
		e.seriesLRU[k] = e.lruTick
	}
}
