package lint

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kacperwerema/dark-factory/internal/outputs"
	"github.com/kacperwerema/dark-factory/internal/queue"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

// bothDevWorkflows is the bundled development template and this repo's
// workflow.yaml, parsed.
func bothDevWorkflows(t *testing.T) map[string]*tracker.WorkflowConfig {
	t.Helper()
	dev, err := workflowupgrade.ReadTemplate("development")
	if err != nil {
		t.Fatal(err)
	}
	repo, err := os.ReadFile("../../workflow.yaml")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*tracker.WorkflowConfig{}
	for name, raw := range map[string][]byte{"development template": dev, "workflow.yaml": repo} {
		var wf tracker.WorkflowConfig
		if err := yaml.Unmarshal(raw, &wf); err != nil {
			t.Fatal(err)
		}
		out[name] = &wf
	}
	return out
}

// The report type walks idea → in progress → human-testing → done: no
// Implementation, Test Plan, Documentation or Shipping, no commit check; the
// report on the shelf gates the owner review, and done is the owner's.
func TestReportTypeHasNoBuildBoxes(t *testing.T) {
	for name, wf := range bothDevWorkflows(t) {
		if _, ok := wf.Types["report"]; !ok {
			t.Errorf("%s: no report type", name)
			continue
		}
		r := wf.ForType("report")
		if got := r.PathLine(); got != "idea → in progress → human-testing → done" {
			t.Errorf("%s: report path = %s", name, got)
		}
		for _, tr := range r.Transitions {
			for _, a := range tr.Actions {
				switch {
				case a.Type == "append_section":
					switch a.Title {
					case "Research", "Owner Review":
					default:
						t.Errorf("%s: report %s → %s appends %s", name, tr.From, tr.To, a.Title)
					}
				case a.Type == "validate" && a.Rule == "commits_reference_issue":
					t.Errorf("%s: report %s → %s checks commits", name, tr.From, tr.To)
				}
			}
		}
		if to, kind, ok := r.HasOutputRule(); !ok || to != "human-testing" || kind != "report" {
			t.Errorf("%s: has_output gate = %q %q %v, want human-testing report", name, to, kind, ok)
		}
		if got := r.RequiredHumanApproval("human-testing", "done"); got != "done" {
			t.Errorf("%s: human-testing → done approval = %q, want done", name, got)
		}
		owner := false
		for _, tr := range r.Transitions {
			for _, a := range tr.Actions {
				if a.Title == "Owner Review" && strings.Contains(a.Body, "(owner)") {
					owner = true
				}
			}
		}
		if !owner {
			t.Errorf("%s: no (owner) box in Owner Review", name)
		}
	}
}

// Adding report changes no other type: the base statuses (the board's
// columns) and every other type's path are as before.
func TestReportTypeLeavesOtherTypesAlone(t *testing.T) {
	base := "idea → in design → backlog → in progress → testing → human-testing → documentation → shipping → done"
	paths := map[string]string{
		"feature": base,
		"tweak":   "idea → in progress → testing → shipping → done",
		"docs":    "idea → in progress → shipping → done",
		"bugfix":  "idea → in progress → testing → human-testing → documentation → shipping → done",
	}
	for name, wf := range bothDevWorkflows(t) {
		if got := wf.PathLine(); got != base {
			t.Errorf("%s: base statuses = %s", name, got)
		}
		for typ, want := range paths {
			scoped := wf.ForType(typ)
			if got := scoped.PathLine(); got != want {
				t.Errorf("%s: %s path = %s, want %s", name, typ, got, want)
			}
			if _, _, ok := scoped.HasOutputRule(); ok {
				t.Errorf("%s: %s gates on has_output", name, typ)
			}
		}
	}
}

