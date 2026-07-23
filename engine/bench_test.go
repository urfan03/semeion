package engine

import (
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

func benchPoints(n, hosts int) []core.DataPoint {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	pts := make([]core.DataPoint, 0, n)
	for i := 0; i < n; i++ {
		v := 100.0 + float64((i*7)%23)
		if i%97 == 0 {
			v = 600
		}
		pts = append(pts, core.DataPoint{
			Time:   t0.Add(time.Duration(i) * time.Second),
			Value:  v,
			Fields: map[string]string{"host": string(rune('a' + i%hosts))},
			Values: map[string]float64{"v": v},
		})
	}
	return pts
}

func benchJob() jobspec.Job {
	return jobspec.Job{Name: "b", BucketSpan: 10 * time.Second,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v", ByField: "host", Side: jobspec.SideHigh}}}
}

func BenchmarkRun(b *testing.B) {
	pts := benchPoints(5000, 8)
	job := benchJob()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		eng, _ := New(job)
		eng.Run(pts, 50)
	}
}

func BenchmarkPushStreaming(b *testing.B) {
	pts := benchPoints(5000, 8)
	job := benchJob()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		eng, _ := New(job)
		eng.SetThreshold(50)
		for _, p := range pts {
			eng.Push(p)
		}
		eng.Flush()
	}
}
