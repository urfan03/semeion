package pipeline

import (
	"math"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/urfan03/semeion/guard"
	"github.com/urfan03/semeion/shape"
)

func series(n int, seed uint64, seasonal bool) []float64 {
	rng := rand.New(rand.NewPCG(seed, seed^0x91))
	out := make([]float64, n)
	for i := range out {
		v := 100 + rng.NormFloat64()
		if seasonal {
			v += 20 * math.Sin(2*math.Pi*float64(i)/144)
		}
		out[i] = v
	}
	return out
}

func TestNewValidatesOptions(t *testing.T) {
	if _, err := New(Options{Sensitivity: "loud"}); err == nil {
		t.Fatal("an unknown sensitivity must be rejected")
	}
	if _, err := New(Options{History: 10}); err == nil {
		t.Fatal("a history too short to calibrate must be rejected")
	}
	if _, err := New(Options{History: 1_000_000}); err == nil {
		t.Fatal("an unbounded history must be rejected")
	}
	if _, err := New(Options{History: 1000, Calibration: 2000}); err == nil {
		t.Fatal("a calibration longer than the history must be rejected")
	}
	d, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Options().Sensitivity != Balanced || d.Options().History != defaultHistory {
		t.Fatalf("defaults wrong: %+v", d.Options())
	}
	if d.Options().Refresh != defaultRefresh {
		t.Fatalf("refresh default wrong: %d", d.Options().Refresh)
	}
}

func TestPushIsBoundedInMemoryAndTime(t *testing.T) {
	d, err := New(Options{History: 1500, Calibration: 500, Sensitivity: Sensitive})
	if err != nil {
		t.Fatal(err)
	}
	values := series(4000, 3, false)
	at := 3000
	for i := at; i < at+40; i++ {
		values[i] += 40
	}

	start := time.Now()
	var fired []Alarm
	for _, v := range values {
		fired = append(fired, d.Push(v)...)
		if len(d.hist) > 1500 {
			t.Fatalf("history must stay bounded, grew to %d", len(d.hist))
		}
	}
	elapsed := time.Since(start)
	if elapsed > 30*time.Second {
		t.Fatalf("4000 pushes took %v — the streaming path must not rescore on every point", elapsed)
	}
	if d.Seen() != len(values) {
		t.Fatalf("Seen must count every push, got %d", d.Seen())
	}
	if len(fired) == 0 {
		t.Fatal("the injected shift must raise at least one alarm")
	}
	near := 0
	for _, a := range fired {
		if a.Start >= at-60 && a.Start <= at+160 {
			near++
		}
	}
	if near == 0 {
		t.Fatalf("no alarm landed near the injected shift at %d: %+v", at, fired)
	}
	t.Logf("4000 pushes in %v, %d alarms", elapsed.Round(time.Millisecond), len(fired))
}

func TestPushReportsAbsoluteIndicesOnce(t *testing.T) {
	d, _ := New(Options{History: 800, Calibration: 300, Sensitivity: Sensitive})
	values := series(3000, 5, false)
	for i := 1500; i < 1540; i++ {
		values[i] += 45
	}
	seen := map[int]bool{}
	for _, v := range values {
		for _, a := range d.Push(v) {
			if seen[a.Start] {
				t.Fatalf("alarm at %d reported twice", a.Start)
			}
			seen[a.Start] = true
			if a.Start < 0 || a.Start >= len(values) {
				t.Fatalf("index out of range: %+v", a)
			}
			if a.End < a.Start {
				t.Fatalf("end before start: %+v", a)
			}
		}
	}
	if len(seen) == 0 {
		t.Fatal("nothing fired at all")
	}
}

func TestPushStaysQuietBeforeCalibration(t *testing.T) {
	d, _ := New(Options{History: 1000, Calibration: 400})
	for _, v := range series(400, 5, false) {
		if a := d.Push(v); len(a) != 0 {
			t.Fatalf("nothing may fire before calibration completes, fired at %d", d.Seen())
		}
	}
	if d.Ready() {
		t.Fatal("the detector must not report ready yet")
	}
}

