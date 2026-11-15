package routing

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Usage is one reading of how much of its weekly limit an agent has used,
// from a line its CLI printed.
type Usage struct {
	Agent   string `json:"agent"`
	Percent int    `json:"percent"`
	// Until is the week's reset; KnownReset is false when the line named
	// none (Until is UnknownRest after now, and the reading ends there).
	Until      time.Time `json:"until"`
	KnownReset bool      `json:"known_reset"`
	SeenAt     time.Time `json:"seen_at"`
	Session    string    `json:"session,omitempty"`
	Project    string    `json:"project,omitempty"`
	Line       string    `json:"line,omitempty"`
	// Cleared is the owner's issue-cli agents clear: this percent no longer
	// rests the agent.
	Cleared bool `json:"cleared,omitempty"`
}

// usageLead is what may stand before a usage line. Each pattern is anchored
// here: the CLI's own line starts its row (a pane's footer, or after a TUI's
// mark), and a line quoted in a diff, an issue body or a test's output must
// not rest a subscription for a week.
const usageLead = `^[\s⎿│■⚠•]*`

// Claude Code's lines, from its bundled source (version 2.1.318, the banner's
// builder): "You've used " + n + "% of your " + the limit's name, or "Over " +
// n + "% of your " + the name + " used", then " · resets " + the time and
// maybe an action. n stops at 99. Only "weekly limit" is the subscription's
// week: "session limit" resets within hours, and "Opus limit" and "Sonnet
// limit" leave the other models free. Seen in a pane's footer: "  You've used
// 92% of your weekly limit · resets Nov 5 at 8am (Europe/Warsaw) …".
//
// Codex's line, from its TUI (version 0.163.0, the rate-limit warning):
// "Heads up, you have less than " + n + "% of your " + the limit's label +
// " limit left. Run /status for a breakdown." It names no reset, and its
// warnings end at 5% left, so it reads 95% at most.
var (
	claudeUsage = regexp.MustCompile(`(?i)` + usageLead + `(?:you[’']ve used (\d{1,3})% of your weekly limit|over (\d{1,3})% of your weekly limit used)`)
	codexUsage  = regexp.MustCompile(`(?i)` + usageLead + `heads up, you have less than (\d{1,3})% of your weekly limit left`)
)

// FindUsage looks for agent's newest weekly usage line in the last lines of
// pane text, as FindLimit does for limit messages. A line whose reset has
// passed is a stale one and is skipped. agent "" tries both CLIs.
func FindUsage(agent, pane string, now time.Time) (Usage, bool) {
	lines := tailLines(pane, scanTail)
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if agent == "" || agent == "claude" {
			if m := claudeUsage.FindStringSubmatch(line); m != nil {
				pct, ok := percent(m[1] + m[2])
				if !ok {
					continue
				}
				l := claudeReset(line, now)
				if !l.Until.After(now) {
					continue
				}
				return Usage{Agent: "claude", Percent: pct, Until: l.Until, KnownReset: l.KnownReset, Line: l.Line}, true
			}
		}
		if agent == "" || agent == "codex" {
			if m := codexUsage.FindStringSubmatch(line); m != nil {
				if left, ok := percent(m[1]); ok {
					return Usage{Agent: "codex", Percent: 100 - left, Until: now.Add(UnknownRest), Line: clip(line)}, true
				}
			}
		}
	}
	return Usage{}, false
}

func percent(s string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	return n, err == nil && n <= 100
}

// UsageOf is agent's reading when its week hasn't ended at now.
func (s *State) UsageOf(agent string, now time.Time) (Usage, bool) {
	if s == nil {
		return Usage{}, false
	}
	u, ok := s.Usage[agent]
	if !ok || !now.Before(u.Until) {
		return Usage{}, false
	}
	return u, true
}

// SeeUsage records a reading seen in project's session and, at or over
// stopAt, rests the agent until the reading's reset, as Mark does for a limit.
// The highest percent of a week stands, whatever pane shows less, and a line
// recorded in the last day isn't read again, so one left on a pane after its
// reading ended doesn't start another. A
// percent the owner cleared rests the agent again only when a higher one
// shows. rested reports whether a rest started or grew longer.
func SeeUsage(dir string, u Usage, stopAt int, project, session string, now time.Time) (rested bool, err error) {
	_, err = update(dir, func(st *State) bool {
		if st.Usage == nil {
			st.Usage = map[string]Usage{}
		}
		changed := false
		old, live := st.UsageOf(u.Agent, now)
		last := st.Usage[u.Agent]
		sameLine := last.Session == session && last.Line == u.Line && now.Sub(last.SeenAt) < seenFor
		if !sameLine && (!live || u.Percent >= old.Percent) {
			u.SeenAt, u.Session, u.Project = now, session, project
			u.Cleared = live && old.Cleared && u.Percent == old.Percent
			st.Usage[u.Agent] = u
			changed = true
		}
		cur := st.Usage[u.Agent]
		if cur.Percent < stopAt || cur.Cleared || !now.Before(cur.Until) {
			return changed
		}
		if r, ok := st.Rests[u.Agent]; ok && !r.Until.Before(cur.Until) {
			return changed
		}
		st.Rests[u.Agent] = Rest{Agent: u.Agent, Until: cur.Until, KnownReset: cur.KnownReset, SeenAt: now,
			Session: cur.Session, Project: cur.Project, Line: cur.Line, By: "stop", Percent: cur.Percent, StopAt: stopAt}
		rested = true
		return true
	})
	return rested, err
}
