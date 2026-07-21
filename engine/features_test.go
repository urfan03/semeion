package engine

import (
	"math"
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

// Population: five peers behave alike; one becomes an outlier and is flagged,
// attributed to that entity via an influencer.
func TestPopulationOutlier(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	hosts := []string{"h1", "h2", "h3", "h4", "h5"}
	var pts []core.DataPoint
	for i := 0; i < 40; i++ {
		v := 100 + 2*math.Sin(float64(i))
		for _, h := range hosts {
			val := v
			if i == 35 && h == "h5" { // h5 goes wild, once, past warm-up
				val = 1000
			}
			pts = append(pts, core.DataPoint{
				Time: start.Add(time.Duration(i) * time.Minute), Value: val,
				Fields: map[string]string{"host": h},
			})
		}
	}

	job := jobspec.Job{Name: "pop", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v", OverField: "host"}}}
	eng, _ := New(job)
	results := eng.Run(pts, 50)

	var hit *core.Record
	for i := range results {
		for j := range results[i].Records {
			r := &results[i].Records[j]
			if r.Kind == "population" && r.Series == "h5" {
				hit = r
			}
		}
	}
	if hit == nil {
		t.Fatal("expected a population anomaly for h5")
	}
	if len(hit.Influencers) == 0 || hit.Influencers[0].Value != "h5" {
		t.Fatalf("expected influencer host=h5, got %+v", hit.Influencers)
	}
}

// Rare: a status value that appears once (after warm-up) is flagged; the common
// value is not.
func TestRareValue(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var pts []core.DataPoint
	for i := 0; i < 30; i++ {
		pts = append(pts, core.DataPoint{
			Time:   start.Add(time.Duration(i) * time.Minute),
			Fields: map[string]string{"status": "200"},
		})
	}
	pts = append(pts, core.DataPoint{ // one rare 500, well past warm-up
		Time: start.Add(28 * time.Minute), Fields: map[string]string{"status": "500"}})

	job := jobspec.Job{Name: "rare", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncRare, ByField: "status"}}}
	eng, _ := New(job)
	results := eng.Run(pts, 50)

	var got500, got200 bool
	for _, br := range results {
		for _, r := range br.Records {
			if r.Kind == "rare" && r.Series == "500" {
				got500 = true
			}
			if r.Kind == "rare" && r.Series == "200" {
				got200 = true
			}
		}
	}
	if !got500 {
		t.Fatal("expected the rare status 500 to be flagged")
	}
	if got200 {
		t.Fatal("the common status 200 must not be flagged as rare")
	}
}

// Multivariate: three metrics normally move together. A correlated joint move
// is NOT flagged, but a broken correlation (one up while another goes down) IS —
// even though each metric stays within its own range.
func TestMultivariateRelationshipBreak(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	common := func(i int) float64 { return 100 + 15*math.Sin(float64(i)*0.3) }
	mk := func(i int, a, b, c float64) core.DataPoint {
		return core.DataPoint{Time: start.Add(time.Duration(i) * time.Minute),
			Values: map[string]float64{"cpu": a, "mem": b, "io": c}}
	}
	var pts []core.DataPoint
	for i := 0; i < 60; i++ {
		v := common(i)
		pts = append(pts, mk(i, v+2*math.Sin(float64(i)*1.1), v+2*math.Sin(float64(i)*1.7), v+2*math.Sin(float64(i)*2.3)))
	}
	// bucket 60: a big CORRELATED move (all together) — should NOT fire.
	pts = append(pts, mk(60, common(60)+15, common(60)+15, common(60)+15))
	// bucket 61: a RELATIONSHIP BREAK (cpu up, mem down) — should fire.
	pts = append(pts, mk(61, common(61)+25, common(61)-25, common(61)))

	job := jobspec.Job{Name: "mv", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Fields: []string{"cpu", "mem", "io"}}}}
	eng, _ := New(job)
	results := eng.Run(pts, 50)

	fired := map[string]bool{}
	var breakRec *core.Record
	for i := range results {
		for j := range results[i].Records {
			r := &results[i].Records[j]
			if r.Kind == "multivariate" {
				fired[r.Time.Format("15:04")] = true
				if r.Time.Equal(start.Add(61 * time.Minute)) {
					breakRec = r
				}
			}
		}
	}
	if fired[start.Add(60*time.Minute).Format("15:04")] {
		t.Fatal("correlated joint move should NOT be flagged")
	}
	if breakRec == nil {
		t.Fatal("relationship break should be flagged")
	}
	if len(breakRec.Influencers) == 0 {
		t.Fatal("break should attribute contributions to metrics")
	}
}

// info_content: entropy of a by_field's value distribution spikes when the set
// of distinct values fans out.
func TestInfoContentEntropySpike(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var pts []core.DataPoint
	// buckets 0..30: every event is the same user → entropy 0.
	for i := 0; i < 31; i++ {
		for k := 0; k < 3; k++ {
			pts = append(pts, core.DataPoint{Time: start.Add(time.Duration(i) * time.Minute),
				Fields: map[string]string{"user": "u1"}})
		}
	}
	// bucket 31: 50 distinct users → high entropy.
	for k := 0; k < 50; k++ {
		pts = append(pts, core.DataPoint{Time: start.Add(31 * time.Minute),
			Fields: map[string]string{"user": "u" + itoa(k)}})
	}
	job := jobspec.Job{Name: "ic", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncInfoContent, ByField: "user"}}}
	eng, _ := New(job)
	results := eng.Run(pts, 50)

	fired := false
	for _, br := range results {
		for _, r := range br.Records {
			if r.Kind == "info_content" {
				fired = true
			}
		}
	}
	if !fired {
		t.Fatal("expected an info_content anomaly on the entropy spike")
	}
}

