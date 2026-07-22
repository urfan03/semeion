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

// Detector is the seam between the built-in ensemble and an out-of-process
// implementation, mirroring model.Provider: the Go path is always available,
// and the Python one can only add capability.
type Detector interface {
	Detect(features []string, rows [][]float64, opt Options) ([]Result, error)
}

// GoDetector is the pure-Go, dependency-free ensemble.
type GoDetector struct{}

func (GoDetector) Detect(features []string, rows [][]float64, opt Options) ([]Result, error) {
	return Detect(features, rows, opt)
}

// HTTPDetector delegates to the Python model plane (`POST /outliers`), where
// pyod's larger algorithm zoo lives. Any transport, protocol or shape error
// falls back to the Go ensemble — enabling the plane can never break a run.
type HTTPDetector struct {
	BaseURL  string
	HTTP     *http.Client
	fallback GoDetector
}

// NewHTTPDetector builds a detector that talks to baseURL (e.g. http://127.0.0.1:8899).
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
	// A short or empty answer is not a usable scoring of this table.
	if len(out.Results) != len(rows) {
		return nil, fmt.Errorf("model plane returned %d results for %d rows", len(out.Results), len(rows))
	}
	return out.Results, nil
}
