package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestForecastLifecycle(t *testing.T) {
	s := NewServer()
	h := s.Handler()

	body := `{"job":"cpu","horizon":6,"expires_in":"1h","series":[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20]}`
	w := do(t, h, http.MethodPost, "/v1/forecasts", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create forecast: %d %s", w.Code, w.Body.String())
	}
	var fc struct {
		ID    string                                  `json:"id"`
		Bands []struct{ Point, Lower, Upper float64 } `json:"bands"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &fc); err != nil {
		t.Fatal(err)
	}
	if fc.ID == "" || len(fc.Bands) != 6 {
		t.Fatalf("forecast should have an id and 6 bands: %+v", fc)
	}

	w = do(t, h, http.MethodGet, "/v1/forecasts/"+fc.ID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("get forecast: %d", w.Code)
	}

	do(t, h, http.MethodPost, "/v1/forecasts", body)
	w = do(t, h, http.MethodGet, "/v1/forecasts", "")
	var list struct {
		Forecasts []json.RawMessage `json:"forecasts"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Forecasts) != 1 {
		t.Fatalf("same-job forecast should overwrite, got %d active", len(list.Forecasts))
	}

	if w := do(t, h, http.MethodGet, "/v1/forecasts/"+fc.ID, ""); w.Code != http.StatusNotFound {
		t.Fatalf("overwritten forecast id should be gone, got %d", w.Code)
	}
}

func TestJobGroupsFilter(t *testing.T) {
	s := NewServer()
	h := s.Handler()
	mk := func(name string, groups string) string {
		return `{"job":{"name":"` + name + `","bucket_span":"1m","groups":[` + groups + `],` +
			`"detectors":[{"function":"mean","field":"v","side":"high"}]},"metric":"m"}`
	}
	if w := do(t, h, http.MethodPost, "/v1/jobs", mk("web-lat", `"web"`)); w.Code != http.StatusCreated {
		t.Fatalf("create web job: %d %s", w.Code, w.Body.String())
	}
	if w := do(t, h, http.MethodPost, "/v1/jobs", mk("db-lat", `"db"`)); w.Code != http.StatusCreated {
		t.Fatalf("create db job: %d", w.Code)
	}
	w := do(t, h, http.MethodGet, "/v1/jobs?group=web", "")
	if !strings.Contains(w.Body.String(), "web-lat") || strings.Contains(w.Body.String(), "db-lat") {
		t.Fatalf("group=web should list only web-lat: %s", w.Body.String())
	}
}
