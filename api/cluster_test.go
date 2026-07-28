package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/urfan03/semeion/cluster"
)

func TestClusterForwardsJobToOwner(t *testing.T) {
	b := NewServer()
	bsrv := httptest.NewServer(b.Handler())
	defer bsrv.Close()
	bAddr := strings.TrimPrefix(bsrv.URL, "http://")

	a := NewServer()
	a.self = "self-node:9999"
	a.ring = cluster.New([]string{bAddr}, 128)

	body := `{"job":{"name":"routed","bucket_span":"1m","detectors":[{"function":"mean","field":"v","side":"high"}]}}`
	w := do(t, a.Handler(), http.MethodPost, "/v1/jobs", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create via A must forward to owner B and return 201, got %d %s", w.Code, w.Body.String())
	}
	if _, ok := b.liveJob("routed"); !ok {
		t.Fatal("the job must be created on the owner node B")
	}
	if _, ok := a.liveJob("routed"); ok {
		t.Fatal("the job must NOT be created on the forwarding node A")
	}

	pts := do(t, a.Handler(), http.MethodPost, "/v1/jobs/routed/points",
		`{"points":[{"time":"2026-01-01T00:00:00Z","value":1,"values":{"v":1}}]}`)
	if pts.Code != http.StatusOK {
		t.Fatalf("points to a job A doesn't own must forward to B, got %d %s", pts.Code, pts.Body.String())
	}
}
