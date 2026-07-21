package datafeed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/urfan03/semeion/core"
)

// PromSource reads from a Prometheus-compatible /api/v1/query_range endpoint.
// Each returned series' labels become the point's dimension Fields, so a PromQL
// query that keeps a label (e.g. `rate(http_requests_total[5m])` by `instance`)
// maps naturally onto by/partition detectors.
type PromSource struct {
	BaseURL string // e.g. http://localhost:9090
	Query   string // PromQL
	HTTP    *http.Client
}

// NewPromSource builds a Prometheus source with the default HTTP client.
func NewPromSource(baseURL, query string) *PromSource {
	return &PromSource{BaseURL: baseURL, Query: query, HTTP: http.DefaultClient}
}

// Fetch runs the range query and returns the flattened points.
func (p *PromSource) Fetch(ctx context.Context, start, end time.Time, step time.Duration) ([]core.DataPoint, error) {
	q := url.Values{}
	q.Set("query", p.Query)
	q.Set("start", strconv.FormatInt(start.Unix(), 10))
	q.Set("end", strconv.FormatInt(end.Unix(), 10))
	q.Set("step", strconv.FormatFloat(step.Seconds(), 'f', -1, 64))

	endpoint := p.BaseURL + "/api/v1/query_range?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prometheus: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("prometheus HTTP %d: %s", resp.StatusCode, string(body))
	}
	return parsePromRange(body)
}

func parsePromRange(body []byte) ([]core.DataPoint, error) {
	var r struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string   `json:"metric"`
				Values [][]json.RawMessage `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("prometheus: decode: %w", err)
	}
	if r.Status != "success" {
		return nil, fmt.Errorf("prometheus: %s", r.Error)
	}
	var out []core.DataPoint
	for _, series := range r.Data.Result {
		fields := series.Metric // label set → dimensions
		for _, pair := range series.Values {
			if len(pair) != 2 {
				continue
			}
			var ts float64
			if err := json.Unmarshal(pair[0], &ts); err != nil {
				continue
			}
			var valStr string
			if err := json.Unmarshal(pair[1], &valStr); err != nil {
				continue
			}
			val, err := strconv.ParseFloat(valStr, 64)
			if err != nil || math.IsNaN(val) || math.IsInf(val, 0) {
				continue // Prometheus emits "NaN"/"+Inf" for gaps
			}
			out = append(out, core.DataPoint{
				Time:   time.UnixMilli(int64(ts * 1000)).UTC(),
				Value:  val,
				Fields: fields,
			})
		}
	}
	return out, nil
}
