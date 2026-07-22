package detect

import (
	"testing"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

func pt(v float64, dim string) core.DataPoint {
	return core.DataPoint{Value: v, Values: map[string]float64{"m": v}, Fields: map[string]string{"host": dim}}
}

func TestAggregateNewFunctions(t *testing.T) {
	pts := []core.DataPoint{pt(0, "a"), pt(5, "a"), pt(0, "b"), pt(10, "c")}

	// distinct_count over the "host" dimension: a,b,c → 3.
	if v, ok := Aggregate(jobspec.FuncDistinctCount, "host", pts); !ok || v != 3 {
		t.Errorf("distinct_count(host)=%v ok=%v want 3", v, ok)
	}
	// non_zero_count over "m": 5,10 are non-zero → 2.
	if v, ok := Aggregate(jobspec.FuncNonZeroCount, "m", pts); !ok || v != 2 {
		t.Errorf("non_zero_count(m)=%v want 2", v)
	}
	// varp over "m": population variance of {0,5,0,10}.
	if v, ok := Aggregate(jobspec.FuncVarp, "m", pts); !ok || v <= 0 {
		t.Errorf("varp(m)=%v ok=%v want >0", v, ok)
	}
	// distinct_count on an empty bucket is a legitimate 0.
	if v, ok := Aggregate(jobspec.FuncDistinctCount, "host", nil); !ok || v != 0 {
		t.Errorf("distinct_count(empty)=%v ok=%v want 0", v, ok)
	}
}
