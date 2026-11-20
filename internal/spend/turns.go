package spend

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kacperwerema/dark-factory/internal/bench"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// spend --turns: each issue's worker turns split by what caused them. A
// turn is one assistant message (one API call, each message id once); its
// cause is the first that fits:
//
//   - transition: a shell call running issue-cli … transition (Refused
//     counts those whose tool result is an error: the gate refused);
//   - validate: a shell call running make validate, or reading its log;
//   - read: one Read call, or one cat/head/tail/nl/sed -n of one file;
//   - red round: any other turn while one of the issue's checks is red
//     (from a red result in its rounds to that check's next green);
//   - rest.
//
// Every turn re-reads the whole context, so a cause's Context is what its
// turns read. A command's text is read only to pick the cause: it is never
// kept, printed or written.

// Turn causes.
const (
	CauseTransition = "transition"
	CauseValidate   = "validate"
	CauseRead       = "read"
	CauseRedRound   = "red_round"
	CauseRest       = "rest"
)

// Causes are the causes in the order they are tried and shown.
var Causes = []string{CauseTransition, CauseValidate, CauseRead, CauseRedRound, CauseRest}

// CauseCount is one cause's turns and the context they read.
type CauseCount struct {
	Turns int `json:"turns"`
	// Context is input + cache read + cache write of those turns.
	Context int64 `json:"context_tokens"`
}

// IssueTurns is one issue's worker turns by cause in the window.
type IssueTurns struct {
	Slug     string `json:"slug"`
	Type     string `json:"type,omitempty"`
	Sessions int    `json:"sessions"`
	Turns    int    `json:"turns"`
	Context  int64  `json:"context_tokens"`
	// By is per cause (Causes); Refused are the transition calls refused.
	By      map[string]CauseCount `json:"by_cause"`
	Refused int                   `json:"transitions_refused"`
	// RedRounds is how many red rounds started in the window: the issue's
	// recorded rounds (reds of one check at one head are one round) and its
	// sessions' red make validate runs (a red verdict up to the next green).
	RedRounds int `json:"red_rounds"`
	// DryRuns are the transition calls with --dry-run: a turn the real
	// call would have spent on the same check.
	DryRuns int `json:"dry_runs"`
	// FollowOnReads are read turns straight after another read turn of the
	// same session: a one-file read that could have gone with the one
	// before it.
	FollowOnReads int `json:"follow_on_reads"`
}

// TurnsReport is spend --turns.
type TurnsReport struct {
	Since   time.Time    `json:"since"`
	Until   time.Time    `json:"until"`
	Project string       `json:"project"`
	Issues  []IssueTurns `json:"issues"`
	Total   IssueTurns   `json:"total"`
}

