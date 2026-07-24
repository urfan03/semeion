package engine

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/detect"
	"github.com/urfan03/semeion/jobspec"
	"github.com/urfan03/semeion/model"
	"github.com/urfan03/semeion/stats"
)

const (
	defaultThreshold = 50
	rareWarmup       = 20
	rareMaxBuckets   = 2
)

type rareTracker struct {
	buckets int
	seen    map[string]int
}

const maxRareValues = 100000

func (t *rareTracker) evictCommon(keep int) {
	type kv struct {
		v string
		n int
	}
	all := make([]kv, 0, len(t.seen))
	for v, n := range t.seen {
		all = append(all, kv{v, n})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].n != all[j].n {
			return all[i].n > all[j].n
		}
		return all[i].v < all[j].v
	})
	for _, e := range all[:len(all)-keep] {
		delete(t.seen, e.v)
	}
}

type Engine struct {
	job        jobspec.Job
	models     map[string]*detect.Model
	seasonal   map[string]*detect.SeasonalModel
	distrib    map[string]*detect.DistributionModel
	multivar   map[string]*detect.MultivariateModel
	slotModels map[string]*detect.Model
	geo        map[string]*detect.GeoModel
	rare       map[string]*rareTracker
	provider   model.Provider
	threshold  float64

	pending   map[time.Time][]core.DataPoint
	watermark time.Time
	hasMark   bool

	seriesLRU        map[string]int64
	seriesScores     map[string][]float64
	lastSeen         map[string]time.Time
	feedback         map[string]int
	countSeries      map[string]map[string]bool
	curBucket        time.Time
	lruTick          int64
	MaxSeries        int
	ModelMemoryLimit int64
	Evicted          int64

	LateDropped  int64
	LateAccepted int64

	GapsFilled  int64
	GapsSkipped int64

	Grace time.Duration
}

const maxGapFill = 1_000_000

const boundsZ = 1.96

const defaultMaxSeries = 50000

func New(job jobspec.Job) (*Engine, error) {
	return NewWithProvider(job, model.NewGoProvider())
}

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

func (e *Engine) SetThreshold(t float64) { e.threshold = t }

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

func (e *Engine) scoreBucket(bt time.Time, pts []core.DataPoint) core.BucketResult {
	br := core.BucketResult{Time: bt}
	e.curBucket = bt

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
		case d.Function == jobspec.FuncRatio:
			e.scoreRatio(&br, d, bt, pts)
		case d.IsPopulation():
			e.scorePopulation(&br, d, bt, pts)
		default:
			e.scoreTemporal(&br, d, bt, pts)
		}
	}
	sortRecords(br.Records)
	return br
}

