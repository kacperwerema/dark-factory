package agentrun

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	sgrDim    = "\x1b[2m"
	sgrUndim  = "\x1b[22m"
	sgrInv    = "\x1b[7m"
	sgrUninv  = "\x1b[27m"
	sgrGrey   = "\x1b[38;5;246m"
	sgrColour = "\x1b[39m"
)

// styledClaudePane is claudePane as capture-pane -e prints it: greyed
// rules, and box as the input line's styled text.
func styledClaudePane(conversation, box string) string {
	rule := sgrGrey + "────────────────────────────────────────" + sgrColour
	return conversation + "\n" + rule + "\n" + "❯ " + box + "\n" + rule + "\n" +
		sgrGrey + "  ⏵⏵ auto mode on (shift+tab to cycle)" + sgrColour + "\n"
}

// A project lead's 22:47 tick (2026-11-13): an idle worker's box showed
// Claude Code's dimmed suggestion, and a plain read (escapes stripped, as
// capture-pane -p prints it) has it as typed text. MarkSuggestion says it
// is a suggestion.
func TestPaneTailMarksAGreyedSuggestion(t *testing.T) {
	suggestion := sgrInv + "G" + sgrUninv + sgrDim + "o with option 3, send it to review" + sgrUndim
	pane := styledClaudePane("⏺ Waiting on the owner's pick.", suggestion)

	plain := paneEscape.ReplaceAllString(pane, "")
	if !strings.Contains(plain, "❯ Go with option 3, send it to review\n") {
		t.Fatalf("the repro no longer holds: a plain read is\n%s", plain)
	}

	got := MarkSuggestion(pane)
	want := "❯ (" + SuggestionNote + "Go with option 3, send it to review)"
	if !strings.Contains(got, want+"\n") {
		t.Errorf("marked pane lacks %q:\n%s", want, got)
	}
	if strings.Contains(got, "\x1b") {
		t.Errorf("escapes left in:\n%q", got)
	}
	if !strings.Contains(got, "⏺ Waiting on the owner's pick.") || !strings.Contains(got, "auto mode on") {
		t.Errorf("the rest of the pane changed:\n%s", got)
	}
}

func TestMarkSuggestion(t *testing.T) {
	for _, tc := range []struct {
		name, box, want string
	}{
		{"typed text stays", "ok, opcja 1" + sgrInv + " " + sgrUninv, "❯ ok, opcja 1 "},
		{"empty box", sgrInv + " " + sgrUninv, "❯  "},
		{"suggestion with no cursor on it", sgrDim + "Zgoda na OpenAI" + sgrUndim, "❯ (" + SuggestionNote + "Zgoda na OpenAI)"},
		{"suggestion after typed text", "run" + sgrInv + " " + sgrUninv + sgrDim + "the tests" + sgrUndim, "❯ run (" + SuggestionNote + "the tests)"},
		{"reset with 0 ends the dim run", sgrDim + "hint" + "\x1b[0m" + " typed", "❯  typed (" + SuggestionNote + "hint)"},
		// 38;5;2 is a colour (index 2), not the dim attribute.
		{"colour argument is no attribute", "\x1b[38;5;2mgreen typed" + sgrColour, "❯ green typed"},
	} {
		got := MarkSuggestion(styledClaudePane("⏺ Done.", tc.box))
		if !strings.Contains(got, "\n"+tc.want+"\n") {
			t.Errorf("%s: box line not %q in\n%s", tc.name, tc.want, got)
		}
	}

	// Dim text in the conversation is not the box: left as it is.
	pane := sgrDim + "  ⎿  (dimmed tool output)" + sgrUndim + "\n" + styledClaudePane("⏺ Done.", "")
	if got := MarkSuggestion(pane); strings.Contains(got, SuggestionNote) {
		t.Errorf("dim text above the box was marked:\n%s", got)
	}
	// A pane with no input box is the plain capture.
	if got := MarkSuggestion(sgrDim + "$ " + sgrUndim + "ls\n"); got != "$ ls\n" {
		t.Errorf("no box: %q", got)
	}
}

