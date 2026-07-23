package ingest

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/urfan03/semeion/engine"
)

func TestParseLogpushMappingAndTimestamps(t *testing.T) {
	nd := strings.Join([]string{
		`{"EdgeStartTimestamp":"2026-01-01T00:00:00Z","ClientRequestHost":"api.example.com","ClientRequestPath":"/user/8412/orders?page=2","ClientRequestMethod":"GET","EdgeResponseStatus":200,"ClientCountry":"us","EdgeColoCode":"FRA","CacheCacheStatus":"hit","ClientIP":"1.2.3.4","OriginResponseDurationMs":42,"EdgeResponseBytes":900}`,
		`{"EdgeStartTimestamp":1767225601000000000,"ClientRequestHost":"api.example.com","ClientRequestPath":"/health","ClientRequestMethod":"GET","EdgeResponseStatus":503,"ClientCountry":"de"}`,
		`{"EdgeStartTimestamp":1767225602,"ClientRequestHost":"api.example.com","ClientRequestPath":"/login","ClientRequestMethod":"POST","EdgeResponseStatus":403,"WAFAction":"block"}`,
		`   `,
		`{bad json`,
	}, "\n")

	pts, skipped, err := ParseLogpush(strings.NewReader(nd))
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 3 {
		t.Fatalf("expected 3 parsed points, got %d", len(pts))
	}
	if skipped != 1 {
		t.Fatalf("expected 1 skipped (malformed) line, got %d", skipped)
	}

	p0 := pts[0]
	if p0.Fields["path"] != "/user/:id/orders" {
		t.Fatalf("path should be normalized to /user/:id/orders, got %q", p0.Fields["path"])
	}
	if p0.Fields["status_class"] != "2xx" || p0.Fields["country"] != "us" {
		t.Fatalf("unexpected dimensions: %+v", p0.Fields)
	}
	if p0.Values["origin_ms"] != 42 || p0.Values["resp_bytes"] != 900 {
		t.Fatalf("unexpected metrics: %+v", p0.Values)
	}

	if !pts[0].Time.Before(pts[1].Time) || !pts[1].Time.Before(pts[2].Time) {
		t.Fatalf("timestamps not increasing across formats: %v %v %v", pts[0].Time, pts[1].Time, pts[2].Time)
	}
	if pts[1].Fields["status_class"] != "5xx" || pts[2].Fields["status_class"] != "4xx" {
		t.Fatalf("status classes wrong: %q %q", pts[1].Fields["status_class"], pts[2].Fields["status_class"])
	}
	if pts[2].Fields["waf"] != "block" {
		t.Fatalf("waf action not mapped: %+v", pts[2].Fields)
	}
}

func TestCloudflareJobDetects5xxSpike(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var b strings.Builder
	line := func(ts time.Time, status int) {
		b.WriteString(`{"EdgeStartTimestamp":"`)
		b.WriteString(ts.Format(time.RFC3339Nano))
		b.WriteString(`","ClientRequestHost":"api.example.com","ClientRequestPath":"/v1/thing","ClientRequestMethod":"GET","ClientCountry":"us","EdgeResponseStatus":`)
		b.WriteString(strconv.Itoa(status))
		b.WriteString("}\n")
	}

	for bkt := 0; bkt < 60; bkt++ {
		base := t0.Add(time.Duration(bkt) * time.Minute)
		for i := 0; i < 48; i++ {
			line(base.Add(time.Duration(i)*time.Second), 200)
		}
		errs := 2
		if bkt == 50 {
			errs = 40
		}
		for i := 0; i < errs; i++ {
			line(base.Add(time.Duration(i)*time.Millisecond*10), 503)
		}
	}
	pts, _, err := ParseLogpush(strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	job := CloudflareJob("cf", time.Minute)
	eng, err := engine.New(job)
	if err != nil {
		t.Fatal(err)
	}
	found5xx := false
	for _, br := range eng.Run(pts, 50) {
		if !br.Time.Equal(t0.Add(50 * time.Minute)) {
			continue
		}
		for _, r := range br.Records {
			if r.Series == "status_class=5xx" || strings.Contains(r.Series, "5xx") {
				found5xx = true
			}
		}
	}
	if !found5xx {
		t.Fatal("expected a 5xx error-class spike to be flagged in the outage bucket")
	}
}
