package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type HTTPProvider struct {
	BaseURL  string
	HTTP     *http.Client
	fallback GoProvider
}

func NewHTTPProvider(baseURL string) *HTTPProvider {
	return &HTTPProvider{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (h *HTTPProvider) call(path string, req, resp any) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	r, err := h.HTTP.Post(h.BaseURL+path, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode >= 400 {
		b, _ := io.ReadAll(r.Body)
		return fmt.Errorf("model service HTTP %d: %s", r.StatusCode, string(b))
	}
	return json.NewDecoder(r.Body).Decode(resp)
}

func (h *HTTPProvider) DetectSeasonality(series []float64) []int {
	var out struct {
		Periods []int `json:"periods"`
	}
	if err := h.call("/detect_seasonality", map[string]any{"series": series}, &out); err != nil {
		return h.fallback.DetectSeasonality(series)
	}
	return out.Periods
}

func (h *HTTPProvider) Decompose(series []float64, period int) Decomposition {
	var out struct {
		Trend    []float64 `json:"trend"`
		Seasonal []float64 `json:"seasonal"`
		Resid    []float64 `json:"resid"`
	}
	if err := h.call("/decompose", map[string]any{"series": series, "period": period}, &out); err != nil {
		return h.fallback.Decompose(series, period)
	}
	return Decomposition{Trend: out.Trend, Seasonal: out.Seasonal, Resid: out.Resid}
}

func (h *HTTPProvider) Forecast(series []float64, horizon int) []float64 {
	var out struct {
		Forecast []float64 `json:"forecast"`
	}
	if err := h.call("/forecast", map[string]any{"series": series, "horizon": horizon}, &out); err != nil {
		return h.fallback.Forecast(series, horizon)
	}
	return out.Forecast
}

func (h *HTTPProvider) ForecastBands(series []float64, horizon int) []Band {
	var out struct {
		Bands []Band `json:"bands"`
	}
	if err := h.call("/forecast_bands", map[string]any{"series": series, "horizon": horizon}, &out); err != nil || len(out.Bands) != horizon {
		return h.fallback.ForecastBands(series, horizon)
	}
	return out.Bands
}

func (h *HTTPProvider) ChangePoints(series []float64) []int {
	var out struct {
		ChangePoints []int `json:"change_points"`
	}
	if err := h.call("/change_points", map[string]any{"series": series}, &out); err != nil {
		return h.fallback.ChangePoints(series)
	}
	return out.ChangePoints
}

func (h *HTTPProvider) FitDistribution(samples []float64) Distribution {
	var out Distribution
	if err := h.call("/fit_distribution", map[string]any{"samples": samples}, &out); err != nil {
		return h.fallback.FitDistribution(samples)
	}
	return out
}
