package outlier

import (
	"encoding/json"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"testing"
)

func table() ([]string, [][]float64) {
	rnd := rand.New(rand.NewSource(9))
	rows := cluster(rnd, 50, []float64{10, 20}, 1)
	rows = append(rows, []float64{80, 90})
	return []string{"a", "b"}, rows
}

func TestHTTPDetectorUsesTheRemoteAnswer(t *testing.T) {
	features, rows := table()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/outliers" {
			t.Errorf("path: %s", r.URL.Path)
		}
		var req struct {
			Rows [][]float64 `json:"rows"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		res := make([]Result, len(req.Rows))
		for i := range res {
			res[i] = Result{Index: i, Score: 0.42}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": res})
	}))
	defer srv.Close()

	got, err := NewHTTPDetector(srv.URL).Detect(features, rows, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(rows) || got[0].Score != 0.42 {
		t.Fatalf("remote result not used: %+v", got[0])
	}
}

func TestHTTPDetectorFallsBackToGo(t *testing.T) {
	features, rows := table()

	cases := map[string]http.HandlerFunc{
		"plane down": nil,
		"error": func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "pyod not installed", http.StatusBadRequest)
		},
		"garbage": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("<html>not json</html>"))
		},
		"short answer": func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []Result{{Index: 0, Score: 1}}})
		},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			url := "http://127.0.0.1:1"
			if h != nil {
				srv := httptest.NewServer(h)
				defer srv.Close()
				url = srv.URL
			}
			got, err := NewHTTPDetector(url).Detect(features, rows, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(rows) {
				t.Fatalf("fallback should score every row, got %d", len(got))
			}

			if top := Top(got, 1); top[0].Index != len(rows)-1 {
				t.Fatalf("fallback did not detect the outlier: %+v", top[0])
			}
		})
	}
}
