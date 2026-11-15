package bench

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/cmd/issue-cli/templates"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The fixture project: a tiny Go package written by us, with three done
// tasks and their known solutions, so a benchmark result doesn't rest on the
// dark-factory repo alone (docs/benchmark.md "The fixture project"). It is
// written as the git history the benchmark reads from any project: a repo
// whose main merged each task's branch, and a tracker whose issue files went
// through the development workflow with their stats sidecars. Every commit
// has a fixed author, committer and date, so the SHAs are the same on every
// machine. Nothing here adds a task to a benchmark: the owner picks.

//go:embed fixture
var fixtureFS embed.FS

// Fixture names.
const (
	FixtureSlug   = "bench-fixture"
	FixtureName   = "Tally fixture"
	FixtureSystem = "Tally"
	fixtureWho    = "Bench Fixture"
	fixtureEmail  = "fixture@example.com"
)

// fixtureStart is the base commit's time; task i's day is i+1 days later.
var fixtureStart = time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)

// fixtureTask is one done task: its folder under fixture/, its issue's
// title and type, and the commit subject of its branch.
type fixtureTask struct {
	dir, title, typ, subject string
}

var fixtureTasks = []fixtureTask{
	{dir: "top-words", title: "TopWords: the n most frequent words", typ: "feature", subject: "feat: TopWords, the n most frequent words"},
	{dir: "last-line", title: "Lines counts a last line without a newline", typ: "tweak", subject: "fix: Lines counts a last line without a newline"},
	{dir: "fold-case", title: "Fold case: an option to count Go and go as one word", typ: "feature", subject: "feat: Options.Fold folds case in WordsWith and CountWith"},
}

// fixturePath is the statuses an issue of type typ goes through in wf, up
// to and including the first closed one: the type's path, read from the
// workflow, never named here.
func fixturePath(wf *tracker.WorkflowConfig, typ string) []string {
	var out []string
	for _, st := range wf.ForType(typ).Statuses {
		out = append(out, st.Name)
		if tracker.IsClosedStatus(wf, st.Name) {
			break
		}
	}
	return out
}

// Fixture is what FixtureInit wrote.
type Fixture struct {
	Dir      string `json:"dir"`
	Repo     string `json:"repo"`
	Tracker  string `json:"tracker"`
	Issues   string `json:"issues"`
	Workflow string `json:"workflow"`
	// Tasks are the done issues' slugs, the candidates the owner picks from.
	Tasks []string `json:"tasks"`
	// Entry is the projects.yaml item for the fixture.
	Entry string `json:"entry"`
}

