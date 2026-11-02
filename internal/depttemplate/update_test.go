package depttemplate

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/kacperwerema/dark-factory/cmd/issue-cli/templates"
	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

// TestBundledTemplatesNeverSayClient: the department templates are for a
// product company with its own site as much as for an agency, so no
// bundled text (department.yaml, role.md, job briefs, the workflow file a
// template installs) speaks of clients.
func TestBundledTemplatesNeverSayClient(t *testing.T) {
	client := regexp.MustCompile(`(?i)\bclients?\b`)
	files := map[string][]byte{}
	err := fs.WalkDir(templates.FS, templatesDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := fs.ReadFile(templates.FS, p)
		files[p] = raw
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tm := range List() {
		if tm.Workflow != "" {
			raw, err := workflowupgrade.ReadTemplate(tm.Workflow)
			if err != nil {
				t.Fatal(err)
			}
			files[path.Join("workflow", tm.Workflow+".yaml")] = raw
		}
	}
	if len(files) < 6 {
		t.Fatalf("read only %d template files", len(files))
	}
	for p, raw := range files {
		for i, line := range strings.Split(string(raw), "\n") {
			if client.MatchString(line) {
				t.Errorf("%s:%d says client: %s", p, i+1, strings.TrimSpace(line))
			}
		}
	}
	m, _ := Load("marketing")
	if !strings.Contains(string(m.RolePrompt), "your site(s)") || !strings.Contains(m.Role, "your site(s)") {
		t.Errorf("marketing role should speak of your site(s): %q / %q", m.Role, m.RolePrompt[:120])
	}
}

// TestEarlierHashesAreBacked: each earlier hash is the sha256 of a file
// kept under testdata/earlier/<key>/<path>.<n> (the text that release
// shipped), every such file is listed, none is today's text, and no earlier
// value is today's (department update would call an up-to-date one old).
func TestEarlierHashesAreBacked(t *testing.T) {
	onDisk := map[string]map[string][]string{} // key -> template path -> hashes
	base := filepath.Join("testdata", "earlier")
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(base, p)
		rel = filepath.ToSlash(rel)
		key, file, _ := strings.Cut(rel, "/")
		file = strings.TrimSuffix(file, path.Ext(file)) // drop .<n>
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if onDisk[key] == nil {
			onDisk[key] = map[string][]string{}
		}
		onDisk[key][file] = append(onDisk[key][file], sha(raw))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for key, e := range earlierTexts {
		tm, err := Load(key)
		if err != nil {
			t.Fatalf("earlier texts for %s: %v", key, err)
		}
		for rel, sums := range e.Files {
			cur, err := tm.file(rel)
			if err != nil {
				t.Fatalf("%s: %v", key, err)
			}
			have := onDisk[key][rel]
			slices.Sort(have)
			want := slices.Clone(sums)
			slices.Sort(want)
			if !slices.Equal(have, want) {
				t.Errorf("%s/%s: hashes %v, testdata files hash to %v", key, rel, want, have)
			}
			for _, s := range sums {
				if s == sha(cur) {
					t.Errorf("%s/%s: earlier hash %s is the current text", key, rel, s)
				}
			}
		}
		for rel := range onDisk[key] {
			if _, ok := e.Files[rel]; !ok {
				t.Errorf("testdata/earlier/%s/%s has no hashes in earlierTexts", key, rel)
			}
		}
		if slices.Contains(e.Role, tm.Role) || slices.Contains(e.Mission, tm.Mission) {
			t.Errorf("%s: an earlier role or mission is the current one", key)
		}
		for id, names := range e.GoalNames {
			g := tm.goal(id)
			if g == nil {
				t.Errorf("%s: earlier goal name for unknown goal %s", key, id)
			} else if slices.Contains(names, g.Name) {
				t.Errorf("%s: earlier name of goal %s is the current one", key, id)
			}
		}
	}
	for key := range onDisk {
		if _, ok := earlierTexts[key]; !ok {
			t.Errorf("testdata/earlier/%s: no earlierTexts entry", key)
		}
	}
}

// TestEarlierWorkflowsDifferInWordingOnly: an earlier workflow text is
// listed only when its YAML has today's shape (the same keys, lists and
// lengths) and only text values or comments differ, so department update
// never adopts a catalog feature (a gate, a status, a features: record)
// that workflow upgrade should offer.
func TestEarlierWorkflowsDifferInWordingOnly(t *testing.T) {
	for key, e := range earlierTexts {
		if len(e.Files["workflow"]) == 0 {
			continue
		}
		tm, _ := Load(key)
		cur, _ := tm.file("workflow")
		var want yaml.Node
		if err := yaml.Unmarshal(cur, &want); err != nil {
			t.Fatal(err)
		}
		files, _ := filepath.Glob(filepath.Join("testdata", "earlier", key, "workflow.*"))
		if len(files) == 0 {
			t.Fatalf("%s: no earlier workflow testdata", key)
		}
		for _, f := range files {
			raw, _ := os.ReadFile(f)
			var got yaml.Node
			if err := yaml.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if where := shapeDiff(&got, &want, ""); where != "" {
				t.Errorf("%s differs from today's %s workflow in more than wording, at %s", f, key, where)
			}
		}
	}
}

// shapeDiff is where a and b differ other than in a text value, "" when
// they don't. Mapping keys, node kinds, list lengths and every value but a
// description: or prompt: must match. The features: record's installed:
// may list the same refs in another order (the catalog's order changed):
// that adopts nothing.
func shapeDiff(a, b *yaml.Node, at string) string {
	if a.Kind != b.Kind || len(a.Content) != len(b.Content) {
		return at + " (kind or length)"
	}
	if a.Kind == yaml.SequenceNode && strings.HasSuffix(at, "/features/installed") {
		var x, y []string
		for i := range a.Content {
			x, y = append(x, a.Content[i].Value), append(y, b.Content[i].Value)
		}
		slices.Sort(x)
		slices.Sort(y)
		if !slices.Equal(x, y) {
			return at + " (refs)"
		}
		return ""
	}
	if a.Kind == yaml.ScalarNode {
		// Text may change only in a description: or prompt: value, and in
		// the template: stamp, which every catalog bump moves.
		key := at[strings.LastIndex(at, "/")+1:]
		if a.Tag != b.Tag || a.Value != b.Value && key != "description" && key != "prompt" && at != "/0/template" {
			return at
		}
		return ""
	}
	for i := range a.Content {
		next := fmt.Sprintf("%s/%d", at, i)
		if a.Kind == yaml.MappingNode {
			if i%2 == 0 {
				if a.Content[i].Value != b.Content[i].Value {
					return at + "/" + a.Content[i].Value + " (key)"
				}
				continue
			}
			next = at + "/" + a.Content[i-1].Value
		}
		if d := shapeDiff(a.Content[i], b.Content[i], next); d != "" {
			return d
		}
	}
	return ""
}

const oldMarketingConfig = `# owner's config
projects:
  - name: Acme   # the sandbox
    issues: ./acme/issues
    workdir: ROOT
    departments:
      - name: Marketing
        color: '#E2559B'
        role: SEO and content for the client sites
        mission: "Every client site refreshed every week"   # mine
        systems: [SEO]
        prompt: departments/marketing/role.md
        workflow: workflows/marketing.yaml
        goals:
          - id: pages
            name: Client pages refreshed
            target: 10
            period: week
          - id: posts
            name: Posts we shipped
            target: 2
            period: week
        jobs:
          - id: weekly-seo-pass
            name: Weekly SEO pass
            every: week
            on: mon
            at: "07:00"
            brief: departments/marketing/jobs/weekly-seo-pass.md
            type: seo-pass
`

// oldMarketing is a project with Marketing installed from the earlier
// template: role.md untouched, the job's brief edited by the owner.
func oldMarketing(t *testing.T) (string, string) {
	t.Helper()
	cfg, proj := sandbox(t, oldMarketingConfig)
	root := proj.Root()
	old, err := os.ReadFile(filepath.Join("testdata", "earlier", "marketing", "role.md.1"))
	if err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(root, "departments", "marketing", "role.md"), string(old))
	mustWriteFile(t, filepath.Join(root, "departments", "marketing", "jobs", "weekly-seo-pass.md"), "Our own brief.\n")
	// The workflow file as an earlier release wrote it, never upgraded.
	wf, err := os.ReadFile(filepath.Join("testdata", "earlier", "marketing", "workflow.1"))
	if err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(root, "workflows", "marketing.yaml"), string(wf))
	return cfg, root
}

