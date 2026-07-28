package evt

import (
	"encoding/json"
	"fmt"
)

type spotState struct {
	Version int       `json:"version"`
	Options Options   `json:"options"`
	Init    float64   `json:"init"`
	ZQ      float64   `json:"zq"`
	Peaks   []float64 `json:"peaks"`
	Fit     GPD       `json:"fit"`
	N       int       `json:"n"`
	NT      int       `json:"nt"`
	Since   int       `json:"since"`
	Ready   bool      `json:"ready"`
}

func (s *SPOT) Snapshot() ([]byte, error) {
	return json.Marshal(spotState{
		Version: 1, Options: s.opt, Init: s.init, ZQ: s.zq,
		Peaks: s.peaks, Fit: s.fit, N: s.n, NT: s.nt, Since: s.since, Ready: s.ready,
	})
}

func RestoreSPOT(b []byte) (*SPOT, error) {
	var st spotState
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, err
	}
	if st.Version != 1 {
		return nil, fmt.Errorf("unsupported SPOT snapshot version %d", st.Version)
	}
	return &SPOT{
		opt: st.Options.withDefaults(), init: st.Init, zq: st.ZQ,
		peaks: st.Peaks, fit: st.Fit, n: st.N, nt: st.NT, since: st.Since, ready: st.Ready,
	}, nil
}

type dspotState struct {
	Version int       `json:"version"`
	SPOT    []byte    `json:"spot"`
	Depth   int       `json:"depth"`
	Win     []float64 `json:"win"`
	Idx     int       `json:"idx"`
	Full    bool      `json:"full"`
	Sum     float64   `json:"sum"`
}

func (d *DSPOT) Snapshot() ([]byte, error) {
	inner, err := d.spot.Snapshot()
	if err != nil {
		return nil, err
	}
	return json.Marshal(dspotState{
		Version: 1, SPOT: inner, Depth: d.depth,
		Win: d.win, Idx: d.idx, Full: d.full, Sum: d.sum,
	})
}

func RestoreDSPOT(b []byte) (*DSPOT, error) {
	var st dspotState
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, err
	}
	if st.Version != 1 {
		return nil, fmt.Errorf("unsupported DSPOT snapshot version %d", st.Version)
	}
	inner, err := RestoreSPOT(st.SPOT)
	if err != nil {
		return nil, err
	}
	if st.Depth < 1 || len(st.Win) != st.Depth {
		return nil, fmt.Errorf("snapshot depth %d does not match its %d-slot window", st.Depth, len(st.Win))
	}
	return &DSPOT{spot: inner, depth: st.Depth, win: st.Win, idx: st.Idx, full: st.Full, sum: st.Sum}, nil
}
