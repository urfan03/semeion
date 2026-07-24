package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/urfan03/semeion/alert"
	"github.com/urfan03/semeion/engine"
	"github.com/urfan03/semeion/jobspec"
)

func promSeries(start time.Time, n int, spikeAt int) string {
	var vals []string
	for i := 0; i < n; i++ {
		v := 100.0
		if i%2 == 0 {
			v = 101.0
		}
		if i == spikeAt {
			v = 900
		}
		vals = append(vals, fmt.Sprintf(`[%d, "%g"]`, start.Add(time.Duration(i)*time.Minute).Unix(), v))
	}
	return `{"status":"success","data":{"resultType":"matrix","result":[
	  {"metric":{"instance":"web-1"},"values":[` + strings.Join(vals, ",") + `]}]}}`
}

func TestWatchTickDetectsAndAlerts(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		_, _ = w.Write([]byte(promSeries(start, 60, 50)))
	}))
	defer prom.Close()

	var (
		mu   sync.Mutex
		got  []alert.Alert
		hook = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var a alert.Alert
			_ = json.NewDecoder(r.Body).Decode(&a)
			mu.Lock()
			got = append(got, a)
			mu.Unlock()
		}))
	)
	defer hook.Close()

	job := jobspec.Job{Name: "watch-test", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncMean, Field: "value", Side: jobspec.SideHigh}}}
	eng, err := engine.New(job)
	if err != nil {
		t.Fatal(err)
	}
	src, err := watchSource(job, sourceOpts{promURL: prom.URL, promQuery: "up"})
	if err != nil {
		t.Fatal(err)
	}

	w := &watcher{job: job, eng: eng, src: src, threshold: 50, lookback: time.Hour,
		notifier: buildNotifier("", hook.URL, "", 0, 50, 30*time.Minute)}

	if err := w.tick(context.Background(), start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) == 0 {
		t.Fatal("the injected spike produced no alert")
	}
	if got[0].Job != "watch-test" || got[0].Score < 50 {
		t.Fatalf("unexpected alert: %+v", got[0])
	}
}

func TestWatchSkipsAlreadySeenPoints(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(promSeries(start, 30, -1)))
	}))
	defer prom.Close()

	job := jobspec.Job{Name: "dedup", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncMean, Field: "value"}}}
	eng, err := engine.New(job)
	if err != nil {
		t.Fatal(err)
	}
	src, _ := watchSource(job, sourceOpts{promURL: prom.URL, promQuery: "up"})
	w := &watcher{job: job, eng: eng, src: src, threshold: 50, lookback: time.Hour,
		notifier: buildNotifier("", "", "", 0, 50, time.Hour)}

	if err := w.tick(context.Background(), start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	firstSeen := w.lastSeen
	if firstSeen.IsZero() {
		t.Fatal("lastSeen not advanced on the first tick")
	}

	if err := w.tick(context.Background(), start.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !w.lastSeen.Equal(firstSeen) {
		t.Fatalf("second tick re-consumed old points: %s → %s", firstSeen, w.lastSeen)
	}
}

func TestWatchSourceRequiresLiveSource(t *testing.T) {
	job := jobspec.Job{Name: "x", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v"}}}
	if _, err := watchSource(job, sourceOpts{csvPath: "data.csv"}); err == nil {
		t.Fatal("expected watch to reject a CSV-only source")
	}
}
