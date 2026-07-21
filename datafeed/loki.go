package datafeed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/urfan03/semeion/core"
)

// LokiSource reads log lines from Grafana Loki (`/loki/api/v1/query_range`) so
// the categorization detector can run over Loki-stored logs — the same role the
// Elasticsearch source plays, for the Loki stack.
type LokiSource struct {
	BaseURL string // e.g. http://localhost:3100
	Query   string // LogQL, e.g. {app="checkout"}
	Limit   int    // max lines per request (default 5000)
	HTTP    *http.Client
	OrgID   string // optional X-Scope-OrgID (multi-tenant)
}

// NewLokiSource builds a Loki source with the default HTTP client.
func NewLokiSource(baseURL, query string) *LokiSource {
	return &LokiSource{BaseURL: baseURL, Query: query, Limit: 5000, HTTP: http.DefaultClient}
}

// FetchLogs returns the log lines in [start, end]; stream labels become the
// line's dimension Fields (service, level, …).
func (l *LokiSource) FetchLogs(ctx context.Context, start, end time.Time) ([]core.LogLine, error) {
	limit := l.Limit
	if limit <= 0 {
		limit = 5000
	}
	q := url.Values{}
	q.Set("query", l.Query)
	q.Set("start", strconv.FormatInt(start.UnixNano(), 10))
	q.Set("end", strconv.FormatInt(end.UnixNano(), 10))
	q.Set("limit", strconv.Itoa(limit))
	q.Set("direction", "forward")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.BaseURL+"/loki/api/v1/query_range?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if l.OrgID != "" {
		req.Header.Set("X-Scope-OrgID", l.OrgID)
	}
	resp, err := l.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("loki: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("loki HTTP %d: %s", resp.StatusCode, string(body))
	}
	return parseLokiStreams(body)
}

func parseLokiStreams(body []byte) ([]core.LogLine, error) {
	var r struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Stream map[string]string `json:"stream"`
				Values [][]string        `json:"values"` // [ [ "<ns>", "<line>" ], … ]
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("loki: decode: %w", err)
	}
	if r.Status != "" && r.Status != "success" {
		return nil, fmt.Errorf("loki: status %s", r.Status)
	}
	var out []core.LogLine
	for _, s := range r.Data.Result {
		for _, v := range s.Values {
			if len(v) != 2 {
				continue
			}
			ns, err := strconv.ParseInt(v[0], 10, 64)
			if err != nil {
				continue
			}
			out = append(out, core.LogLine{
				Time:    time.Unix(0, ns).UTC(),
				Message: v[1],
				Fields:  s.Stream,
			})
		}
	}
	return out, nil
}
