package agentrun

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/clilog"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// CodexStartFlags are the flags every interactive codex start carries, so
// that it reaches its prompt with nothing to press: nobody watches a worker's
// pane, and on 2026-11-05 three workers sat at a dialog for half an hour.
//
//   - It trusts dir, its own folder, for this run. The projects table is an
//     inline value because codex splits a -c key on every dot and keeps the
//     quotes, so -c projects."<dir>".trust_level=… never names the folder
//     (tried on codex-cli 0.163.0). Nothing is written to ~/.codex/config.toml,
//     and the sandbox and approval settings stay as they are.
//   - It skips the update prompt, whose first choice runs curl | sh.
//
// dir has its links resolved, as codex compares it with its working
// directory (on a Mac /tmp is /private/tmp).
func CodexStartFlags(dir string) []string {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	key := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(dir)
	return []string{
		"-c", `projects={"` + key + `"={trust_level="trusted"}}`,
		"-c", "check_for_update_on_startup=false",
	}
}

// DialogStall is how long a launch may sit at a startup dialog before it is
// reported: well past any start, and short of a lead's next look.
const DialogStall = 60 * time.Second

// dialogLines is how many of a pane's last non-empty lines a dialog is looked
// for in: a dialog fills the end of the pane, a quote of one scrolls away.
const dialogLines = 15

// startupDialogs are the dialogs an agent CLI can stop at before its prompt,
// each known by its first choice as a menu line.
var startupDialogs = []struct {
	name   string
	choice *regexp.Regexp
}{
	{"Codex's trust dialog", regexp.MustCompile(`^(?:[›❯>]\s*)?\d+\.\s+Trust and continue\b`)},
	{"Codex's update prompt", regexp.MustCompile(`^(?:[›❯>]\s*)?\d+\.\s+Update now\b`)},
	{"Claude's trust dialog", regexp.MustCompile(`^(?:[›❯>]\s*)?\d+\.\s+Yes, I trust this folder\b`)},
}

// StartupDialog names the startup dialog pane shows at its end, "" for none.
// An agent in the middle of a turn is past its dialogs, whatever it quotes.
func StartupDialog(pane string) string {
	tail := lastLines(pane)
	if strings.Contains(strings.Join(tail, "\n"), paneWorkingMark) {
		return ""
	}
	for _, l := range tail {
		for _, d := range startupDialogs {
			if d.choice.MatchString(l) {
				return d.name
			}
		}
	}
	return ""
}

// FirstCallLimit is how long a worker may run with no issue-cli call before
// it is reported: its briefing's first step is issue-cli start, so anything
// that stops a start, named here or not, shows up as this silence. On
// 2026-11-05 three dispatches lost 75 to 87 minutes that way.
const FirstCallLimit = 5 * time.Minute

// midRunPrompts are the prompts a worker can stop at after its start: a last
// line, which a prompt always ends the pane with, and a first choice above
// it, both as the CLIs print them (testdata/panes, captured from codex-cli
// 0.163.0 and Claude Code 2.1.318). Both must hold, so an agent printing a
// prompt is not at one: its own input box comes after the quote.
var midRunPrompts = []struct {
	name   string
	footer *regexp.Regexp
	choice *regexp.Regexp
}{
	{"Codex's approval prompt", regexp.MustCompile(`^Press enter to confirm or esc to cancel$`), regexp.MustCompile(`^(?:›\s*)?1\.\s+Yes\b`)},
	{"Claude's approval prompt", regexp.MustCompile(`^Esc to cancel\b`), regexp.MustCompile(`^(?:❯\s*)?1\.\s+Yes\b`)},
	{"Claude's question", regexp.MustCompile(`^Enter to select\b.*\bEsc to cancel$`), regexp.MustCompile(`^(?:❯\s*)?1\.\s`)},
}

