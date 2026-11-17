package viewer

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/postmortem"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// tillCopy saves issue till's post-mortem (the same actions as cart's, a
// day later) and returns its path.
func tillCopy(t *testing.T, proj tracker.Project) string {
	t.Helper()
	os.WriteFile(filepath.Join(proj.IssueDir, "till.md"), []byte("---\ntitle: \"Till\"\nslug: \"till\"\nstatus: \"done\"\n---\n\nBody.\n"), 0o644)
	p, err := postmortem.Save(filepath.Join(proj.Root(), "retros"), postmortem.Meta{Issue: "till", Title: "Till", Status: "done"}, pmRetroBody, time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func markItems(act string, note string, rows ...[2]string) string {
	var b strings.Builder
	b.WriteString(`{"act":"` + act + `","note":"` + note + `","items":[`)
	for i, r := range rows {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"file":"` + r[0] + `","id":"` + r[1] + `"}`)
	}
	b.WriteString("]}")
	return b.String()
}

// AC2: one Agree on a group of copies applies once (one memory, one
// experiment) and ticks every copy with the same owner words; each issue's
// trail says so.
func TestPostmortemsAgreeGroupAppliesOnce(t *testing.T) {
	proj, url, cart := postmortemSandbox(t)
	till := tillCopy(t, proj)
	issues, _ := proj.LoadIssues()
	body := markItems("agree", "one for all",
		[2]string{filepath.Base(till), "A1"}, [2]string{filepath.Base(cart), "A1"},
		[2]string{filepath.Base(till), "A3"}, [2]string{filepath.Base(cart), "A3"})
	code, out := postMark(t, url, body, nil)
	if code != 200 || out["ok"] != true {
		t.Fatalf("agree: %d %v", code, out)
	}
	set, _ := memory.ProjectStore(proj.IssueDir).Load()
	if n := len(set.Active(time.Now())); n != 1 {
		t.Errorf("%d memories, want one for the group", n)
	}
	after, _ := proj.LoadIssues()
	if len(after) != len(issues)+1 {
		t.Errorf("%d issues filed, want one experiment for the group", len(after)-len(issues))
	}
	for _, p := range []string{cart, till} {
		raw, _ := os.ReadFile(p)
		acts := postmortem.Actions(string(raw))
		for _, i := range []int{0, 2} {
			if !acts[i].Done || !strings.HasSuffix(acts[i].Applied, "(owner): one for all") {
				t.Errorf("%s %s: %+v", filepath.Base(p), acts[i].ID, acts[i])
			}
		}
		if strings.Contains(p, "cart") && !strings.HasPrefix(postmortem.Actions(string(raw))[0].Applied, "as "+filepath.Base(till)+"#A1: memory m-") {
			t.Errorf("cart's copy isn't ticked as till's: %q", postmortem.Actions(string(raw))[0].Applied)
		}
	}
	for _, slug := range []string{"cart", "till"} {
		comments, _ := tracker.LoadComments(filepath.Join(proj.IssueDir, slug+".md"))
		if len(comments) != 1 || !strings.Contains(comments[0].Text, "A1, A3 agreed on the actions page") {
			t.Errorf("%s's trail: %+v", slug, comments)
		}
	}
	// The round on the post-mortem page now asks only what is left.
	if _, page := getResp(t, url+"/p/acme/postmortems"); !strings.Contains(page, `data-testid="postmortems.count">4</span>`) {
		t.Error("the queue still counts the agreed copies")
	}
}

// AC2: when the group's first copy can't apply, no copy is marked.
func TestPostmortemsAgreeGroupFailsWhole(t *testing.T) {
	proj, url, cart := postmortemSandbox(t)
	till := tillCopy(t, proj)
	// A2 goes under "## Validation" in CLAUDE.md: gone, it can't apply.
	os.WriteFile(filepath.Join(proj.Root(), "CLAUDE.md"), []byte("# Acme\n"), 0o644)
	gitIn(t, proj.Root(), "commit", "-qam", "no validation heading")
	code, out := postMark(t, url, markItems("agree", "", [2]string{filepath.Base(till), "A2"}, [2]string{filepath.Base(cart), "A2"}), nil)
	if code != 200 || out["ok"] != false {
		t.Fatalf("agree: %d %v", code, out)
	}
	outs, _ := out["outcomes"].([]any)
	if len(outs) != 2 || !strings.Contains(outs[1].(map[string]any)["error"].(string), "not agreed: its copy") {
		t.Fatalf("outcomes: %v", outs)
	}
	for _, p := range []string{cart, till} {
		raw, _ := os.ReadFile(p)
		if a := postmortem.Actions(string(raw))[1]; a.Decided() {
			t.Errorf("%s A2 marked: %+v", filepath.Base(p), a)
		}
	}
}

// AC2: Agree is the owner's, like the other marks.
func TestPostmortemsAgreeIsTheOwners(t *testing.T) {
	proj, url, cart := postmortemSandbox(t)
	before, _ := os.ReadFile(cart)
	if code, out := postMark(t, url, markItems("agree", "", [2]string{filepath.Base(cart), "A1"}), map[string]string{strangerHeader: "1"}); code != http.StatusForbidden {
		t.Fatalf("an agent's agree: %d %v", code, out)
	}
	if after, _ := os.ReadFile(cart); string(after) != string(before) {
		t.Fatal("a refused agree changed the retro")
	}
	if set, _ := memory.ProjectStore(proj.IssueDir).Load(); len(set.Active(time.Now())) != 0 {
		t.Fatal("a refused agree made a memory")
	}
}

// AC3: an action linked to a done issue shows as "fixed by <slug>" and
// leaves what waits: the queue's rows, its count and Needs you's; one linked
// to an open issue still waits and says the fix is underway.
func TestPostmortemsFixedNeedNoAnswer(t *testing.T) {
	proj, url, cart := postmortemSandbox(t)
	os.WriteFile(filepath.Join(proj.IssueDir, "lock-fix.md"), []byte("---\ntitle: \"Lock fix\"\nslug: \"lock-fix\"\nstatus: \"done\"\n---\n\nBody.\n"), 0o644)
	os.WriteFile(filepath.Join(proj.IssueDir, "docs-fix.md"), []byte("---\ntitle: \"Docs fix\"\nslug: \"docs-fix\"\nstatus: \"in progress\"\n---\n\nBody.\n"), 0o644)
	if err := postmortem.LinkFix(cart, "A1", "lock-fix"); err != nil {
		t.Fatal(err)
	}
	if err := postmortem.LinkFix(cart, "A4", "docs-fix"); err != nil {
		t.Fatal(err)
	}
	_, page := getResp(t, url+"/p/acme/postmortems")
	for _, want := range []string{
		`data-testid="postmortems.count">3</span>`,
		`data-testid="postmortems.fixed.row.cart.a1.by">fixed by lock-fix</span>`,
		`data-testid="postmortems.row.fix">fix underway: docs-fix (in progress)</span>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if strings.Contains(page, `data-testid="postmortems.row.cart.a1"`) {
		t.Error("the fixed action is still a row to answer")
	}
	for _, r := range needsYouRows(&proj, "", "/p/acme", nil, time.Now()) {
		if r.Kind == "postmortem" && !strings.Contains(r.Note, "3 actions wait") {
			t.Errorf("Needs you: %q", r.Note)
		}
	}
	code, out := postMark(t, url, markItems("refuse", "", [2]string{filepath.Base(cart), "A1"}), nil)
	if code != 200 || out["ok"] != false {
		t.Errorf("a fixed action took an answer: %d %v", code, out)
	}
}
