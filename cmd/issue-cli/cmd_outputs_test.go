package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/outputs"
	"github.com/kacperwerema/dark-factory/internal/standin"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// outputsProject is goalsProject (Marketing owns system SEO) with a report
// file in the repo.
func outputsProject(t *testing.T) *tracker.Project {
	t.Helper()
	proj := goalsProject(t)
	mustWrite(t, filepath.Join(proj.WorkDir, "docs", "report.md"), "# report\n")
	return proj
}

// asLead makes whoAmI see the project lead, whatever shell runs the test.
func asLead(t *testing.T) {
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", tracker.LeadScopeProject)
}

func addOutput(t *testing.T, proj *tracker.Project, args ...string) *outputs.Output {
	t.Helper()
	ctx, out := goalsCtx(t, proj, true)
	if err := runOutput(ctx, append([]string{"add"}, args...)); err != nil {
		t.Fatalf("output add %v: %v", args, err)
	}
	var o outputs.Output
	if err := json.Unmarshal(out.Bytes(), &o); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return &o
}

func TestOutputAdd(t *testing.T) {
	asLead(t)
	proj := outputsProject(t)
	ctx, out := goalsCtx(t, proj, false)
	err := runOutput(ctx, []string{"add", "--issue", "seo-pass", "--title", "Acme FAQ", "--kind", "page", "--url", "https://acme.example/faq"})
	if err != nil {
		t.Fatal(err)
	}
	s := out.String()
	assertContains(t, s, "✓ On Marketing's shelf: o-20261029-")
	assertContains(t, s, "📄 ")
	assertContains(t, s, "[review] page · Acme FAQ")
	assertContains(t, s, "https://acme.example/faq · issue seo-pass · by lead-acme")
	assertContains(t, s, "/p/acme/shelf?dept=marketing")
	assertContains(t, s, "issue-cli output publish ")
	assertContains(t, s, "file: "+outputs.LogPath(proj))

	// No issue, no department: Product's. --path stays a link into the repo.
	o := addOutput(t, proj, "--title", "Report", "--kind", "report", "--path", "docs/report.md", "--job", "weekly")
	if o.Department != "product" || o.Path != "docs/report.md" || o.Job != "weekly" {
		t.Errorf("report = %+v", o)
	}
	// --department with no issue.
	src := filepath.Join(t.TempDir(), "hero.png")
	mustWrite(t, src, "png")
	o = addOutput(t, proj, "--department", "Marketing", "--title", "Hero", "--kind", "design", "--file", src)
	if o.Department != "marketing" || !strings.HasPrefix(o.File, "marketing/") {
		t.Errorf("hero = %+v", o)
	}
}

func TestOutputAddRefusals(t *testing.T) {
	asLead(t)
	proj := outputsProject(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--kind", "page", "--url", "https://a.example"}, "usage: issue-cli output add"},
		{[]string{"--issue", "seo-pass", "--department", "product", "--title", "t", "--kind", "page", "--url", "https://a.example"}, "--department Product, but seo-pass is Marketing's issue"},
		{[]string{"--department", "sales", "--title", "t", "--kind", "page", "--url", "https://a.example"}, "sales"},
		{[]string{"--title", "t", "--kind", "page", "--url", "javascript:alert(1)"}, "http://"},
		{[]string{"--title", "t", "--kind", "page", "--path", "nope.md"}, "nope.md"},
		{[]string{"--issue", "nope", "--title", "t", "--kind", "page", "--url", "https://a.example"}, "nope"},
	} {
		ctx, _ := goalsCtx(t, proj, false)
		if err := runOutput(ctx, append([]string{"add"}, c.args...)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: err = %v, want %q", c.args, err, c.want)
		}
	}
	if list, _ := outputs.Load(proj); len(list) != 0 {
		t.Errorf("refused adds recorded %d outputs", len(list))
	}
}