// LoadTurns reads the window for p's issues (only slug's when not "").
func LoadTurns(p *tracker.Project, issues []*tracker.Issue, slug string, since, until time.Time, opt Options) *TurnsReport {
	if opt.ProjectsDir == "" {
		opt.ProjectsDir = DefaultProjectsDir()
	}
	rep := &TurnsReport{Since: since, Until: until, Project: p.Slug, Total: IssueTurns{Slug: "all", By: map[string]CauseCount{}}}
	pk := newProjKinds(p, issues)
	// The project folder's sessions named for an issue's worker are its too.
	named := map[string][]string{}
	for _, folder := range bench.TranscriptFolders(opt.ProjectsDir, p.Root()) {
		for top := range bench.SessionFiles(folder) {
			st, err := os.Stat(top)
			if err != nil || st.ModTime().Before(since) {
				continue
			}
			if o, ok := pk.names[readFile(top, st).agent]; ok && o.kind == KindWorker && o.issue != nil {
				named[o.issue.Slug] = append(named[o.issue.Slug], top)
			}
		}
	}
	for _, is := range issues {
		if slug != "" && is.Slug != slug {
			continue
		}
		var files []string
		for _, folder := range bench.TranscriptFolders(opt.ProjectsDir, filepath.Join(p.Root(), ".worktrees", is.Slug)) {
			for top := range bench.SessionFiles(folder) {
				st, err := os.Stat(top)
				if err != nil || st.ModTime().Before(since) {
					continue
				}
				// A truncated folder name can match another issue's
				// worktree: a session named for another worker is not this
				// one's.
				if o, ok := pk.names[readFile(top, st).agent]; ok && o.issue != nil && o.issue.Slug != is.Slug {
					continue
				}
				if name := readFile(top, st).agent; name != "" && !strings.HasPrefix(name, "agent-") {
					continue // a critic, a post-mortem: not the worker's turns
				}
				files = append(files, top)
			}
		}
		files = append(files, named[is.Slug]...)
		if len(files) == 0 {
			continue
		}
		it := issueTurns(is, files, since, until)
		if it.Turns == 0 {
			continue
		}
		rep.Issues = append(rep.Issues, it)
		rep.Total.Sessions += it.Sessions
		rep.Total.Turns += it.Turns
		rep.Total.Context += it.Context
		rep.Total.Refused += it.Refused
		rep.Total.RedRounds += it.RedRounds
		rep.Total.DryRuns += it.DryRuns
		rep.Total.FollowOnReads += it.FollowOnReads
		for c, n := range it.By {
			t := rep.Total.By[c]
			t.Turns += n.Turns
			t.Context += n.Context
			rep.Total.By[c] = t
		}
	}
	sort.Slice(rep.Issues, func(i, j int) bool {
		if rep.Issues[i].Turns != rep.Issues[j].Turns {
			return rep.Issues[i].Turns > rep.Issues[j].Turns
		}
		return rep.Issues[i].Slug < rep.Issues[j].Slug
	})
	return rep
}

// window is a span a check was red: [from, to); to zero is still red.
type window struct{ from, to time.Time }

func redWindows(rounds []tracker.RoundResult) (ws []window, count int) {
	rs := append([]tracker.RoundResult(nil), rounds...)
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].At.Before(rs[j].At) })
	open := map[string]time.Time{}
	counted := map[string]bool{}
	for _, r := range rs {
		if r.Red {
			if key := r.Check + "@" + r.Head; !counted[key] {
				counted[key] = true
				count++
			}
			if _, ok := open[r.Check]; !ok {
				open[r.Check] = r.At
			}
			continue
		}
		if from, ok := open[r.Check]; ok {
			ws = append(ws, window{from, r.At})
			delete(open, r.Check)
		}
	}
	for _, from := range open {
		ws = append(ws, window{from: from})
	}
	return ws, count
}

// verdictWindows are the spans a session's make validate was red: from a
// red verdict to the next green one (or the session's end).
func verdictWindows(vs []verdict) []window {
	var out []window
	var from time.Time
	for _, v := range vs {
		switch {
		case v.red && from.IsZero():
			from = v.at
		case !v.red && !from.IsZero():
			out = append(out, window{from, v.at})
			from = time.Time{}
		}
	}
	if !from.IsZero() {
		out = append(out, window{from: from})
	}
	return out
}

func inRed(ws []window, t time.Time) bool {
	for _, w := range ws {
		if !t.Before(w.from) && (w.to.IsZero() || t.Before(w.to)) {
			return true
		}
	}
	return false
}

