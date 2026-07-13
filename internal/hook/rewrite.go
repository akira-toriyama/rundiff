package hook

import (
	"bytes"
	"encoding/json"
)

// event is the slice of a PreToolUse hook event this package reads. ToolInput is
// a RawMessage map, not a struct: the hook must ECHO the tool input back with
// only `command` changed, and a struct would silently drop every field it does
// not know about — including fields Claude Code has not shipped yet. Decoding to
// raw messages makes the echo lossless by construction rather than by
// maintenance.
type event struct {
	HookEventName string                     `json:"hook_event_name"`
	ToolName      string                     `json:"tool_name"`
	ToolInput     map[string]json.RawMessage `json:"tool_input"`
}

// response is the hook's entire output type, and what it OMITS is the point.
//
// There is no permissionDecision field — not unset, not empty: absent from the
// type, so no future edit can set one by accident. Returning
// permissionDecision:"allow" would bypass the user's permission system entirely,
// and rundiff execs whatever argv it is handed; a hook that both chooses the
// command and approves it is a hole shaped exactly like `Bash(*)`. updatedInput
// ALONE is honored by Claude Code, and permissions are then re-checked against
// the REWRITTEN string — which is why Print emits one narrow permission entry
// per target instead of blanket approval.
type response struct {
	HookSpecificOutput hookSpecificOutput `json:"hookSpecificOutput"`
}

type hookSpecificOutput struct {
	HookEventName     string                     `json:"hookEventName"`
	UpdatedInput      map[string]json.RawMessage `json:"updatedInput"`
	AdditionalContext string                     `json:"additionalContext"`
}

// decode applies every EVENT-level gate and yields the tool input and the
// command string. ok=false is "this is not an event the hook acts on at all" —
// as distinct from "this is such an event, and the COMMAND is one we decline to
// wrap", which is Command's answer to give.
//
// Rewrite and EventCommand share this so they cannot drift: the debug output
// must never claim a reason for a decision that was actually made somewhere else.
func decode(eventJSON []byte) (toolInput map[string]json.RawMessage, command string, ok bool) {
	var ev event
	if err := json.Unmarshal(eventJSON, &ev); err != nil {
		return nil, "", false
	}
	if ev.HookEventName != "PreToolUse" || ev.ToolName != "Bash" || ev.ToolInput == nil {
		return nil, "", false
	}

	// A backgrounded Bash call is already detached from its output: the agent
	// gets a shell id, not a transcript, so rundiff's delta would go nowhere and
	// its cache entry would record a run nobody read. A run_in_background that is
	// present but NOT a bool is a shape we do not understand — and not
	// understanding is a refusal, not a default.
	if raw, present := ev.ToolInput["run_in_background"]; present {
		var bg bool
		if err := json.Unmarshal(raw, &bg); err != nil || bg {
			return nil, "", false
		}
	}

	raw, present := ev.ToolInput["command"]
	if !present {
		return nil, "", false
	}
	if err := json.Unmarshal(raw, &command); err != nil {
		return nil, "", false
	}
	return ev.ToolInput, command, true
}

// Rewrite decides one PreToolUse event. It returns the hook response, or nil for
// "no decision" — which is the answer to every doubt: a malformed event, a
// truncated one, a different tool, a command we cannot express, or a field that
// is not the shape it should be. nil is not an error path; it is this package's
// normal, frequent, correct output. Rewrite never errors and never panics.
func Rewrite(eventJSON []byte, opt Options) []byte {
	toolInput, command, ok := decode(eventJSON)
	if !ok {
		return nil
	}

	rewritten, _, ok := Command(command, opt)
	if !ok {
		return nil
	}

	// Echo the WHOLE tool input, replacing only `command`. Dropping a field here
	// would be a silent behavior change wearing a rewrite's clothes: losing
	// `timeout` alone would revert a deliberately-extended 10-minute suite to the
	// Bash default and kill it mid-run — a "diff" that ate the test run.
	updated := make(map[string]json.RawMessage, len(toolInput))
	for k, v := range toolInput {
		updated[k] = v
	}
	encoded, err := encodeJSON(rewritten)
	if err != nil {
		return nil
	}
	updated["command"] = encoded

	out, err := encodeJSON(response{HookSpecificOutput: hookSpecificOutput{
		HookEventName:     "PreToolUse",
		UpdatedInput:      updated,
		AdditionalContext: AdditionalContext(),
	}})
	if err != nil {
		return nil
	}
	return out
}

// encodeJSON marshals v with HTML escaping OFF and no trailing newline.
//
// json.Marshal is not usable here, and the difference is not cosmetic: it always
// escapes < > &, so the `&&` of a `cd <dir> && …` rewrite would come out as two
// & escapes. Escaping the command string EARLY is the trap — once it is
// embedded as a RawMessage, no amount of escape-free encoding at the outer level
// can undo it, so the inner value must be encoded escape-free too. The escaped
// form decodes identically and nothing breaks; but a hook's output is read by
// humans debugging it as often as by a parser, and a command full of & reads
// like a bug in the thing you are being asked to trust. Every value in the
// response goes through here, which is also what keeps the echoed tool_input
// fields byte-identical.
func encodeJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// AdditionalContext explains the wrapped command's unusual output shape to the
// agent that is about to read it.
//
// It is strictly DECLARATIVE — no imperatives, no "run this", no "use that". Not
// a style choice: text that instructs is what prompt-injection defenses are
// built to catch, and a hook's additionalContext that reads like a command to
// the model is liable to be surfaced to the USER as a suspicious instruction
// rather than consumed as context. Stating what is true about the output leaves
// the agent to draw its own conclusion, which is also the only honest thing a
// wrapper can do.
func AdditionalContext() string {
	return "This command was wrapped by rundiff, so its output has an unusual shape. " +
		"Line 1 of stdout is a JSON record with the tool name and the file-level " +
		"failing/fixed/new sets; a null field there means rundiff is not certain of that " +
		"answer and is declining to guess rather than risk a wrong one. Everything after " +
		"line 1 is a delta, not a transcript: it contains only the lines that CHANGED " +
		"since the last run of this same command, so an empty body means this run's output " +
		"was identical to the previous one. To see the whole output, `rundiff --full -- <cmd>` " +
		"re-runs the command and shows all of it instead of the delta, and `RUNDIFF_HOOK=0 " +
		"<cmd>` runs the same command unwrapped."
}