func TestOutputReviewPublishShow(t *testing.T) {
	asLead(t)
	proj := outputsProject(t)
	o := addOutput(t, proj, "--issue", "seo-pass", "--title", "Post", "--kind", "post", "--url", "https://a.example/draft")
	id := shortID(o.ID)

	run := func(args ...string) (string, error) {
		ctx, out := goalsCtx(t, proj, false)
		err := runOutput(ctx, args)
		return out.String(), err
	}
	if _, err := run("publish", id); err == nil || !strings.Contains(err.Error(), "issue-cli output review "+o.ID+" approve") {
		t.Errorf("publish before approval: %v", err)
	}
	if _, err := run("review", id, "approve"); err == nil || !strings.Contains(err.Error(), "--quote") {
		t.Errorf("review without quote: %v", err)
	}
	if _, err := run("review", id, "maybe", "--quote", "x"); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("bad verdict: %v", err)
	}
	s, err := run("review", id, "approve", "--quote", "ok, ship it")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, s, "✓ Approved "+o.ID+": Post (\"ok, ship it\", in the CEO chat)")
	s, err = run("publish", id, "--url", "https://a.example/live")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, s, "✓ Published "+o.ID)
	assertContains(t, s, "[published]")
	s, err = run("show", id)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, s, "== Post ==")
	assertContains(t, s, "https://a.example/live")
	assertContains(t, s, "by lead-acme, \"ok, ship it\"")
	assertContains(t, s, "Published: ")
	if _, err := run("show", "zzzz"); err == nil || !strings.Contains(err.Error(), "newest: "+o.ID) {
		t.Errorf("unknown id: %v", err)
	}

	// A worker never reviews.
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "seo-pass")
	o2 := addOutput(t, proj, "--issue", "seo-pass", "--title", "Second", "--kind", "post", "--url", "https://a.example/2")
	if o2.By != tracker.AgentSessionName("seo-pass") {
		t.Errorf("by = %q", o2.By)
	}
	if _, err := run("review", shortID(o2.ID), "approve", "--quote", "ok"); err == nil || !strings.Contains(err.Error(), "workers never review outputs") {
		t.Errorf("worker review: %v", err)
	}
}

func TestOutputsList(t *testing.T) {
	asLead(t)
	proj := outputsProject(t)
	list := func(jsonOut bool, args ...string) string {
		ctx, out := goalsCtx(t, proj, jsonOut)
		if err := runOutputs(ctx, args); err != nil {
			t.Fatalf("outputs %v: %v", args, err)
		}
		return out.String()
	}
	assertContains(t, list(false), "The shelf is empty. Record what an issue or job made:")
	if s := list(true); strings.TrimSpace(s) != "[]" {
		t.Errorf("empty json = %q", s)
	}

	a := addOutput(t, proj, "--issue", "seo-pass", "--title", "FAQ", "--kind", "page", "--url", "https://a.example/faq")
	addOutput(t, proj, "--title", "Report", "--kind", "report", "--path", "docs/report.md")
	s := list(false)
	assertContains(t, s, "== Marketing · 1 to review · 1 this week ==")
	assertContains(t, s, "== Product · 1 to review · 1 this week ==")

	s = list(false, "marketing")
	if strings.Contains(s, "Report") || !strings.Contains(s, "FAQ") {
		t.Errorf("marketing only:\n%s", s)
	}
	var got []outputs.Output
	if err := json.Unmarshal([]byte(list(true, "--issue", "seo-pass")), &got); err != nil || len(got) != 1 || got[0].ID != a.ID {
		t.Errorf("by issue: %v %+v", err, got)
	}
	assertContains(t, list(false, "--state", "published"), "No outputs match.")
	ctx, _ := goalsCtx(t, proj, false)
	if err := runOutputs(ctx, []string{"--state", "maybe"}); err == nil || !strings.Contains(err.Error(), "review, approved, rejected, published") {
		t.Errorf("bad state: %v", err)
	}
	ctx, _ = goalsCtx(t, proj, false)
	if err := runOutput(ctx, nil); err == nil {
		t.Error("output with no subcommand")
	}
	ctx, _ = goalsCtx(t, proj, false)
	if err := runOutput(ctx, []string{"nope"}); err == nil || !strings.Contains(err.Error(), "add, review, publish, use, show, revise, comments or reply") {
		t.Errorf("unknown subcommand: %v", err)
	}
}

