package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// hostTable renders a JSON body of n normal hosts plus one with a broken io_wait.
func hostTable(n int) string {
	rows := make([]string, 0, n+1)
	for i := 0; i < n; i++ {
		rows = append(rows, fmt.Sprintf(
			`{"host":"web-%02d","cpu":%d,"mem":%d,"io_wait":%d}`, i, 50+i%3, 60+i%4, 10+i%2))
	}
	rows = append(rows, `{"host":"web-bad","cpu":51,"mem":61,"io_wait":95}`)
	return `{"rows":[` + strings.Join(rows, ",") + `],"top":3}`
}

func TestOutliersEndpoint(t *testing.T) {
	s := NewServer()
	w := do(t, s.Handler(), http.MethodPost, "/v1/outliers", hostTable(30))
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var res struct {
		Features []string `json:"features"`
		Rows     int      `json:"rows"`
		Results  []struct {
			Index     int                `json:"index"`
			Score     float64            `json:"score"`
			Influence map[string]float64 `json:"influence"`
			Labels    map[string]string  `json:"labels"`
		} `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Rows != 31 || len(res.Features) != 3 {
		t.Fatalf("features/rows: %+v", res)
	}
	if len(res.Results) != 3 {
		t.Fatalf("top=3 should return 3 results, got %d", len(res.Results))
	}
	first := res.Results[0]
	if first.Labels["host"] != "web-bad" {
		t.Fatalf("the broken host should rank first, got %+v", first)
	}
	if first.Influence["io_wait"] < 0.8 {
		t.Errorf("io_wait should explain it: %+v", first.Influence)
	}
	if first.Score <= 0.5 {
		t.Errorf("score too low: %v", first.Score)
	}
}

func TestOutliersRejectsIncompleteRows(t *testing.T) {
	s := NewServer()
	// A missing feature must fail loudly — imputing it would invent data.
	body := `{"rows":[{"cpu":1,"mem":2},{"cpu":2,"mem":3},{"cpu":3}]}`
	w := do(t, s.Handler(), http.MethodPost, "/v1/outliers", body)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "missing feature") {
		t.Fatalf("expected a missing-feature error, got %d %s", w.Code, w.Body)
	}

	// Text-only rows have nothing to score.
	w = do(t, s.Handler(), http.MethodPost, "/v1/outliers", `{"rows":[{"host":"a"},{"host":"b"},{"host":"c"}]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a table with no numeric fields, got %d", w.Code)
	}
}

func TestOutliersRespectsExplicitFeatures(t *testing.T) {
	s := NewServer()
	// io_wait is the only broken column; excluding it must calm the score down.
	body := `{"features":["cpu","mem"],"rows":[` + strings.Join([]string{
		`{"host":"a","cpu":50,"mem":60,"io_wait":10}`,
		`{"host":"b","cpu":51,"mem":61,"io_wait":11}`,
		`{"host":"c","cpu":49,"mem":59,"io_wait":9}`,
		`{"host":"d","cpu":50,"mem":60,"io_wait":95}`,
		`{"host":"e","cpu":51,"mem":60,"io_wait":10}`,
	}, ",") + `]}`
	w := do(t, s.Handler(), http.MethodPost, "/v1/outliers", body)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var res struct {
		Features []string `json:"features"`
		Results  []struct {
			Score     float64            `json:"score"`
			Influence map[string]float64 `json:"influence"`
		} `json:"results"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if len(res.Features) != 2 {
		t.Fatalf("explicit features ignored: %+v", res.Features)
	}
	for _, r := range res.Results {
		if _, ok := r.Influence["io_wait"]; ok {
			t.Fatalf("excluded feature leaked into the explanation: %+v", r.Influence)
		}
	}
}