func TestCheckReports(t *testing.T) {
	dev, err := workflowupgrade.ReadTemplate("development")
	if err != nil {
		t.Fatal(err)
	}
	issue := func(title, status, typ string) string {
		return "---\ntitle: \"" + title + "\"\nstatus: \"" + status + "\"\ntype: \"" + typ + "\"\ncreated: \"2026-10-31\"\n---\n\nBody.\n"
	}
	env := lintProject(t, string(dev), map[string]string{
		"researching.md": issue("Researching", "in progress", "report"),
		"none.md":        issue("None", "human-testing", "report"),
		"shelved.md":     issue("Shelved", "done", "report"),
		"rejected.md":    issue("Rejected", "done", "report"),
		"gone.md":        issue("Gone", "human-testing", "report"),
		"feature.md":     issue("Feature", "done", "feature"),
	})
	now := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	file := filepath.Join(t.TempDir(), "r.md")
	if err := os.WriteFile(file, []byte("# r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	add := func(slug string) *outputs.Output {
		o, err := outputs.Add(env.Project, outputs.AddInput{Department: "product", Title: slug, Kind: "report", File: file, Issue: slug}, now)
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	add("shelved")
	rej := add("rejected")
	if _, err := outputs.Review(env.Project, rej.ID, outputs.StateRejected, "no", "", "owner", now); err != nil {
		t.Fatal(err)
	}
	gone := add("gone")
	if err := os.Remove(filepath.Join(outputs.FilesDir(env.Project), filepath.FromSlash(gone.File))); err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, f := range run(t, env, "reports") {
		got = append(got, f.Where+": "+f.Message+" | "+f.Fix)
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{
		"none: at human-testing, past the has_output gate into human-testing, but no report on the shelf for none",
		"rejected: at done, past the has_output gate into human-testing, but no report on the shelf for rejected (" + rej.ID + " rejected by the owner",
		"gone: at human-testing, past the has_output gate into human-testing, but no report on the shelf for gone (" + gone.ID + ": its file is gone)",
		`issue-cli output add --issue none --kind report --title "..." --file <file>`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	if len(got) != 3 {
		t.Errorf("got %d findings, want 3 (researching is before the gate, shelved has its report, feature has no gate):\n%s", len(got), all)
	}
}

// A done report whose output lists Follow-ups warns until an issue or a queue
// item quotes the report; each way of quoting clears it, and each kind of
// report that has nothing to pick up stays quiet.
func TestCheckReportsFollowUps(t *testing.T) {
	dev, err := workflowupgrade.ReadTemplate("development")
	if err != nil {
		t.Fatal(err)
	}
	const list = "# R\n\n## 9. Follow-ups (for the lead)\n\n1. Fix one.\n2. Fix two.\n\n## Sources\n\n- a link\n"
	issue := func(title, status, typ, extra, body string) string {
		return "---\ntitle: \"" + title + "\"\nstatus: \"" + status + "\"\ntype: \"" + typ + "\"\ncreated: \"2026-10-31\"\n" + extra + "---\n\n" + body + "\n"
	}
	done := func(title string) string { return issue(title, "done", "report", "", "Body.") }
	env := lintProject(t, string(dev), map[string]string{
		"unpicked.md":    done("Unpicked"),
		"unpicked-2.md":  done("Unpicked 2"),
		"bysluga.md":     done("BySlugA"),
		"byid.md":        done("ById"),
		"byshort.md":     done("ByShort"),
		"bychild.md":     done("ByChild"),
		"byqueue.md":     done("ByQueue"),
		"ownqueue.md":    done("OwnQueue"),
		"noheading.md":   done("NoHeading"),
		"prose.md":       done("Prose"),
		"saysnone.md":    done("SaysNone"),
		"waiting.md":     issue("Waiting", "human-testing", "report", "", "Body."),
		"rejected.md":    done("Rejected"),
		"selfquote.md":   issue("SelfQuote", "done", "report", "", "See selfquote and this very report."),
		"cites-slug.md":  issue("Cites slug", "backlog", "feature", "", "From bysluga, follow-up 1. Not unpicked-2-more."),
		"child.md":       issue("Child", "backlog", "feature", "parent: \"bychild\"\n", "Body."),
		"cites-id.md":    issue("Cites id", "backlog", "feature", "", "IDPLACEHOLDER"),
		"cites-short.md": issue("Cites short", "backlog", "feature", "", "SHORTPLACEHOLDER"),
	})
	now := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	add := func(slug, text string) *outputs.Output {
		file := filepath.Join(t.TempDir(), "r.md")
		if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		o, err := outputs.Add(env.Project, outputs.AddInput{Department: "product", Title: slug, Kind: "report", File: file, Issue: slug}, now)
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	unpicked := add("unpicked", list)
	add("unpicked-2", list)
	add("bysluga", list)
	byID := add("byid", list)
	byShort := add("byshort", list)
	add("bychild", list)
	add("byqueue", list)
	add("ownqueue", list)
	add("noheading", "# R\n\n- not a follow-up\n")
	add("prose", "# R\n\n## Follow-ups\n\nCode fixes: none.\n")
	add("saysnone", "# R\n\n## Follow-ups\n\n- None.\n")
	add("waiting", list)
	add("selfquote", list)
	add("rejected", "# R\n")
	rej := add("rejected", list)
	if _, err := outputs.Review(env.Project, rej.ID, outputs.StateRejected, "no", "", "owner", now); err != nil {
		t.Fatal(err)
	}
	short := byShort.ID[strings.LastIndex(byShort.ID, "-")+1:]
	for _, c := range []struct{ file, was, now string }{
		{"cites-id.md", "IDPLACEHOLDER", "From " + byID.ID + "."},
		{"cites-short.md", "SHORTPLACEHOLDER", "From shelf output " + short + ", item 2."},
	} {
		path := filepath.Join(env.Project.IssueDir, c.file)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(strings.Replace(string(raw), c.was, c.now, 1)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := queue.Update(env.Project.IssueDir, func(q *queue.Queue) error {
		q.Items = append(q.Items,
			queue.Item{ID: 1, Idea: "Fix one (from byqueue)", State: queue.Open},
			queue.Item{ID: 2, Slug: "ownqueue", Quote: "ownqueue, start it", State: queue.Started})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := env.Project.LoadIssues()
	if err != nil {
		t.Fatal(err)
	}
	env.Issues = reloaded

	var got []string
	for _, f := range run(t, env, "reports") {
		got = append(got, f.Where)
		if f.Where == "unpicked" {
			if !strings.Contains(f.Message, unpicked.ID+" lists 2 follow-up(s) and no issue or queue item quotes the report") {
				t.Errorf("message = %s", f.Message)
			}
			if !strings.Contains(f.Fix, `issue-cli queue add --idea "<the follow-up> (from unpicked)"`) {
				t.Errorf("fix = %s", f.Fix)
			}
		}
	}
	want := "ownqueue,selfquote,unpicked,unpicked-2"
	sort.Strings(got)
	if strings.Join(got, ",") != want {
		t.Errorf("warned on %s, want %s", strings.Join(got, ","), want)
	}
}

func TestNamesAnyWholeNamesOnly(t *testing.T) {
	for text, want := range map[string]bool{
		"from a/report.":      true,
		"a/report#D-2":        true,
		"see a/report-more":   false,
		"xa/report":           false,
		"a/report-2 a/report": true,
	} {
		if got := namesAny(text, []string{"a/report"}); got != want {
			t.Errorf("namesAny(%q) = %v", text, got)
		}
	}
}

// followUpEnv is a done report per entry of reports, each output's text in
// order of adding, an hour apart.
func followUpEnv(t *testing.T, reports map[string][]string) (*Env, map[string][]*outputs.Output) {
	t.Helper()
	dev, err := workflowupgrade.ReadTemplate("development")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for slug := range reports {
		files[slug+".md"] = "---\ntitle: \"" + slug + "\"\nstatus: \"done\"\ntype: \"report\"\ncreated: \"2026-11-14\"\n---\n\nBody.\n"
	}
	env := lintProject(t, string(dev), files)
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	added := map[string][]*outputs.Output{}
	for slug, texts := range reports {
		for i, text := range texts {
			file := filepath.Join(t.TempDir(), "r.md")
			if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
			o, err := outputs.Add(env.Project, outputs.AddInput{Department: "product", Title: slug, Kind: "report", File: file, Issue: slug}, now.Add(time.Duration(i)*time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			added[slug] = append(added[slug], o)
		}
	}
	return env, added
}

// The owner's calls are no work for an issue or the queue: a list whose
// items all end "(owner)", or sit under a sub-heading or lead-in naming the
// owner, warns no more.
func TestReportsSkipsOwnerFollowUps(t *testing.T) {
	env, _ := followUpEnv(t, map[string][]string{
		"marked":  {"# R\n\n## Follow-ups\n\n1. Pick a blog host (owner)\n2. Approve the listings (owner).\n"},
		"heading": {"# R\n\n## Follow-ups\n\n### Owner actions\n\n- Pick a blog host\n- Approve the listings\n"},
		"lead-in": {"# R\n\n## Follow-ups\n\nCode fixes: **none**. The research found no bug.\n\nMarketing actions, each the owner's call because each publishes outside the machine:\n1. Pick a blog host.\n2. Approve the listings.\n"},
	})
	if got := run(t, env, "reports"); len(got) != 0 {
		t.Errorf("owner-only follow-ups warned: %+v", got)
	}
}

// A report put on the shelf twice (the competitor scan's o-…-b871 and
// o-…-d5d9) is one report: one finding, naming the newest output.
func TestReportsOneFindingPerIssue(t *testing.T) {
	list := "# R\n\n## Follow-ups\n\n1. Fix one.\n2. Fix two.\n"
	env, added := followUpEnv(t, map[string][]string{"twice": {list, list + "3. Fix three.\n"}})
	got := run(t, env, "reports")
	if len(got) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(got), got)
	}
	newest := added["twice"][1]
	if want := newest.ID + " lists 3 follow-up(s) (as do 1 more output(s) of this report) and no issue or queue item quotes the report"; !strings.Contains(got[0].Message, want) {
		t.Errorf("message = %s\nwant it to hold %s", got[0].Message, want)
	}
	if !strings.Contains(got[0].Fix, `mark the owner's items "(owner)"`) {
		t.Errorf("fix = %s", got[0].Fix)
	}
}

// A mixed list counts only what an issue or the queue has to pick up.
func TestReportsCountsOnlyAgentFollowUps(t *testing.T) {
	env, added := followUpEnv(t, map[string][]string{
		"mixed": {"# R\n\n## Follow-ups\n\n- Fix the parser.\n- Pick a blog host (owner)\n\nOwner actions:\n- Approve the listings\n\n### Code\n\n- Fix the lint.\n"},
	})
	got := run(t, env, "reports")
	if len(got) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(got), got)
	}
	if want := added["mixed"][0].ID + " lists 2 follow-up(s) and"; !strings.Contains(got[0].Message, want) {
		t.Errorf("message = %s\nwant it to hold %s", got[0].Message, want)
	}
}
