package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// pngBytes is a small real PNG.
func pngBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// attachFixture is a project with cli/sample and a scratch folder attach may
// read from (the only root while the test runs). Tests using it are not
// parallel: they replace attachRoots.
func attachFixture(t *testing.T) (*tracker.Project, string, string) {
	t.Helper()
	proj, issuePath := makeGateFixture(t, "in progress", "", true)
	scratch := realTempDir(t)
	old := attachRoots
	attachRoots = func(*Context) []string { return []string{scratch} }
	t.Cleanup(func() { attachRoots = old })
	return proj, issuePath, scratch
}

func attachmentFiles(t *testing.T, proj *tracker.Project) []string {
	t.Helper()
	entries, _ := os.ReadDir(tracker.AttachmentsDir(proj.IssueDir))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestAttachSavesPNGAndPrintsLink(t *testing.T) {
	proj, issuePath, scratch := attachFixture(t)
	shot := filepath.Join(scratch, "board shot.png")
	data := pngBytes(t)
	os.WriteFile(shot, data, 0o644)

	ctx, stdout, stderr := newTestContext(proj, false)
	if err := runAttach(ctx, []string{"cli/sample", shot, "--section", "Design"}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	names := attachmentFiles(t, proj)
	if len(names) != 1 || !tracker.AttachmentNamePattern.MatchString(names[0]) || !strings.HasSuffix(names[0], ".png") {
		t.Fatalf("attachments = %v, want one hash-named .png", names)
	}
	saved, _ := os.ReadFile(filepath.Join(tracker.AttachmentsDir(proj.IssueDir), names[0]))
	if !bytes.Equal(saved, data) {
		t.Fatal("saved bytes differ from the file")
	}
	want := "![board shot](/p/test/attachments/" + names[0] + ")\n"
	if stdout.String() != want {
		t.Errorf("stdout = %q, want only the link %q", stdout.String(), want)
	}
	assertContains(t, stderr.String(), "✓ Appended 1 link(s) to cli/sample under ## Design")
	assertContains(t, readFile(t, issuePath), "## Design\n- [x] Approach documented\n- [x] Dependencies identified\n\n"+want)

	// The same bytes again: same name, nothing new written; --json names it.
	ctx, stdout, _ = newTestContext(proj, true)
	if err := runAttach(ctx, []string{"cli/sample", shot, "--alt", "x[y]"}); err != nil {
		t.Fatalf("attach again: %v", err)
	}
	var got []attachment
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout)
	}
	if len(got) != 1 || got[0].Name != names[0] || got[0].Markdown != "![x(y)](/p/test/attachments/"+names[0]+")" {
		t.Errorf("json = %+v", got)
	}
	if n := attachmentFiles(t, proj); len(n) != 1 {
		t.Errorf("attachments after a second attach = %v", n)
	}
}

// A file named like an image whose bytes are none (HTML in fake.png) is
// refused, and the refusal saves none of the call's files.
func TestAttachRefusesFakeImage(t *testing.T) {
	proj, issuePath, scratch := attachFixture(t)
	before := readFile(t, issuePath)
	good := filepath.Join(scratch, "good.png")
	os.WriteFile(good, pngBytes(t), 0o644)
	fake := filepath.Join(scratch, "fake.png")
	os.WriteFile(fake, []byte("<html><script>alert(1)</script></html>"), 0o644)

	ctx, stdout, _ := newTestContext(proj, false)
	err := runAttach(ctx, []string{"cli/sample", good, fake, "--section", "Design"})
	if code := exitCode(t, err); code != 1 {
		t.Fatalf("exit = %d", code)
	}
	assertContains(t, err.Error(), "✗ "+fake+": named like an image but the bytes are no PNG, JPEG, GIF or WebP")
	assertContains(t, err.Error(), "nothing saved")
	if n := attachmentFiles(t, proj); len(n) != 0 {
		t.Errorf("one bad file must save nothing, got %v", n)
	}
	if stdout.Len() != 0 || readFile(t, issuePath) != before {
		t.Error("refused attach printed a link or changed the issue")
	}
}