func mustWriteFile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateReplacesOnlyUntouchedEarlierText(t *testing.T) {
	cfg, root := oldMarketing(t)
	proj := loadProject(t, cfg, "acme")
	tm, _ := Load("marketing")
	u, err := NewUpdate(proj, tm)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, it := range u.Items {
		got[it.What] = it.State
	}
	want := map[string]string{
		"departments/marketing/role.md":                 UpdateOld,
		"departments/marketing/jobs/weekly-seo-pass.md": UpdateEdited,
		"workflows/marketing.yaml":                      UpdateOld,
		"role":                                          UpdateOld,
		"mission":                                       UpdateOld,
		"goal pages name":                               UpdateOld,
		"goal posts name":                               UpdateEdited,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q (items %v)", k, got[k], v, got)
		}
	}
	if u.Pending() != 5 {
		t.Fatalf("pending = %d, want 5", u.Pending())
	}
	before, _ := os.ReadFile(cfg)
	if err := u.Apply(proj); err != nil {
		t.Fatal(err)
	}
	role, _ := os.ReadFile(filepath.Join(root, "departments", "marketing", "role.md"))
	if string(role) != string(tm.RolePrompt) {
		t.Fatalf("role.md not updated:\n%s", role)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "departments", "marketing", "jobs", "weekly-seo-pass.md")); string(b) != "Our own brief.\n" {
		t.Fatalf("edited brief overwritten: %q", b)
	}
	wantWF, _ := workflowupgrade.ReadTemplate("marketing")
	if b, _ := os.ReadFile(filepath.Join(root, "workflows", "marketing.yaml")); string(b) != string(wantWF) {
		t.Fatal("the untouched earlier workflow file was not brought to the new text")
	}
	sides, err := u.SideFiles()
	if err != nil || len(sides) != 1 || sides[0].Path != "departments/marketing/jobs/weekly-seo-pass.template.md" || sides[0].Kept {
		t.Fatalf("side files = %+v, %v", sides, err)
	}
	side := filepath.Join(root, "departments", "marketing", "jobs", "weekly-seo-pass.template.md")
	mustWriteFile(t, side, "my notes\n")
	if sides, _ := u.SideFiles(); len(sides) != 1 || !sides[0].Kept {
		t.Fatalf("an edited side file: %+v", sides)
	}
	if b, _ := os.ReadFile(side); string(b) != "my notes\n" {
		t.Fatalf("side file overwritten: %q", b)
	}
	after, _ := os.ReadFile(cfg)
	s := string(after)
	for _, w := range []string{"role: " + tm.Role, "mission: " + tm.Mission, `name: ` + tm.goal("pages").Name, "name: Posts we shipped", "# owner's config", "# the sandbox", "# mine"} {
		if !strings.Contains(s, w) {
			t.Errorf("projects.yaml lacks %q:\n%s", w, s)
		}
	}
	if strings.Count(string(before), "\n") != strings.Count(s, "\n") {
		t.Errorf("line count changed:\n%s", s)
	}
	proj = loadProject(t, cfg, "acme")
	u, err = NewUpdate(proj, tm)
	if err != nil {
		t.Fatal(err)
	}
	if u.Pending() != 0 {
		t.Fatalf("pending after update = %d: %+v", u.Pending(), u.Items)
	}
}

