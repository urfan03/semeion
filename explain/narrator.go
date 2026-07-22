package explain

import (
	"fmt"
	"strings"
)

// Narrator turns a Brief into prose. The default is deterministic (the Brief's
// own template narrative); a caller may plug in an LLM-backed one. Critically,
// a Narrator only *rewrites* — it is handed the finished facts and may not add
// findings, so the explanation can never drift from what the engine established.
type Narrator interface {
	Narrate(b Brief) string
}

// TemplateNarrator returns the deterministic narrative unchanged — the zero-dep
// default, and the fallback whenever an LLM narrator is unavailable.
type TemplateNarrator struct{}

func (TemplateNarrator) Narrate(b Brief) string { return b.Narrative }

// Prompt renders the instruction + grounded facts to hand to an external LLM
// (e.g. a copilot). semeion ships no LLM client — this is the contract that lets
// one narrate without semeion depending on any model SDK. The rules in the
// preamble are what keep a summariser from becoming a (bad) detector.
func Prompt(b Brief) string {
	var s strings.Builder
	s.WriteString("You are explaining a production incident to an on-call engineer. ")
	s.WriteString("Rewrite the facts below into a short, calm paragraph. ")
	s.WriteString("Rules: use ONLY the facts given; do not invent metrics, causes, or services; ")
	s.WriteString("do not soften or overstate the confidence; if the cause is unknown, say so.\n\n")

	fmt.Fprintf(&s, "Headline: %s\n", b.Headline)
	fmt.Fprintf(&s, "Cause (%s): %s", b.Cause.Kind, b.Cause.Target)
	if b.Cause.Detail != "" {
		fmt.Fprintf(&s, " (%s)", b.Cause.Detail)
	}
	fmt.Fprintf(&s, "\nConfidence: %.0f%%\n", b.Confidence*100)

	if len(b.Evidence) > 0 {
		s.WriteString("\nEvidence:\n")
		for _, e := range b.Evidence {
			fmt.Fprintf(&s, "  - %s\n", e)
		}
	}
	if len(b.Actions) > 0 {
		s.WriteString("\nRecommended actions (already decided — present them, do not re-rank):\n")
		for _, a := range b.Actions {
			fmt.Fprintf(&s, "  %d. %s — %s\n", a.Priority, a.Title, a.Rationale)
		}
	}
	return s.String()
}
