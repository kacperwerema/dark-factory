package routing

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Limit is a usage-limit message found in an agent's pane.
type Limit struct {
	Agent string
	// Line is the CLI's message, trimmed.
	Line string
	// Until is when the subscription resets; KnownReset is false when the
	// message named no time and Until is a guess (UnknownRest after now).
	Until      time.Time
	KnownReset bool
}

// UnknownRest is how long a subscription rests when its limit message names
// no reset time.
const UnknownRest = time.Hour

// scanTail is how many of the pane's last non-empty lines a scan reads: a
// limit message is the newest thing a stalled CLI shows, and a message
// quoted further up (an issue body, a diff) must not count.
const scanTail = 12

// Codex's message, from codex-rs/protocol/src/error.rs (UsageLimitReachedError's
// Display): "You’ve hit your usage limit." … then " Try again at 3:05 PM.",
// " or try again at Nov 2nd, 2026 3:05 PM.", or " Try again later.". The
// TUI shows the error after "■ ". Both apostrophes are accepted.
var (
	codexLimit = regexp.MustCompile(`(?i)you[’']ve hit your usage limit`)
	codexAt    = regexp.MustCompile(`(?i)try again at ([A-Z][a-z]{2} \d{1,2}(?:st|nd|rd|th), \d{4} )?(\d{1,2}:\d{2} ?[AP]M)`)
)

// Claude Code's messages, from its bundled source (version 2.1.316, the
// limit banner's builder): "You've hit your " + the limit's name + " · resets "
// + the reset time, where the names are "5-hour limit", "weekly limit",
// "Opus limit", "Sonnet limit" and "Fable limit", and the time is
// toLocaleTimeString with the zone: "3pm (Europe/Warsaw)", "3:45pm
// (Europe/Warsaw)", or past 24 hours "Nov 3, 9am (Europe/Warsaw)" (with the
// year when it differs). Also "Usage limit reached · continuing automatically
// …" and the older "Claude usage limit reached. Your limit will reset at 5pm
// (America/New_York)." / "Claude AI usage limit reached|1749924000" (a unix
// time). Spend caps and usage credits (paid extras, not the plan) don't count.
var (
	claudeLimit = regexp.MustCompile(`(?i)(you[’']ve hit your (5-hour|weekly|opus|sonnet|fable|session) limit|usage limit reached)`)
	claudeEpoch = regexp.MustCompile(`(?i)usage limit reached\|(\d{9,11})`)
	claudeAt    = regexp.MustCompile(`(?i)resets?(?: at)? (?:([A-Z][a-z]{2}) (\d{1,2}),? (?:\d{4},? )?)?(\d{1,2})(?::(\d{2}))? ?([ap]m)(?: \(([A-Za-z0-9_+\-]+(?:/[A-Za-z0-9_+\-]+)*)\))?`)
)

// FindLimit looks for agent's own limit message in the last lines of pane
// text and parses its reset time against now. ok is false when there is
// none. agent "" tries both CLIs.
func FindLimit(agent, pane string, now time.Time) (Limit, bool) {
	lines := tailLines(pane, scanTail)
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if (agent == "" || agent == "codex") && codexLimit.MatchString(line) {
			return codexReset(line, now), true
		}
		if (agent == "" || agent == "claude") && claudeLimit.MatchString(line) && !codexLimit.MatchString(line) {
			return claudeReset(line, now), true
		}
	}
	return Limit{}, false
}

func tailLines(pane string, n int) []string {
	var out []string
	for _, l := range strings.Split(pane, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

func unknown(agent, line string, now time.Time) Limit {
	return Limit{Agent: agent, Line: clip(line), Until: now.Add(UnknownRest)}
}

func clip(line string) string {
	line = strings.TrimSpace(strings.TrimLeft(line, "■⎿│ \t"))
	if r := []rune(line); len(r) > 200 {
		line = string(r[:200]) + "…"
	}
	return line
}

func codexReset(line string, now time.Time) Limit {
	m := codexAt.FindStringSubmatch(line)
	if m == nil {
		return unknown("codex", line, now)
	}
	loc := now.Location()
	clock, err := time.ParseInLocation("3:04PM", strings.ToUpper(strings.ReplaceAll(m[2], " ", "")), loc)
	if err != nil {
		return unknown("codex", line, now)
	}
	var until time.Time
	if m[1] != "" {
		day, err := time.ParseInLocation("Jan 2 2006", dropOrdinal(strings.TrimSpace(m[1])), loc)
		if err != nil {
			return unknown("codex", line, now)
		}
		until = time.Date(day.Year(), day.Month(), day.Day(), clock.Hour(), clock.Minute(), 0, 0, loc)
	} else {
		until = nextAt(now, clock.Hour(), clock.Minute(), loc)
	}
	return Limit{Agent: "codex", Line: clip(line), Until: until, KnownReset: true}
}

var ordinal = regexp.MustCompile(`(\d)(st|nd|rd|th),`)

func dropOrdinal(s string) string { return ordinal.ReplaceAllString(s, "$1") }

func claudeReset(line string, now time.Time) Limit {
	if m := claudeEpoch.FindStringSubmatch(line); m != nil {
		if sec, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			return Limit{Agent: "claude", Line: clip(line), Until: time.Unix(sec, 0).In(now.Location()), KnownReset: true}
		}
	}
	m := claudeAt.FindStringSubmatch(line)
	if m == nil {
		return unknown("claude", line, now)
	}
	loc := now.Location()
	if m[6] != "" {
		if l, err := time.LoadLocation(m[6]); err == nil {
			loc = l
		}
	}
	hour, _ := strconv.Atoi(m[3])
	minute := 0
	if m[4] != "" {
		minute, _ = strconv.Atoi(m[4])
	}
	if hour < 1 || hour > 12 || minute > 59 {
		return unknown("claude", line, now)
	}
	hour %= 12
	if strings.EqualFold(m[5], "pm") {
		hour += 12
	}
	var until time.Time
	if m[1] != "" {
		mon, err := time.Parse("Jan", m[1])
		if err != nil {
			return unknown("claude", line, now)
		}
		day, _ := strconv.Atoi(m[2])
		ref := now.In(loc)
		until = time.Date(ref.Year(), mon.Month(), day, hour, minute, 0, 0, loc)
		if until.Before(ref.Add(-24 * time.Hour)) {
			until = until.AddDate(1, 0, 0)
		}
	} else {
		until = nextAt(now, hour, minute, loc)
	}
	return Limit{Agent: "claude", Line: clip(line), Until: until.In(now.Location()), KnownReset: true}
}

// nextAt is the next hour:minute in loc at or after now: a time already past
// today means tomorrow.
func nextAt(now time.Time, hour, minute int, loc *time.Location) time.Time {
	ref := now.In(loc)
	t := time.Date(ref.Year(), ref.Month(), ref.Day(), hour, minute, 0, 0, loc)
	if t.Before(ref) {
		t = t.AddDate(0, 0, 1)
	}
	return t
}
