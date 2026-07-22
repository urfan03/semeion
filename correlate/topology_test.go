package correlate

import (
	"strings"
	"testing"
	"time"
)

// fakeTopo is a hand-written dependency graph: gateway → checkout → payments-db.
type fakeTopo struct{ edges map[string]string }

func newTopo() fakeTopo {
	return fakeTopo{edges: map[string]string{"gateway": "checkout", "checkout": "payments-db"}}
}

func (f fakeTopo) Related(a, b string) bool {
	return f.edges[a] == b || f.edges[b] == a
}

func (f fakeTopo) reaches(from, to string, depth int) bool {
	for i := 0; i < depth && from != ""; i++ {
		next := f.edges[from]
		if next == to {
			return true
		}
		from = next
	}
	return false
}

func (f fakeTopo) UpstreamOf(service string, others []string, depth int) int {
	n := 0
	for _, o := range others {
		if f.reaches(o, service, depth) {
			n++
		}
	}
	return n
}

func svcSym(job, service string, offset time.Duration, score float64) Symptom {
	return Symptom{Job: job, Time: t0.Add(offset), Detector: "mean(latency)", Score: score,
		Kind: "metric", Entities: map[string]string{"service": service}}
}

// The database is the cause even though it was noticed last and scores lowest:
// the other two services depend on it, and nothing depends on them.
func TestTopologyBeatsEarlinessWhenTheEvidenceIsStrong(t *testing.T) {
	symptoms := []Symptom{
		svcSym("gateway-latency", "gateway", 0, 95),
		svcSym("checkout-latency", "checkout", 30*time.Second, 90),
		svcSym("db-latency", "payments-db", time.Minute, 70),
	}
	opt := Options{Window: 10 * time.Minute, Topology: newTopo()}

	inc := Correlate(symptoms, nil, opt)
	if len(inc) != 1 {
		t.Fatalf("expected one incident, got %d", len(inc))
	}
	top := inc[0].RootCause[0]
	if top.Symptom.Entities["service"] != "payments-db" {
		t.Fatalf("the upstream service should lead, got %+v (reasons %v)", top.Symptom, top.Reasons)
	}
	if !strings.Contains(strings.Join(top.Reasons, "; "), "upstream of 2") {
		t.Errorf("the reason must name the topological evidence: %v", top.Reasons)
	}

	// Same data without the graph: earliness wins and the answer is the leaf.
	if noTopo := Correlate(symptoms, nil, Options{Window: 10 * time.Minute}); noTopo[0].RootCause[0].Symptom.Entities["service"] != "gateway" {
		t.Fatalf("without topology the earliest symptom should lead, got %+v", noTopo[0].RootCause[0].Symptom)
	}
}

// A caller and its callee are one incident even when they are far apart in the
// window and share no entity — that is the shape of a cascade.
func TestTopologyLinksAcrossTheWindow(t *testing.T) {
	symptoms := []Symptom{
		svcSym("checkout-errors", "checkout", 0, 90),
		svcSym("db-latency", "payments-db", 9*time.Minute, 80),
	}
	// Beyond the co-occurrence window (5m), so only the graph can link them.
	if inc := Correlate(symptoms, nil, Options{Window: 10 * time.Minute}); len(inc) != 2 {
		t.Fatalf("without a graph these are two incidents, got %d", len(inc))
	}
	inc := Correlate(symptoms, nil, Options{Window: 10 * time.Minute, Topology: newTopo()})
	if len(inc) != 1 {
		t.Fatalf("the call relationship should link them, got %d incidents", len(inc))
	}
	if len(inc[0].Services) != 2 {
		t.Errorf("both services should be listed: %v", inc[0].Services)
	}
}

// Unrelated services still must not be merged just because a graph exists.
func TestTopologyDoesNotLinkUnrelatedServices(t *testing.T) {
	symptoms := []Symptom{
		svcSym("a", "gateway", 0, 90),
		svcSym("b", "reporting", 9*time.Minute, 80),
	}
	if inc := Correlate(symptoms, nil, Options{Window: 10 * time.Minute, Topology: newTopo()}); len(inc) != 2 {
		t.Fatalf("expected 2 incidents for unrelated services, got %d", len(inc))
	}
}

// A deploy on the upstream service should still win over the upstream symptom
// itself — the change is the thing a human can revert.
func TestChangeStillOutranksTopologyEvidence(t *testing.T) {
	symptoms := []Symptom{
		svcSym("checkout-errors", "checkout", time.Minute, 90),
		svcSym("db-latency", "payments-db", 2*time.Minute, 85),
	}
	changes := []Change{{Time: t0, Name: "payments-db config", Kind: "config",
		Labels: map[string]string{"service": "payments-db"}}}

	inc := Correlate(symptoms, changes, Options{Window: 10 * time.Minute, Topology: newTopo()})
	if len(inc) != 1 {
		t.Fatalf("expected one incident, got %d", len(inc))
	}
	if inc[0].RootCause[0].Change == nil {
		t.Fatalf("the change should lead, got %+v", inc[0].RootCause[0])
	}
}

func TestCustomEntityKey(t *testing.T) {
	symptoms := []Symptom{
		{Job: "a", Time: t0, Score: 90, Entities: map[string]string{"service.name": "gateway"}},
		{Job: "b", Time: t0.Add(9 * time.Minute), Score: 80, Entities: map[string]string{"service.name": "checkout"}},
	}
	inc := Correlate(symptoms, nil, Options{Window: 10 * time.Minute, Topology: newTopo()})
	if len(inc) != 1 {
		t.Fatalf("service.name should be recognised by default, got %d incidents", len(inc))
	}
}

// P1-L2 regression: a coarse influencer shared by every symptom (env=prod) must
// NOT collapse unrelated symptoms into one mega-incident.
func TestCoarseInfluencerDoesNotMergeEverything(t *testing.T) {
	var syms []Symptom
	// 8 unrelated hosts, each its own symptom, all tagged env=prod, spread out
	// beyond the co-occurrence window so only a shared entity could link them.
	for i := 0; i < 8; i++ {
		syms = append(syms, Symptom{
			Job: "cpu", Time: t0.Add(time.Duration(i) * 20 * time.Minute), Score: 80,
			Entities: map[string]string{
				"host": string(rune('a' + i)), // distinct identity
				"env":  "prod",                // coarse attribute shared by all
			},
		})
	}
	inc := Correlate(syms, nil, Options{Window: time.Hour})
	if len(inc) < 8 {
		t.Fatalf("coarse env=prod must not merge 8 unrelated host incidents, got %d incidents", len(inc))
	}
}
