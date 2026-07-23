package explain

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/urfan03/semeion/correlate"
)

type Brief struct {
	IncidentID string    `json:"incident_id"`
	Headline   string    `json:"headline"`
	Narrative  string    `json:"narrative"`
	Cause      Cause     `json:"cause"`
	Evidence   []string  `json:"evidence"`
	Actions    []Action  `json:"actions"`
	Confidence float64   `json:"confidence"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
}

type Cause struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Detail string `json:"detail,omitempty"`
}

type Action struct {
	Title     string `json:"title"`
	Rationale string `json:"rationale"`
	Priority  int    `json:"priority"`
}

func Explain(inc correlate.Incident) Brief {
	b := Brief{
		IncidentID: inc.ID,
		Start:      inc.Start,
		End:        inc.End,
		Evidence:   evidence(inc),
	}
	if len(inc.RootCause) == 0 {
		b.Headline = "Incident with no ranked cause"
		b.Cause = Cause{Kind: "unknown", Target: "the incident's symptoms"}
		b.Narrative = "An incident was detected but no root-cause candidate could be ranked."
		return b
	}

	lead := inc.RootCause[0]
	b.Confidence = lead.Confidence
	b.Cause = causeOf(lead)
	b.Headline = headline(inc, b.Cause)
	b.Actions = actions(inc, lead)
	b.Narrative = narrate(inc, b.Cause, lead)
	return b
}

func causeOf(c correlate.Candidate) Cause {
	if c.Change != nil {
		return Cause{Kind: "change", Target: c.Change.Name, Detail: c.Change.Kind}
	}
	s := c.Symptom
	if svc := s.Entities["service"]; svc != "" {
		return Cause{Kind: "service", Target: svc, Detail: s.Detector}
	}
	if s.Kind == "new" || s.Kind == "rare" {
		return Cause{Kind: "log", Target: firstNonEmpty(s.Template, s.Job), Detail: s.Kind}
	}
	return Cause{Kind: "metric", Target: firstNonEmpty(s.Series, s.Job), Detail: s.Detector}
}

func headline(inc correlate.Incident, c Cause) string {
	scope := fmt.Sprintf("%d symptom(s)", len(inc.Symptoms))
	if len(inc.Services) > 0 {
		scope = strings.Join(inc.Services, ", ")
	}
	switch c.Kind {
	case "change":
		return fmt.Sprintf("Likely caused by %s (%s) — affecting %s", c.Target, orText(c.Detail, "change"), scope)
	case "service":
		return fmt.Sprintf("Originating in %s — affecting %s", c.Target, scope)
	case "log":
		return fmt.Sprintf("New log pattern in %s — affecting %s", c.Target, scope)
	default:
		return fmt.Sprintf("Anomaly in %s — affecting %s", c.Target, scope)
	}
}

func actions(inc correlate.Incident, lead correlate.Candidate) []Action {
	var out []Action

	if lead.Change != nil && changePrecedes(inc, lead.Change.Time) {
		out = append(out, Action{
			Priority:  1,
			Title:     "Roll back " + lead.Change.Name,
			Rationale: "it is a deliberate change that preceded the incident's onset, and is the fastest thing to reverse",
		})
	}

	if svc := lead.Symptom.Entities["service"]; svc != "" && upstreamReason(lead.Reasons) != "" {
		out = append(out, Action{
			Priority:  len(out) + 1,
			Title:     "Investigate " + svc,
			Rationale: upstreamReason(lead.Reasons) + " — fixing it should clear the downstream symptoms",
		})
	}

	for _, c := range inc.RootCause {
		if (c.Symptom.Kind == "new" || c.Symptom.Kind == "rare") && c.Symptom.Template != "" {
			out = append(out, Action{
				Priority:  len(out) + 1,
				Title:     "Inspect the new log pattern",
				Rationale: fmt.Sprintf("%q first appeared in this incident", c.Symptom.Template),
			})
			break
		}
	}

	if len(out) == 0 {

		t := firstNonEmpty(lead.Symptom.Entities["service"], lead.Symptom.Series, lead.Symptom.Job)
		out = append(out, Action{
			Priority:  1,
			Title:     "Investigate " + t,
			Rationale: "it is the highest-ranked symptom of the incident (" + strings.Join(lead.Reasons, "; ") + ")",
		})
	}
	return out
}

func narrate(inc correlate.Incident, c Cause, lead correlate.Candidate) string {
	var b strings.Builder
	dur := inc.End.Sub(inc.Start).Round(time.Second)

	fmt.Fprintf(&b, "Between %s and %s, %d symptom(s) across %s were correlated into one incident",
		inc.Start.UTC().Format("15:04:05"), inc.End.UTC().Format("15:04:05"),
		len(inc.Symptoms), plural(len(inc.Jobs), "signal"))
	if dur > 0 {
		fmt.Fprintf(&b, " spanning %s", dur)
	}
	b.WriteString(". ")

	switch c.Kind {
	case "change":
		fmt.Fprintf(&b, "The most likely origin is the %s %q, which %s. ",
			orText(c.Detail, "change"), c.Target, strings.Join(lead.Reasons, " and "))
	case "service":
		fmt.Fprintf(&b, "The most likely origin is %s (%s), which %s. ",
			c.Target, c.Detail, strings.Join(lead.Reasons, " and "))
	default:
		fmt.Fprintf(&b, "The leading candidate is %s, which %s. ", c.Target, strings.Join(lead.Reasons, " and "))
	}

	if len(inc.Services) > 1 {
		fmt.Fprintf(&b, "Affected services: %s. ", strings.Join(inc.Services, ", "))
	}
	fmt.Fprintf(&b, "Confidence in this ranking is %.0f%% relative to the other candidates.", lead.Confidence*100)
	return b.String()
}

func evidence(inc correlate.Incident) []string {
	var ev []string
	if len(inc.Changes) > 0 {
		names := make([]string, 0, len(inc.Changes))
		for _, c := range inc.Changes {
			names = append(names, c.Name)
		}
		ev = append(ev, "changes in window: "+strings.Join(names, ", "))
	}
	for i, c := range inc.RootCause {
		if i >= 3 {
			break
		}
		what := c.Symptom.Job
		if c.Change != nil {
			what = "change " + c.Change.Name
		} else if svc := c.Symptom.Entities["service"]; svc != "" {
			what = svc
		}
		ev = append(ev, fmt.Sprintf("#%d %s (%.0f%%): %s", i+1, what, c.Confidence*100, strings.Join(c.Reasons, "; ")))
	}

	if len(inc.Entities) > 0 {
		keys := make([]string, 0, len(inc.Entities))
		for k := range inc.Entities {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		ev = append(ev, "entities: "+strings.Join(keys, ", "))
	}
	return ev
}

func changePrecedes(inc correlate.Incident, t time.Time) bool {
	first := inc.End
	for _, s := range inc.Symptoms {
		if s.Time.Before(first) {
			first = s.Time
		}
	}
	return !t.After(first)
}

func upstreamReason(reasons []string) string {
	for _, r := range reasons {
		if strings.Contains(r, "upstream of") {
			return r
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return "the incident"
}

func orText(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
