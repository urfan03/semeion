// Package otlp decodes OpenTelemetry's JSON wire format (OTLP/HTTP) into
// semeion's core types.
//
// This is deliberately a *decoder*, not an OTel SDK dependency: any collector
// can `otlphttp`-export to semeion with `encoding: json`, and semeion stays a
// zero-dependency static binary. Only the parts that carry a signal are
// decoded — gauges, sums and histograms for metrics, and log records.
package otlp

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/urfan03/semeion/core"
)

// MetricPoint is one OTLP data point, tagged with the metric it belongs to.
type MetricPoint struct {
	Metric string
	Point  core.DataPoint
}

// ── wire types ───────────────────────────────────────────────────────────────

type kv struct {
	Key   string   `json:"key"`
	Value anyValue `json:"value"`
}

// anyValue is OTLP's AnyValue. Ints arrive as JSON *strings* (int64 safety), so
// every numeric field is decoded through json.Number.
type anyValue struct {
	StringValue *string      `json:"stringValue"`
	IntValue    *json.Number `json:"intValue"`
	DoubleValue *json.Number `json:"doubleValue"`
	BoolValue   *bool        `json:"boolValue"`
}

func (v anyValue) String() string {
	switch {
	case v.StringValue != nil:
		return *v.StringValue
	case v.IntValue != nil:
		return v.IntValue.String()
	case v.DoubleValue != nil:
		return v.DoubleValue.String()
	case v.BoolValue != nil:
		return strconv.FormatBool(*v.BoolValue)
	}
	return ""
}

type dataPoint struct {
	TimeUnixNano json.Number  `json:"timeUnixNano"`
	AsDouble     *json.Number `json:"asDouble"`
	AsInt        *json.Number `json:"asInt"`
	Attributes   []kv         `json:"attributes"`

	// Histogram fields — semeion scores the bucket mean (sum/count), which is
	// what a "mean latency" detector expects.
	Count *json.Number `json:"count"`
	Sum   *json.Number `json:"sum"`
}

// dpContainer is the shape gauge, sum and histogram all share.
type dpContainer struct {
	DataPoints []dataPoint `json:"dataPoints"`
}

func (c *dpContainer) points() []dataPoint {
	if c == nil {
		return nil
	}
	return c.DataPoints
}

type metric struct {
	Name      string       `json:"name"`
	Gauge     *dpContainer `json:"gauge"`
	Sum       *dpContainer `json:"sum"`
	Histogram *dpContainer `json:"histogram"`
}

type resource struct {
	Attributes []kv `json:"attributes"`
}

type metricsPayload struct {
	ResourceMetrics []struct {
		Resource     resource `json:"resource"`
		ScopeMetrics []struct {
			Metrics []metric `json:"metrics"`
		} `json:"scopeMetrics"`
	} `json:"resourceMetrics"`
}

type logsPayload struct {
	ResourceLogs []struct {
		Resource  resource `json:"resource"`
		ScopeLogs []struct {
			LogRecords []struct {
				TimeUnixNano         json.Number `json:"timeUnixNano"`
				ObservedTimeUnixNano json.Number `json:"observedTimeUnixNano"`
				SeverityText         string      `json:"severityText"`
				Body                 anyValue    `json:"body"`
				Attributes           []kv        `json:"attributes"`
			} `json:"logRecords"`
		} `json:"scopeLogs"`
	} `json:"resourceLogs"`
}

// ── decoding ─────────────────────────────────────────────────────────────────

// ParseMetrics decodes an OTLP/JSON ExportMetricsServiceRequest. Resource and
// point attributes both become dimensions, so `by`/`partition` splitting works
// on service.name, host, route, … without any extra configuration.
func ParseMetrics(body []byte) ([]MetricPoint, error) {
	var p metricsPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("otlp metrics: %w", err)
	}
	var out []MetricPoint
	for _, rm := range p.ResourceMetrics {
		base := attrs(rm.Resource.Attributes, nil)
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				for _, dps := range [][]dataPoint{m.Gauge.points(), m.Sum.points(), m.Histogram.points()} {
					for _, dp := range dps {
						mp, ok := toPoint(m.Name, dp, base)
						if ok {
							out = append(out, mp)
						}
					}
				}
			}
		}
	}
	return out, nil
}

func toPoint(name string, dp dataPoint, base map[string]string) (MetricPoint, bool) {
	ts, ok := nanos(dp.TimeUnixNano)
	if !ok {
		return MetricPoint{}, false
	}
	v, ok := value(dp)
	if !ok {
		return MetricPoint{}, false
	}
	p := core.DataPoint{
		Time:   ts,
		Value:  v,
		Fields: attrs(dp.Attributes, base),
		// Named too, so a detector can address the metric by name — and several
		// metrics can feed one multivariate detector.
		Values: map[string]float64{name: v},
	}
	return MetricPoint{Metric: name, Point: p}, true
}

func value(dp dataPoint) (float64, bool) {
	if dp.AsDouble != nil {
		if f, err := dp.AsDouble.Float64(); err == nil {
			return f, true
		}
	}
	if dp.AsInt != nil {
		if f, err := dp.AsInt.Float64(); err == nil {
			return f, true
		}
	}
	// Histogram: the mean is the meaningful scalar. A zero-count bucket carries
	// no observation, so it is skipped rather than scored as 0.
	if dp.Sum != nil && dp.Count != nil {
		sum, e1 := dp.Sum.Float64()
		cnt, e2 := dp.Count.Float64()
		if e1 == nil && e2 == nil && cnt > 0 {
			return sum / cnt, true
		}
	}
	return 0, false
}

// ParseLogs decodes an OTLP/JSON ExportLogsServiceRequest into log lines ready
// for the Drain categorizer.
func ParseLogs(body []byte) ([]core.LogLine, error) {
	var p logsPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("otlp logs: %w", err)
	}
	var out []core.LogLine
	for _, rl := range p.ResourceLogs {
		base := attrs(rl.Resource.Attributes, nil)
		for _, sl := range rl.ScopeLogs {
			for _, lr := range sl.LogRecords {
				ts, ok := nanos(lr.TimeUnixNano)
				if !ok {
					// Emitters may only set the observed time.
					if ts, ok = nanos(lr.ObservedTimeUnixNano); !ok {
						continue
					}
				}
				msg := lr.Body.String()
				if msg == "" {
					continue
				}
				f := attrs(lr.Attributes, base)
				if lr.SeverityText != "" {
					if f == nil {
						f = map[string]string{}
					}
					f["severity"] = lr.SeverityText
				}
				out = append(out, core.LogLine{Time: ts, Message: msg, Fields: f})
			}
		}
	}
	return out, nil
}

// attrs merges point attributes over the resource-level base without mutating it.
func attrs(list []kv, base map[string]string) map[string]string {
	if len(list) == 0 && len(base) == 0 {
		return nil
	}
	out := make(map[string]string, len(list)+len(base))
	for k, v := range base {
		out[k] = v
	}
	for _, a := range list {
		if s := a.Value.String(); s != "" {
			out[a.Key] = s
		}
	}
	return out
}

func nanos(n json.Number) (time.Time, bool) {
	if n == "" {
		return time.Time{}, false
	}
	i, err := n.Int64()
	if err != nil || i <= 0 {
		return time.Time{}, false
	}
	return time.Unix(0, i).UTC(), true
}
