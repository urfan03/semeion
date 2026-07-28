package evt

import (
	"math/rand/v2"
	"testing"
)

func TestSPOTSnapshotResumesStream(t *testing.T) {
	rng := rand.New(rand.NewPCG(41, 43))
	n := 5000
	vals := make([]float64, n)
	for i := range vals {
		vals[i] = rng.NormFloat64()
	}
	vals[4200] = 22

	opt := Options{Q: 1e-4, Level: 0.98}
	straight := NewSPOT(opt)
	if !straight.Calibrate(vals[:2000]) {
		t.Fatal("calibration failed")
	}
	want := make([]bool, n)
	for i := 2000; i < n; i++ {
		want[i] = straight.Step(vals[i])
	}

	split := NewSPOT(opt)
	if !split.Calibrate(vals[:2000]) {
		t.Fatal("calibration failed")
	}
	got := make([]bool, n)
	for i := 2000; i < 3500; i++ {
		got[i] = split.Step(vals[i])
	}
	blob, err := split.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := RestoreSPOT(blob)
	if err != nil {
		t.Fatal(err)
	}
	if !resumed.Ready() || resumed.Threshold() != split.Threshold() || resumed.Peaks() != split.Peaks() {
		t.Fatalf("restored SPOT differs: ready=%v zq=%v peaks=%d", resumed.Ready(), resumed.Threshold(), resumed.Peaks())
	}
	for i := 3500; i < n; i++ {
		got[i] = resumed.Step(vals[i])
	}
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("snapshot/restore changed the alarm stream at %d", i)
		}
	}
	if !got[4200] {
		t.Fatal("the spike must still alarm after a restore")
	}
}

func TestDSPOTSnapshotResumesStream(t *testing.T) {
	rng := rand.New(rand.NewPCG(51, 53))
	n := 5000
	vals := make([]float64, n)
	for i := range vals {
		vals[i] = 0.01*float64(i) + rng.NormFloat64()
	}
	vals[4300] += 30

	opt := Options{Q: 1e-4, Level: 0.98}
	straight := NewDSPOT(opt, 20)
	if !straight.Calibrate(vals[:2000]) {
		t.Fatal("calibration failed")
	}
	want := make([]bool, n)
	for i := 2000; i < n; i++ {
		want[i] = straight.Step(vals[i])
	}

	split := NewDSPOT(opt, 20)
	if !split.Calibrate(vals[:2000]) {
		t.Fatal("calibration failed")
	}
	got := make([]bool, n)
	for i := 2000; i < 3000; i++ {
		got[i] = split.Step(vals[i])
	}
	blob, err := split.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := RestoreDSPOT(blob)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Threshold() != split.Threshold() {
		t.Fatalf("restored DSPOT threshold differs: %v vs %v", resumed.Threshold(), split.Threshold())
	}
	for i := 3000; i < n; i++ {
		got[i] = resumed.Step(vals[i])
	}
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("snapshot/restore changed the alarm stream at %d", i)
		}
	}
	if !got[4300] {
		t.Fatal("the spike must still alarm after a restore")
	}
}

func TestRestoreRejectsBadSnapshots(t *testing.T) {
	if _, err := RestoreSPOT([]byte("not json")); err == nil {
		t.Fatal("malformed JSON must error")
	}
	if _, err := RestoreSPOT([]byte(`{"version":9}`)); err == nil {
		t.Fatal("an unknown version must error")
	}
	if _, err := RestoreDSPOT([]byte(`{"version":9}`)); err == nil {
		t.Fatal("an unknown DSPOT version must error")
	}
	if _, err := RestoreDSPOT([]byte(`{"version":1,"spot":"e30=","depth":4,"win":[1,2]}`)); err == nil {
		t.Fatal("a window that does not match the depth must error")
	}
}
