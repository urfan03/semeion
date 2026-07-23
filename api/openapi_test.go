package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestOpenAPISpecServed(t *testing.T) {
	s := NewServer()
	w := do(t, s.Handler(), http.MethodGet, "/openapi.json", "")
	if w.Code != http.StatusOK {
		t.Fatalf("openapi should return 200, got %d", w.Code)
	}
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("openapi must be valid JSON: %v", err)
	}
	if v, _ := doc["openapi"].(string); v == "" {
		t.Fatal("missing openapi version field")
	}
	paths, ok := doc["paths"].(map[string]any)
	if !ok || len(paths) < 20 {
		t.Fatalf("expected the documented paths, got %d", len(paths))
	}
	for _, p := range []string{"/v1/analyze", "/v1/catalog", "/v1/cloudflare/logs", "/v1/leadlag"} {
		if _, ok := paths[p]; !ok {
			t.Fatalf("spec is missing path %s", p)
		}
	}
}
