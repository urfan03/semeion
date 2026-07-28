package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/urfan03/semeion/cluster"
)

const forwardedHeader = "X-Semeion-Forwarded"

func (s *Server) WithCluster(self string, peers []string) *Server {
	if self == "" {
		return s
	}
	members := append([]string{self}, peers...)
	s.self = self
	s.ring = cluster.New(members, 128)
	s.clusterClient = &http.Client{Timeout: 30 * time.Second}
	return s
}

func (s *Server) clusterKey(r *http.Request) (string, bool) {
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/v1/jobs/"):
		name, _, _ := strings.Cut(strings.TrimPrefix(p, "/v1/jobs/"), "/")
		return name, name != ""
	case strings.HasPrefix(p, "/v1/results/"):
		return strings.TrimPrefix(p, "/v1/results/"), true
	case strings.HasPrefix(p, "/v1/history/"):
		return strings.TrimPrefix(p, "/v1/history/"), true
	case strings.HasPrefix(p, "/v1/influencers/"):
		return strings.TrimPrefix(p, "/v1/influencers/"), true
	case strings.HasPrefix(p, "/v1/grafana/"):
		return strings.TrimPrefix(p, "/v1/grafana/"), true
	case p == "/v1/jobs" && r.Method == http.MethodPost:
		return s.jobNameFromBody(r)
	}
	return "", false
}

func (s *Server) jobNameFromBody(r *http.Request) (string, bool) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return "", false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	var req struct {
		Name string          `json:"name"`
		Job  json.RawMessage `json:"job"`
	}
	if json.Unmarshal(body, &req) != nil {
		return "", false
	}
	if req.Name != "" {
		return req.Name, true
	}
	if len(req.Job) > 0 {
		var j struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(req.Job, &j) == nil && j.Name != "" {
			return j.Name, true
		}
	}
	return "", false
}

func (s *Server) withCluster(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.ring == nil || s.self == "" || r.Header.Get(forwardedHeader) != "" {
			next.ServeHTTP(w, r)
			return
		}
		key, ok := s.clusterKey(r)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		owner := s.ring.Owner(key)
		if owner == "" || owner == s.self {
			next.ServeHTTP(w, r)
			return
		}
		s.forward(w, r, owner)
	})
}

func (s *Server) forward(w http.ResponseWriter, r *http.Request, owner string) {
	var body io.Reader
	if r.Body != nil {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			httpError(w, http.StatusBadGateway, "cluster: read body")
			return
		}
		body = bytes.NewReader(b)
	}
	target := "http://" + owner + r.URL.RequestURI()
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, body)
	if err != nil {
		httpError(w, http.StatusBadGateway, "cluster: build request")
		return
	}
	req.Header = r.Header.Clone()
	req.Header.Set(forwardedHeader, "1")
	client := s.clusterClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		httpError(w, http.StatusBadGateway, "cluster: forward to "+owner+": "+err.Error())
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (s *Server) handleCluster(w http.ResponseWriter, r *http.Request) {
	if s.ring == nil {
		writeJSON(w, map[string]any{"enabled": false, "self": s.self})
		return
	}
	writeJSON(w, map[string]any{"enabled": true, "self": s.self, "members": s.ring.Members()})
}
