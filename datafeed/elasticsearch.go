package datafeed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/urfan03/semeion/core"
)

type ESMetric struct {
	Func  string
	Field string
}

type ESSource struct {
	BaseURL           string
	Index             string
	TimeField         string
	Metric            ESMetric
	SplitField        string
	TermsSize         int
	CompositePageSize int
	Frequency         time.Duration
	HTTP              *http.Client
	Username          string
	Password          string
}

func NewESSource(baseURL, index, timeField string, m ESMetric) *ESSource {
	return &ESSource{BaseURL: baseURL, Index: index, TimeField: timeField, Metric: m, HTTP: http.DefaultClient}
}

func esAgg(fn string) string {
	if fn == "mean" {
		return "avg"
	}
	return fn
}

const maxESChunkBuckets = 5000

func (s *ESSource) termsSize() int {
	if s.TermsSize > 0 {
		return s.TermsSize
	}
	return 1000
}

func (s *ESSource) buildBody(start, end time.Time, step time.Duration) ([]byte, error) {
	agg := esAgg(s.Metric.Func)
	if agg != "count" && s.Metric.Field == "" {
		return nil, fmt.Errorf("elasticsearch: metric %q needs a field", s.Metric.Func)
	}
	metricAgg := map[string]any{"metric": map[string]any{agg: map[string]any{"field": s.Metric.Field}}}

	series := map[string]any{
		"date_histogram": map[string]any{
			"field":          s.TimeField,
			"fixed_interval": strconv.FormatInt(step.Milliseconds(), 10) + "ms",
		},
	}
	if s.SplitField != "" {
		split := map[string]any{
			"terms": map[string]any{"field": s.SplitField, "size": s.termsSize()},
		}
		if agg != "count" {
			split["aggs"] = metricAgg
		}
		series["aggs"] = map[string]any{"split": split}
	} else if agg != "count" {
		series["aggs"] = metricAgg
	}

	body := map[string]any{
		"size": 0,
		"query": map[string]any{
			"range": map[string]any{
				s.TimeField: map[string]any{
					"gte":    start.UnixMilli(),
					"lte":    end.UnixMilli(),
					"format": "epoch_millis",
				},
			},
		},
		"aggs": map[string]any{"series": series},
	}
	return json.Marshal(body)
}

func (s *ESSource) Fetch(ctx context.Context, start, end time.Time, step time.Duration) ([]core.DataPoint, error) {
	if s.SplitField != "" {
		return s.fetchComposite(ctx, start, end, step)
	}
	if step <= 0 || !end.After(start) {
		return s.fetchWindow(ctx, start, end, step)
	}
	chunk := step * maxESChunkBuckets
	var out []core.DataPoint
	for cur := start; cur.Before(end); cur = cur.Add(chunk) {
		ce := cur.Add(chunk)
		if ce.After(end) {
			ce = end
		}
		pts, err := s.fetchWindow(ctx, cur, ce, step)
		if err != nil {
			return nil, err
		}
		out = append(out, pts...)
	}
	return out, nil
}

func (s *ESSource) doSearch(ctx context.Context, body []byte) ([]byte, error) {
	endpoint := s.BaseURL + "/" + s.Index + "/_search"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.Username != "" {
		req.SetBasicAuth(s.Username, s.Password)
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("elasticsearch HTTP %d: %s", resp.StatusCode, string(raw))
	}
	return raw, nil
}

func (s *ESSource) fetchWindow(ctx context.Context, start, end time.Time, step time.Duration) ([]core.DataPoint, error) {
	body, err := s.buildBody(start, end, step)
	if err != nil {
		return nil, err
	}
	raw, err := s.doSearch(ctx, body)
	if err != nil {
		return nil, err
	}
	return parseESAgg(raw, esAgg(s.Metric.Func) == "count", s.SplitField)
}

const maxCompositePages = 100000

func (s *ESSource) compositePageSize() int {
	if s.CompositePageSize > 0 {
		return s.CompositePageSize
	}
	return 10000
}

func (s *ESSource) fetchComposite(ctx context.Context, start, end time.Time, step time.Duration) ([]core.DataPoint, error) {
	isCount := esAgg(s.Metric.Func) == "count"
	var out []core.DataPoint
	var after map[string]any
	for page := 0; page < maxCompositePages; page++ {
		body, err := s.buildCompositeBody(start, end, step, after)
		if err != nil {
			return nil, err
		}
		raw, err := s.doSearch(ctx, body)
		if err != nil {
			return nil, err
		}
		pts, next, err := parseESComposite(raw, isCount, s.SplitField)
		if err != nil {
			return nil, err
		}
		out = append(out, pts...)
		if next == nil || len(pts) == 0 {
			break
		}
		after = next
	}
	return out, nil
}

