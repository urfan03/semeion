package hst

import (
	"encoding/json"
	"fmt"
)

type nodeState struct {
	Ref float64 `json:"r"`
	Cur float64 `json:"c"`
}

type forestState struct {
	Version int         `json:"version"`
	Options Options     `json:"options"`
	Dims    int         `json:"dims"`
	Count   int         `json:"count"`
	Warm    bool        `json:"warm"`
	Nodes   []nodeState `json:"nodes"`
}

func collect(n *node, out *[]nodeState) {
	if n == nil {
		return
	}
	*out = append(*out, nodeState{Ref: n.ref, Cur: n.cur})
	collect(n.left, out)
	collect(n.right, out)
}

func restore(n *node, in []nodeState, at *int) bool {
	if n == nil {
		return true
	}
	if *at >= len(in) {
		return false
	}
	n.ref, n.cur = in[*at].Ref, in[*at].Cur
	*at++
	return restore(n.left, in, at) && restore(n.right, in, at)
}

func (f *Forest) Snapshot() ([]byte, error) {
	st := forestState{Version: 1, Options: f.opt, Dims: f.dims, Count: f.count, Warm: f.warm}
	for _, t := range f.trees {
		collect(t, &st.Nodes)
	}
	return json.Marshal(st)
}

func Restore(b []byte) (*Forest, error) {
	var st forestState
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, err
	}
	if st.Version != 1 {
		return nil, fmt.Errorf("unsupported half-space-trees snapshot version %d", st.Version)
	}
	f := New(st.Dims, st.Options)
	f.count, f.warm = st.Count, st.Warm
	at := 0
	for _, t := range f.trees {
		if !restore(t, st.Nodes, &at) {
			return nil, fmt.Errorf("snapshot holds %d node counters, the rebuilt forest needs more", len(st.Nodes))
		}
	}
	if at != len(st.Nodes) {
		return nil, fmt.Errorf("snapshot holds %d node counters, the rebuilt forest consumed %d", len(st.Nodes), at)
	}
	return f, nil
}

func SeriesMulti(rows [][]float64, opt Options) []float64 {
	if len(rows) == 0 {
		return nil
	}
	dims := 0
	for _, r := range rows {
		if len(r) > dims {
			dims = len(r)
		}
	}
	if dims == 0 {
		return make([]float64, len(rows))
	}
	f := New(dims, opt)
	sc := NewScaler(dims)
	out := make([]float64, len(rows))
	for i, r := range rows {
		out[i] = f.Update(sc.Transform(r))
	}
	return out
}
