package engine

import (
	"reflect"
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

func mixedJob() jobspec.Job {
	return jobspec.Job{Name: "mix", BucketSpan: time.Minute, Influencers: []string{"host", "code"},
		Detectors: []jobspec.Detector{
			{Function: jobspec.FuncMean, Field: "v", ByField: "host", Side: jobspec.SideHigh},
			{Function: jobspec.FuncMean, Field: "v", Distribution: true},
			{Fields: []string{"v", "w"}, ByField: "host"},
			{Function: jobspec.FuncRare, ByField: "code"},
			{Function: jobspec.FuncLatLong, ByField: "host"},
			{Function: jobspec.FuncRatio, Field: "err", DenomField: "tot", Side: jobspec.SideHigh},
		}}
}

func mixedPoints() []core.DataPoint {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var pts []core.DataPoint
	for b := 0; b < 80; b++ {
		bt := t0.Add(time.Duration(b) * time.Minute)
		for _, host := range []string{"a", "b"} {
			v := 100.0 + float64((b*7+len(host))%13)
			w := 50.0 + float64((b*3)%9)
			if b == 60 && host == "a" {
				v, w = 500, 5
			}
			code := "ok"
			if b == 55 {
				code = "rare_evt"
			}
			lat, lon := 51.5, -0.12
			if b == 65 && host == "b" {
				lat, lon = -33.8, 151.2
			}
			pts = append(pts, core.DataPoint{Time: bt, Value: v,
				Fields: map[string]string{"host": host, "code": code},
				Values: map[string]float64{"v": v, "w": w, "lat": lat, "lon": lon, "err": float64(b % 5), "tot": 100}})
		}
	}
	return pts
}

func TestRunIsDeterministic(t *testing.T) {
	pts := mixedPoints()
	job := mixedJob()
	e1, _ := New(job)
	e2, _ := New(job)
	r1 := e1.Run(pts, 50)
	r2 := e2.Run(pts, 50)
	if !reflect.DeepEqual(r1, r2) {
		t.Fatal("two runs of the same job over the same points must be byte-identical (order included)")
	}
	if len(r1) == 0 {
		t.Fatal("fixture produced no buckets")
	}
}

func TestSnapshotRestoreIsFixedPoint(t *testing.T) {
	pts := mixedPoints()
	k := len(pts) / 2
	job := mixedJob()

	e1, _ := New(job)
	e1.Run(pts[:k], 50)
	snap := e1.Snapshot()

	e2, _ := New(job)
	e2.Restore(snap)

	tail1 := e1.Run(pts[k:], 50)
	tail2 := e2.Run(pts[k:], 50)
	if !reflect.DeepEqual(tail1, tail2) {
		t.Fatal("a restored engine must reproduce the original's subsequent results exactly (snapshot is not a fixed point)")
	}
}
