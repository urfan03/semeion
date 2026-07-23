package otlp

import "testing"

func FuzzParseMetrics(f *testing.F) {
	f.Add([]byte(""))
	f.Add([]byte("{}"))
	f.Add([]byte(`{"resourceMetrics":[{"scopeMetrics":[{"metrics":[{"name":"m","gauge":{"dataPoints":[{"timeUnixNano":"1","asDouble":1}]}}]}]}]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = ParseMetrics(data)
	})
}

func FuzzParseLogs(f *testing.F) {
	f.Add([]byte("{}"))
	f.Add([]byte(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"timeUnixNano":"1","body":{"stringValue":"x"}}]}]}]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = ParseLogs(data)
	})
}

func FuzzParseTraces(f *testing.F) {
	f.Add([]byte("{}"))
	f.Add([]byte(`{"resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":"a","spanId":"b","name":"n"}]}]}]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = ParseTraces(data)
	})
}