// paneSenders are the Go callers, outside this package, that put text or
// keys into a tmux pane, each with how its text is checked. Text for an
// agent goes through Paste or Nudge (bracketed paste, Enter, the delivery
// poll that presses Enter again while it sits in the input box), or
// PasteSubmit followed by its caller's own PromptDeliveryStep. A new caller
// turns TestEveryPaneSenderIsListed red until it is added here with its
// check; one that skips the check needs a reason as good as these.
var paneSenders = map[string]string{
	"cmd/issue-cli/lead_live.go agentrun.Paste":                  "lead message to Codex: Paste's delivery check",
	"internal/viewer/ceo_question.go agentrun.SendKey":           "keys in the CEO's question widget",
	"internal/viewer/ceo_question.go agentrun.SendText":          "the widget's Type something field, not the input box",
	"internal/viewer/crew_all.go agentrun.Nudge":                 "Crew page nudge: Nudge's delivery check",
	"internal/viewer/docs_wb.go tmuxTypeCommand":                 "an editor command into a shell pane, no agent",
	"internal/viewer/handlers_issue_mutate.go tmuxSendKeys":      "approval nudge: Nudge's delivery check",
	"internal/viewer/handlers_issue_mutate.go tmux send-keys":    "nvim command into the body-edit shell pane, no agent",
	"internal/viewer/handlers_lead_chat.go agentrun.PasteSubmit": "CEO chat: leadChatConfirm polls delivery after the lock",
	"internal/viewer/handlers_lead_chat.go agentrun.SendKey":     "allowed keys (Esc, Ctrl+C, digits) only",
	"internal/viewer/partner_view.go tmuxSendKeys":               "partner nudge: Nudge's delivery check",
	"internal/viewer/send_back.go tmuxSendKeys":                  "send-back nudge: Nudge's delivery check",
	"internal/viewer/tmux.go agentrun.Nudge":                     "tmuxSendKeys is Nudge",
	"internal/viewer/tmux.go agentrun.SendKeys":                  "tmuxTypeCommand: shell commands, no agent",
}

// senderNames are this package's text and key senders and the viewer's
// seams over them; tmuxVerbs the tmux commands that type into a pane.
var (
	senderNames = []string{"Paste", "PasteSubmit", "Nudge", "SendKeys", "SendText", "SendKey"}
	viewerSeams = []string{"tmuxSendKeys", "tmuxTypeCommand"}
	tmuxVerbs   = []string{"send-keys", "paste-buffer", "load-buffer"}
)

// Every path that sends text into an agent's pane is listed in paneSenders
// with its delivery check, so a new one can't skip the check unseen.
func TestEveryPaneSenderIsListed(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); n == ".git" || n == ".worktrees" || n == "issues" || n == "node_modules" || n == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "internal/agentrun/") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
		if err != nil {
			return err
		}
		isViewer := strings.HasPrefix(rel, "internal/viewer/")
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectorExpr:
				if id, ok := x.X.(*ast.Ident); ok && id.Name == "agentrun" && slices.Contains(senderNames, x.Sel.Name) {
					found[rel+" agentrun."+x.Sel.Name] = true
				}
			case *ast.CallExpr:
				if id, ok := x.Fun.(*ast.Ident); ok && isViewer && slices.Contains(viewerSeams, id.Name) {
					found[rel+" "+id.Name] = true
				}
				if verb := tmuxVerb(x); verb != "" {
					found[rel+" tmux "+verb] = true
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for key := range found {
		if _, ok := paneSenders[key]; !ok {
			t.Errorf("%s sends into a pane and is not in paneSenders: deliver agent text through agentrun.Paste or Nudge, then list it with its check", key)
		}
	}
	for key := range paneSenders {
		if !found[key] {
			t.Errorf("paneSenders lists %s, which no longer sends into a pane: drop it", key)
		}
	}
}

// tmuxVerb is the typing verb of exec.Command("tmux", verb, …), "" for
// any other call.
func tmuxVerb(call *ast.CallExpr) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || (sel.Sel.Name != "Command" && sel.Sel.Name != "CommandContext") {
		return ""
	}
	args := call.Args
	if sel.Sel.Name == "CommandContext" && len(args) > 0 {
		args = args[1:]
	}
	if len(args) < 2 {
		return ""
	}
	lit := func(e ast.Expr) string {
		if b, ok := e.(*ast.BasicLit); ok && b.Kind == token.STRING {
			s, _ := strconv.Unquote(b.Value)
			return s
		}
		return ""
	}
	if lit(args[0]) != "tmux" {
		return ""
	}
	if v := lit(args[1]); slices.Contains(tmuxVerbs, v) {
		return v
	}
	return ""
}