func TestUpdatesListsOnlyInstalledTemplatesWithSomethingToDo(t *testing.T) {
	cfg, _ := oldMarketing(t)
	proj := loadProject(t, cfg, "acme")
	us := Updates(proj)
	if len(us) != 1 || us[0].Template.Key != "marketing" || us[0].Pending() == 0 {
		t.Fatalf("updates = %+v", us)
	}
	_, fresh := sandbox(t, commentedConfig)
	if us := Updates(fresh); len(us) != 0 {
		t.Fatalf("a project without departments has updates: %+v", us)
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestUntrackedAndCommit(t *testing.T) {
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t", "GIT_CONFIG_GLOBAL": "/dev/null"} {
		t.Setenv(k, v)
	}
	root := t.TempDir()
	if got, err := Untracked(root, []string{"departments/x"}); err != nil || got != nil {
		t.Fatalf("no repo: %v %v", got, err)
	}
	gitRun(t, root, "init", "-q", "-b", "main")
	gitRun(t, root, "commit", "-q", "--allow-empty", "-m", "init")
	mustWriteFile(t, filepath.Join(root, "departments", "x", "role.md"), "r\n")
	mustWriteFile(t, filepath.Join(root, "departments", "x", "jobs", "a.md"), "a\n")
	mustWriteFile(t, filepath.Join(root, "workflows", "x.yaml"), "w\n")
	mustWriteFile(t, filepath.Join(root, "other.txt"), "mine\n")
	mustWriteFile(t, filepath.Join(root, ".gitignore"), "departments/x/secret.md\n")
	mustWriteFile(t, filepath.Join(root, "departments", "x", "secret.md"), "s\n")
	paths := []string{"departments/x/role.md", "workflows/x.yaml", "departments/x", "workflows/gone.yaml"}
	got, err := Untracked(root, paths)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "departments/x/jobs/a.md,departments/x/role.md,workflows/x.yaml" {
		t.Fatalf("untracked = %v", got)
	}
	sha, err := CommitFiles(root, got, "chore(departments): add X")
	if err != nil || sha == "" {
		t.Fatalf("commit: %q %v", sha, err)
	}
	if got, _ := Untracked(root, paths); len(got) != 0 {
		t.Fatalf("after commit: %v", got)
	}
	out, _ := exec.Command("git", "-C", root, "status", "--porcelain").Output()
	if !strings.Contains(string(out), "?? other.txt") {
		t.Fatalf("commit took other files: %s", out)
	}

	// A commit a hook refuses leaves nothing staged for the next commit.
	mustWriteFile(t, filepath.Join(root, "departments", "y", "role.md"), "y\n")
	hook := filepath.Join(root, ".git", "hooks", "pre-commit")
	mustWriteFile(t, hook, "#!/bin/sh\nexit 1\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := CommitFiles(root, []string{"departments/y/role.md"}, "chore(departments): add Y"); err == nil {
		t.Fatal("the hook should refuse the commit")
	}
	if out, _ := exec.Command("git", "-C", root, "diff", "--cached", "--name-only").Output(); len(out) != 0 {
		t.Fatalf("left staged after a failed commit: %s", out)
	}
}
