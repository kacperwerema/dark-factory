package agentrun

import "strings"

// FieldSep separates the fields of every -F format handed to tmux. It is
// printable on purpose: without a UTF-8 locale (no LANG, as in a container,
// launchd or cron) tmux prints a tab in a format's output as '_', which
// joins the fields into one. tmux turns
// ':' and '.' in session names into '_', and "|:|" is unlikely in a window
// name or a path.
const FieldSep = "|:|"

// Format is a tmux -F format printing each variable (session_name,
// @iv_lead_view, ...) as #{var}, joined by FieldSep.
func Format(vars ...string) string {
	parts := make([]string, len(vars))
	for i, v := range vars {
		parts[i] = "#{" + v + "}"
	}
	return strings.Join(parts, FieldSep)
}

// SplitFields splits one line of a Format output into at most n fields, the
// last one kept whole (a path may hold anything). Surrounding white space is
// dropped first.
func SplitFields(line string, n int) []string {
	return strings.SplitN(strings.TrimSpace(line), FieldSep, n)
}
