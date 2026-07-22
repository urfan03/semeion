package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthTokenRequired(t *testing.T) {
	s := NewServer().WithAuthToken("s3cret")
	h := s.Handler()

	// No token → 401 on an API endpoint.
	if w := do(t, h, http.MethodGet, "/v1/jobs", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without a token, got %d", w.Code)
	}
	// Wrong token → 401.
	req := httptest.NewRequest(http.MethodGet, "/v1/jobs", nil)
	req.Header.Set("Authorization", "Bearer nope")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with a wrong token, got %d", w.Code)
	}
	// Correct token → allowed.
	req = httptest.NewRequest(http.MethodGet, "/v1/jobs", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with the right token, got %d", w.Code)
	}
	// Health + metrics stay open.
	for _, p := range []string{"/healthz", "/metrics"} {
		if w := do(t, h, http.MethodGet, p, ""); w.Code != http.StatusOK {
			t.Errorf("%s should stay open, got %d", p, w.Code)
		}
	}
}

func TestBodyLimitRejectsHugePost(t *testing.T) {
	s := NewServer()
	// A body over the cap on a raw-decoder endpoint (/v1/analyze) must fail, not OOM.
	req := httptest.NewRequest(http.MethodPost, "/v1/analyze", bytes.NewReader(make([]byte, maxBodyBytes+1024)))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code == http.StatusOK {
		t.Fatalf("oversized body should be rejected, got %d", w.Code)
	}
}

func TestRateLimit(t *testing.T) {
	s := NewServer().WithRateLimit(5) // 5 rps, burst 10
	h := s.Handler()
	got200, got429 := 0, 0
	for i := 0; i < 30; i++ {
		w := do(t, h, http.MethodGet, "/v1/jobs", "")
		switch w.Code {
		case http.StatusOK:
			got200++
		case http.StatusTooManyRequests:
			got429++
		}
	}
	if got429 == 0 {
		t.Fatalf("burst of 30 should trip the limiter, got %d ok / %d limited", got200, got429)
	}
	// Health is exempt.
	for i := 0; i < 30; i++ {
		if w := do(t, h, http.MethodGet, "/healthz", ""); w.Code != http.StatusOK {
			t.Fatalf("/healthz must not be rate-limited, got %d", w.Code)
		}
	}
}