// time_of_day: a burst at an hour whose baseline count is low fires; the same
// hour's normal count does not.
func TestTimeOfDayBurst(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) // midnight UTC
	var pts []core.DataPoint
	// 10 days; business hours 8..18 get 3 events each. Establishes per-hour norm.
	for d := 0; d < 10; d++ {
		for h := 8; h <= 18; h++ {
			for k := 0; k < 3; k++ {
				pts = append(pts, core.DataPoint{
					Time: start.Add(time.Duration(d*24+h) * time.Hour)})
			}
		}
	}
	// Day 10, hour 10: a burst of 40.
	burst := start.Add(time.Duration(10*24+10) * time.Hour)
	for k := 0; k < 40; k++ {
		pts = append(pts, core.DataPoint{Time: burst})
	}
	job := jobspec.Job{Name: "tod", BucketSpan: time.Hour,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncTimeOfDay}}}
	eng, _ := New(job)
	results := eng.Run(pts, 50)

	n := 0
	for _, br := range results {
		for _, r := range br.Records {
			if r.Kind == "time_of_day" {
				n++
				if !r.Time.Equal(burst) {
					t.Fatalf("unexpected time_of_day anomaly at %v", r.Time)
				}
			}
		}
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 time_of_day anomaly (the burst), got %d", n)
	}
}

// A distribution-based detector still catches a clear spike (and sets a family).
func TestDistributionDetector(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var pts []core.DataPoint
	base := []float64{4, 5, 3, 6, 4, 5, 4, 3, 5, 6}
	for i := 0; i < 60; i++ {
		v := base[i%len(base)]
		if i == 55 {
			v = 400
		}
		pts = append(pts, core.DataPoint{Time: start.Add(time.Duration(i) * time.Minute), Value: v})
	}
	job := jobspec.Job{Name: "dist", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v", Distribution: true}}}
	eng, _ := New(job)
	if !anyRecord(eng.Run(pts, 50)) {
		t.Fatal("distribution detector should catch the 400 spike")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// spikeSeries: 40 one-minute buckets at ~100, with a single spike at bucket 35.
func spikeSeries(start time.Time) []core.DataPoint {
	var pts []core.DataPoint
	for i := 0; i < 40; i++ {
		v := 100.0
		if i == 35 {
			v = 900
		}
		pts = append(pts, core.DataPoint{Time: start.Add(time.Duration(i) * time.Minute), Value: v})
	}
	return pts
}

func anyRecord(results []core.BucketResult) bool {
	for _, br := range results {
		if len(br.Records) > 0 {
			return true
		}
	}
	return false
}

// A calendar window over the spike bucket suppresses the anomaly; without it,
// the spike fires.
func TestCalendarSuppression(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	pts := spikeSeries(start)
	det := []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v"}}

	base, _ := New(jobspec.Job{Name: "b", BucketSpan: time.Minute, Detectors: det})
	if !anyRecord(base.Run(pts, 50)) {
		t.Fatal("baseline: expected the spike to fire")
	}

	spikeT := start.Add(35 * time.Minute)
	withCal, _ := New(jobspec.Job{Name: "c", BucketSpan: time.Minute, Detectors: det,
		Calendars: []jobspec.Calendar{{Name: "release", Start: spikeT.Add(-time.Minute), End: spikeT.Add(time.Minute)}}})
	if anyRecord(withCal.Run(pts, 50)) {
		t.Fatal("calendar window should suppress the spike")
	}
}

// A rule that skips anomalies whose actual is above a bound suppresses the spike.
func TestRuleSuppression(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	pts := spikeSeries(start)
	bound := 500.0
	det := []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v",
		Rules: []jobspec.Rule{{SkipActualAbove: &bound}}}}

	eng, _ := New(jobspec.Job{Name: "r", BucketSpan: time.Minute, Detectors: det})
	if anyRecord(eng.Run(pts, 50)) {
		t.Fatal("rule skip_actual_above should suppress the spike (actual 900 > 500)")
	}
}

// A partition detector attaches the partition value as an influencer.
func TestInfluencersOnPartition(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var pts []core.DataPoint
	for i := 0; i < 40; i++ {
		val := 100.0
		if i == 35 {
			val = 900
		}
		pts = append(pts, core.DataPoint{
			Time: start.Add(time.Duration(i) * time.Minute), Value: val,
			Fields: map[string]string{"host": "web1"},
		})
	}
	job := jobspec.Job{Name: "part", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v", PartitionField: "host"}}}
	eng, _ := New(job)
	results := eng.Run(pts, 50)

	found := false
	for _, br := range results {
		for _, r := range br.Records {
			for _, in := range r.Influencers {
				if in.Field == "host" && in.Value == "web1" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("expected influencer host=web1 on the partition anomaly")
	}
}