func sortRecords(recs []core.Record) {
	sort.SliceStable(recs, func(i, j int) bool {
		if recs[i].Detector != recs[j].Detector {
			return recs[i].Detector < recs[j].Detector
		}
		if recs[i].Series != recs[j].Series {
			return recs[i].Series < recs[j].Series
		}
		return recs[i].Kind < recs[j].Kind
	})
}

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
			val, ok = e.aggregate(d, sp)
		}
		if !ok {
			continue
		}
		mdl := e.models[d.ID()+"|"+sk]
		if mdl == nil {
			continue
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

func (e *Engine) scoreRatio(br *core.BucketResult, d jobspec.Detector, bt time.Time, pts []core.DataPoint) {
	bySeries := make(map[string][]core.DataPoint)
	for _, p := range pts {
		sk := seriesKey(d, p.Fields)
		bySeries[sk] = append(bySeries[sk], p)
	}
	for sk, sp := range bySeries {
		num, den := 0.0, 0.0
		for _, p := range sp {
			if p.Values != nil {
				num += p.Values[d.Field]
				den += p.Values[d.DenomField]
			}
		}
		if den == 0 {
			continue
		}
		val := num / den
		mk := d.ID() + "|" + sk
		e.touchSeries(mk)
		mdl := e.model(mk, d.EffectiveSide())
		prob, score, typical, dir := mdl.Observe(val)
		lower, upper := mdl.Bounds(boundsZ)
		admit := score >= e.threshold
		if admit && e.job.Sensitivity > 0 {
			admit = e.adaptiveAdmit(mk, score)
		}
		if admit {
			e.emit(br, d, core.Record{
				Time: bt, Detector: d.ID(), Series: sk,
				Actual: val, Typical: typical, Lower: lower, Upper: upper, Probability: prob,
				Score: score, Direction: dir, Kind: "ratio",
				Influencers: e.influencers(d, sp),
			})
		}
	}
}

func (e *Engine) scoreTemporal(br *core.BucketResult, d jobspec.Detector, bt time.Time, pts []core.DataPoint) {
	bySeries := make(map[string][]core.DataPoint)
	for _, p := range pts {
		sk := seriesKey(d, p.Fields)
		bySeries[sk] = append(bySeries[sk], p)
	}

	if d.CountsEmptyAsZero() {
		if _, ok := bySeries[""]; !ok {
			bySeries[""] = nil
		}
	}
	if d.CountFamilySplit() {
		e.zeroFillKnown(d, bySeries)
	}
	for sk, sp := range bySeries {
		var val float64
		var ok bool
		if d.Function == jobspec.FuncRate {
			val, ok = e.rateValue(d, sp)
		} else {
			val, ok = e.aggregate(d, sp)
		}
		if !ok {
			continue
		}
		mk := d.ID() + "|" + sk
		e.touchSeries(mk)
		var prob, score, typical float64
		var dir core.Direction
		var lower, upper, mbImpact float64
		kind := "metric"
		switch {
		case d.Distribution:
			dm := e.distrib[mk]
			if dm == nil {
				dm = detect.NewDistributionModel(d.EffectiveSide(), e.provider)
				e.distrib[mk] = dm
			}
			prob, score, typical, dir = dm.Observe(val)
			lower, upper = dm.Bounds(boundsZ)
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
			if skipLearn(d, val) {
				prob, score, typical, dir = mdl.Score(val)
			} else {
				prob, score, typical, dir = mdl.Observe(val)
				if mdl.LastMulti() {
					kind = "multi_bucket"
				}
				mbImpact = mdl.MultiBucketImpact()
			}
			lower, upper = mdl.Bounds(boundsZ)
		}
		admit := score >= e.threshold
		if admit && e.job.Sensitivity > 0 {
			admit = e.adaptiveAdmit(mk, score)
		}
		if admit {
			e.emit(br, d, core.Record{
				Time: bt, Detector: d.ID(), Series: sk,
				Actual: val, Typical: typical, Lower: lower, Upper: upper, Probability: prob,
				Score: score, Direction: dir, Kind: kind, MultiBucketImpact: mbImpact,
				Influencers: e.influencers(d, sp),
			})
		}
	}
}

func skipLearn(d jobspec.Detector, val float64) bool {
	for _, rule := range d.Rules {
		if !rule.SkipModelUpdate {
			continue
		}
		if rule.SkipActualAbove != nil && val > *rule.SkipActualAbove {
			return true
		}
		if rule.SkipActualBelow != nil && val < *rule.SkipActualBelow {
			return true
		}
		if rule.SkipActualAbove == nil && rule.SkipActualBelow == nil {
			return true
		}
	}
	return false
}

func (e *Engine) aggregate(d jobspec.Detector, pts []core.DataPoint) (float64, bool) {
	if d.SummaryCountField != "" && d.Function == jobspec.FuncCount {
		var s float64
		for _, p := range pts {
			if p.Values != nil {
				s += p.Values[d.SummaryCountField]
			}
		}
		return s, true
	}
	return detect.Aggregate(d.Function, d.Field, pts)
}

func (e *Engine) rateValue(d jobspec.Detector, pts []core.DataPoint) (float64, bool) {
	secs := e.job.BucketSpan.Seconds()
	if secs <= 0 {
		secs = 1
	}
	if d.Field == "" {
		return float64(len(pts)) / secs, true
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

func valueRate(p core.DataPoint, field string) float64 {
	if field != "" && p.Values != nil {
		if v, ok := p.Values[field]; ok {
			return v
		}
	}
	return p.Value
}

func (e *Engine) scorePopulation(br *core.BucketResult, d jobspec.Detector, bt time.Time, pts []core.DataPoint) {
	bySplit := make(map[string][]core.DataPoint)
	for _, p := range pts {
		if p.Fields[d.OverField] == "" {
			continue
		}
		bySplit[seriesKey(d, p.Fields)] = append(bySplit[seriesKey(d, p.Fields)], p)
	}
	splits := make([]string, 0, len(bySplit))
	for sk := range bySplit {
		splits = append(splits, sk)
	}
	sort.Strings(splits)
	for _, split := range splits {
		e.scorePopulationSplit(br, d, bt, split, bySplit[split])
	}
}

func (e *Engine) scorePopulationSplit(br *core.BucketResult, d jobspec.Detector, bt time.Time, split string, pts []core.DataPoint) {
	byEntity := make(map[string][]core.DataPoint)
	for _, p := range pts {
		byEntity[p.Fields[d.OverField]] = append(byEntity[p.Fields[d.OverField]], p)
	}
	if len(byEntity) == 0 {
		return
	}
	m := e.model(d.ID()+"|"+split+"|__pool__", d.EffectiveSide())

	type ev struct {
		entity              string
		val, prob, score, t float64
		lower, upper        float64
		dir                 core.Direction
	}
	ents := make([]string, 0, len(byEntity))
	for ent := range byEntity {
		ents = append(ents, ent)
	}
	sort.Strings(ents)

	evs := make([]ev, 0, len(ents))
	for _, ent := range ents {
		val, ok := e.aggregate(d, byEntity[ent])
		if !ok {
			continue
		}
		prob, score, typical, dir := m.Score(val)
		lower, upper := m.Bounds(boundsZ)
		evs = append(evs, ev{ent, val, prob, score, typical, lower, upper, dir})
	}
	for _, x := range evs {
		m.Learn(x.val)
	}
	for _, x := range evs {
		if x.score >= e.threshold {
			infl := []core.Influencer{{Field: d.OverField, Value: x.entity}}
			if p0 := byEntity[x.entity]; len(p0) > 0 {
				if d.PartitionField != "" {
					infl = append(infl, core.Influencer{Field: d.PartitionField, Value: p0[0].Fields[d.PartitionField]})
				}
				if d.ByField != "" {
					infl = append(infl, core.Influencer{Field: d.ByField, Value: p0[0].Fields[d.ByField]})
				}
			}
			series := x.entity
			if split != "" {
				series = split + ";" + d.OverField + "=" + x.entity
			}
			e.emit(br, d, core.Record{
				Time: bt, Detector: d.ID(), Series: series,
				Actual: x.val, Typical: x.t, Lower: x.lower, Upper: x.upper, Probability: x.prob,
				Score: x.score, Direction: x.dir, Kind: "population",
				Influencers: infl,
			})
		}
	}
}

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
				Lower: 0, Upper: chiRadius95(len(d.Fields)),
				Probability: prob, Score: score, Direction: core.DirUp, Kind: "multivariate",
				Influencers: multivarInfluencers(d.Fields, contrib),
			})
		}
	}
}