func TestOutputsLineAndHint(t *testing.T) {
	asLead(t)
	proj := outputsProject(t)
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	seo, engine := tracker.FindIssue(issues, "seo-pass"), tracker.FindIssue(issues, "engine")

	if got := outputsLine(proj, engine, nil); got != "" {
		t.Errorf("Product issue without outputs: %q", got)
	}
	if got := outputsLine(proj, engine, &tracker.WorkflowConfig{Template: "content@v0.55.0"}); !strings.Contains(got, "Outputs: none yet") {
		t.Errorf("content workflow issue: %q", got)
	}
	got := outputsLine(proj, seo, nil)
	assertContains(t, got, "Outputs: none yet · record what this makes: issue-cli output add --issue seo-pass")
	assertContains(t, outputsTransitionHint(proj, seo, nil, "done"), "Made something? Put it on the shelf: issue-cli output add --issue seo-pass")
	if h := outputsTransitionHint(proj, seo, nil, "in progress"); h != "" {
		t.Errorf("hint on in progress: %q", h)
	}
	if h := outputsTransitionHint(proj, engine, nil, "done"); h != "" {
		t.Errorf("hint on a Product issue: %q", h)
	}

	addOutput(t, proj, "--issue", "seo-pass", "--title", "FAQ", "--kind", "page", "--url", "https://a.example/faq")
	if got := outputsLine(proj, seo, nil); got != "Outputs: 1 (1 to review) · issue-cli outputs --issue seo-pass" {
		t.Errorf("with outputs: %q", got)
	}
	if h := outputsTransitionHint(proj, seo, nil, "publish"); h != "" {
		t.Errorf("hint with outputs: %q", h)
	}

	// show prints it.
	ctx, out := goalsCtx(t, proj, false)
	if err := runShow(ctx, []string{"seo-pass"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Outputs: 1 (1 to review)")
}

func TestOutputsListGroupsEachDepartmentOnce(t *testing.T) {
	asLead(t)
	proj := outputsProject(t)
	addOutput(t, proj, "--department", "marketing", "--title", "M1", "--kind", "post", "--url", "https://a.example/1")
	addOutput(t, proj, "--title", "P1", "--kind", "report", "--url", "https://a.example/2")
	addOutput(t, proj, "--department", "marketing", "--title", "M2", "--kind", "post", "--url", "https://a.example/3")
	ctx, out := goalsCtx(t, proj, false)
	if err := runOutputs(ctx, nil); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if n := strings.Count(s, "== Marketing"); n != 1 {
		t.Errorf("Marketing heading %d times:\n%s", n, s)
	}
	if !(strings.Index(s, "== Product") < strings.Index(s, "== Marketing") && strings.Index(s, "M2") < strings.Index(s, "M1")) {
		t.Errorf("want Product first, then Marketing newest first:\n%s", s)
	}
}

// A video added to the shelf says where it plays and whether it got a
// poster; over 200 MB it is refused naming the limit.
func TestOutputAddVideo(t *testing.T) {
	asLead(t)
	proj := outputsProject(t)
	bin := t.TempDir()
	standin.Write(t, filepath.Join(bin, "ffmpeg"), "for a; do last=$a; done\nprintf jpg > \"$last\"\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/bin:/usr/bin")
	src := filepath.Join(t.TempDir(), "final.mp4")
	mustWrite(t, src, "mp4")
	ctx, out := goalsCtx(t, proj, false)
	if err := runOutput(ctx, []string{"add", "--title", "Draft", "--kind", "video", "--file", src}); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	assertContains(t, s, "🎬 ")
	assertContains(t, s, "  video   .outputs/files/product/o-20261029-")
	assertContains(t, s, "-final.mp4 (0.0 MB, video/mp4, up to 200 MB)")
	assertContains(t, s, "-final.mp4.poster.jpg (frame at 1 s, ffmpeg)")
	assertContains(t, s, "Plays at: ")
	assertContains(t, s, "/p/acme/shelf/read/o-20261029-")

	// Without ffmpeg the add goes through and says why there is no poster.
	t.Setenv("PATH", t.TempDir())
	ctx, out = goalsCtx(t, proj, false)
	if err := runOutput(ctx, []string{"add", "--title", "Draft 2", "--kind", "video", "--file", src}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "poster  none: ffmpeg is not on PATH (the shelf shows 🎬")

	big := filepath.Join(t.TempDir(), "master.mp4")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	f.Truncate(outputs.MaxVideoBytes + 1)
	f.Close()
	ctx, _ = goalsCtx(t, proj, false)
	err = runOutput(ctx, []string{"add", "--title", "Master", "--kind", "video", "--file", big})
	if err == nil || !strings.Contains(err.Error(), "over the shelf's 200 MB for a video") {
		t.Errorf("over the limit: %v", err)
	}
}

func TestOutputAddHintsReviseOnTheSameTitle(t *testing.T) {
	asLead(t)
	proj := outputsProject(t)
	file := filepath.Join(t.TempDir(), "scan.md")
	mustWrite(t, file, "# scan\n")
	add := func(jsonOut bool, args ...string) (string, string) {
		t.Helper()
		ctx, out, errOut := newTestContext(proj, jsonOut)
		if err := runOutput(ctx, append([]string{"add", "--issue", "seo-pass", "--kind", "report"}, args...)); err != nil {
			t.Fatal(err)
		}
		return out.String(), errOut.String()
	}
	first, _ := add(false, "--title", "Keyword scan", "--file", file)
	assertNotContains(t, first, "Hint:")
	list, _ := outputs.Load(proj)
	id := list[0].ID
	short := id[strings.LastIndex(id, "-")+1:]

	second, _ := add(false, "--title", " keyword SCAN ", "--file", file)
	assertContains(t, second, "Hint: "+short+" on this issue has the same title")
	assertContains(t, second, "issue-cli output revise "+short+" --file <file>")

	// --json keeps stdout one object; the hint goes to stderr.
	stdout, stderr := add(true, "--title", "Keyword scan", "--file", file)
	var o outputs.Output
	if err := json.Unmarshal([]byte(stdout), &o); err != nil {
		t.Fatalf("%v: %s", err, stdout)
	}
	assertContains(t, stderr, "output revise")

	// Another title, or a same-titled output on another issue, is no hint.
	other, _ := add(false, "--title", "Another", "--file", file)
	assertNotContains(t, other, "Hint:")
	ctx, out, _ := newTestContext(proj, false)
	if err := runOutput(ctx, []string{"add", "--issue", "engine", "--title", "Keyword scan", "--kind", "report", "--file", file}); err != nil {
		t.Fatal(err)
	}
	assertNotContains(t, out.String(), "Hint:")
}

// revise refuses a link and a published output, so neither is hinted at.
func TestOutputSameTitleSkipsWhatReviseRefuses(t *testing.T) {
	list := []*outputs.Output{
		{ID: "o-3", Issue: "a", Title: "T", URL: "https://a.example"},
		{ID: "o-2", Issue: "a", Title: "T", File: "f2", State: outputs.StatePublished},
		{ID: "o-1", Issue: "a", Title: "T", File: "f1", State: outputs.StateRejected},
	}
	if got := outputs.SameTitle(list, "a", "t"); got == nil || got.ID != "o-1" {
		t.Fatalf("got %+v, want o-1", got)
	}
	if got := outputs.SameTitle(list[:2], "a", "T"); got != nil {
		t.Fatalf("nothing revisable: got %+v", got)
	}
	if got := outputs.SameTitle(list, "", "T"); got != nil {
		t.Fatalf("no issue: got %+v", got)
	}
}

// output show prints the file's absolute path, one a caller in any folder
// can open, for a --file and a --path output alike.
func TestOutputShowAbsolutePath(t *testing.T) {
	asLead(t)
	proj := outputsProject(t)
	src := filepath.Join(t.TempDir(), "brief.md")
	mustWrite(t, src, "# Brief\n")
	shelved := addOutput(t, proj, "--issue", "seo-pass", "--title", "Brief", "--kind", "report", "--file", src)
	linked := addOutput(t, proj, "--title", "Report", "--kind", "report", "--path", "docs/report.md")
	url := addOutput(t, proj, "--title", "Live", "--kind", "page", "--url", "https://a.example/live")
	t.Chdir(t.TempDir())
	for _, o := range []*outputs.Output{shelved, linked} {
		ctx, out := goalsCtx(t, proj, false)
		if err := runOutput(ctx, []string{"show", shortID(o.ID)}); err != nil {
			t.Fatal(err)
		}
		var path string
		for _, line := range strings.Split(out.String(), "\n") {
			if p, ok := strings.CutPrefix(line, "File: "); ok {
				path = p
			}
		}
		if !filepath.IsAbs(path) {
			t.Fatalf("%s: File: line %q is not absolute:\n%s", o.Title, path, out)
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s: %v", o.Title, err)
		}
	}
	ctx, out := goalsCtx(t, proj, false)
	if err := runOutput(ctx, []string{"show", shortID(url.ID)}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "File: ") {
		t.Errorf("a link output has no file:\n%s", out)
	}
}

// output show --text prints a markdown output's newest version; any other
// output refuses, naming where it is.
func TestOutputShowText(t *testing.T) {
	asLead(t)
	proj := outputsProject(t)
	src := filepath.Join(t.TempDir(), "brief.md")
	mustWrite(t, src, "# Brief\n\nv1\n")
	o := addOutput(t, proj, "--issue", "seo-pass", "--title", "Brief", "--kind", "report", "--file", src)
	mustWrite(t, src, "# Brief\n\n## Follow-ups\n\n- v2 item")
	ctx, _ := goalsCtx(t, proj, false)
	if err := runOutput(ctx, []string{"revise", shortID(o.ID), "--file", src}); err != nil {
		t.Fatal(err)
	}
	ctx, out := goalsCtx(t, proj, false)
	if err := runOutput(ctx, []string{"show", shortID(o.ID), "--text"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "# Brief\n\n## Follow-ups\n\n- v2 item\n" {
		t.Errorf("--text = %q, want the newest version", got)
	}

	png := filepath.Join(t.TempDir(), "hero.png")
	mustWrite(t, png, "png")
	img := addOutput(t, proj, "--title", "Hero", "--kind", "design", "--file", png)
	link := addOutput(t, proj, "--title", "Live", "--kind", "page", "--url", "https://a.example/live")
	for _, c := range []struct {
		o    *outputs.Output
		want string
	}{
		{img, outputs.LocalPath(proj, img)},
		{link, "https://a.example/live"},
	} {
		ctx, out := goalsCtx(t, proj, false)
		err := runOutput(ctx, []string{"show", shortID(c.o.ID), "--text"})
		if err == nil || !strings.Contains(err.Error(), "is not markdown") || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want a refusal naming %q", c.o.Title, err, c.want)
		}
		if out.Len() != 0 {
			t.Errorf("%s: refused --text printed %q", c.o.Title, out)
		}
	}
}
