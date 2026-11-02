package viewer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// fakeGH puts a stand-in gh first on PATH that records its arguments and
// the GH_TOKEN it saw in rec, then exits with exit. HOME is an empty folder,
// so nothing under a home path can be what makes the close work.
func fakeGH(t *testing.T, exit int, stderr string) (rec string) {
	t.Helper()
	bin := t.TempDir()
	rec = filepath.Join(t.TempDir(), "gh.rec")
	writeScript(t, filepath.Join(bin, "gh"),
		`printf 'token=%s\nargs=%s\n' "${GH_TOKEN-unset}" "$*" > "`+rec+`.tmp" && mv "`+rec+`.tmp" "`+rec+`"`+"\n"+
			`printf '%s' '`+stderr+`' >&2`+"\n"+
			"exit "+string(rune('0'+exit)))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GH_TOKEN", "")
	os.Unsetenv("GH_TOKEN")
	return rec
}

func readRec(t *testing.T, rec string) string {
	t.Helper()
	data, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("gh was not run: %v", err)
	}
	return string(data)
}

var ghIssue = &tracker.Issue{Title: "Login breaks", Repo: "o/r", Number: 7}

func TestCloseGithubIssue_UsesGhsOwnLogin(t *testing.T) {
	rec := fakeGH(t, 0, "")
	if err := closeGithubIssue(context.Background(), ghIssue, ""); err != nil {
		t.Fatalf("close = %v", err)
	}
	got := readRec(t, rec)
	if !strings.Contains(got, "token=unset") {
		t.Errorf("gh saw a GH_TOKEN with no github_token_command:\n%s", got)
	}
	if !strings.Contains(got, "args=issue close 7 --repo o/r --comment") {
		t.Errorf("gh args:\n%s", got)
	}
}

func TestCloseGithubIssue_ConfiguredTokenCommand(t *testing.T) {
	rec := fakeGH(t, 0, "")
	if err := closeGithubIssue(context.Background(), ghIssue, "echo '  tok-123  '"); err != nil {
		t.Fatalf("close = %v", err)
	}
	if got := readRec(t, rec); !strings.Contains(got, "token=tok-123\n") {
		t.Errorf("gh did not get the configured token:\n%s", got)
	}
}

func TestCloseGithubIssue_ClearMessageWhenGhIsNotSignedIn(t *testing.T) {
	fakeGH(t, 4, "To get started with GitHub CLI, please run: gh auth login")
	err := closeGithubIssue(context.Background(), ghIssue, "")
	if err == nil {
		t.Fatal("close passed with gh failing")
	}
	for _, want := range []string{"o/r#7", "gh issue close", "gh auth login", "github_token_command: in projects.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
}

func TestCloseGithubIssue_TokenCommandFailsOrPrintsNothing(t *testing.T) {
	rec := fakeGH(t, 0, "")
	for _, cmd := range []string{"echo no keychain >&2; exit 3", "true"} {
		err := closeGithubIssue(context.Background(), ghIssue, cmd)
		if err == nil || !strings.Contains(err.Error(), "github_token_command") {
			t.Errorf("%q: error = %v, want one naming github_token_command", cmd, err)
		}
	}
	if _, err := os.Stat(rec); err == nil {
		t.Error("gh ran without the token the config asked for")
	}
}

func TestLoadProjectsConfig_GitHubTokenCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects.yaml")
	if err := os.WriteFile(path, []byte("github_token_command: \"op read op://x/gh\"\nprojects: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := tracker.LoadProjectsConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GitHubTokenCommand != "op read op://x/gh" {
		t.Errorf("GitHubTokenCommand = %q", cfg.GitHubTokenCommand)
	}
}
