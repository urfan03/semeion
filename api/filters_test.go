package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/urfan03/semeion/jobspec"
)

func TestFilterListCRUDAndResolve(t *testing.T) {
	s := NewServer()
	h := s.Handler()

	if w := do(t, h, http.MethodPut, "/v1/filters/safe", `{"items":["web1","web2","web1"]}`); w.Code != http.StatusOK {
		t.Fatalf("put filter: %d %s", w.Code, w.Body.String())
	}
	if w := do(t, h, http.MethodGet, "/v1/filters/safe", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "web1") {
		t.Fatalf("get filter: %d %s", w.Code, w.Body.String())
	}
	if w := do(t, h, http.MethodGet, "/v1/filters", ""); !strings.Contains(w.Body.String(), "safe") {
		t.Fatalf("list must include the filter: %s", w.Body.String())
	}

	job := jobspec.Job{Detectors: []jobspec.Detector{{
		Function: jobspec.FuncMean, Field: "v",
		Rules: []jobspec.Rule{{Scope: []jobspec.ScopeClause{{Field: "host", FilterID: "safe", Include: true}}}},
	}}}
	s.resolveFilters(&job)
	if got := job.Detectors[0].Rules[0].ResolvedFilters["safe"]; len(got) != 2 {
		t.Fatalf("resolveFilters must expand the referenced filter (2 deduped items), got %v", got)
	}

	if w := do(t, h, http.MethodDelete, "/v1/filters/safe", ""); w.Code != http.StatusOK {
		t.Fatalf("delete filter: %d", w.Code)
	}
	if w := do(t, h, http.MethodGet, "/v1/filters/safe", ""); w.Code != http.StatusNotFound {
		t.Fatalf("deleted filter must be gone, got %d", w.Code)
	}
}
