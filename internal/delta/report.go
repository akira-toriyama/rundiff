package delta

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// schemaVersion is the JSON contract version (field "v"). Bump only on a
// breaking change to a field's type or meaning; adding a field is compatible.
const schemaVersion = 1

// Report is the outcome of a Diff: the machine-readable summary (line 1) plus the
// human/agent-facing body. It is pure data — Render turns it into bytes.
type Report struct {
	V             int
	Key           string
	Exit          int
	PrevExit      *int
	Transition    string
	Degraded      bool
	DegradeReason *string
	Added         *int
	Removed       *int
	Unchanged     *int
	Churn         *float64
	TotalPrev     *int
	TotalCur      *int
	BaselineAgeS  *int
	Normalized    bool
	Truncated     bool

	// Presentation, filled by body()/delta(); Render selects by mode.
	addedLines   []string
	removedLines []string
	jsonBody     string // bounded full output for the --json body
	textBody     string // default-mode body (header + delta or bounded full)
	fullBody     bool   // true ⇒ the body is bounded full output (baseline/degrade/--full), not a delta
}

func (r *Report) setReason(reason string) {
	r.DegradeReason = &reason
}

// body renders the bounded full current output — used for baseline, degrade, and
// --full. In --json mode it becomes the "body" string; otherwise a text block
// under a header.
func (r *Report) body(output []byte, opt Options) {
	r.fullBody = true
	lines, truncated := boundedFull(output, opt.MaxBodyLines)
	if truncated {
		r.Truncated = true
	}
	if opt.JSON {
		r.jsonBody = strings.Join(lines, "\n")
		return
	}
	var b strings.Builder
	b.WriteString(r.headerFull())
	for _, l := range lines {
		b.WriteByte('\n')
		b.WriteString(l)
	}
	r.textBody = b.String()
}

// delta renders a trusted line diff. In --json mode it fills the added/removed
// arrays; otherwise a text block of `-`/`+` lines under a header.
func (r *Report) delta(dc diffCounts, opt Options) {
	if opt.JSON {
		r.addedLines = dc.addedLines
		r.removedLines = dc.removedLines
		if r.addedLines == nil {
			r.addedLines = []string{}
		}
		if r.removedLines == nil {
			r.removedLines = []string{}
		}
		return
	}
	var b strings.Builder
	b.WriteString(r.headerDelta())
	for _, l := range dc.removedLines {
		b.WriteString("\n- ")
		b.WriteString(clip(l))
	}
	for _, l := range dc.addedLines {
		b.WriteString("\n+ ")
		b.WriteString(clip(l))
	}
	r.textBody = b.String()
}

const maxDisplayLine = 2048 // cap a single displayed line; matching uses the full line

func clip(s string) string {
	if len(s) <= maxDisplayLine {
		return s
	}
	return s[:maxDisplayLine] + "…"
}

// boundedFull returns a head+tail slice of the output within budget lines, with a
// tail bias (failures cluster at the end) and an omission marker.
func boundedFull(output []byte, budget int) ([]string, bool) {
	lines := splitLines(output)
	if len(lines) <= budget {
		return lines, false
	}
	head := 50
	if head > budget/2 {
		head = budget / 2
	}
	tail := budget - head
	omitted := len(lines) - head - tail
	out := make([]string, 0, budget+1)
	out = append(out, lines[:head]...)
	out = append(out, fmt.Sprintf("[... %d lines omitted ...]", omitted))
	out = append(out, lines[len(lines)-tail:]...)
	return out, true
}

func (r *Report) headerFull() string {
	label := "full output"
	switch {
	case r.Transition == string(TransitionBaseline):
		label = "baseline"
	case r.DegradeReason != nil:
		label = "full output (degraded: " + *r.DegradeReason + ")"
	}
	return fmt.Sprintf("── %s · %s%s ──", label, r.Transition, r.exitTag())
}

func (r *Report) headerDelta() string {
	counts := ""
	if r.Added != nil {
		counts = fmt.Sprintf("  +%d −%d ~%d", *r.Added, *r.Removed, *r.Unchanged)
	}
	churnTag := ""
	if r.Churn != nil {
		churnTag = fmt.Sprintf("  churn=%g", *r.Churn)
	}
	ageTag := ""
	if r.BaselineAgeS != nil {
		ageTag = fmt.Sprintf("  age=%ds", *r.BaselineAgeS)
	}
	return fmt.Sprintf("── delta · %s%s%s%s%s ──", r.Transition, r.exitTag(), counts, churnTag, ageTag)
}

func (r *Report) exitTag() string {
	if r.PrevExit != nil {
		return fmt.Sprintf("  exit=%d (prev %d)", r.Exit, *r.PrevExit)
	}
	return fmt.Sprintf("  exit=%d", r.Exit)
}

// --- serialization (the single JSON funnel) ---

type lineCore struct {
	V             int      `json:"v"`
	Key           string   `json:"key"`
	Exit          int      `json:"exit"`
	PrevExit      *int     `json:"prev_exit"`
	Transition    string   `json:"transition"`
	Degraded      bool     `json:"degraded"`
	DegradeReason *string  `json:"degrade_reason"`
	Added         *int     `json:"added"`
	Removed       *int     `json:"removed"`
	Unchanged     *int     `json:"unchanged"`
	Churn         *float64 `json:"churn"`
	TotalPrev     *int     `json:"total_prev"`
	TotalCur      *int     `json:"total_cur"`
	BaselineAgeS  *int     `json:"baseline_age_s"`
	Normalized    bool     `json:"normalized"`
	Truncated     bool     `json:"truncated"`
}

func (r Report) core() lineCore {
	return lineCore{
		V: r.V, Key: r.Key, Exit: r.Exit, PrevExit: r.PrevExit, Transition: r.Transition,
		Degraded: r.Degraded, DegradeReason: r.DegradeReason, Added: r.Added, Removed: r.Removed,
		Unchanged: r.Unchanged, Churn: r.Churn, TotalPrev: r.TotalPrev, TotalCur: r.TotalCur,
		BaselineAgeS: r.BaselineAgeS, Normalized: r.Normalized, Truncated: r.Truncated,
	}
}

// Render produces the line-1 JSON object and the text body to print after it.
// In --json mode the object carries the arrays/body and the returned body is "".
// The JSON is HTML-escape-free with a trailing newline (the house funnel).
func Render(r Report, opt Options) (line []byte, body string) {
	var v any
	switch {
	case !opt.JSON:
		v = r.core()
		body = r.textBody
	case r.fullBody:
		// baseline / degrade / --full: the body is bounded full output, not a delta.
		v = struct {
			lineCore
			Body string `json:"body"`
		}{r.core(), r.jsonBody}
	default:
		v = struct {
			lineCore
			AddedLines   []string `json:"added_lines"`
			RemovedLines []string `json:"removed_lines"`
		}{r.core(), nonNil(r.addedLines), nonNil(r.removedLines)}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	// Marshal cannot fail for these plain types; ignore the error to keep the
	// signature clean (the house JSON funnel).
	_ = enc.Encode(v)
	return buf.Bytes(), body
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