func (s *ESSource) buildCompositeBody(start, end time.Time, step time.Duration, after map[string]any) ([]byte, error) {
	agg := esAgg(s.Metric.Func)
	if agg != "count" && s.Metric.Field == "" {
		return nil, fmt.Errorf("elasticsearch: metric %q needs a field", s.Metric.Func)
	}
	sources := []any{
		map[string]any{"time": map[string]any{"date_histogram": map[string]any{
			"field":          s.TimeField,
			"fixed_interval": strconv.FormatInt(step.Milliseconds(), 10) + "ms",
		}}},
		map[string]any{"term": map[string]any{"terms": map[string]any{"field": s.SplitField}}},
	}
	composite := map[string]any{"size": s.compositePageSize(), "sources": sources}
	if after != nil {
		composite["after"] = after
	}
	series := map[string]any{"composite": composite}
	if agg != "count" {
		series["aggs"] = map[string]any{"metric": map[string]any{agg: map[string]any{"field": s.Metric.Field}}}
	}
	body := map[string]any{
		"size": 0,
		"query": map[string]any{
			"range": map[string]any{
				s.TimeField: map[string]any{
					"gte":    start.UnixMilli(),
					"lte":    end.UnixMilli(),
					"format": "epoch_millis",
				},
			},
		},
		"aggs": map[string]any{"series": series},
	}
	return json.Marshal(body)
}

func parseESComposite(body []byte, isCount bool, splitField string) ([]core.DataPoint, map[string]any, error) {
	var r struct {
		Error        json.RawMessage `json:"error"`
		Aggregations struct {
			Series struct {
				AfterKey map[string]any `json:"after_key"`
				Buckets  []struct {
					Key      map[string]json.RawMessage `json:"key"`
					DocCount int64                      `json:"doc_count"`
					Metric   *esMetricVal               `json:"metric"`
				} `json:"buckets"`
			} `json:"series"`
		} `json:"aggregations"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, nil, fmt.Errorf("elasticsearch: decode: %w", err)
	}
	if len(r.Error) > 0 && string(r.Error) != "null" {
		return nil, nil, fmt.Errorf("elasticsearch: %s", string(r.Error))
	}
	var out []core.DataPoint
	for _, b := range r.Aggregations.Series.Buckets {
		tsRaw, ok := b.Key["time"]
		if !ok {
			continue
		}
		var ms int64
		if json.Unmarshal(tsRaw, &ms) != nil {
			continue
		}
		v, ok := valueOfBucket(isCount, b.DocCount, b.Metric)
		if !ok {
			continue
		}
		out = append(out, core.DataPoint{
			Time: time.UnixMilli(ms).UTC(), Value: v,
			Fields: map[string]string{splitField: termKey(b.Key["term"])},
		})
	}
	return out, r.Aggregations.Series.AfterKey, nil
}

type esMetricVal struct {
	Value *float64 `json:"value"`
}

type esTermBucket struct {
	Key      json.RawMessage `json:"key"`
	DocCount int64           `json:"doc_count"`
	Metric   *esMetricVal    `json:"metric"`
}

func parseESAgg(body []byte, isCount bool, splitField string) ([]core.DataPoint, error) {
	var r struct {
		Error        json.RawMessage `json:"error"`
		Aggregations struct {
			Series struct {
				Buckets []struct {
					Key      int64        `json:"key"`
					DocCount int64        `json:"doc_count"`
					Metric   *esMetricVal `json:"metric"`
					Split    *struct {
						Buckets []esTermBucket `json:"buckets"`
					} `json:"split"`
				} `json:"buckets"`
			} `json:"series"`
		} `json:"aggregations"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("elasticsearch: decode: %w", err)
	}
	if len(r.Error) > 0 && string(r.Error) != "null" {
		return nil, fmt.Errorf("elasticsearch: %s", string(r.Error))
	}
	var out []core.DataPoint
	for _, b := range r.Aggregations.Series.Buckets {
		ts := time.UnixMilli(b.Key).UTC()
		if splitField != "" {
			if b.Split == nil {
				continue
			}
			for _, tb := range b.Split.Buckets {
				v, ok := valueOfBucket(isCount, tb.DocCount, tb.Metric)
				if !ok {
					continue
				}
				out = append(out, core.DataPoint{Time: ts, Value: v,
					Fields: map[string]string{splitField: termKey(tb.Key)}})
			}
			continue
		}
		v, ok := valueOfBucket(isCount, b.DocCount, b.Metric)
		if !ok {
			continue
		}
		out = append(out, core.DataPoint{Time: ts, Value: v})
	}
	return out, nil
}

func valueOfBucket(isCount bool, docCount int64, m *esMetricVal) (float64, bool) {
	if isCount {
		return float64(docCount), true
	}
	if m == nil || m.Value == nil {
		return 0, false
	}
	return *m.Value, true
}

func termKey(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		var str string
		if json.Unmarshal(raw, &str) == nil {
			return str
		}
	}
	return s
}
