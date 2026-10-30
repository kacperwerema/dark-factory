package outputs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// project is a project rooted in a temp dir with one department.
func project(t *testing.T) *tracker.Project {
	t.Helper()
	root := t.TempDir()
	issues := filepath.Join(root, "issues")
	if err := os.MkdirAll(issues, 0o755); err != nil {
		t.Fatal(err)
	}
	return &tracker.Project{
		Slug: "acme", IssueDir: issues, WorkDir: root,
		Departments: []tracker.Department{{Name: "Marketing", Systems: []string{"SEO"}}},
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Thursday 2026-10-29: the week starts Monday 10-26.
var now = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

func TestAddURLPathAndFile(t *testing.T) {
	p := project(t)
	writeFile(t, filepath.Join(p.WorkDir, "docs/report.md"), "# report")
	src := filepath.Join(t.TempDir(), "hero.png")
	writeFile(t, src, "png")

	page, err := Add(p, AddInput{Department: "marketing", Title: "Acme FAQ", Kind: "Page", URL: "https://acme.example/faq", Issue: "seo/acme", By: "agent-seo-acme"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if page.State != StateReview || page.Kind != "page" || page.Name != "Marketing" || page.Icon() != "📄" {
		t.Errorf("page = %+v", page)
	}
	if !strings.HasPrefix(page.ID, "o-20261029-") || len(page.ID) != len("o-20261029-abcd") {
		t.Errorf("id = %q", page.ID)
	}
	rep, err := Add(p, AddInput{Department: "product", Title: "Weekly report", Kind: "report", Path: "docs/report.md"}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Path != "docs/report.md" || rep.Missing {
		t.Errorf("report = %+v", rep)
	}
	img, err := Add(p, AddInput{Department: "marketing", Title: "Hero", Kind: "banner", File: src, Job: "weekly-pass"}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if img.File != "marketing/"+img.ID+"-hero.png" || !img.IsImage() || img.Icon() != "📦" {
		t.Errorf("image = %+v", img)
	}
	if b, err := os.ReadFile(filepath.Join(FilesDir(p), "marketing", img.ID+"-hero.png")); err != nil || string(b) != "png" {
		t.Errorf("copied file: %q %v", b, err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("--file must copy, not move: %v", err)
	}

	list, bad := Load(p)
	if len(bad) != 0 || len(list) != 3 || list[0].ID != img.ID || list[2].ID != page.ID {
		t.Fatalf("load = %v bad %v", ids(list), bad)
	}
	c := Counts(list, now)
	if c["marketing"].ToReview != 2 || c["marketing"].Week != 2 || c["product"].Total != 1 {
		t.Errorf("counts = %+v", c)
	}
	if got := Select(list, Filter{Issue: "SEO/acme"}); len(got) != 1 || got[0].ID != page.ID {
		t.Errorf("by issue = %v", ids(got))
	}

	// A path gone later shows as missing.
	os.Remove(filepath.Join(p.WorkDir, "docs/report.md"))
	list, _ = Load(p)
	if o, _ := Find(list, rep.ID); !o.Missing {
		t.Errorf("removed path not missing: %+v", o)
	}
}

func TestAddRefusals(t *testing.T) {
	p := project(t)
	big := filepath.Join(t.TempDir(), "big.bin")
	f, _ := os.Create(big)
	f.Truncate(MaxFileBytes + 1)
	f.Close()
	cases := []struct {
		in   AddInput
		want string
	}{
		{AddInput{Department: "marketing", Kind: "post", URL: "https://x.example"}, "--title"},
		{AddInput{Department: "marketing", Title: "t", URL: "https://x.example"}, "--kind"},
		{AddInput{Department: "marketing", Title: "t", Kind: "post"}, "exactly one"},
		{AddInput{Department: "marketing", Title: "t", Kind: "post", URL: "https://x.example", Path: "a"}, "exactly one"},
		{AddInput{Department: "marketing", Title: "t", Kind: "post", URL: "javascript:alert(1)"}, "http://"},
		{AddInput{Department: "marketing", Title: "t", Kind: "post", URL: "ftp://x.example/a"}, "http://"},
		{AddInput{Department: "marketing", Title: "t", Kind: "post", Path: "nope.md"}, "nope.md"},
		{AddInput{Department: "marketing", Title: "t", Kind: "post", Path: "../../etc/passwd"}, "outside the project"},
		{AddInput{Department: "marketing", Title: "t", Kind: "post", File: big}, "over the shelf's 25 MB"},
		{AddInput{Department: "sales", Title: "t", Kind: "post", URL: "https://x.example"}, "departments: product, marketing"},
	}
	for _, c := range cases {
		if _, err := Add(p, c.in, now); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: err = %v, want %q", c.in, err, c.want)
		}
	}
	if list, _ := Load(p); len(list) != 0 {
		t.Errorf("refused adds recorded: %v", ids(list))
	}
}

func TestReviewAndPublish(t *testing.T) {
	p := project(t)
	o, err := Add(p, AddInput{Department: "marketing", Title: "Post", Kind: "post", URL: "https://x.example/draft"}, now)
	if err != nil {
		t.Fatal(err)
	}
	short := o.ID[len(o.ID)-4:]
	if _, err := Publish(p, short, "", "agent", now); err == nil || !strings.Contains(err.Error(), "issue-cli output review "+o.ID+" approve") {
		t.Errorf("publish before approval: %v", err)
	}
	if _, err := Review(p, short, StateApproved, "", "", "owner", now); err == nil {
		t.Error("review without quote accepted")
	}
	if _, err := Review(p, short, "maybe", "ok", "", "owner", now); err == nil {
		t.Error("unknown review state accepted")
	}
	r, err := Review(p, short, StateRejected, "not yet", "", "owner", now.Add(time.Hour))
	if err != nil || r.State != StateRejected {
		t.Fatalf("reject: %+v %v", r, err)
	}
	if _, err := Publish(p, o.ID, "", "agent", now); err == nil {
		t.Error("published a rejected output")
	}
	r, err = Review(p, o.ID, StateApproved, "ok, ship it", "on the shelf", "owner", now.Add(2*time.Hour))
	if err != nil || r.State != StateApproved || r.Quote != "ok, ship it" || r.ReviewedBy != "owner" {
		t.Fatalf("approve: %+v %v", r, err)
	}
	if _, err := Publish(p, o.ID, "mailto:a@b", "agent", now); err == nil {
		t.Error("bad publish url accepted")
	}
	r, err = Publish(p, o.ID, "https://x.example/live", "agent", now.Add(3*time.Hour))
	if err != nil || r.State != StatePublished || r.URL != "https://x.example/live" {
		t.Fatalf("publish: %+v %v", r, err)
	}
	if _, err := Review(p, o.ID, StateRejected, "no", "", "owner", now); err == nil {
		t.Error("reviewed a published output")
	}
}

func TestLoadSkipsBadLines(t *testing.T) {
	p := project(t)
	writeFile(t, LogPath(p), strings.Join([]string{
		`{"op":"add","id":"o-1","date":"2026-10-29T10:00:00Z","department":"marketing","title":"A","kind":"post","url":"https://a.example"}`,
		`not json`,
		`{"op":"review","id":"o-9","date":"2026-10-29T10:00:00Z","state":"approved"}`,
		`{"op":"add","id":"o-2","date":"yesterday","department":"gone","title":"B","kind":"post"}`,
		`{"op":"add","id":"o-3","date":"2026-10-28T10:00:00Z","department":"gone","title":"C","kind":"post","url":"https://c.example"}`,
		``,
	}, "\n"))
	list, bad := Load(p)
	if len(list) != 2 || list[0].ID != "o-1" || list[1].Name != "gone" {
		t.Errorf("list = %v", ids(list))
	}
	if len(bad) != 3 || bad[0].Line != 2 || bad[1].Line != 3 || bad[2].Line != 4 {
		t.Errorf("bad = %+v", bad)
	}
	if Configured(p, "gone") || !Configured(p, "product") {
		t.Error("Configured")
	}
}

func TestFind(t *testing.T) {
	list := []*Output{{ID: "o-20261029-a1b2", Title: "A"}, {ID: "o-20261028-a1b2", Title: "B"}, {ID: "o-20261028-ffff", Title: "C"}}
	if o, err := Find(list, "ffff"); err != nil || o.Title != "C" {
		t.Errorf("short: %v %v", o, err)
	}
	if _, err := Find(list, "a1b2"); err == nil || !strings.Contains(err.Error(), "matches 2") {
		t.Errorf("ambiguous: %v", err)
	}
	if _, err := Find(list, "zzzz"); err == nil || !strings.Contains(err.Error(), "newest: o-20261029-a1b2 (A)") {
		t.Errorf("unknown: %v", err)
	}
	if _, err := Find(nil, "x"); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("empty: %v", err)
	}
}

func ids(list []*Output) []string {
	var out []string
	for _, o := range list {
		out = append(out, o.ID)
	}
	return out
}
