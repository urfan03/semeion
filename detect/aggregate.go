package detect

import (
	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
	"github.com/urfan03/semeion/stats"
)

// Aggregate reduces one bucket's points to a single value per the detector
// function. For count the points are counted (an empty bucket is a legitimate
// count of 0); every other function reduces the points' Value. The bool is
// false when the function can't produce a value (e.g. a metric over 0 points).
func Aggregate(fn jobspec.Function, pts []core.DataPoint) (float64, bool) {
	if fn == jobspec.FuncCount {
		return float64(len(pts)), true
	}
	if len(pts) == 0 {
		return 0, false
	}
	vals := make([]float64, len(pts))
	for i, p := range pts {
		vals[i] = p.Value
	}
	switch fn {
	case jobspec.FuncSum:
		var s float64
		for _, v := range vals {
			s += v
		}
		return s, true
	case jobspec.FuncMean:
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
