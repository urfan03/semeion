package ingest

import (
	"strings"
	"testing"
)

func TestParseCSV(t *testing.T) {
	data := `time,latency,host
2026-01-01T00:00:00Z,120.5,web-1
2026-01-01T00:05:00Z,131,web-2
1767225900,140,web-1
`
	pts, err := parseCSV(strings.NewReader(data), "time", "latency")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pts) != 3 {
		t.Fatalf("expected 3 points, got %d", len(pts))
	}
	if pts[0].Value != 120.5 {
		t.Fatalf("value[0]: got %v", pts[0].Value)
	}
	if pts[0].Fields["host"] != "web-1" {
		t.Fatalf("dim host[0]: got %q", pts[0].Fields["host"])
	}

	if pts[2].Time.IsZero() {
		t.Fatal("epoch time did not parse")
	}
}

func TestParseCSVMissingColumn(t *testing.T) {
	if _, err := parseCSV(strings.NewReader("t,v\n1,2\n"), "time", "v"); err == nil {
		t.Fatal("expected error for missing time column")
	}
}
