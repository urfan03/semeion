package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/urfan03/semeion/model"
)

type storedForecast struct {
	ID      string       `json:"id"`
	Job     string       `json:"job"`
	Horizon int          `json:"horizon"`
	Bands   []model.Band `json:"bands"`
	Created time.Time    `json:"created"`
	Expires time.Time    `json:"expires"`
}

type forecastStore struct {
	mu    sync.Mutex
	items map[string]storedForecast
	seq   int64
}

func (fs *forecastStore) put(f storedForecast) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.items == nil {
		fs.items = map[string]storedForecast{}
	}
	for id, ex := range fs.items {
		if ex.Job == f.Job {
			delete(fs.items, id)
		}
	}
	fs.items[f.ID] = f
}

func (fs *forecastStore) nextID(job string) string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.seq++
	return fmt.Sprintf("%s-fc-%d", job, fs.seq)
}

func (fs *forecastStore) list(now time.Time) []storedForecast {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	out := make([]storedForecast, 0, len(fs.items))
	for id, f := range fs.items {
		if now.After(f.Expires) {
			delete(fs.items, id)
			continue
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (fs *forecastStore) get(id string, now time.Time) (storedForecast, bool) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	f, ok := fs.items[id]
	if !ok {
		return storedForecast{}, false
	}
	if now.After(f.Expires) {
		delete(fs.items, id)
		return storedForecast{}, false
	}
	return f, true
}

func (fs *forecastStore) del(id string) bool {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if _, ok := fs.items[id]; !ok {
		return false
	}
	delete(fs.items, id)
	return true
}

func (s *Server) handleForecasts(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/forecasts"), "/")
	switch {
	case rest == "" && r.Method == http.MethodGet:
		writeJSON(w, map[string]any{"forecasts": s.forecasts.list(time.Now())})
	case rest == "" && r.Method == http.MethodPost:
		s.createForecast(w, r)
	case rest != "" && r.Method == http.MethodGet:
		if f, ok := s.forecasts.get(rest, time.Now()); ok {
			writeJSON(w, f)
		} else {
			httpError(w, http.StatusNotFound, "no forecast "+rest+" (expired or unknown)")
		}
	case rest != "" && r.Method == http.MethodDelete:
		if s.forecasts.del(rest) {
			writeJSON(w, map[string]string{"deleted": rest})
		} else {
			httpError(w, http.StatusNotFound, "no forecast "+rest)
		}
	default:
		httpError(w, http.StatusMethodNotAllowed, "GET or POST")
	}
}

func (s *Server) createForecast(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Job       string    `json:"job"`
		Series    []float64 `json:"series"`
		Horizon   int       `json:"horizon"`
		ExpiresIn string    `json:"expires_in"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "decode: "+err.Error())
		return
	}
	if req.Job == "" {
		httpError(w, http.StatusBadRequest, "job is required")
		return
	}
	if req.Horizon <= 0 {
		req.Horizon = 12
	}
	ttl := 14 * 24 * time.Hour
	if req.ExpiresIn != "" {
		if d, err := time.ParseDuration(req.ExpiresIn); err == nil && d > 0 {
			ttl = d
		}
	}
	now := time.Now()
	f := storedForecast{
		ID:      s.forecasts.nextID(req.Job),
		Job:     req.Job,
		Horizon: req.Horizon,
		Bands:   s.provider.ForecastBands(req.Series, req.Horizon),
		Created: now,
		Expires: now.Add(ttl),
	}
	s.forecasts.put(f)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(f)
}
