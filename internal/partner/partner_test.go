package partner

import (
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

const fixture = "testdata/openmontage"

func omCfg() tracker.Partner {
	return tracker.Partner{Name: "OpenMontage", Adapter: "openmontage", Path: fixture, LivePort: 4750}
}

func issueWith(slug, project string) *tracker.Issue {
	is := &tracker.Issue{Slug: slug, Title: "A video"}
	if project != "" {
		is.ExtraFields = []tracker.ExtraField{{Key: ProjectField, Value: project}}
	}
	return is
}

func read(t *testing.T, project string) (Status, error) {
	t.Helper()
	wd, _ := os.Getwd()
	return Read(omCfg(), wd, issueWith("video/"+project, ""))
}

func TestOpenMontageAwaitingHuman(t *testing.T) {
	st, err := read(t, "waiting")
	if err != nil {
		t.Fatal(err)
	}
	if st.Stage != "pitch" || st.State != StateAwaiting || !st.Awaiting() {
		t.Fatalf("stage %q state %q, want pitch awaiting", st.Stage, st.State)
	}
	if st.Summary != "Three concepts; the second fits a 30 s cut" {
		t.Errorf("summary %q", st.Summary)
	}
	if strings.Join(st.Artifacts, ",") != "cost_plan,pitch_packet" {
		t.Errorf("artifacts %v", st.Artifacts)
	}
	if st.Cost == nil || st.Cost.Spent != 0.40 || st.Cost.Remaining != 1.50 || st.Cost.Budget() != 2.0 {
		t.Errorf("cost %+v, want the newest snapshot (0.40 spent, 1.50 left, 2.00 budget)", st.Cost)
	}
	if st.LiveURL != "http://127.0.0.1:4750/p/waiting" || st.HealthURL != "http://127.0.0.1:4750/api/health" {
		t.Errorf("live %q health %q", st.LiveURL, st.HealthURL)
	}
	if st.Since.IsZero() {
		t.Error("no checkpoint time")
	}
}

func TestOpenMontageStates(t *testing.T) {
	for _, tc := range []struct{ project, stage, state, err string }{
		{"in-flight", "pitch", StateWorking, ""},
		{"broke", "brief", StateFailed, "no narration tool available"},
		{"all-done", "", StateDone, ""},
		{"not-started-yet", "", StateNone, ""},
	} {
		st, err := read(t, tc.project)
		if err != nil {
			t.Fatalf("%s: %v", tc.project, err)
		}
		if st.Stage != tc.stage || st.State != tc.state || st.Error != tc.err {
			t.Errorf("%s: got %q/%q/%q, want %q/%q/%q", tc.project, st.Stage, st.State, st.Error, tc.stage, tc.state, tc.err)
		}
	}
}

// AC2: an unknown checkpoint version is not understood, and no stage is
// guessed.
func TestUnknownCheckpointVersionNotUnderstood(t *testing.T) {
	st, err := read(t, "newer-format")
	if !errors.Is(err, ErrFormat) {
		t.Fatalf("err %v, want ErrFormat", err)
	}
	if !strings.Contains(err.Error(), FormatWords) || !strings.Contains(err.Error(), `checkpoint_brief.json version "2.0"`) {
		t.Errorf("err %q names neither the words nor the file", err)
	}
	if st.Stage != "" || st.State == StateWorking || st.State == StateAwaiting {
		t.Errorf("guessed a stage: %+v", st)
	}
}

func TestUnknownProjectVersionNotUnderstood(t *testing.T) {
	dir := copyFixture(t)
	write(t, filepath.Join(dir, "projects", "waiting", "project.json"), `{"version": "9", "pipeline_type": "short-explainer"}`)
	_, err := Read(tracker.Partner{Adapter: "openmontage", Path: dir}, dir, issueWith("video/waiting", ""))
	if !errors.Is(err, ErrFormat) {
		t.Fatalf("err %v, want ErrFormat", err)
	}
}

func TestPartnerProjectIDRefusesTraversal(t *testing.T) {
	for _, bad := range []string{"../etc", "..", ".hidden", "a/b", `a\b`, "x..y"} {
		if _, err := ProjectID(issueWith("video/ok", bad)); err == nil {
			t.Errorf("partner_project %q accepted", bad)
		}
	}
	if id, err := ProjectID(issueWith("Video/what-is-dark-factory", "")); err != nil || id != "what-is-dark-factory" {
		t.Errorf("slug fallback: %q %v", id, err)
	}
	if id, err := ProjectID(issueWith("video/long-title", "short-id")); err != nil || id != "short-id" {
		t.Errorf("field: %q %v", id, err)
	}
	// A pipeline_type that walks out of pipeline_defs is a format error.
	dir := copyFixture(t)
	write(t, filepath.Join(dir, "projects", "waiting", "project.json"), `{"version": "1.0", "pipeline_type": "../../secret"}`)
	if _, err := Read(tracker.Partner{Adapter: "openmontage", Path: dir}, dir, issueWith("video/waiting", "")); !errors.Is(err, ErrFormat) {
		t.Errorf("pipeline_type traversal: %v", err)
	}
}

func TestPartnerFileSizeCap(t *testing.T) {
	dir := copyFixture(t)
	write(t, filepath.Join(dir, "projects", "waiting", "checkpoint_brief.json"), strings.Repeat(" ", MaxFile+1))
	if _, err := Read(tracker.Partner{Adapter: "openmontage", Path: dir}, dir, issueWith("video/waiting", "")); err == nil || !strings.Contains(err.Error(), "not read") {
		t.Errorf("an oversized file was read: %v", err)
	}
}

func TestPartnerConfigProblems(t *testing.T) {
	wd, _ := os.Getwd()
	is := issueWith("video/waiting", "")
	if _, err := Read(tracker.Partner{Path: fixture}, wd, is); err == nil || !strings.Contains(err.Error(), "no adapter") {
		t.Errorf("no adapter: %v", err)
	}
	if _, err := Read(tracker.Partner{Adapter: "nope", Path: fixture}, wd, is); err == nil || !strings.Contains(err.Error(), `"nope" is unknown`) {
		t.Errorf("unknown adapter: %v", err)
	}
	if _, err := Read(tracker.Partner{Adapter: "openmontage", Path: "no/such/dir"}, wd, is); err == nil || !strings.Contains(err.Error(), "is missing") {
		t.Errorf("missing path: %v", err)
	}
}

// TestPartnerAdapterOnlyReads keeps the package a reader: it runs nothing
// and writes nothing (Must not).
func TestPartnerAdapterOnlyReads(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	banned := map[string]bool{"os/exec": true, "net/http": true, "syscall": true}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(token.NewFileSet(), f, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, im := range file.Imports {
			if p, _ := strconv.Unquote(im.Path.Value); banned[p] {
				t.Errorf("%s imports %s: the partner package only reads files", f, p)
			}
		}
		for _, call := range []string{"os.WriteFile", "os.Create", "os.OpenFile", "os.Remove", "os.Mkdir", "os.Rename"} {
			if strings.Contains(string(src), call+"(") {
				t.Errorf("%s calls %s: the partner package only reads files", f, call)
			}
		}
	}
}

func copyFixture(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(fixture, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(fixture, path)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}
