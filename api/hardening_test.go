package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestCorrelateSymptomCap(t *testing.T) {
	s := NewServer()
	h := s.Handler()
	body := `{"symptoms":[` + strings.Repeat(`{},`, maxCorrelateSymptoms) + `{}]}`
	if w := do(t, h, http.MethodPost, "/v1/correlate", body); w.Code != http.StatusBadRequest {
		t.Fatalf("too many symptoms must be rejected (O(n^2) DoS guard), got %d %s", w.Code, w.Body.String())
	}
	if w := do(t, h, http.MethodPost, "/v1/correlate", `{"symptoms":[],"window":"10m"}`); w.Code != http.StatusOK {
		t.Fatalf("a normal correlate request must succeed, got %d %s", w.Code, w.Body.String())
	}
}

func TestOutlierKCap(t *testing.T) {
	s := NewServer()
	h := s.Handler()
	if w := do(t, h, http.MethodPost, "/v1/outliers", `{"rows":[{"x":1.0},{"x":2.0},{"x":3.0}],"k":100000}`); w.Code != http.StatusBadRequest {
		t.Fatalf("an unbounded k must be rejected (OOM guard), got %d %s", w.Code, w.Body.String())
	}
	if w := do(t, h, http.MethodPost, "/v1/outliers", `{"rows":[{"x":1.0},{"x":2.0},{"x":3.0}],"k":2}`); w.Code != http.StatusOK {
		t.Fatalf("a normal outliers request must succeed, got %d %s", w.Code, w.Body.String())
	}
}

func TestIncidentsMethodGuard(t *testing.T) {
	s := NewServer()
	h := s.Handler()
	if w := do(t, h, http.MethodDelete, "/v1/incidents", ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("non-GET on /v1/incidents must be rejected, got %d", w.Code)
	}
}

func TestForecastHorizonClamp(t *testing.T) {
	s := NewServer()
	h := s.Handler()
	body := `{"job":"x","horizon":20000,"series":[1,2,3,4,5,6,7,8,9,10,11,12]}`
	if w := do(t, h, http.MethodPost, "/v1/forecasts", body); w.Code != http.StatusBadRequest {
		t.Fatalf("oversized horizon on /v1/forecasts must be rejected, got %d %s", w.Code, w.Body.String())
	}
	body2 := `{"horizon":20000,"series":[1,2,3,4,5,6,7,8,9,10,11,12]}`
	if w := do(t, h, http.MethodPost, "/v1/forecast", body2); w.Code != http.StatusBadRequest {
		t.Fatalf("oversized horizon on /v1/forecast must be rejected, got %d %s", w.Code, w.Body.String())
	}
}

func TestLeadLagClamp(t *testing.T) {
	s := NewServer()
	h := s.Handler()
	if w := do(t, h, http.MethodPost, "/v1/leadlag", `{"a":[1,2],"b":[3,4],"max_lag":5000000000}`); w.Code != http.StatusBadRequest {
		t.Fatalf("an unbounded max_lag must be rejected (OOM guard), got %d %s", w.Code, w.Body.String())
	}
	if w := do(t, h, http.MethodPost, "/v1/leadlag", `{"a":[1,2],"b":[3,4],"order":100000}`); w.Code != http.StatusBadRequest {
		t.Fatalf("an unbounded order must be rejected (OOM guard), got %d %s", w.Code, w.Body.String())
	}
	body := `{"a":[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20],"b":[2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21],"max_lag":3}`
	if w := do(t, h, http.MethodPost, "/v1/leadlag", body); w.Code != http.StatusOK {
		t.Fatalf("a normal leadlag request must succeed, got %d %s", w.Code, w.Body.String())
	}
}

func TestForecastStoreCap(t *testing.T) {
	var fs forecastStore
	for i := 0; i < maxForecasts; i++ {
		if !fs.put(storedForecast{ID: fmt.Sprintf("id-%d", i), Job: fmt.Sprintf("job-%d", i)}) {
			t.Fatalf("put %d should succeed while under the cap", i)
		}
	}
	if fs.put(storedForecast{ID: "overflow", Job: "job-new"}) {
		t.Fatalf("a new-job forecast beyond the cap must be refused")
	}
	if !fs.put(storedForecast{ID: "again", Job: "job-0"}) {
		t.Fatalf("overwriting an existing job's forecast must still succeed at the cap")
	}
}

func TestLiveJobCap(t *testing.T) {
	s := NewServer()
	spec := func(name string) liveJobRequest {
		return liveJobRequest{Job: json.RawMessage(`{"name":"` + name + `","bucket_span":"1m","detectors":[{"function":"mean","field":"v","side":"high"}]}`)}
	}
	for i := 0; i < maxLiveJobsCount; i++ {
		if _, err := s.RegisterJob(spec(fmt.Sprintf("j%d", i))); err != nil {
			t.Fatalf("register %d: %v", i, err)
		}
	}
	if _, err := s.RegisterJob(spec("overflow")); err == nil {
		t.Fatalf("registering beyond the live-job cap must error")
	}
	if _, err := s.RegisterJob(spec("j0")); err != nil {
		t.Fatalf("re-registering an existing job must succeed at the cap: %v", err)
	}
}

func TestSLOSeriesCap(t *testing.T) {
	s := NewServer()
	for i := 0; i < maxSLOSeries; i++ {
		if s.sloSeries(fmt.Sprintf("slo-%d", i), true) == nil {
			t.Fatalf("creating SLO series %d under the cap should succeed", i)
		}
	}
	if s.sloSeries("overflow", true) != nil {
		t.Fatalf("creating an SLO series beyond the cap must be refused")
	}
	if s.sloSeries("slo-0", true) == nil {
		t.Fatalf("an existing SLO series must still resolve at the cap")
	}
}
