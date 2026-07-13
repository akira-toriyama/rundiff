package hook

// EventCommand returns the Bash command a PreToolUse event carries, or "" when
// the event is not one the hook acts on (a different hook event, a different
// tool, malformed bytes, a backgrounded call, a command that is not a string).
//
// Rewrite does not need this — it decides and returns bytes. It exists for the
// human-facing paths that must say WHY nothing happened (`hook rewrite
// --explain`, RUNDIFF_HOOK_DEBUG), which need the command string back out of the
// event. Keeping the decode here rather than in the CLI is what stops the two
// from drifting apart: there is exactly one definition of "the command this event
// is about".
//
// It delegates to decode — the same gates Rewrite runs, not a copy of them —
// which buys one invariant:
//
//	EventCommand(e) != "" && Rewrite(e) == nil  ⇒  Command(EventCommand(e)) refuses
//
// so a non-empty result GUARANTEES Command has a non-empty reason to report. A
// re-implementation that let, say, a backgrounded event through would break it:
// Rewrite would decline on the event while Command happily accepted the command,
// and the debug path would print "left alone ()" — a blank where the reason
// should be, for a decision Command never made. The gates have to be shared for
// the explanation to be true.
func EventCommand(eventJSON []byte) string {
	_, command, ok := decode(eventJSON)
	if !ok {
		return ""
	}
	return command
}
