package otlp

import (
	"encoding/json"
	"fmt"
	"time"
)

type Span struct {
	TraceID  string
	SpanID   string
	ParentID string
	Name     string
	Service  string
	Start    time.Time
	End      time.Time
	Error    bool
	Attrs    map[string]string
}

func (s Span) Duration() time.Duration {
	if s.End.IsZero() || s.Start.IsZero() {
		return 0
	}
	return s.End.Sub(s.Start)
}

type tracesPayload struct {
	ResourceSpans []struct {
		Resource   resource `json:"resource"`
		ScopeSpans []struct {
			Spans []struct {
				TraceID           string      `json:"traceId"`
				SpanID            string      `json:"spanId"`
				ParentSpanID      string      `json:"parentSpanId"`
				Name              string      `json:"name"`
				StartTimeUnixNano json.Number `json:"startTimeUnixNano"`
				EndTimeUnixNano   json.Number `json:"endTimeUnixNano"`
				Attributes        []kv        `json:"attributes"`
				Status            struct {
					Code string `json:"code"`
				} `json:"status"`
			} `json:"spans"`
		} `json:"scopeSpans"`
	} `json:"resourceSpans"`
}

func ParseTraces(body []byte) ([]Span, error) {
	var p tracesPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("otlp traces: %w", err)
	}
	var out []Span
	for _, rs := range p.ResourceSpans {
		base := attrs(rs.Resource.Attributes, nil)
		service := base["service.name"]
		for _, ss := range rs.ScopeSpans {
			for _, sp := range ss.Spans {
				if sp.SpanID == "" || service == "" {
					continue
				}
				start, _ := nanos(sp.StartTimeUnixNano)
				end, _ := nanos(sp.EndTimeUnixNano)
				out = append(out, Span{
					TraceID: sp.TraceID, SpanID: sp.SpanID, ParentID: sp.ParentSpanID,
					Name: sp.Name, Service: service, Start: start, End: end,

					Error: sp.Status.Code == "STATUS_CODE_ERROR" || sp.Status.Code == "ERROR" || sp.Status.Code == "2",
					Attrs: attrs(sp.Attributes, base),
				})
			}
		}
	}
	return out, nil
}
