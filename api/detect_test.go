package api

import (
	"bytes"
	"encoding/json"
	"math"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"testing"
)

func detectSeriesFixture(n int, spikeAt, width int) []float64 {
	rng := rand.New(rand.NewPCG(7, 11))
	out := make([]float64, n)
	for i := range out {
		out[i] = 100 + rng.NormFloat64()
	}
	for i := spikeAt; i < spikeAt+width && i < n; i++ {
		out[i] += 45
	}
	return out
}

func postDetect(t *testing.T, req detectRequest) (*httptest.ResponseRecorder, detectResponse) {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{}
	rec := httptest.NewRecorder()
	s.handleDetect(rec, httptest.NewRequest(http.MethodPost, "/v1/detect", bytes.NewReader(body)))
	var out detectResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decoding response: %v (%s)", err, rec.Body.String())
		}
	}
	return rec, out
}

func TestDetectFlagsAnOngoingAnomaly(t *testing.T) {
	n := 3000
	values := detectSeriesFixture(n, n-40, 40)
	rec, out := postDetect(t, detectRequest{
		Config: detectConfig{Sensitivity: "sensitive", History: 4000, Calibration: 800},
		Series: []detectSeries{{Key: "cf_errors_5xx|zone-1", Values: values}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if len(out.Verdicts) != 1 {
		t.Fatalf("expected one verdict, got %d", len(out.Verdicts))
	}
	v := out.Verdicts[0]
	if v.Skipped != "" {
		t.Fatalf("unexpected skip: %s", v.Skipped)
	}
	if !v.Fired {
		t.Fatalf("an anomaly running to the last sample must fire: %+v", v)
	}
	if v.Key != "cf_errors_5xx|zone-1" {
		t.Fatalf("the key must round-trip, got %q", v.Key)
	}
	if v.Effect < 3 {
		t.Fatalf("effect size too small: %+v", v)
	}
	if v.Shape == "" || v.Reason == "" {
		t.Fatalf("a fired verdict must explain itself: %+v", v)
	}
	if v.P <= 0 || v.P > 1 {
		t.Fatalf("p-value out of range: %+v", v)
	}
}

func TestDetectStaysQuietOnCleanData(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 5))
	values := make([]float64, 3000)
	for i := range values {
		values[i] = 50 + 5*math.Sin(float64(i)/40) + rng.NormFloat64()
	}
	_, out := postDetect(t, detectRequest{
		Config: detectConfig{Sensitivity: "precise", History: 4000, Calibration: 800},
		Series: []detectSeries{{Key: "quiet", Values: values}},
	})
	if out.Verdicts[0].Fired {
		t.Fatalf("clean data must not fire: %+v", out.Verdicts[0])
	}
}

func TestDetectHandlesBatchesAndSkips(t *testing.T) {
	good := detectSeriesFixture(3000, 2940, 60)
	_, out := postDetect(t, detectRequest{
		Config: detectConfig{Sensitivity: "sensitive", History: 4000, Calibration: 800},
		Series: []detectSeries{
			{Key: "a", Values: good},
			{Key: "b", Values: nil},
			{Key: "c", Values: []float64{1, 2, 3}},
		},
	})
	if len(out.Verdicts) != 3 {
		t.Fatalf("every series must get a verdict, got %d", len(out.Verdicts))
	}
	if out.Verdicts[1].Skipped == "" {
		t.Fatal("an empty series must be skipped with a reason")
	}
	if out.Verdicts[2].Skipped == "" {
		t.Fatal("a series too short to calibrate must be skipped with a reason")
	}
	if out.Verdicts[1].Fired || out.Verdicts[2].Fired {
		t.Fatal("a skipped series must never fire")
	}
	if !out.Verdicts[0].Fired {
		t.Fatalf("the usable series must still be scored: %+v", out.Verdicts[0])
	}
}

func TestDetectRejectsBadRequests(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.handleDetect(rec, httptest.NewRequest(http.MethodGet, "/v1/detect", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET must be rejected, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	s.handleDetect(rec, httptest.NewRequest(http.MethodPost, "/v1/detect", bytes.NewReader([]byte("{"))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed JSON must be rejected, got %d", rec.Code)
	}

	if rec, _ := postDetect(t, detectRequest{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("an empty batch must be rejected, got %d", rec.Code)
	}
	if rec, _ := postDetect(t, detectRequest{
		Config: detectConfig{Sensitivity: "loud"},
		Series: []detectSeries{{Key: "a", Values: []float64{1}}},
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("an invalid config must be rejected before any work, got %d", rec.Code)
	}
}

func TestDetectConfigMapsToPipelineOptions(t *testing.T) {
	c := detectConfig{
		Sensitivity: "paranoid", History: 5000, Calibration: 900, Refresh: 64,
		Period: 24, Deseasonal: true, BudgetAlarms: 1, BudgetPer: 2000,
		MinEffect: 3, MinDuration: 5, Q: 1e-4,
	}
	o := c.options()
	if string(o.Sensitivity) != "paranoid" || o.History != 5000 || o.Calibration != 900 {
		t.Fatalf("core options lost: %+v", o)
	}
	if o.Budget.Alarms != 1 || o.Budget.Per != 2000 {
		t.Fatalf("budget lost: %+v", o.Budget)
	}
	if o.MinEffect != 3 || o.MinDuration != 5 || o.Q != 1e-4 || !o.Deseasonal || o.Period != 24 {
		t.Fatalf("gates lost: %+v", o)
	}
}
