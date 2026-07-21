package logcat

// State is a serialisable snapshot of a Drain (its templates + parameters), so
// discovered categories survive restarts.
type State struct {
	MaxDepth int       `json:"max_depth"`
	SimTh    float64   `json:"sim_th"`
	MaxChild int       `json:"max_child"`
	NextID   int       `json:"next_id"`
	Clusters []Cluster `json:"clusters"`
}

// Export captures the current templates and parameters.
func (d *Drain) Export() State {
	cs := make([]Cluster, len(d.clusters))
	for i, c := range d.clusters {
		cs[i] = Cluster{ID: c.ID, Tokens: append([]string(nil), c.Tokens...), Count: c.Count}
	}
	return State{MaxDepth: d.maxDepth, SimTh: d.simTh, MaxChild: d.maxChild, NextID: d.nextID, Clusters: cs}
}

// LoadState rebuilds a Drain from a snapshot, re-placing each template in the
// parse tree so future messages match the restored clusters.
func LoadState(s State) *Drain {
	d := NewDrain()
	if s.MaxDepth > 0 {
		d.maxDepth = s.MaxDepth
	}
	if s.SimTh > 0 {
		d.simTh = s.SimTh
	}
	if s.MaxChild > 0 {
		d.maxChild = s.MaxChild
	}
	d.nextID = s.NextID
	for i := range s.Clusters {
		src := s.Clusters[i]
		c := &Cluster{ID: src.ID, Tokens: append([]string(nil), src.Tokens...), Count: src.Count}
		leaf := d.descend(c.Tokens, true)
		leaf.clusters = append(leaf.clusters, c)
		d.clusters = append(d.clusters, c)
	}
	return d
}
