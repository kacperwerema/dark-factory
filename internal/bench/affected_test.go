package bench

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

func TestABranchsFilesMarkTheWorkflowsTheyReach(t *testing.T) {
	sources := []WorkflowSource{
		{Key: ProductWorkflow, File: "workflow.yaml", Template: "development"},
		{Key: "marketing", File: "workflows/marketing.yaml", Template: "marketing", Prompt: "prompts/marketing.md", Folder: "departments/marketing/"},
		{Key: "content", File: "workflows/content.yaml", Template: "marketing", Folder: "departments/content/"},
	}
	cases := []struct {
		name    string
		files   []string
		changed map[string][]string
		all     []string
		none    []string
	}{
		{name: "a department's own file", files: []string{"workflows/marketing.yaml"},
			changed: map[string][]string{"marketing": {"workflows/marketing.yaml"}}},
		{name: "Product's file", files: []string{"workflow.yaml"},
			changed: map[string][]string{ProductWorkflow: {"workflow.yaml"}}},
		{name: "a department's prompt and folder", files: []string{"prompts/marketing.md", "departments/content/brand.md"},
			changed: map[string][]string{"marketing": {"prompts/marketing.md"}, "content": {"departments/content/brand.md"}}},
		{name: "a bundled template reaches every workflow stamped with it", files: []string{"cmd/issue-cli/templates/workflow/marketing.yaml"},
			changed: map[string][]string{"marketing": {"cmd/issue-cli/templates/workflow/marketing.yaml"}, "content": {"cmd/issue-cli/templates/workflow/marketing.yaml"}}},
		{name: "a template nobody is stamped with", files: []string{"cmd/issue-cli/templates/workflow/review.yaml"},
			none: []string{"cmd/issue-cli/templates/workflow/review.yaml"}},
		{name: "engine code reaches all", files: []string{"internal/tracker/workflow.go", "cmd/issue-cli/cmd_start.go", "CLAUDE.md", "go.mod"},
			all: []string{"internal/tracker/workflow.go", "cmd/issue-cli/cmd_start.go", "CLAUDE.md", "go.mod"}},
		{name: "docs and viewer code reach none", files: []string{"docs/benchmark.md", "static/style.css", "handlers_list.go"},
			none: []string{"docs/benchmark.md", "static/style.css", "handlers_list.go"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := AffectedWorkflows(c.files, sources)
			if c.changed == nil {
				c.changed = map[string][]string{}
			}
			if c.all == nil {
				c.all = []string{}
			}
			if c.none == nil {
				c.none = []string{}
			}
			if !reflect.DeepEqual(a.Changed, c.changed) || !reflect.DeepEqual(a.All, c.all) || !reflect.DeepEqual(a.NotMapped, c.none) {
				t.Errorf("got changed %v all %v none %v", a.Changed, a.All, a.NotMapped)
			}
			if a.Files() != len(c.files) {
				t.Errorf("Files() = %d, want %d", a.Files(), len(c.files))
			}
		})
	}

	a := AffectedWorkflows([]string{"workflows/marketing.yaml"}, sources)
	if m, files := a.Mark("marketing"); m != MarkChanged || !reflect.DeepEqual(files, []string{"workflows/marketing.yaml"}) {
		t.Errorf("marketing: %s %v", m, files)
	}
	if m, files := a.Mark(ProductWorkflow); m != MarkControl || files != nil {
		t.Errorf("product: %s %v", m, files)
	}
	a = AffectedWorkflows([]string{"internal/bench/run.go"}, sources)
	if m, _ := a.Mark("content"); m != MarkChanged {
		t.Errorf("engine code leaves content a %s", m)
	}
}

func TestWorkflowSourcesReadTheStampAsTheBranchHasIt(t *testing.T) {
	repo := t.TempDir()
	for rel, data := range map[string]string{
		"workflow.yaml":            "template: development@v0.58.0\nstatuses: [{name: a}]\n",
		"workflows/marketing.yaml": "template: marketing@v0.58.0\nstatuses: [{name: a}]\n",
	} {
		p := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	proj := &tracker.Project{Slug: "shop", IssueDir: filepath.Join(repo, "issues"), WorkflowFile: filepath.Join(repo, "workflow.yaml"), WorkDir: repo,
		Departments: []tracker.Department{{Name: "Marketing", Workflow: "workflows/marketing.yaml", Prompt: "prompts/mkt.md"}, {Name: "Sales"}}}
	// The branch restamps marketing's file as content.
	read := func(rel string) ([]byte, error) {
		if rel == "workflows/marketing.yaml" {
			return []byte("template: content@v0.58.0\n"), nil
		}
		return os.ReadFile(filepath.Join(repo, rel))
	}
	got := WorkflowSources(proj, repo, read)
	want := []WorkflowSource{
		{Key: ProductWorkflow, File: "workflow.yaml", Template: "development"},
		{Key: "marketing", File: "workflows/marketing.yaml", Template: "content", Prompt: "prompts/mkt.md", Folder: "departments/marketing/"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sources:\n got %+v\nwant %+v", got, want)
	}
	// No project: Product's file at the root and engine code only.
	if got := WorkflowSources(nil, repo, nil); len(got) != 1 || got[0].File != "workflow.yaml" {
		t.Errorf("no project: %+v", got)
	}
}

func TestNoisyControlsNameOnlyControlsThatGotWorse(t *testing.T) {
	vs := []WorkflowVerdict{
		{Workflow: "product", Mark: MarkControl, Verdict: NetNegative},
		{Workflow: "marketing", Mark: MarkChanged, Verdict: NetNegative},
		{Workflow: "content", Mark: MarkControl, Verdict: NoChange},
	}
	got := NoisyControls(vs)
	if len(got) != 1 || !strings.HasPrefix(got[0], "control product is net negative") {
		t.Errorf("got %v", got)
	}
	if s := FilesText([]string{"a", "b", "c", "d", "e", "f", "g"}); s != "a, b, c, d, e +2 more" {
		t.Errorf("FilesText = %q", s)
	}
}
