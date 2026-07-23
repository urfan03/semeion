package model

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPProviderCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/detect_seasonality":
			_ = json.NewEncoder(w).Encode(map[string]any{"periods": []int{24}})
		case "/forecast":
			_ = json.NewEncoder(w).Encode(map[string]any{"forecast": []float64{1, 2, 3}})
		case "/fit_distribution":
			_ = json.NewEncoder(w).Encode(Distribution{Family: "poisson", Params: []float64{3}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := NewHTTPProvider(srv.URL)
	if got := p.DetectSeasonality([]float64{1, 2, 3}); len(got) != 1 || got[0] != 24 {
		t.Fatalf("DetectSeasonality: got %v", got)
	}
	if got := p.Forecast([]float64{1, 2, 3}, 3); len(got) != 3 {
		t.Fatalf("Forecast: got %v", got)
	}
	if got := p.FitDistribution([]float64{1, 2, 3}); got.Family != "poisson" {
		t.Fatalf("FitDistribution: got %q", got.Family)
	}
}

func TestHTTPProviderFallback(t *testing.T) {
	p := NewHTTPProvider("http://127.0.0.1:1")
	x := make([]float64, 240)
	for i := range x {
		x[i] = 100 + 50*math.Sin(2*math.Pi*float64(i)/24)
	}

	if got := p.DetectSeasonality(x); len(got) == 0 {
		t.Fatal("expected fallback to Go provider to detect a period")
	}
}
