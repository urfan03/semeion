package outlier

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Detector interface {
	Detect(features []string, rows [][]float64, opt Options) ([]Result, error)
}

type GoDetector struct{}

func (GoDetector) Detect(features []string, rows [][]float64, opt Options) ([]Result, error) {
	return Detect(features, rows, opt)
}

type HTTPDetector struct {
	BaseURL  string
	HTTP     *http.Client
	fallback GoDetector
}

func NewHTTPDetector(baseURL string) *HTTPDetector {
	return &HTTPDetector{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: 60 * time.Second},
	}
}

func (h *HTTPDetector) Detect(features []string, rows [][]float64, opt Options) ([]Result, error) {
	res, err := h.remote(features, rows, opt)
	if err != nil {
		return h.fallback.Detect(features, rows, opt)
	}
	return res, nil
}

func (h *HTTPDetector) remote(features []string, rows [][]float64, opt Options) ([]Result, error) {
	body, err := json.Marshal(map[string]any{
		"features": features, "rows": rows, "k": opt.K, "raw": opt.Raw,
	})
	if err != nil {
		return nil, err
	}
	resp, err := h.HTTP.Post(h.BaseURL+"/outliers", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("model plane HTTP %d: %s", resp.StatusCode, string(b))
	}
	var out struct {
		Results []Result `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}

	if len(out.Results) != len(rows) {
		return nil, fmt.Errorf("model plane returned %d results for %d rows", len(out.Results), len(rows))
	}
	return out.Results, nil
}
