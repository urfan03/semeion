package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"

	"github.com/urfan03/semeion/outlier"
)

type outlierRequest struct {
	// Rows are free-form objects: numeric fields become features, string fields
	// are kept as labels and echoed back ("which host is the odd one out").
	Rows []map[string]any `json:"rows"`
	// Features optionally restricts (and orders) the columns used.
	Features []string `json:"features"`
	K        int      `json:"k"`
	Top      int      `json:"top"`
	Raw      bool     `json:"raw"`
}

type outlierResult struct {
	outlier.Result
	Labels map[string]string `json:"labels,omitempty"`
}

// outliers returns the configured detector, defaulting to the built-in ensemble.
func (s *Server) outliers() outlier.Detector {
	if s.outlierDetector != nil {
		return s.outlierDetector
	}
	return outlier.GoDetector{}
}

// handleOutliers scores a table of rows against each other — the batch,
// population question, as opposed to the streaming, time-series one.
func (s *Server) handleOutliers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	body, err := readLimited(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	var req outlierRequest
	if err := json.Unmarshal(body, &req); err != nil {
		httpError(w, http.StatusBadRequest, "decode: "+err.Error())
		return
	}

	features, rows, labels, err := tabulate(req.Rows, req.Features)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := s.outliers().Detect(features, rows, outlier.Options{K: req.K, Raw: req.Raw})
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Top > 0 {
		res = outlier.Top(res, req.Top)
	}
	out := make([]outlierResult, len(res))
	for i, r := range res {
		out[i] = outlierResult{Result: r, Labels: labels[r.Index]}
	}
	writeJSON(w, map[string]any{"features": features, "rows": len(rows), "results": out})
}

// tabulate turns JSON objects into a numeric matrix plus per-row labels. Every
// row must carry every feature: silently imputing a missing value would invent
// data, and an outlier score built on invented data is worse than no answer.
func tabulate(rows []map[string]any, want []string) ([]string, [][]float64, []map[string]string, error) {
	if len(rows) == 0 {
		return nil, nil, nil, fmt.Errorf("no rows")
	}
	features := want
	if len(features) == 0 {
		seen := map[string]bool{}
		for _, row := range rows {
			for k, v := range row {
				if _, ok := v.(float64); ok {
					seen[k] = true
				}
			}
		}
		for k := range seen {
			features = append(features, k)
		}
		sort.Strings(features)
	}
	if len(features) == 0 {
		return nil, nil, nil, fmt.Errorf("no numeric fields found")
	}

	matrix := make([][]float64, len(rows))
	labels := make([]map[string]string, len(rows))
	for i, row := range rows {
		vals := make([]float64, len(features))
		for f, name := range features {
			v, ok := row[name]
			if !ok {
				return nil, nil, nil, fmt.Errorf("row %d is missing feature %q", i, name)
			}
			num, ok := v.(float64)
			if !ok {
				return nil, nil, nil, fmt.Errorf("row %d: feature %q is not numeric", i, name)
			}
			vals[f] = num
		}
		matrix[i] = vals

		for k, v := range row {
			if s, ok := v.(string); ok {
				if labels[i] == nil {
					labels[i] = map[string]string{}
				}
				labels[i][k] = s
			}
		}
	}
	return features, matrix, labels, nil
}