// Any other file (a gzipped pprof profile, a text capture) is saved as
// attachments/<hash>/<name> and printed as a plain link; --json names its
// kind, and an image beside it keeps its image link.
func TestAttachSavesAnyFile(t *testing.T) {
	proj, issuePath, scratch := attachFixture(t)
	prof := filepath.Join(scratch, "cpu.pprof")
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write([]byte("profile\x00\x01"))
	zw.Close()
	os.WriteFile(prof, gz.Bytes(), 0o644)
	top := filepath.Join(scratch, "pprof top.txt")
	os.WriteFile(top, []byte("Showing: 12.40s\n 4.10s os.Stat\n"), 0o644)
	shot := filepath.Join(scratch, "shot.png")
	os.WriteFile(shot, pngBytes(t), 0o644)

	ctx, stdout, _ := newTestContext(proj, true)
	if err := runAttach(ctx, []string{"cli/sample", prof, top, shot, "--section", "Design"}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	var got []attachment
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout)
	}
	if len(got) != 3 {
		t.Fatalf("records = %+v", got)
	}
	for i, want := range []struct{ kind, base string }{{"file", "cpu.pprof"}, {"text", "pprof-top.txt"}, {"image", ""}} {
		r := got[i]
		if r.Kind != want.kind {
			t.Errorf("%s: kind %q, want %q", r.File, r.Kind, want.kind)
		}
		if want.kind == "image" {
			if !strings.HasPrefix(r.Markdown, "![shot](") {
				t.Errorf("image link = %q", r.Markdown)
			}
			continue
		}
		if !tracker.AttachmentFilePattern.MatchString(r.Name) || filepath.Base(r.Name) != want.base {
			t.Errorf("%s saved as %q", r.File, r.Name)
		}
		if wantMD := "[" + want.base + "](/p/test/attachments/" + r.Name + ")"; r.Markdown != wantMD || r.URL != "/p/test/attachments/"+r.Name {
			t.Errorf("%s: markdown %q url %q, want %q", r.File, r.Markdown, r.URL, wantMD)
		}
		saved, err := os.ReadFile(filepath.Join(tracker.AttachmentsDir(proj.IssueDir), filepath.FromSlash(r.Name)))
		src, _ := os.ReadFile(r.File)
		if err != nil || !bytes.Equal(saved, src) {
			t.Errorf("%s: saved bytes differ (%v)", r.File, err)
		}
	}
	assertContains(t, readFile(t, issuePath), got[0].Markdown+"\n"+got[1].Markdown+"\n"+got[2].Markdown)

	// Plain output: one link per line, no "!" for a file.
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runAttach(ctx, []string{"cli/sample", prof}); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != got[0].Markdown+"\n" {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestAttachRefusesPathOutsideRoots(t *testing.T) {
	proj, _, scratch := attachFixture(t)
	outside := realTempDir(t) // not a root while attachFixture's override holds
	private := filepath.Join(outside, "private.png")
	os.WriteFile(private, pngBytes(t), 0o644)
	link := filepath.Join(scratch, "link.png")
	if err := os.Symlink(private, link); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{private, link, filepath.Join(scratch, "..", filepath.Base(outside), "private.png")} {
		ctx, _, _ := newTestContext(proj, false)
		err := runAttach(ctx, []string{"cli/sample", p})
		if err == nil {
			t.Errorf("attach %s: want refused", p)
			continue
		}
		assertContains(t, err.Error(), "outside your worktree, the project and the temp folder")
		assertContains(t, err.Error(), "copy it into your scratchpad or worktree first")
	}
	if n := attachmentFiles(t, proj); len(n) != 0 {
		t.Errorf("attachments = %v, want none", n)
	}
}

func TestAttachNeedsAFileAndARealIssue(t *testing.T) {
	proj, _, scratch := attachFixture(t)
	shot := filepath.Join(scratch, "s.png")
	os.WriteFile(shot, pngBytes(t), 0o644)
	ctx, _, _ := newTestContext(proj, false)
	if err := runAttach(ctx, []string{"cli/sample"}); err == nil || !strings.Contains(err.Error(), "at least one file") {
		t.Errorf("no file: %v", err)
	}
	ctx, _, _ = newTestContext(proj, false)
	if err := runAttach(ctx, []string{"cli/nope", shot}); err == nil {
		t.Error("unknown slug: want an error")
	}
	if n := attachmentFiles(t, proj); len(n) != 0 {
		t.Errorf("attachments = %v, want none", n)
	}
}

// The default roots: the working directory's checkout, the project root and
// the temp folder.
func TestAttachDefaultRoots(t *testing.T) {
	proj := &tracker.Project{Name: "p", Slug: "p", IssueDir: filepath.Join(t.TempDir(), "issues")}
	ctx, _, _ := newTestContext(proj, false)
	roots := attachRoots(ctx)
	wd, _ := os.Getwd()
	top := gitTopOrSelf(wd)
	if _, err := os.Lstat(filepath.Join(top, ".git")); err != nil {
		t.Fatalf("gitTopOrSelf(%s) = %s, has no .git", wd, top)
	}
	for _, want := range []string{top, proj.Root(), os.TempDir(), "/tmp"} {
		found := false
		for _, r := range roots {
			found = found || r == want
		}
		if !found {
			t.Errorf("roots %v miss %s", roots, want)
		}
	}
}
