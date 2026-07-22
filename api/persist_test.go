package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/correlate"
	"github.com/urfan03/semeion/slo"
)

// buildRichServer stands up a server with something in every persisted store.
func buildRichServer(t *testing.T) *Server {
	t.Helper()
	s := NewServer()
	h := s.Handler()
	base := time.Now().UTC()

	// A change + a symptom → a tracked incident + a change-log entry.
	ch, _ := json.Marshal(correlate.Change{Time: base.Add(-2 * time.Minute), Name: "checkout v2",
		Kind: "deploy", Labels: map[string]string{"service": "checkout"}})
	do(t, h, http.MethodPost, "/v1/changes", string(ch))
	storeSym(s, "checkout-errors", "checkout", base, 90)
	do(t, h, http.MethodGet, "/v1/incidents?window=10m", "") // opens the incident

	// Traces → dependency graph.
	do(t, h, http.MethodPost, "/v1/otlp/v1/traces", threeTierTrace)

	// A named SLO with samples.
	sl, _ := json.Marshal(map[string]any{"objective": 0.99, "window": "1h",
		"samples": sloSamples(base, 120, 100, 0.02), "now": base})
	do(t, h, http.MethodPost, "/v1/slo/api-availability", string(sl))

	// A live metric job with an ingested spike (learned baseline).
	do(t, h, http.MethodPost, "/v1/jobs",
		`{"job":{"name":"live-lat","bucket_span":"1m","detectors":[{"function":"mean","field":"value","side":"high"}]},"metric":"m"}`)
	var pts []core.DataPoint
	for i := 0; i < 40; i++ {
		v := 100.0
		if i%2 == 0 {
			v = 101
		}
		pts = append(pts, core.DataPoint{Time: base.Add(time.Duration(i) * time.Minute), Value: v})
	}
	body, _ := json.Marshal(map[string]any{"points": pts})
	do(t, h, http.MethodPost, "/v1/jobs/live-lat/points", string(body))
	return s
}

func TestServerStateRoundTrip(t *testing.T) {
	src := buildRichServer(t)
	path := filepath.Join(t.TempDir(), "state.json")
	if err := src.SaveState(path); err != nil {
		t.Fatal(err)
	}

	dst := NewServer()
	if loaded, err := dst.LoadState(path); err != nil || !loaded {
		t.Fatalf("load: loaded=%v err=%v", loaded, err)
	}
	h := dst.Handler()

	// Changes survived.
	var chg struct {
		Changes []correlate.Change `json:"changes"`
	}
	json.Unmarshal(do(t, h, http.MethodGet, "/v1/changes", "").Body.Bytes(), &chg)
	if len(chg.Changes) != 1 || chg.Changes[0].Name != "checkout v2" {
		t.Fatalf("changes not restored: %+v", chg.Changes)
	}

	// Topology survived.
	var topo struct {
		Edges []map[string]any `json:"edges"`
	}
	json.Unmarshal(do(t, h, http.MethodGet, "/v1/topology", "").Body.Bytes(), &topo)
	if len(topo.Edges) != 2 {
		t.Fatalf("graph not restored: %d edges", len(topo.Edges))
	}

	// Open incident survived, keeping its id.
	var open struct {
		Incidents []correlate.Tracked `json:"incidents"`
	}
	json.Unmarshal(do(t, h, http.MethodGet, "/v1/incidents/open", "").Body.Bytes(), &open)
	if len(open.Incidents) != 1 {
		t.Fatalf("tracked incident not restored: %+v", open.Incidents)
	}
	srcOpen := src.tracker.Open()
	if open.Incidents[0].ID != srcOpen[0].ID {
		t.Errorf("incident id changed across restore: %s vs %s", open.Incidents[0].ID, srcOpen[0].ID)
	}

	// SLO survived with its target and samples.
	var rep slo.Report
	json.Unmarshal(do(t, h, http.MethodGet, "/v1/slo/api-availability", "").Body.Bytes(), &rep)
	if rep.Objective != 0.99 {
		t.Errorf("SLO target not restored: %+v", rep.Objective)
	}
	if rep.SLI <= 0.9 || rep.SLI >= 1.0 {
		t.Errorf("SLO samples not restored (SLI %.3f)", rep.SLI)
	}

	// Live job survived and keeps its learned baseline: a fresh spike is caught
	// immediately, without a warm-up.
	var st map[string]any
	json.Unmarshal(do(t, h, http.MethodGet, "/v1/jobs/live-lat", "").Body.Bytes(), &st)
	if st["points"].(float64) != 40 {
		t.Fatalf("live job point count not restored: %v", st["points"])
	}
	base := time.Now().UTC()
	spike := []core.DataPoint{
		{Time: base.Add(60 * time.Minute), Value: 900},
		{Time: base.Add(61 * time.Minute), Value: 100}, // closes the spike bucket
	}
	body, _ := json.Marshal(map[string]any{"points": spike})
	w := do(t, h, http.MethodPost, "/v1/jobs/live-lat/points", string(body))
	var pushed struct {
		Anomalies []core.BucketResult `json:"anomalies"`
	}
	json.Unmarshal(w.Body.Bytes(), &pushed)
	if len(pushed.Anomalies) == 0 {
		t.Fatal("restored job should detect a spike on its resumed baseline, not re-warm")
	}
}

func TestLoadStateMissingFileIsNoError(t *testing.T) {
	s := NewServer()
	loaded, err := s.LoadState(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("a missing state file must not be an error, got %v", err)
	}
	if loaded {
		t.Fatal("a missing file should report loaded=false")
	}
}

func TestRestoreRejectsWrongVersion(t *testing.T) {
	s := NewServer()
	if err := s.Restore(ServerState{Version: 999}); err == nil {
		t.Fatal("expected a version-mismatch error")
	}
}

func TestSaveStateAtomicOverwrite(t *testing.T) {
	s := buildRichServer(t)
	path := filepath.Join(t.TempDir(), "state.json")
	if err := s.SaveState(path); err != nil {
		t.Fatal(err)
	}
	// A second save over the same path must succeed (temp-file rename).
	storeSym(s, "another", "cart", time.Now().UTC(), 80)
	if err := s.SaveState(path); err != nil {
		t.Fatalf("overwrite save failed: %v", err)
	}
	// And the result reloads cleanly.
	if _, err := NewServer().LoadState(path); err != nil {
		t.Fatalf("reload after overwrite failed: %v", err)
	}
}