// FixtureInit writes the fixture project into dir, which must be missing
// or empty: dir/repo (the code) and dir/tracker (workflow.yaml and the
// issues). It never overwrites a file.
func FixtureInit(dir string) (*Fixture, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if entries, err := os.ReadDir(abs); err == nil && len(entries) > 0 {
		return nil, fmt.Errorf("%s is not empty: the fixture is written only into a new or empty folder", abs)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	f := &Fixture{Dir: abs, Repo: filepath.Join(abs, "repo"), Tracker: filepath.Join(abs, "tracker")}
	f.Issues = filepath.Join(f.Tracker, "issues")
	f.Workflow = filepath.Join(f.Tracker, "workflow.yaml")
	for _, d := range []string{f.Repo, filepath.Join(f.Issues, FixtureSystem)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	at := fixtureStart
	if err := fixtureGitInit(f.Repo, f.Tracker); err != nil {
		return nil, err
	}
	if err := writeFixtureFiles(f.Repo, "fixture/base"); err != nil {
		return nil, err
	}
	if err := fixtureCommit(f.Repo, at, "chore: tally, a word and line counter"); err != nil {
		return nil, err
	}
	wf, err := templates.FS.ReadFile("workflow/development.yaml")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(f.Workflow, wf, 0o644); err != nil {
		return nil, err
	}
	if err := fixtureCommit(f.Tracker, at, "chore: the development workflow"); err != nil {
		return nil, err
	}
	wfc := (&tracker.Project{IssueDir: f.Issues, WorkflowFile: f.Workflow}).LoadWorkflow()
	for i, t := range fixtureTasks {
		slug, err := writeFixtureTask(f, wfc, t, fixtureStart.Add(time.Duration(i+1)*24*time.Hour))
		if err != nil {
			return nil, fmt.Errorf("fixture task %s: %w", t.dir, err)
		}
		f.Tasks = append(f.Tasks, slug)
	}
	f.Entry = fmt.Sprintf("- name: %q\n  slug: %q\n  issues: %q\n  workflow: %q\n  workdir: %q\n  terminal: \"none\"\n",
		FixtureName, FixtureSlug, f.Issues, f.Workflow, f.Repo)
	return f, nil
}

// writeFixtureTask commits one task: its issue at each status in the
// tracker, its branch in the repo merged into main at done, and the stats
// sidecar the benchmark finds its range from.
func writeFixtureTask(f *Fixture, wf *tracker.WorkflowConfig, t fixtureTask, day time.Time) (string, error) {
	raw, err := fixtureFS.ReadFile(path.Join("fixture", t.dir, "issue.md.txt"))
	if err != nil {
		return "", err
	}
	full := strings.TrimSpace(string(raw)) + "\n"
	brief := full
	if i := strings.Index(full, "\n## "); i >= 0 {
		brief = full[:i+1]
	}
	file := filepath.Join(f.Issues, FixtureSystem, tracker.Slugify(t.title)+".md")
	created := day.Format("2006-01-02")
	steps := fixturePath(wf, t.typ)
	// The ## Design is in the file from the move out of the design status
	// on, or from the start on a path with none.
	designed := true
	for _, st := range steps {
		if wf.HasRole(st, tracker.StatusRoleDesign) {
			designed = false
		}
	}
	var stats tracker.StatsStore
	var head, merge string
	for k, status := range steps {
		at := day.Add(time.Duration(k) * time.Hour)
		closed := tracker.IsClosedStatus(wf, status)
		if k > 0 {
			tr := tracker.TransitionStat{From: steps[k-1], To: status, TS: at}
			if wf.HasRole(steps[k-1], tracker.StatusRoleDesign) {
				designed = true
			}
			switch {
			case wf.HasRole(status, tracker.StatusRoleWork):
				stats.Dispatches = append(stats.Dispatches, tracker.Dispatch{At: at, Session: tracker.AgentSessionName(FixtureSystem + "/" + t.dir), Agent: "claude"})
			case wf.HasRole(status, tracker.StatusRoleVerify):
				// The branch's work, made while the issue was at work.
				if head, err = fixtureBranch(f.Repo, t, at.Add(-30*time.Minute)); err != nil {
					return "", err
				}
				tr.BranchHead = head
			case closed:
				if merge, err = fixtureMerge(f.Repo, t, at); err != nil {
					return "", err
				}
				tr.BranchHead, tr.MergeSHA = head, merge
			}
			stats.Transitions = append(stats.Transitions, tr)
		}
		body := brief
		if designed {
			body = full
		}
		front := fmt.Sprintf("---\ntitle: %q\nstatus: %q\nsystem: %q\ntype: %q\npriority: \"medium\"\ncreated: %q\n", t.title, status, FixtureSystem, t.typ, created)
		if closed {
			front += fmt.Sprintf("done_at: %q\n", at.Format(time.RFC3339))
		}
		if err := os.WriteFile(file, []byte(front+"---\n\n"+body), 0o644); err != nil {
			return "", err
		}
		if closed {
			data, err := json.MarshalIndent(stats, "", "  ")
			if err != nil {
				return "", err
			}
			if err := os.WriteFile(tracker.StatsSidecarPath(file), append(data, '\n'), 0o644); err != nil {
				return "", err
			}
		}
		if err := fixtureCommit(f.Tracker, at, fmt.Sprintf("chore(issues): %s → %s", t.dir, status)); err != nil {
			return "", err
		}
	}
	issues, err := tracker.LoadIssues(f.Issues)
	if err != nil {
		return "", err
	}
	for _, is := range issues {
		if is.FilePath == file {
			return is.Slug, nil
		}
	}
	return "", fmt.Errorf("%s did not load as an issue", file)
}

// fixtureBranch makes the task's branch from main with its one commit.
func fixtureBranch(repo string, t fixtureTask, at time.Time) (string, error) {
	if err := fixtureGit(repo, at, "checkout", "-q", "-b", "work/"+t.dir); err != nil {
		return "", err
	}
	if err := writeFixtureFiles(repo, path.Join("fixture", t.dir)); err != nil {
		return "", err
	}
	if err := fixtureCommit(repo, at, t.subject); err != nil {
		return "", err
	}
	out, err := git(repo, "rev-parse", "HEAD")
	return strings.TrimSpace(out), err
}

// fixtureMerge merges the task's branch into main with a merge commit.
func fixtureMerge(repo string, t fixtureTask, at time.Time) (string, error) {
	if err := fixtureGit(repo, at, "checkout", "-q", "main"); err != nil {
		return "", err
	}
	if err := fixtureGit(repo, at, "merge", "-q", "--no-ff", "-m", "chore(merge): work/"+t.dir, "work/"+t.dir); err != nil {
		return "", err
	}
	out, err := git(repo, "rev-parse", "HEAD")
	return strings.TrimSpace(out), err
}

// writeFixtureFiles writes the embedded folder's files over dir, each
// without its .txt suffix (issue.md.txt is the tracker's, not the code's).
func writeFixtureFiles(dir, from string) error {
	entries, err := fixtureFS.ReadDir(from)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || e.Name() == "issue.md.txt" {
			continue
		}
		data, err := fixtureFS.ReadFile(path.Join(from, e.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, strings.TrimSuffix(e.Name(), ".txt")), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func fixtureGitInit(dirs ...string) error {
	for _, d := range dirs {
		if err := fixtureGit(d, fixtureStart, "init", "-q", "-b", "main"); err != nil {
			return err
		}
		// No maintenance run left writing .git/objects after a commit.
		for _, kv := range [][2]string{{"maintenance.auto", "false"}, {"gc.auto", "0"}, {"core.autocrlf", "false"}} {
			if err := fixtureGit(d, fixtureStart, "config", kv[0], kv[1]); err != nil {
				return err
			}
		}
	}
	return nil
}

func fixtureCommit(dir string, at time.Time, msg string) error {
	if err := fixtureGit(dir, at, "add", "-A"); err != nil {
		return err
	}
	return fixtureGit(dir, at, "commit", "-q", "--no-verify", "-m", msg)
}

// fixtureGit runs git with the fixture's identity and the commit time at,
// so every machine makes the same commits.
func fixtureGit(dir string, at time.Time, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	date := "@" + strconv.FormatInt(at.Unix(), 10) + " +0000"
	cmd.Env = append(gitEnv(os.Environ()),
		"GIT_AUTHOR_NAME="+fixtureWho, "GIT_AUTHOR_EMAIL="+fixtureEmail, "GIT_AUTHOR_DATE="+date,
		"GIT_COMMITTER_NAME="+fixtureWho, "GIT_COMMITTER_EMAIL="+fixtureEmail, "GIT_COMMITTER_DATE="+date)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return nil
}
