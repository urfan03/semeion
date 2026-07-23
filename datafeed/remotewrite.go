package datafeed

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"github.com/urfan03/semeion/core"
)

// Prometheus remote-write receiver — a PUSH datafeed. Prometheus (or an agent)
// can `remote_write` straight to semeion, no scraping loop. The payload is a
// Snappy-BLOCK-compressed protobuf WriteRequest; both the decompressor and the
// protobuf decoder are hand-rolled here so semeion keeps its "single binary, no
// dependencies" promise.
//
// Wire schema (prometheus/prompb):
//   WriteRequest { repeated TimeSeries timeseries = 1 }
//   TimeSeries   { repeated Label labels = 1; repeated Sample samples = 2 }
//   Label        { string name = 1; string value = 2 }
//   Sample       { double value = 1; int64 timestamp = 2 }  // timestamp in ms

// Sample is one decoded remote-write point tagged with its metric name.
type Sample struct {
	Metric string
	Point  core.DataPoint
}

// ParseRemoteWrite decompresses a Snappy-block body and decodes the protobuf
// WriteRequest into samples. Each series' non-__name__ labels become the point's
// Fields (dimensions); the metric name comes from __name__.
func ParseRemoteWrite(body []byte) ([]Sample, error) {
	raw, err := snappyDecode(body)
	if err != nil {
		return nil, fmt.Errorf("remote-write snappy: %w", err)
	}
	return decodeWriteRequest(raw)
}

// ── protobuf decode ──────────────────────────────────────────────────────────

func decodeWriteRequest(buf []byte) ([]Sample, error) {
	var out []Sample
	p := &pbReader{buf: buf}
	for p.more() {
		field, wire, err := p.tag()
		if err != nil {
			return nil, err
		}
		if field == 1 && wire == 2 { // timeseries
			ts, err := p.bytes()
			if err != nil {
				return nil, err
			}
			series, err := decodeTimeSeries(ts)
			if err != nil {
				return nil, err
			}
			out = append(out, series...)
			continue
		}
		if err := p.skip(wire); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func decodeTimeSeries(buf []byte) ([]Sample, error) {
	labels := map[string]string{}
	type sample struct {
		v  float64
		ts int64
	}
	var samples []sample
	p := &pbReader{buf: buf}
	for p.more() {
		field, wire, err := p.tag()
		if err != nil {
			return nil, err
		}
		switch {
		case field == 1 && wire == 2: // Label
			lb, err := p.bytes()
			if err != nil {
				return nil, err
			}
			name, value, err := decodeLabel(lb)
			if err != nil {
				return nil, err
			}
			labels[name] = value
		case field == 2 && wire == 2: // Sample
			sb, err := p.bytes()
			if err != nil {
				return nil, err
			}
			v, tsms, err := decodeSample(sb)
			if err != nil {
				return nil, err
			}
			samples = append(samples, sample{v, tsms})
		default:
			if err := p.skip(wire); err != nil {
				return nil, err
			}
		}
	}
	metric := labels["__name__"]
	fields := make(map[string]string, len(labels))
	for k, v := range labels {
		if k != "__name__" {
			fields[k] = v
		}
	}
	out := make([]Sample, 0, len(samples))
	for _, s := range samples {
		out = append(out, Sample{
			Metric: metric,
			Point:  core.DataPoint{Time: time.UnixMilli(s.ts).UTC(), Value: s.v, Fields: fields},
		})
	}
	return out, nil
}

func decodeLabel(buf []byte) (name, value string, err error) {
	p := &pbReader{buf: buf}
	for p.more() {
		field, wire, e := p.tag()
		if e != nil {
			return "", "", e
		}
		switch {
		case field == 1 && wire == 2:
			b, e := p.bytes()
			if e != nil {
				return "", "", e
			}
			name = string(b)
		case field == 2 && wire == 2:
			b, e := p.bytes()
			if e != nil {
				return "", "", e
			}
			value = string(b)
		default:
			if e := p.skip(wire); e != nil {
				return "", "", e
			}
		}
	}
	return name, value, nil
}

func decodeSample(buf []byte) (value float64, tsms int64, err error) {
	p := &pbReader{buf: buf}
	for p.more() {
		field, wire, e := p.tag()
		if e != nil {
			return 0, 0, e
		}
		switch {
		case field == 1 && wire == 1: // double (fixed64)
			bits, e := p.fixed64()
			if e != nil {
				return 0, 0, e
			}
			value = math.Float64frombits(bits)
		case field == 2 && wire == 0: // int64 timestamp (varint, ms)
			v, e := p.varint()
			if e != nil {
				return 0, 0, e
			}
			tsms = int64(v)
		default:
			if e := p.skip(wire); e != nil {
				return 0, 0, e
			}
		}
	}
	return value, tsms, nil
}

// pbReader is a minimal protobuf wire reader.
type pbReader struct {
	buf []byte
	pos int
}

func (p *pbReader) more() bool { return p.pos < len(p.buf) }

func (p *pbReader) tag() (field int, wire int, err error) {
	v, err := p.varint()
	if err != nil {
		return 0, 0, err
	}
	return int(v >> 3), int(v & 7), nil
}

func (p *pbReader) varint() (uint64, error) {
	var x uint64
	var shift uint
	for {
		if p.pos >= len(p.buf) {
			return 0, fmt.Errorf("protobuf: varint truncated")
		}
		b := p.buf[p.pos]
		p.pos++
		x |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return x, nil
		}
		shift += 7
		if shift >= 64 {
			return 0, fmt.Errorf("protobuf: varint overflow")
		}
	}
}

func (p *pbReader) bytes() ([]byte, error) {
	n, err := p.varint()
	if err != nil {
		return nil, err
	}
	if p.pos+int(n) > len(p.buf) {
		return nil, fmt.Errorf("protobuf: length-delimited field truncated")
	}
	b := p.buf[p.pos : p.pos+int(n)]
	p.pos += int(n)
	return b, nil
}

func (p *pbReader) fixed64() (uint64, error) {
	if p.pos+8 > len(p.buf) {
		return 0, fmt.Errorf("protobuf: fixed64 truncated")
	}
	v := binary.LittleEndian.Uint64(p.buf[p.pos:])
	p.pos += 8
	return v, nil
}

func (p *pbReader) skip(wire int) error {
	switch wire {
	case 0:
		_, err := p.varint()
		return err
	case 1:
		_, err := p.fixed64()
		return err
	case 2:
		_, err := p.bytes()
		return err
	case 5:
		if p.pos+4 > len(p.buf) {
			return fmt.Errorf("protobuf: fixed32 truncated")
		}
		p.pos += 4
		return nil
	default:
		return fmt.Errorf("protobuf: unknown wire type %d", wire)
	}
}