func issueTurns(is *tracker.Issue, files []string, since, until time.Time) IssueTurns {
	it := IssueTurns{Slug: is.Slug, Type: is.Type, By: map[string]CauseCount{}}
	var ws []window
	if st, err := tracker.LoadStats(is.FilePath); err == nil {
		ws, _ = redWindows(st.Rounds)
		var inWin []tracker.RoundResult
		for _, r := range st.Rounds {
			if !r.At.Before(since) && !r.At.After(until) {
				inWin = append(inWin, r)
			}
		}
		_, it.RedRounds = redWindows(inWin)
	}
	seen := map[string]bool{}
	sort.Strings(files)
	for _, f := range files {
		st, err := os.Stat(f)
		if err != nil {
			continue
		}
		counted := false
		tr := readTurns(f, st)
		vws := verdictWindows(tr.reds)
		for _, w := range vws {
			if !w.from.Before(since) && !w.from.After(until) {
				it.RedRounds++
			}
		}
		fileWs := append(append([]window(nil), ws...), vws...)
		prevRead := false
		for _, tn := range tr.turns {
			if tn.id != "" && seen[tn.id] {
				continue
			}
			seen[tn.id] = true
			if tn.at.Before(since) || tn.at.After(until) {
				continue
			}
			counted = true
			if tn.cause == CauseRead && prevRead {
				it.FollowOnReads++
			}
			prevRead = tn.cause == CauseRead
			it.DryRuns += tn.dryRuns
			cause := tn.cause
			if cause == CauseRest && inRed(fileWs, tn.at) {
				cause = CauseRedRound
			}
			c := it.By[cause]
			c.Turns++
			c.Context += tn.context
			it.By[cause] = c
			it.Turns++
			it.Context += tn.context
			if tn.refused {
				it.Refused++
			}
		}
		if counted {
			it.Sessions++
		}
	}
	return it
}

// turn is one assistant message: numbers and its cause, no text.
type turn struct {
	id      string
	at      time.Time
	context int64
	cause   string
	refused bool
	// dryRuns are its transition calls with --dry-run.
	dryRuns int
}

type turnsRead struct {
	size  int64
	mod   time.Time
	turns []turn
	// reds are the validate verdicts its tool results printed: red
	// (validate: FAIL, red so far) or green (validate: PASS).
	reds []verdict
}

// verdict is one validate verdict seen in a tool result.
type verdict struct {
	at  time.Time
	red bool
}

var (
	turnsMu    sync.Mutex
	turnsCache = map[string]*turnsRead{}
)

// readTurns is a transcript's turns with their causes, cached until the
// file changes.
func readTurns(path string, st os.FileInfo) *turnsRead {
	turnsMu.Lock()
	if c := turnsCache[path]; c != nil && c.size == st.Size() && c.mod.Equal(st.ModTime()) {
		turnsMu.Unlock()
		return c
	}
	turnsMu.Unlock()
	tr := &turnsRead{size: st.Size(), mod: st.ModTime()}
	tr.turns, tr.reds = scanTurns(path)
	turnsMu.Lock()
	turnsCache[path] = tr
	turnsMu.Unlock()
	return tr
}

func scanTurns(path string) ([]turn, []verdict) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	var reds []verdict
	defer f.Close()
	var order []string
	byID := map[string]*turn{}
	// transitionCall is each transition tool call's turn, by tool use id,
	// until its result says whether it was refused.
	transitionCall := map[string]*turn{}
	anon := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var line struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				ID      string          `json:"id"`
				Usage   *usage          `json:"usage"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		b := sc.Bytes()
		if json.Unmarshal(b, &line) != nil || (line.Type != "assistant" && line.Type != "user") {
			continue
		}
		var blocks []struct {
			Type      string          `json:"type"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			ToolUseID string          `json:"tool_use_id"`
			IsError   bool            `json:"is_error"`
			Result    json.RawMessage `json:"content"`
			Input     struct {
				Command  string `json:"command"`
				FilePath string `json:"file_path"`
			} `json:"input"`
		}
		_ = json.Unmarshal(line.Message.Content, &blocks)
		if line.Type == "user" {
			for _, bl := range blocks {
				if bl.Type != "tool_result" {
					continue
				}
				text := resultText(bl.Result)
				if tn := transitionCall[bl.ToolUseID]; tn != nil && (bl.IsError || refusedText(text)) {
					tn.refused = true
				}
				if red, ok := validateVerdict(text); ok {
					if at, err := time.Parse(time.RFC3339Nano, line.Timestamp); err == nil {
						reds = append(reds, verdict{at: at, red: red})
					}
				}
			}
			continue
		}
		id := line.Message.ID
		if id == "" {
			anon++
			id = "\x00" + strconv.Itoa(anon)
		}
		tn := byID[id]
		if tn == nil {
			tn = &turn{id: line.Message.ID, cause: CauseRest}
			if at, err := time.Parse(time.RFC3339Nano, line.Timestamp); err == nil {
				tn.at = at
			}
			byID[id] = tn
			order = append(order, id)
		}
		if u := line.Message.Usage; u != nil && tn.context == 0 {
			tn.context = u.Input + u.CacheRead + u.CacheWrite
		}
		for _, bl := range blocks {
			if bl.Type != "tool_use" {
				continue
			}
			c := toolCause(bl.Name, bl.Input.Command)
			if rank(c) < rank(tn.cause) {
				tn.cause = c
			}
			if c == CauseTransition {
				transitionCall[bl.ID] = tn
				if dryRunRe.MatchString(bl.Input.Command) {
					tn.dryRuns++
				}
			}
		}
	}
	out := make([]turn, 0, len(order))
	for _, id := range order {
		if tn := byID[id]; !tn.at.IsZero() {
			out = append(out, *tn)
		}
	}
	return out, reds
}