// PanePrompt names the prompt pane ends with, "" for none.
func PanePrompt(pane string) string {
	tail := lastLines(pane)
	if len(tail) == 0 || strings.Contains(strings.Join(tail, "\n"), paneWorkingMark) {
		return ""
	}
	for _, p := range midRunPrompts {
		if !p.footer.MatchString(tail[0]) {
			continue
		}
		for _, l := range tail[1:] {
			if p.choice.MatchString(l) {
				return p.name
			}
		}
	}
	return ""
}

// lastLines is pane's last dialogLines non-empty lines, trimmed, last first.
func lastLines(pane string) []string {
	var tail []string
	all := strings.Split(pane, "\n")
	for i := len(all) - 1; i >= 0 && len(tail) < dialogLines; i-- {
		if l := strings.TrimSpace(all[i]); l != "" {
			tail = append(tail, l)
		}
	}
	return tail
}

// PaneStall is a live agent nobody is hearing from: at a startup dialog, at a
// prompt, or with no issue-cli call since its launch. It is reported, never
// typed into (choosing for it is the owner's or the lead's call).
type PaneStall struct {
	Session string
	// Dialog names the dialog or prompt; "" when NoCall says it.
	Dialog string
	// NoCall is how long the worker has run with no issue-cli call.
	NoCall time.Duration
}

// Words is the stall as a report says it.
func (s PaneStall) Words() string {
	if s.Dialog == "" {
		return fmt.Sprintf("no issue-cli call in the %d m since launch", int(s.NoCall/time.Minute))
	}
	return "stalled at " + s.Dialog
}

// Ask is what a report asks of whoever reads it.
func (s PaneStall) Ask() string {
	if s.Dialog == "" {
		return "attach and see why, or stop it"
	}
	return "attach and answer it, or stop it"
}

// ScanStalls reads the panes of proj's live agents and leads (root scopes
// standalone sessions, as ForProject) and returns those stalled DialogStall
// after their launch: at a startup dialog, or for a worker at a prompt or
// with no issue-cli call since launch after FirstCallLimit. A prompt has no
// age of its own, tmux's activity times move while a CLI sits at one, and
// nobody else answers a worker's pane. A lead's prompt is in the owner's chat.
// It reads panes and logs only.
func ScanStalls(proj *tracker.Project, root string, now time.Time) []PaneStall {
	var out []PaneStall
	for _, s := range ForProject(proj, root, ListSessions()) {
		name := BareName(s.Name)
		if !hasManagedPrefix(name) {
			continue
		}
		at, ok := launchedAt(s, name, root)
		if !ok || now.Sub(at) < DialogStall {
			continue
		}
		pane, err := CapturePane(Target(proj, name))
		if err != nil {
			continue
		}
		worker := strings.HasPrefix(name, "agent-")
		d := StartupDialog(pane)
		if d == "" && worker {
			d = PanePrompt(pane)
		}
		switch {
		case d != "":
			out = append(out, PaneStall{Session: name, Dialog: d})
		case worker && now.Sub(at) >= FirstCallLimit && !calledSince(proj, root, name, at):
			out = append(out, PaneStall{Session: name, NoCall: now.Sub(at)})
		}
	}
	return out
}

// calledSince says whether the worker's clilog has a call for proj that ended
// at or after its launch: a re-dispatch keeps the session name and the log,
// and the earlier worker's calls say nothing about this one. tmux dates a
// launch to the second, so that second counts.
func calledSince(proj *tracker.Project, root, name string, launch time.Time) bool {
	rec, ok := clilog.Newest(proj.Slug, clilog.AgentPath(root, name))
	return ok && !rec.End().Before(launch.Truncate(time.Second))
}

// launchedAt is when the session started. A shared-session window has no
// start time from tmux, so its launch is the prompt file the launcher wrote
// under the project root's .agent-logs.
func launchedAt(s Session, name, root string) (time.Time, bool) {
	if at, err := time.ParseInLocation("2006-01-02 15:04:05", s.StartTime, time.Local); err == nil {
		return at, true
	}
	if info, err := os.Stat(filepath.Join(root, ".agent-logs", name, "dispatch-prompt.txt")); err == nil {
		return info.ModTime(), true
	}
	return time.Time{}, false
}