func TestScanFindsTheAnomaly(t *testing.T) {
	d, _ := New(Options{History: 4000, Calibration: 800, Sensitivity: Sensitive})
	values := series(3000, 7, false)
	at := 2200
	for i := at; i < at+30; i++ {
		values[i] += 50
	}
	alarms := d.Scan(values)
	if len(alarms) == 0 {
		t.Fatal("Scan found nothing")
	}
	hit := false
	for _, a := range alarms {
		if a.Start < at-40 || a.Start > at+70 {
			continue
		}
		hit = true
		if a.Effect < 3 {
			t.Fatalf("the reported effect size is too small: %+v", a)
		}
		if a.Shape == shape.Unknown {
			t.Fatalf("a large sustained shift must classify: %+v", a)
		}
		if a.Reason == "" {
			t.Fatal("every alarm must carry a reason")
		}
		if a.P <= 0 || a.P > 1 {
			t.Fatalf("p-value out of range: %+v", a)
		}
		if a.Level <= a.Baseline {
			t.Fatalf("an upward shift must report a level above its baseline: %+v", a)
		}
	}
	if !hit {
		t.Fatalf("no alarm near the injected anomaly at %d: %+v", at, alarms)
	}
}

func TestSensitivityOrdersAlarmVolume(t *testing.T) {
	values := series(3000, 11, false)
	for _, at := range []int{1200, 1800, 2400} {
		for i := at; i < at+20; i++ {
			values[i] += 35
		}
	}
	counts := map[Sensitivity]int{}
	for _, s := range []Sensitivity{Sensitive, Balanced, Precise, Paranoid} {
		d, err := New(Options{History: 4000, Calibration: 800, Sensitivity: s})
		if err != nil {
			t.Fatal(err)
		}
		counts[s] = len(d.Scan(values))
	}
	if counts[Sensitive] < counts[Paranoid] {
		t.Fatalf("sensitive must not be quieter than paranoid: %v", counts)
	}
	if counts[Sensitive] == 0 {
		t.Fatalf("the sensitive setting must fire on three injected shifts: %v", counts)
	}
}

func TestBudgetCapsAlarmVolume(t *testing.T) {
	values := series(4000, 13, false)
	for i := 500; i < 4000; i += 300 {
		values[i] += 30
	}
	d, err := New(Options{
		History: 4000, Calibration: 800, Sensitivity: Sensitive,
		Budget: guard.Budget{Alarms: 1, Per: 1000},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(d.Scan(values)); got > 4 {
		t.Fatalf("a budget of 1 per 1000 over 4000 points must cap alarms, got %d", got)
	}
}

func TestMinDurationDropsMomentarySpikes(t *testing.T) {
	values := series(3000, 17, false)
	for i := 600; i < 3000; i += 200 {
		values[i] += 40
	}
	at := 2000
	for i := at; i < at+40; i++ {
		values[i] += 40
	}

	loose, _ := New(Options{History: 4000, Calibration: 800, Sensitivity: Sensitive})
	strict, _ := New(Options{History: 4000, Calibration: 800, Sensitivity: Sensitive, MinDuration: 8})

	long, short := loose.Scan(values), strict.Scan(values)
	if len(short) >= len(long) {
		t.Fatalf("a minimum duration must drop the momentary spikes: %d vs %d", len(short), len(long))
	}
	for _, a := range short {
		if a.Duration < 8 {
			t.Fatalf("a surviving alarm must meet the minimum duration: %+v", a)
		}
	}
}

func TestDeseasonalOptionDetectsThePeriod(t *testing.T) {
	values := series(3000, 19, true)
	d, _ := New(Options{History: 4000, Calibration: 800, Sensitivity: Sensitive, Deseasonal: true})
	d.Scan(values)
	if d.Period() == 0 {
		t.Fatal("a strongly seasonal series must be detected as such")
	}
	if math.Abs(float64(d.Period()-144)) > 20 {
		t.Fatalf("the detected period should be near 144, got %d", d.Period())
	}
}

func TestScanRejectsUnusableInput(t *testing.T) {
	d, _ := New(Options{History: 1000, Calibration: 400})
	if got := d.Scan(series(100, 23, false)); got != nil {
		t.Fatalf("too little data must give no alarms, got %d", len(got))
	}
	if d.Ready() {
		t.Fatal("and the detector must not claim to be ready")
	}
}
