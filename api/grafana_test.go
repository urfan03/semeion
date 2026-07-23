package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/urfan03/semeion/core"
)

// #12: the Grafana SimpleJSON surface — /search lists jobs, /query returns a
// per-bucket score timeseries, /annotations returns anomaly events.
func TestGrafanaSimpleJSON(t *testing.T) {
	s := NewServer()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.results["web"] = []core.BucketResult{
		{Time: t0, Score: 20},
		{Time: t0.Add(time.Minute), Score: 88, Records: []core.Record{
			{Time: t0.Add(time.Minute), Detector: "mean(latency)", Series: "host=a", Actual: 900, Typical: 100, Score: 88, Kind: "metric"},
		}},
	}
	h := s.Handler()

	// Health.
	if w := do(t, h, http.MethodGet, "/grafana/", ""); w.Code != http.StatusOK {
		t.Fatalf("health: got %d", w.Code)
	}

	// Search → job names.
	w := do(t, h, http.MethodPost, "/grafana/search", `{}`)
	var names []string
	if err := json.Unmarshal(w.Body.Bytes(), &names); err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "web" {
		t.Fatalf("search should list [web], got %v", names)
	}

	// Query timeseries.
	q := `{"range":{"from":"2025-12-31T00:00:00Z","to":"2026-01-02T00:00:00Z"},"targets":[{"target":"web","type":"timeserie"}]}`
	w = do(t, h, http.MethodPost, "/grafana/query", q)
	var series []struct {
		Target     string       `json:"target"`
		Datapoints [][2]float64 `json:"datapoints"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &series); err != nil {
		t.Fatalf("query decode: %v (body %s)", err, w.Body.String())
	}
	if len(series) != 1 || series[0].Target != "web" || len(series[0].Datapoints) != 2 {
		t.Fatalf("query series wrong: %+v", series)
	}
	if series[0].Datapoints[1][0] != 88 {
		t.Fatalf("second datapoint score should be 88, got %v", series[0].Datapoints[1][0])
	}

	// Annotations → one anomaly event.
	a := `{"range":{"from":"2025-12-31T00:00:00Z","to":"2026-01-02T00:00:00Z"},"annotation":{"name":"anom","query":"web"}}`
	w = do(t, h, http.MethodPost, "/grafana/annotations", a)
	var anns []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &anns); err != nil {
		t.Fatal(err)
	}
	if len(anns) != 1 {
		t.Fatalf("expected 1 annotation, got %d", len(anns))
	}
}
