package delta

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// schemaVersion is the JSON contract version (field "v"). Bump only on a
// breaking change to a field's type or meaning; adding a field is compatible.
const schemaVersion = 1

// Report is the outcome of a Diff: the machine-readable summary (line 1) plus the
// human/agent-facing body. It is pure data — Render turns it into bytes. The
// line-1 fields live in the embedded lineCore (promoted, so callers still write
// r.Added etc.), so the JSON field set is declared in exactly one place — adding
// or renaming a field can no longer silently desync a hand-copied duplicate.
type Report struct {
	lineCore

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

// setClaim copies the injected file-level claim into the line-1 fields,
// defensively re-establishing the contract whatever the caller passed: slices
// are copied (purity — the caller's arrays are never mutated), sorted and
// deduped (determinism), Fixed/New are nil-paired, and a baseline never carries
// a cross-run claim. nil stays nil — for these fields null is semantic ("no
// claim"), distinct from empty ("confidently none").
func (r *Report) setClaim(c *FileClaim, baseline bool) {
	if c == nil {
		return
	}
	tool := c.Tool
	r.Tool = &tool
	r.Failing = sortedCopy(c.Failing)
	if baseline || c.Fixed == nil || c.New == nil {
		return
	}
	r.Fixed = sortedCopy(c.Fixed)
	r.New = sortedCopy(c.New)
}

// sortedCopy returns a sorted, deduped copy; nil in ⇒ nil out, empty ⇒ empty.
func sortedCopy(s []string) []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s))
	out = append(out, s...)
	sort.Strings(out)
	return slices.Compact(out)
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
	// The run just lost its detail view (baseline/degrade/--full), so a known
	// failing list is the signal an agent reads first.
	if len(r.Failing) > 0 {
		b.WriteString("\n")
		b.WriteString(r.claimLine("failing", r.Failing))
	}
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
	// Non-empty cross-run claims render before the line delta — they are the
	// agent's first read. Empty/absent claims stay silent (the JSON carries the
	// null-vs-[] distinction).
	if len(r.Fixed) > 0 {
		b.WriteString("\n")
		b.WriteString(r.claimLine("fixed", r.Fixed))
	}
	if len(r.New) > 0 {
		b.WriteString("\n")
		b.WriteString(r.claimLine("new", r.New))
	}
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

// claimLine renders one file-level claim list, clipped like any displayed
// line. The clip only shortens the TEXT rendering — the JSON line always
// carries the full arrays — and it is surfaced: a clipped claim line sets
// Truncated, and clip's trailing marker shows the cut.
func (r *Report) claimLine(label string, ids []string) string {
	tool := ""
	if r.Tool != nil {
		tool = " (" + *r.Tool + ")"
	}
	line := label + tool + ": " + strings.Join(ids, ", ")
	if len(line) > maxDisplayLine {
		r.Truncated = true
	}
	return clip(line)
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
	head := min(50, budget/2)
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
	case r.Transition == string(TransitionInterrupted):
		label = "partial output (interrupted before the command finished)"
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

	// File-level claim (internal/adapter). For these four fields null is
	// semantic — "no claim" — so nil is deliberately NOT coerced to [] (the
	// inverse of the nonNil habit): null ≠ [] is the contract. Failing is the
	// current run's complete failing identities; Fixed/New are the cross-run
	// claim and are null or non-null together.
	Tool    *string  `json:"tool"`
	Failing []string `json:"failing"`
	Fixed   []string `json:"fixed"`
	New     []string `json:"new"`
}

func (r Report) core() lineCore { return r.lineCore }

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
