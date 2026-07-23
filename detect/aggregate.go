package detect

import (
	"strconv"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
	"github.com/urfan03/semeion/stats"
)

func Aggregate(fn jobspec.Function, field string, pts []core.DataPoint) (float64, bool) {
	if fn == jobspec.FuncCount {
		return float64(len(pts)), true
	}

	if fn == jobspec.FuncDistinctCount {
		seen := make(map[string]struct{}, len(pts))
		for _, p := range pts {
			seen[distinctKey(p, field)] = struct{}{}
		}
		return float64(len(seen)), true
	}
	if len(pts) == 0 {
		return 0, false
	}
	vals := make([]float64, len(pts))
	for i, p := range pts {
		vals[i] = valueOf(p, field)
	}
	switch fn {
	case jobspec.FuncNonZeroCount:
		n := 0
		for _, v := range vals {
			if v != 0 {
				n++
			}
		}
		return float64(n), true
	case jobspec.FuncVarp:
		_, std := stats.MeanStd(vals)
		return std * std, true
	case jobspec.FuncSum, jobspec.FuncNonNullSum:

		var s float64
		for _, v := range vals {
			s += v
		}
		return s, true
	case jobspec.FuncMean, jobspec.FuncMetric:

		var s float64
		for _, v := range vals {
			s += v
		}
		return s / float64(len(vals)), true
	case jobspec.FuncMin:
		m := vals[0]
		for _, v := range vals[1:] {
			if v < m {
				m = v
			}
		}
		return m, true
	case jobspec.FuncMax:
		m := vals[0]
		for _, v := range vals[1:] {
			if v > m {
				m = v
			}
		}
		return m, true
	case jobspec.FuncMedian:
		return stats.Median(vals), true
	default:
		return 0, false
	}
}

func distinctKey(p core.DataPoint, field string) string {
	if field != "" {
		if p.Fields != nil {
			if s, ok := p.Fields[field]; ok {
				return s
			}
		}
		if p.Values != nil {
			if v, ok := p.Values[field]; ok {
				return strconv.FormatFloat(v, 'g', -1, 64)
			}
		}
	}
	return strconv.FormatFloat(p.Value, 'g', -1, 64)
}

func valueOf(p core.DataPoint, field string) float64 {
	if field != "" && p.Values != nil {
		if v, ok := p.Values[field]; ok {
			return v
		}
	}
	return p.Value
}
