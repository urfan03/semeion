package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/urfan03/semeion/jobspec"
)

const (
	maxFilters     = 1000
	maxFilterItems = 100000
)

type filterStore struct {
	mu    sync.Mutex
	items map[string][]string
}

func normalizeFilter(values []string) []string {
	hint := len(values)
	if hint > maxFilterItems {
		hint = maxFilterItems
	}
	seen := make(map[string]bool, hint)
	out := make([]string, 0, hint)
	for _, v := range values {
		if len(out) >= maxFilterItems {
			break
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

func (fs *filterStore) put(id string, values []string) bool {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.items == nil {
		fs.items = map[string][]string{}
	}
	if _, ok := fs.items[id]; !ok && len(fs.items) >= maxFilters {
		return false
	}
	fs.items[id] = normalizeFilter(values)
	return true
}

func (fs *filterStore) get(id string) ([]string, bool) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	v, ok := fs.items[id]
	if !ok {
		return nil, false
	}
	return append([]string(nil), v...), true
}

func (fs *filterStore) del(id string) bool {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if _, ok := fs.items[id]; !ok {
		return false
	}
	delete(fs.items, id)
	return true
}

func (fs *filterStore) snapshot() map[string][]string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	out := make(map[string][]string, len(fs.items))
	for k, v := range fs.items {
		out[k] = append([]string(nil), v...)
	}
	return out
}

func (fs *filterStore) load(m map[string][]string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.items = make(map[string][]string, len(m))
	for k, v := range m {
		if len(fs.items) >= maxFilters {
			break
		}
		fs.items[k] = normalizeFilter(v)
	}
}

func (s *Server) resolveFilters(job *jobspec.Job) {
	for di := range job.Detectors {
		for ri := range job.Detectors[di].Rules {
			rule := &job.Detectors[di].Rules[ri]
			if len(rule.Scope) == 0 {
				continue
			}
			rule.ResolvedFilters = map[string][]string{}
			for _, sc := range rule.Scope {
				if vals, ok := s.filters.get(sc.FilterID); ok {
					rule.ResolvedFilters[sc.FilterID] = vals
				}
			}
		}
	}
}

func (s *Server) handleFilters(w http.ResponseWriter, r *http.Request) {
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/filters"), "/")
	if id == "" {
		if r.Method != http.MethodGet {
			httpError(w, http.StatusMethodNotAllowed, "GET required")
			return
		}
		writeJSON(w, map[string]any{"filters": s.filters.snapshot()})
		return
	}
	switch r.Method {
	case http.MethodGet:
		if v, ok := s.filters.get(id); ok {
			writeJSON(w, map[string]any{"id": id, "items": v})
		} else {
			httpError(w, http.StatusNotFound, "no filter "+id)
		}
	case http.MethodPut:
		var req struct {
			Items []string `json:"items"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpError(w, http.StatusBadRequest, "decode: "+err.Error())
			return
		}
		if !s.filters.put(id, req.Items) {
			httpError(w, http.StatusTooManyRequests, "filter limit reached")
			return
		}
		v, _ := s.filters.get(id)
		writeJSON(w, map[string]any{"id": id, "items": v})
	case http.MethodDelete:
		if s.filters.del(id) {
			writeJSON(w, map[string]string{"deleted": id})
		} else {
			httpError(w, http.StatusNotFound, "no filter "+id)
		}
	default:
		httpError(w, http.StatusMethodNotAllowed, "GET, PUT or DELETE")
	}
}
