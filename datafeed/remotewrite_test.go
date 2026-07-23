package datafeed

import (
	"encoding/binary"
	"math"
	"testing"
	"time"
)

func pbTag(field, wire int) []byte {
	return binary.AppendUvarint(nil, uint64(field<<3|wire))
}
func pbLenField(field int, b []byte) []byte {
	out := pbTag(field, 2)
	out = binary.AppendUvarint(out, uint64(len(b)))
	return append(out, b...)
}
func pbString(field int, s string) []byte { return pbLenField(field, []byte(s)) }
func pbVarintField(field int, v uint64) []byte {
	return binary.AppendUvarint(pbTag(field, 0), v)
}
func pbFixed64(field int, bits uint64) []byte {
	out := pbTag(field, 1)
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], bits)
	return append(out, b[:]...)
}

func snappyEncodeLiteral(data []byte) []byte {
	out := binary.AppendUvarint(nil, uint64(len(data)))
	for i := 0; i < len(data); {
		chunk := len(data) - i
		if chunk > 60 {
			chunk = 60
		}
		out = append(out, byte((chunk-1)<<2))
		out = append(out, data[i:i+chunk]...)
		i += chunk
	}
	return out
}

func TestParseRemoteWriteRoundTrip(t *testing.T) {
	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).UnixMilli()
	mkLabel := func(name, value string) []byte {
		return append(pbString(1, name), pbString(2, value)...)
	}
	mkSample := func(v float64, tms int64) []byte {
		return append(pbFixed64(1, math.Float64bits(v)), pbVarintField(2, uint64(tms))...)
	}
	series1 := append(append(
		pbLenField(1, mkLabel("__name__", "http_requests_total")),
		pbLenField(1, mkLabel("host", "web1"))...),
		append(pbLenField(2, mkSample(100, ts)), pbLenField(2, mkSample(150, ts+60000))...)...)
	series2 := append(
		pbLenField(1, mkLabel("__name__", "cpu_seconds")),
		pbLenField(2, mkSample(0.42, ts))...)

	req := append(pbLenField(1, series1), pbLenField(1, series2)...)
	body := snappyEncodeLiteral(req)

	samples, err := ParseRemoteWrite(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 3 {
		t.Fatalf("expected 3 samples, got %d", len(samples))
	}

	s0 := samples[0]
	if s0.Metric != "http_requests_total" || s0.Point.Value != 100 {
		t.Fatalf("sample 0 wrong: %+v", s0)
	}
	if s0.Point.Fields["host"] != "web1" {
		t.Fatalf("labels not mapped to fields: %+v", s0.Point.Fields)
	}
	if _, ok := s0.Point.Fields["__name__"]; ok {
		t.Fatal("__name__ should be lifted to Metric, not left in Fields")
	}
	if !s0.Point.Time.Equal(time.UnixMilli(ts).UTC()) {
		t.Fatalf("timestamp not decoded: %v", s0.Point.Time)
	}

	if samples[2].Metric != "cpu_seconds" || math.Abs(samples[2].Point.Value-0.42) > 1e-9 {
		t.Fatalf("sample 2 wrong: %+v", samples[2])
	}
}

func TestSnappyDecodeRejectsGarbage(t *testing.T) {
	if _, err := snappyDecode([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}); err == nil {
		t.Fatal("a bad length prefix / truncated stream should error, not panic")
	}
}
