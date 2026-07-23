package api

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestConcurrentIngestAndReads(t *testing.T) {
	s := NewServer().WithHistory(t.TempDir())
	h := s.Handler()

	create := `{"job":{"name":"c","bucket_span":"1m","detectors":[{"function":"mean","field":"v","by_field":"host","side":"high"}]},"metric":"m","threshold":50}`
	if w := do(t, h, http.MethodPost, "/v1/jobs", create); w.Code != http.StatusCreated {
		t.Fatalf("create job: %d", w.Code)
	}

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup

	for g := 0; g < 12; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				ts := t0.Add(time.Duration(g*40+i) * time.Second)
				v := 100.0
				if i%17 == 0 {
					v = 900
				}
				body := fmt.Sprintf(`{"points":[{"time":%q,"value":%g,"fields":{"host":"h%d"},"values":{"v":%g}}]}`,
					ts.Format(time.RFC3339Nano), v, g%3, v)
				do(t, h, http.MethodPost, "/v1/jobs/c/points", body)
			}
		}(g)
	}

	readers := []string{"/v1/jobs/c", "/v1/results/c", "/v1/jobs/c/interim", "/v1/jobs/c/stale", "/v1/influencers/c", "/v1/jobs"}
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 60; i++ {
				do(t, h, http.MethodGet, readers[i%len(readers)], "")
			}
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("concurrent ingest/read deadlocked or hung")
	}

	w := do(t, h, http.MethodGet, "/v1/jobs/c", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"points"`) {
		t.Fatalf("server unresponsive after concurrent load: %d %s", w.Code, w.Body.String())
	}
}