// resultText is a tool result's text, held only while its answers are
// read (refused, a validate verdict).
func resultText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var parts []struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &parts)
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

// refusedText reports whether a transition call's result says it was
// refused (a pipe often hides the exit code).
func refusedText(text string) bool {
	return strings.Contains(text, "failed to transition") || strings.Contains(text, "Error: ")
}

// validateVerdict is the last make validate verdict a result prints: red
// for "validate: FAIL" or "validate: red so far", green for "validate:
// PASS"; ok is false when it prints none.
func validateVerdict(text string) (red, ok bool) {
	i := strings.LastIndex(text, "validate: ")
	for i >= 0 {
		rest := text[i+len("validate: "):]
		switch {
		case strings.HasPrefix(rest, "PASS"):
			return false, true
		case strings.HasPrefix(rest, "FAIL"), strings.HasPrefix(rest, "red so far"):
			return true, true
		}
		i = strings.LastIndex(text[:i], "validate: ")
	}
	return false, false
}

type usage struct {
	Input      int64 `json:"input_tokens"`
	CacheRead  int64 `json:"cache_read_input_tokens"`
	CacheWrite int64 `json:"cache_creation_input_tokens"`
}

func rank(c string) int {
	for i, x := range Causes {
		if x == c {
			return i
		}
	}
	return len(Causes)
}

var (
	transitionRe = regexp.MustCompile(`(^|[\s;&|(/])issue-cli\b[^;&|\n]*\stransition\b`)
	dryRunRe     = regexp.MustCompile(`(^|[\s;&|(/])issue-cli\b[^;&|\n]*\stransition\b[^;&|\n]*\s--dry-run\b`)
	validateRe   = regexp.MustCompile(`\bmake\b[^;&|\n]*\bvalidate\b|\b(tail|cat|grep|head|rg|wc)\b[^;&|\n]*validate[^\s;&|]*\.(log|txt|out)\b`)
)

// toolCause is the cause of one tool call: Bash's by its command, Read a
// read, anything else rest.
func toolCause(name, command string) string {
	switch name {
	case "Read":
		return CauseRead
	case "Bash":
	default:
		return CauseRest
	}
	switch {
	case transitionRe.MatchString(command):
		return CauseTransition
	case validateRe.MatchString(command):
		return CauseValidate
	case singleFileRead(command):
		return CauseRead
	}
	return CauseRest
}

// ResetTurnsCache drops the cached turn reads (tests).
func ResetTurnsCache() {
	turnsMu.Lock()
	turnsCache = map[string]*turnsRead{}
	turnsMu.Unlock()
}
