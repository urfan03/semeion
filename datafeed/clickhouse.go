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

// ClickHouseSource runs SQL against ClickHouse's HTTP interface and turns the
// rows into points. The query must select a time column and a value column;
// any other column becomes a dimension (string) or a named metric (number).
//
// Use {{start}} / {{end}} placeholders for the window, e.g.
//
//	SELECT toStartOfMinute(ts) AS time, avg(latency) AS value, host
//	FROM metrics WHERE ts BETWEEN {{start}} AND {{end}} GROUP BY time, host ORDER BY time
type ClickHouseSource struct {
	BaseURL  string // e.g. http://localhost:8123
	Query    string // SQL with {{start}}/{{end}}
	TimeCol  string // default "time"
	ValueCol string // default "value"
	Database string
	User     string
	Password string
	HTTP     *http.Client
}

// NewClickHouseSource builds a ClickHouse source with the default HTTP client.
func NewClickHouseSource(baseURL, query string) *ClickHouseSource {
	return &ClickHouseSource{BaseURL: baseURL, Query: query, TimeCol: "time", ValueCol: "value", HTTP: http.DefaultClient}
}

// Fetch runs the query for [start, end] and returns the rows as points. step is
// accepted for the Source interface but the SQL controls the bucketing.
func (c *ClickHouseSource) Fetch(ctx context.Context, start, end time.Time, _ time.Duration) ([]core.DataPoint, error) {
	sql := strings.NewReplacer(
		"{{start}}", "toDateTime("+strconv.FormatInt(start.Unix(), 10)+")",
		"{{end}}", "toDateTime("+strconv.FormatInt(end.Unix(), 10)+")",
	).Replace(c.Query)
	sql += " FORMAT JSON"

	endpoint := c.BaseURL + "/"
	if c.Database != "" {
		endpoint += "?database=" + c.Database
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader([]byte(sql)))
	if err != nil {
		return nil, err
	}
	if c.User != "" {
		req.SetBasicAuth(c.User, c.Password)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("clickhouse HTTP %d: %s", resp.StatusCode, string(raw))
	}
	return parseClickHouse(raw, c.timeCol(), c.valueCol())
}

func (c *ClickHouseSource) timeCol() string {
	if c.TimeCol == "" {
		return "time"
	}
	return c.TimeCol
}

func (c *ClickHouseSource) valueCol() string {
	if c.ValueCol == "" {
		return "value"
	}
	return c.ValueCol
}

func parseClickHouse(body []byte, timeCol, valueCol string) ([]core.DataPoint, error) {
	var r struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("clickhouse: decode: %w", err)
	}
	var out []core.DataPoint
	for _, row := range r.Data {
		tv, ok := row[timeCol]
		if !ok {
			continue
		}
		ts, err := parseCHTime(tv)
		if err != nil {
			continue
		}
		p := core.DataPoint{Time: ts}
		for k, v := range row {
			if k == timeCol {
				continue
			}
			if f, ok := toFloat(v); ok {
				if p.Values == nil {
					p.Values = make(map[string]float64)
				}
				p.Values[k] = f
				if k == valueCol {
					p.Value = f
				}
				continue
			}
			if s, ok := v.(string); ok && k != valueCol {
				if p.Fields == nil {
					p.Fields = make(map[string]string)
				}
				p.Fields[k] = s
			}
		}
		out = append(out, p)
	}
	return out, nil
}

// parseCHTime accepts ClickHouse's "2026-01-01 00:00:00", RFC3339, or an epoch.
func parseCHTime(v any) (time.Time, error) {
	switch t := v.(type) {
	case string:
		for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339, "2006-01-02"} {
			if ts, err := time.Parse(layout, t); err == nil {
				return ts.UTC(), nil
			}
		}
		if n, err := strconv.ParseInt(t, 10, 64); err == nil {
			return time.Unix(n, 0).UTC(), nil
		}
	case float64:
		return time.Unix(int64(t), 0).UTC(), nil
	}
	return time.Time{}, fmt.Errorf("unrecognised time %v", v)
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	}
	return 0, false
}
