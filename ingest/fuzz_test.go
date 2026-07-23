package ingest

import (
	"strings"
	"testing"
)

func FuzzParseLogpush(f *testing.F) {
	f.Add("")
	f.Add("{}")
	f.Add(`{"EdgeStartTimestamp":"2026-01-01T00:00:00Z","ClientRequestHost":"h","ClientRequestPath":"/a/1","EdgeResponseStatus":200}`)
	f.Add("{bad\n1767225600\n")
	f.Fuzz(func(t *testing.T, data string) {
		_, _, _ = ParseLogpush(strings.NewReader(data))
	})
}

func FuzzParseCSV(f *testing.F) {
	f.Add("time,value\n2026-01-01T00:00:00Z,1.0\n")
	f.Add("")
	f.Add("garbage,,,\n\n,,")
	f.Fuzz(func(t *testing.T, data string) {
		_, _ = parseCSV(strings.NewReader(data), "time", "value")
	})
}
