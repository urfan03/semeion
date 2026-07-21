package datafeed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/urfan03/semeion/core"
)

// ESMetric describes the aggregation an Elasticsearch datafeed computes per
// bucket. Func is "count" or a metric name ("mean"/"avg", "sum", "min", "max").
type ESMetric struct {
	Func  string
	Field string // required unless Func == "count"
}

// ESSource reads from Elasticsearch (or OpenSearch) via a date_histogram
// aggregation — the free path that needs no ML license. It turns a raw index
// into a per-bucket time series the engine can score.
type ESSource struct {
	BaseURL   string // e.g. http://localhost:9200
	Index     string
	TimeField string
	Metric    ESMetric
	HTTP      *http.Client
	Username  string // optional basic auth
	Password  string
}

// NewESSource builds an Elasticsearch source with the default HTTP client.
func NewESSource(baseURL, index, timeField string, m ESMetric) *ESSource {
	return &ESSource{BaseURL: baseURL, Index: index, TimeField: timeField, Metric: m, HTTP: http.DefaultClient}
}

func esAgg(fn string) string {
	if fn == "mean" {
		return "avg"
	}
	return fn
}

func (s *ESSource) buildBody(start, end time.Time, step time.Duration) ([]byte, error) {
	agg := esAgg(s.Metric.Func)
	series := map[string]any{
		"date_histogram": map[string]any{
			"field":          s.TimeField,
			"fixed_interval": strconv.FormatInt(step.Milliseconds(), 10) + "ms",
		},
	}
	if agg != "count" {
		if s.Metric.Field == "" {
			return nil, fmt.Errorf("elasticsearch: metric %q needs a field", s.Metric.Func)
		}
		series["aggs"] = map[string]any{
			"metric": map[string]any{agg: map[string]any{"field": s.Metric.Field}},
		}
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

// Fetch runs the aggregation and returns one point per histogram bucket.
func (s *ESSource) Fetch(ctx context.Context, start, end time.Time, step time.Duration) ([]core.DataPoint, error) {
	body, err := s.buildBody(start, end, step)
	if err != nil {
		return nil, err
	}
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
	return parseESAgg(raw, esAgg(s.Metric.Func) == "count")
}

func parseESAgg(body []byte, isCount bool) ([]core.DataPoint, error) {
	var r struct {
		Error        json.RawMessage `json:"error"`
		Aggregations struct {
			Series struct {
				Buckets []struct {
					Key      int64 `json:"key"`
					DocCount int64 `json:"doc_count"`
					Metric   *struct {
						Value *float64 `json:"value"`
					} `json:"metric"`
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
		var v float64
		if isCount {
			v = float64(b.DocCount)
		} else {
			if b.Metric == nil || b.Metric.Value == nil {
				continue // empty bucket → no metric value
			}
			v = *b.Metric.Value
		}
		out = append(out, core.DataPoint{Time: time.UnixMilli(b.Key).UTC(), Value: v})
	}
	return out, nil
}