func chiRadius95(k int) float64 {
	if k <= 0 {
		return 0
	}
	const z = 1.645
	t := 2.0 / (9.0 * float64(k))
	q := float64(k) * math.Pow(1-t+z*math.Sqrt(t), 3)
	if q < 0 {
		return 0
	}
	return math.Sqrt(q)
}

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
	sort.SliceStable(arr, func(i, j int) bool {
		if arr[i].c != arr[j].c {
			return arr[i].c > arr[j].c
		}
		return arr[i].f < arr[j].f
	})
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
		actualKm, _ := gm.DistanceKm(lat, lon)
		prob, score, typicalKm, dir := gm.Observe(lat, lon)
		if score >= e.threshold {
			lower, upper := gm.Bounds(boundsZ)
			e.emit(br, d, core.Record{
				Time: bt, Detector: d.ID(), Series: sk,
				Actual: actualKm, Typical: typicalKm, Lower: lower, Upper: upper, Probability: prob,
				Score: score, Direction: dir, Kind: "lat_long",
				Influencers: e.influencers(d, sp),
			})
		}
	}
}

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
	keys := make([]string, 0, len(counts))
	for v := range counts {
		keys = append(keys, v)
	}
	sort.Strings(keys)
	var h float64
	for _, v := range keys {
		p := float64(counts[v]) / float64(total)
		h -= p * math.Log2(p)
	}
	return h
}

func (e *Engine) scoreRare(br *core.BucketResult, d jobspec.Detector, bt time.Time, pts []core.DataPoint) {
	freq := d.Function == jobspec.FuncFreqRare
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

	if len(tr.seen) > maxRareValues {
		tr.evictCommon(maxRareValues * 9 / 10)
	}
	for _, v := range vals {
		tr.seen[v]++
		if tr.buckets < rareWarmup {
			continue
		}
		if tr.seen[v] <= rareMaxBuckets {
			freqAcross := float64(tr.seen[v]) / float64(tr.buckets)
			severity := -math.Log10(freqAcross)
			score := 50 + 15*severity
			kind := "rare"
			if freq {
				score += 10 * math.Log2(float64(present[v]+1))
				kind = "freq_rare"
			}
			if score > 100 {
				score = 100
			}
			if score < e.threshold {
				continue
			}
			e.emit(br, d, core.Record{
				Time: bt, Detector: d.ID(), Series: v,
				Actual: float64(present[v]), Probability: freqAcross, Score: score, Direction: core.DirUp,
				Kind: kind, Influencers: []core.Influencer{{Field: d.ByField, Value: v}},
			})
		}
	}
}

