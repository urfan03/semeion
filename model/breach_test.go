package model

import (
	"math"
	"testing"
)

func TestForecastBreachRisingCrossesUpperLimit(t *testing.T) {
	p := NewGoProvider()

	series := make([]float64, 50)
	for i := range series {
		series[i] = float64(i) * 2
	}
	bands := p.ForecastBands(series, 40)
	b := ForecastBreach(bands, 130, true)
	if !b.WillBreach {
		t.Fatalf("rising series should breach upper limit 130 within 40 steps: %+v", b)
	}
	if b.Step < 10 || b.Step > 25 {
		t.Fatalf("breach step ~16 expected, got %d", b.Step)
	}
	if b.At < 130 {
		t.Fatalf("breach point should be >= threshold, got %v", b.At)
	}
	if b.Probability < 0.5 {
		t.Fatalf("point-crossing breach probability should be >= 0.5, got %v", b.Probability)
	}
	if b.Side != "high" {
		t.Fatalf("side should be high, got %q", b.Side)
	}
}

func TestForecastBreachFlatDoesNotCross(t *testing.T) {
	p := NewGoProvider()
	series := make([]float64, 50)
	for i := range series {
		series[i] = 10 + math.Sin(float64(i))
	}
	b := ForecastBreach(p.ForecastBands(series, 20), 1000, true)
	if b.WillBreach {
		t.Fatalf("flat series should not breach a far upper limit: %+v", b)
	}

	if b.Probability > 0.5 {
		t.Fatalf("no-breach probability should be low, got %v", b.Probability)
	}
}

func TestForecastBreachDecliningCrossesLowerLimit(t *testing.T) {
	p := NewGoProvider()
	series := make([]float64, 50)
	for i := range series {
		series[i] = 100 - float64(i)
	}
	b := ForecastBreach(p.ForecastBands(series, 40), 20, false)
	if !b.WillBreach {
		t.Fatalf("declining series should breach lower limit 20: %+v", b)
	}
	if b.Side != "low" {
		t.Fatalf("side should be low, got %q", b.Side)
	}
	if b.At > 20 {
		t.Fatalf("breach point should be <= threshold, got %v", b.At)
	}
}