const adaptiveScoreWindow = 512

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
		return true
	}
	return score >= stats.Quantile(buf, e.job.Sensitivity)
}

const countSeriesCap = 50000

func (e *Engine) zeroFillKnown(d jobspec.Detector, bySeries map[string][]core.DataPoint) {
	if e.countSeries == nil {
		e.countSeries = map[string]map[string]bool{}
	}
	ks := e.countSeries[d.ID()]
	if ks == nil {
		ks = map[string]bool{}
		e.countSeries[d.ID()] = ks
	}
	for sk := range bySeries {
		if len(ks) < countSeriesCap {
			ks[sk] = true
		}
	}
	for sk := range ks {
		if _, ok := bySeries[sk]; !ok {
			bySeries[sk] = nil
		}
	}
}

func (e *Engine) gapFillActive() bool {
	for _, d := range e.job.Detectors {
		if d.CountsEmptyAsZero() || d.CountFamilySplit() {
			return true
		}
	}
	return false
}

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

func (e *Engine) model(key string, side jobspec.Side) *detect.Model {
	e.touchSeries(key)
	m := e.models[key]
	if m == nil {
		m = detect.NewModel(side)
		e.models[key] = m
	}
	return m
}

func (e *Engine) touchSeries(key string) {
	if e.seriesLRU == nil {
		e.seriesLRU = make(map[string]int64)
	}
	if e.lastSeen == nil {
		e.lastSeen = make(map[string]time.Time)
	}
	if !e.curBucket.IsZero() {
		e.lastSeen[key] = e.curBucket
	}
	e.lruTick++
	_, existed := e.seriesLRU[key]
	e.seriesLRU[key] = e.lruTick
	if !existed {
		e.evictSeries()
	}
}

func (e *Engine) EstimateModelBytes() int64 {
	const perFloat = 8
	var b int64
	for _, m := range e.models {
		b += int64(m.Count())*perFloat + 320
	}
	b += int64(len(e.seasonal)) * (6000*perFloat + 512)
	b += int64(len(e.distrib)) * (1024*perFloat + 256)
	b += int64(len(e.multivar)) * 4096
	b += int64(len(e.slotModels)) * (512*perFloat + 128)
	b += int64(len(e.geo)) * 512
	return b
}

func (e *Engine) MemoryStatus() (bytes int64, status string) {
	bytes = e.EstimateModelBytes()
	if e.ModelMemoryLimit <= 0 {
		return bytes, "ok"
	}
	switch {
	case bytes >= e.ModelMemoryLimit:
		return bytes, "hard_limit"
	case float64(bytes) >= 0.85*float64(e.ModelMemoryLimit):
		return bytes, "soft_limit"
	default:
		return bytes, "ok"
	}
}

type StaleSeries struct {
	Series string        `json:"series"`
	Last   time.Time     `json:"last"`
	Age    time.Duration `json:"age"`
}

func (e *Engine) Stale(maxAge time.Duration) []StaleSeries {
	if maxAge <= 0 || len(e.lastSeen) == 0 {
		return nil
	}
	asOf := e.watermark
	for _, last := range e.lastSeen {
		if last.After(asOf) {
			asOf = last
		}
	}
	var out []StaleSeries
	for key, last := range e.lastSeen {
		age := asOf.Sub(last)
		if age > maxAge {
			out = append(out, StaleSeries{Series: key, Last: last, Age: age})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Age > out[j].Age })
	return out
}

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

func (e *Engine) dropSeries(key string) {
	delete(e.seriesLRU, key)
	delete(e.models, key)
	delete(e.seasonal, key)
	delete(e.distrib, key)
	delete(e.multivar, key)
	delete(e.slotModels, key)
	delete(e.geo, key)
	delete(e.seriesScores, key)
	delete(e.lastSeen, key)
	for detID, set := range e.countSeries {
		prefix := detID + "|"
		if strings.HasPrefix(key, prefix) {
			delete(set, key[len(prefix):])
			if len(set) == 0 {
				delete(e.countSeries, detID)
			}
		}
	}
	e.Evicted++
}

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
		if n > bestN || (n == bestN && v < best) {
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

func RenormalizeResults(results []core.BucketResult) {
	const fullScale = 12.0
	sevOf := func(p float64) float64 {
		if p < 1e-12 {
			return fullScale
		}
		return -math.Log10(p)
	}
	key := func(r core.Record) string { return r.Detector + "\x00" + r.Series }
	anchor := map[string]float64{}
	for i := range results {
		for _, r := range results[i].Records {
			k := key(r)
			cur, ok := anchor[k]
			if !ok {
				cur = fullScale
			}
			if s := sevOf(r.Probability); s > cur {
				cur = s
			}
			anchor[k] = cur
		}
	}
	for i := range results {
		br := &results[i]
		if br.InitialScore == 0 {
			br.InitialScore = br.Score
		}
		br.Score = 0
		for j := range br.Records {
			r := &br.Records[j]
			if r.InitialScore == 0 {
				r.InitialScore = r.Score
			}
			a := anchor[key(*r)]
			if a < fullScale {
				a = fullScale
			}
			sc := sevOf(r.Probability) / a * 100
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

func (e *Engine) emit(br *core.BucketResult, d jobspec.Detector, r core.Record) {
	if e.suppressed(d, r) {
		return
	}
	if e.feedback != nil {
		if fp := e.feedback[d.ID()+"|"+r.Series]; fp > 0 {
			penalty := float64(fp) * fpPenaltyStep
			if penalty > fpPenaltyMax {
				penalty = fpPenaltyMax
			}
			if r.Score < e.threshold+penalty {
				return
			}
		}
	}
	if r.Probability <= 0 && r.Score > 0 {
		r.Probability = probFromScore(r.Score)
	}
	addRec(br, r)
}

const (
	fpPenaltyStep = 8.0
	fpPenaltyMax  = 40.0
)

func (e *Engine) MarkFalsePositive(detectorID, series string) {
	if e.feedback == nil {
		e.feedback = make(map[string]int)
	}
	e.feedback[detectorID+"|"+series]++
}

func (e *Engine) ClearFeedback(detectorID, series string) {
	delete(e.feedback, detectorID+"|"+series)
}

func probFromScore(score float64) float64 {
	if score <= 0 {
		return 1
	}
	if score >= 100 {
		return 1e-12
	}
	return math.Pow(10, -score/100*12)
}

func (e *Engine) inCalendar(bt time.Time) bool {
	for _, c := range e.job.Calendars {
		if c.Covers(bt) {
			return true
		}
	}
	return false
}

func (e *Engine) suppressed(d jobspec.Detector, r core.Record) bool {
	for _, c := range e.job.Calendars {
		if c.Covers(r.Time) {
			return true
		}
	}
	for _, rule := range d.Rules {
		if rule.SkipModelUpdate {
			continue
		}
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

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

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

func (e *Engine) Push(p core.DataPoint) []core.BucketResult {
	bt := p.Time.Truncate(e.job.BucketSpan)
	if !e.hasMark {
		e.watermark, e.hasMark = bt, true
	}

	if _, open := e.pending[bt]; !open && bt.Before(e.watermark) {
		e.LateDropped++
		return nil
	}
	if bt.Before(e.watermark) {
		e.LateAccepted++
	}
	var out []core.BucketResult
	if bt.After(e.watermark) {
		e.watermark = bt
		out = e.closeBefore(e.watermark.Add(-e.Grace))
	}
	e.pending[bt] = append(e.pending[bt], p)
	return out
}

func (e *Engine) Flush() []core.BucketResult {
	return e.closeBefore(time.Time{})
}

func (e *Engine) closeBefore(limit time.Time) []core.BucketResult {
	var times []time.Time
	for t := range e.pending {
		if limit.IsZero() || t.Before(limit) {
			times = append(times, t)
		}
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })

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

type Snapshot struct {
	JobName   string                       `json:"job"`
	Threshold float64                      `json:"threshold"`
	Watermark time.Time                    `json:"watermark"`
	HasMark   bool                         `json:"has_mark"`
	Models    map[string]detect.ModelState `json:"models"`
	Rare      map[string]RareState         `json:"rare,omitempty"`

	Seasonal map[string]detect.SeasonalState     `json:"seasonal,omitempty"`
	Distrib  map[string]detect.DistributionState `json:"distrib,omitempty"`
	Multivar map[string]detect.MultivariateState `json:"multivar,omitempty"`
	Slots    map[string]detect.ModelState        `json:"slots,omitempty"`
	Geo      map[string]detect.GeoState          `json:"geo,omitempty"`
}

type RareState struct {
	Buckets int            `json:"buckets"`
	Seen    map[string]int `json:"seen"`
}

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
