package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"gopkg.in/yaml.v3"
)

// ScenarioSpec is playtest/scenarios/<name>/scenario.yaml: a named starting
// state. Its issue fixtures live in tracker/, not issues/, so they never read
// as a real issues tree; they are copied to issues/ in the throwaway project.
type ScenarioSpec struct {
	Title   string `yaml:"title"`
	Project struct {
		Name string `yaml:"name"`
		Slug string `yaml:"slug"`
		// Repo is the project's repo: in projects.yaml; the engine's
		// (kacperwerema/dark-factory) makes the board treat it as the
		// engine project (report-bug's board, a release candidate's soak).
		Repo string `yaml:"repo"`
		// Extra is raw YAML added to the project's entry in projects.yaml
		// (company:, departments:, …), indented under it.
		Extra string `yaml:"extra"`
	} `yaml:"project"`
	Issues   string `yaml:"issues"`
	Workflow string `yaml:"workflow"`
	Docs     string `yaml:"docs"`
	// Root, when set, is a folder copied into the project root as is: files a
	// view reads beside the issues (sessions/ handoff notes, a changelog,
	// playtest/ sessions and .playtest/ runs).
	Root string `yaml:"root"`
	// Commits are applied on main after the first commit, in order: each
	// overlays its folder on the project and commits it.
	Commits []ScenarioCommit `yaml:"commits"`
	// Tags are annotated tags made after the commits, each on main (or Ref).
	Tags []ScenarioTag `yaml:"tags"`
	// Branches are created after the commits, each from main (or From).
	Branches []ScenarioBranch `yaml:"branches"`
	// Upstream, when set, is a branch main tracks (git branch -u), for views
	// that compare main with its upstream (the Status page's Not pushed).
	Upstream string `yaml:"upstream"`
	// Generate writes issues into the project next to the fixtures, for
	// scenarios that need volume (hundreds of issues) without a committed
	// fixture per issue.
	Generate []ScenarioGenerate `yaml:"generate"`
	// Transcripts are Claude Code sessions written under the scenario's
	// home (~/.claude/projects), for views that read spend.
	Transcripts []ScenarioTranscript `yaml:"transcripts"`
	// TelemetryData, when set, is a folder in the scenario holding a
	// telemetry collector's batches.jsonl: copied beside the project and
	// named by telemetry_data: in projects.yaml, for the /telemetry page.
	TelemetryData string `yaml:"telemetry_data"`
	// ExtraConfig is merged into the staged projects.yaml's top level
	// (identity:, allowed_hosts:, telemetry_data: …), for views that read a
	// key outside the project's entry. projects and viewer_url are the
	// stager's own and refused.
	ExtraConfig map[string]any `yaml:"extra_config"`
}

// stagerKeys are the projects.yaml keys the stager writes itself.
var stagerKeys = []string{"projects", "viewer_url"}

// ScenarioTranscript is one Claude Code session run in Cwd (relative to the
// project root: "" for the root, ".worktrees/<slug>" for an issue's
// worktree), named Agent ("" for none), with one assistant message per
// Messages entry: numbers only.
type ScenarioTranscript struct {
	Cwd      string            `yaml:"cwd"`
	Agent    string            `yaml:"agent"`
	Messages []ScenarioMessage `yaml:"messages"`
}

// ScenarioMessage is one assistant message: Ago is "-3d" (days before the
// run; "" is now), Model its model, and its token counts.
type ScenarioMessage struct {
	Ago       string `yaml:"ago"`
	Model     string `yaml:"model"`
	Input     int64  `yaml:"input"`
	Output    int64  `yaml:"output"`
	CacheRead int64  `yaml:"cache_read"`
}

// ScenarioGenerate writes Count issues titled Title (a fmt pattern with one
// %d, numbered from 1), all in Status, under issues/<Dir>/ with Dir as their
// system. Created pins their mtime like a fixture's created:; "-3d" is three
// days before the run, for views that look at the last few days. Extra is
// raw frontmatter added to each, Body their body.
type ScenarioGenerate struct {
	Count   int    `yaml:"count"`
	Dir     string `yaml:"dir"`
	Title   string `yaml:"title"`
	Status  string `yaml:"status"`
	Created string `yaml:"created"`
	Extra   string `yaml:"extra"`
	Body    string `yaml:"body"`
}

// ScenarioCommit overlays Files (a folder in the scenario) and commits it
// with Message.
type ScenarioCommit struct {
	Files   string `yaml:"files"`
	Message string `yaml:"message"`
}

// ScenarioTag is an annotated tag on Ref (default main), such as a release
// candidate tag. Its date is pinned like a commit's.
type ScenarioTag struct {
	Name string `yaml:"name"`
	Ref  string `yaml:"ref"`
}

// ScenarioBranch is a branch the scenario creates: with Worktree it gets a
// worktree under .worktrees/ (the name's slashes become dashes), then its
// Commits, then Uncommitted overlaid and left uncommitted (worktree only).
type ScenarioBranch struct {
	Name        string           `yaml:"name"`
	From        string           `yaml:"from"`
	Worktree    bool             `yaml:"worktree"`
	Commits     []ScenarioCommit `yaml:"commits"`
	Uncommitted string           `yaml:"uncommitted"`
}

// LoadScenarioSpec reads dir/scenario.yaml and fills defaults.
func LoadScenarioSpec(dir string) (*ScenarioSpec, error) {
	data, err := os.ReadFile(filepath.Join(dir, "scenario.yaml"))
	if err != nil {
		return nil, err
	}
	var s ScenarioSpec
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("%s: %v", filepath.Join(dir, "scenario.yaml"), err)
	}
	if s.Project.Slug == "" {
		s.Project.Slug = "demo"
	}
	if s.Project.Name == "" {
		s.Project.Name = "Playtest " + s.Project.Slug
	}
	if s.Issues == "" {
		s.Issues = "tracker"
	}
	if s.Workflow == "" {
		s.Workflow = "workflow.yaml"
	}
	for _, k := range stagerKeys {
		if _, ok := s.ExtraConfig[k]; ok {
			return nil, fmt.Errorf("%s: extra_config: %s is written by the stager; use project: for the project's entry", filepath.Join(dir, "scenario.yaml"), k)
		}
	}
	if _, ok := s.ExtraConfig["telemetry_data"]; ok && s.TelemetryData != "" {
		return nil, fmt.Errorf("%s: extra_config: telemetry_data and telemetry_data: both set; keep one", filepath.Join(dir, "scenario.yaml"))
	}
	for i, g := range s.Generate {
		if g.Count <= 0 || g.Status == "" || strings.Count(g.Title, "%") != 1 {
			return nil, fmt.Errorf("%s: generate[%d] needs count > 0, a status and a title with one %%d", filepath.Join(dir, "scenario.yaml"), i)
		}
	}
	return &s, nil
}

// generateIssues writes the spec's generated issues under dir (the project's
// issues/), before their times are pinned.
func generateIssues(dir string, gens []ScenarioGenerate) error {
	for _, g := range gens {
		for i := 1; i <= g.Count; i++ {
			title := fmt.Sprintf(g.Title, i)
			var b strings.Builder
			fmt.Fprintf(&b, "---\ntitle: %q\nstatus: %q\n", title, g.Status)
			if g.Dir != "" {
				fmt.Fprintf(&b, "system: %q\n", g.Dir)
			}
			if g.Created != "" {
				fmt.Fprintf(&b, "created: %q\n", createdDate(g.Created, time.Now()))
			}
			if extra := strings.TrimRight(g.Extra, "\n"); extra != "" {
				b.WriteString(extra + "\n")
			}
			b.WriteString("---\n\n" + g.Body + "\n")
			p := filepath.Join(dir, g.Dir, generatedName(title)+".md")
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeTranscripts writes each session as Claude Code would: in
// projectsDir/<cwd with every non-alphanumeric as ->/<n>.jsonl.
func writeTranscripts(projectsDir, proj string, ts []ScenarioTranscript, now time.Time) error {
	slug := regexp.MustCompile(`[^A-Za-z0-9]`)
	for i, t := range ts {
		cwd := filepath.Join(proj, filepath.FromSlash(t.Cwd))
		dir := filepath.Join(projectsDir, slug.ReplaceAllString(cwd, "-"))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		var b strings.Builder
		if t.Agent != "" {
			fmt.Fprintf(&b, "{\"type\":\"agent-name\",\"agentName\":%q}\n", t.Agent)
		}
		for j, m := range t.Messages {
			at := now
			if m := daysAgoRe.FindStringSubmatch(m.Ago); m != nil {
				n, _ := strconv.Atoi(m[1])
				at = now.AddDate(0, 0, -n)
			}
			fmt.Fprintf(&b, "{\"type\":\"assistant\",\"timestamp\":%q,\"message\":{\"id\":\"msg-%d-%d\",\"model\":%q,\"usage\":{\"input_tokens\":%d,\"output_tokens\":%d,\"cache_read_input_tokens\":%d}}}\n",
				at.UTC().Format(time.RFC3339), i, j, m.Model, m.Input, m.Output, m.CacheRead)
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("session-%d.jsonl", i)), []byte(b.String()), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// daysAgoRe is a relative created: date, "-3d".
var daysAgoRe = regexp.MustCompile(`^-(\d+)d$`)

// createdDate resolves a relative created: ("-3d") against now; any other
// value is kept as written.
func createdDate(created string, now time.Time) string {
	if m := daysAgoRe.FindStringSubmatch(created); m != nil {
		n, _ := strconv.Atoi(m[1])
		return now.AddDate(0, 0, -n).Format("2006-01-02")
	}
	return created
}

// generatedName is a title's file name: lower case, runs of other characters
// become one "-", matching the slug the viewer derives from the title.
func generatedName(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

// Binaries are issue-viewer and issue-cli built from the current checkout,
// once per run.
type Binaries struct {
	Dir    string
	Viewer string
	CLI    string
}

// viewerPkg is the viewer's main package in repo: cmd/issue-viewer, or the
// repo root for a checkout from before the viewer moved there (make serve
// BASE=<an older ref>).
func viewerPkg(repo string) string {
	if _, err := os.Stat(filepath.Join(repo, "cmd", "issue-viewer", "main.go")); err == nil {
		return "./cmd/issue-viewer"
	}
	return "."
}

// BuildBinaries runs `go build` for both binaries into dir.
func BuildBinaries(repo, dir string, log io.Writer) (*Binaries, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	b := &Binaries{Dir: dir, Viewer: filepath.Join(dir, "issue-viewer"), CLI: filepath.Join(dir, "issue-cli")}
	for _, t := range []struct{ out, pkg string }{{b.Viewer, viewerPkg(repo)}, {b.CLI, "./cmd/issue-cli"}} {
		cmd := exec.Command("go", "build", "-o", t.out, t.pkg)
		cmd.Dir = repo
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("go build %s: %v", t.pkg, err)
		}
	}
	return b, nil
}

// markerName proves a temp dir (and its tmux server) belongs to a playtest
// scenario, so teardown never removes or kills anything it didn't create.
const markerName = ".playtest-scenario"

// Scenario is one running copy of a scenario: a throwaway git project, its
// own projects.yaml, HOME and XDG dirs, a private tmux server, and a board
// on a free port. Every process it starts gets Env(), never the caller's.
type Scenario struct {
	Name string
	Spec *ScenarioSpec
	Root string // temp dir, holds the marker
	Proj string // the git project (issues/, workflow.yaml, docs/)
	Port int
	Base string // http://127.0.0.1:<port>
	// PortTaken is the port asked for (StartOpts.Port) when another process
	// held it and Port is a different, free one; 0 otherwise.
	PortTaken int

	bins    *Binaries
	board   *exec.Cmd
	commits int // scenario commits made so far (pins their dates)
	log     io.Writer
	env     []string
	// OwnerSession is the owner's sign-in cookie on this board (the runner
	// signs in with the code its own board prints): the browser pages and
	// post: steps carry it, as the owner's browser would.
	OwnerSession string
	codes        *codeWatch
}

// StartScenario copies the fixtures from scenarioDir/<name> into a fresh
// temp project and starts the board on it.
func StartScenario(scenarioDir, name string, bins *Binaries, log io.Writer) (*Scenario, error) {
	return StartScenarioWith(scenarioDir, name, bins, log, StartOpts{})
}

// StartOpts changes where and how StartScenario stages a board. The zero
// value is what ux-test uses: a fresh temp dir, any free port, the board's
// output to log and the owner signed in.
type StartOpts struct {
	// Root is the folder to stage in; it must not exist yet ("" is a fresh
	// pt-* temp dir).
	Root string
	// Port is the port to serve on when it is free; 0, or a port already
	// taken, picks a free one (PortTaken says which).
	Port int
	// Detached sends the board's output to Root/board.log instead of a
	// pipe and skips the owner sign-in, so the board outlives the process
	// that started it (playtest serve).
	Detached bool
}

// StartScenarioWith is StartScenario with options.
func StartScenarioWith(scenarioDir, name string, bins *Binaries, log io.Writer, o StartOpts) (*Scenario, error) {
	src := filepath.Join(scenarioDir, name)
	spec, err := LoadScenarioSpec(src)
	if err != nil {
		return nil, err
	}
	// A short path under the system temp dir: tmux sockets must fit in ~108
	// bytes, which a worktree path often doesn't.
	root := o.Root
	if root == "" {
		if root, err = os.MkdirTemp("", "pt-"); err != nil {
			return nil, err
		}
	} else if err := os.Mkdir(root, 0o755); err != nil {
		return nil, err
	}
	s := &Scenario{Name: name, Spec: spec, Root: root, Proj: filepath.Join(root, "proj"), bins: bins, log: log}
	fail := func(err error) (*Scenario, error) {
		s.Stop(false)
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(root, markerName), []byte(name+"\n"), 0o644); err != nil {
		return fail(err)
	}
	for _, d := range []string{"home", "state", "config", "cache", "tmux", "proj/docs"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			return fail(err)
		}
	}
	if err := os.WriteFile(filepath.Join(s.TmuxDir(), markerName), []byte(name+"\n"), 0o644); err != nil {
		return fail(err)
	}
	if err := copyTree(filepath.Join(src, spec.Issues), filepath.Join(s.Proj, "issues")); err != nil {
		return fail(fmt.Errorf("scenario %s: issues: %v", name, err))
	}
	if err := generateIssues(filepath.Join(s.Proj, "issues"), spec.Generate); err != nil {
		return fail(fmt.Errorf("scenario %s: generate: %v", name, err))
	}
	if err := pinIssueTimes(filepath.Join(s.Proj, "issues")); err != nil {
		return fail(err)
	}
	if err := writeTranscripts(filepath.Join(root, "home", ".claude", "projects"), s.Proj, spec.Transcripts, time.Now()); err != nil {
		return fail(fmt.Errorf("scenario %s: transcripts: %v", name, err))
	}
	if err := copyFile(filepath.Join(src, spec.Workflow), filepath.Join(s.Proj, "workflow.yaml")); err != nil {
		return fail(fmt.Errorf("scenario %s: workflow: %v", name, err))
	}
	if spec.Docs != "" {
		if err := copyTree(filepath.Join(src, spec.Docs), filepath.Join(s.Proj, "docs")); err != nil {
			return fail(fmt.Errorf("scenario %s: docs: %v", name, err))
		}
	}
	if spec.Root != "" {
		if err := copyTree(filepath.Join(src, spec.Root), s.Proj); err != nil {
			return fail(fmt.Errorf("scenario %s: root: %v", name, err))
		}
	}
	// terminal: none keeps dispatch and lead spawns headless: otherwise the
	// board falls back to `i3-msg exec alacritty`, which opens a window on the
	// owner's desktop attached to the real tmux server.
	// viewer_url points every link and issue-cli call in the scenario at
	// its own board.
	if s.Port, s.PortTaken, err = pickPort(o.Port); err != nil {
		return fail(err)
	}
	s.Base = fmt.Sprintf("http://127.0.0.1:%d", s.Port)
	telemetryLine := ""
	if spec.TelemetryData != "" {
		dst := filepath.Join(s.Root, "telemetry-data")
		if err := copyTree(filepath.Join(src, spec.TelemetryData), dst); err != nil {
			return fail(fmt.Errorf("scenario %s: telemetry_data: %v", name, err))
		}
		telemetryLine = fmt.Sprintf("telemetry_data: %q\n", dst)
	}
	cfg, err := scenarioConfig(spec, s.Base, s.Proj, telemetryLine)
	if err != nil {
		return fail(fmt.Errorf("scenario %s: %v", name, err))
	}
	if err := os.WriteFile(s.ConfigPath(), []byte(cfg), 0o644); err != nil {
		return fail(err)
	}
	s.env = scenarioEnv(os.Environ(), s)
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "-A"},
		{"commit", "-q", "-m", "scenario " + name},
	} {
		cmd := s.command("git", args...)
		cmd.Env = append(cmd.Env, s.nextCommitDate()...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fail(fmt.Errorf("git %s: %v: %s", args[0], err, out))
		}
	}
	if err := s.buildHistory(src); err != nil {
		return fail(fmt.Errorf("scenario %s: %v", name, err))
	}
	// -owner-code terminal: the board prints its sign-in code on stderr,
	// which only the runner reads (codeWatch), never a dialog on the
	// owner's desktop.
	s.board = s.command(bins.Viewer, "-config", s.ConfigPath(), "-port", fmt.Sprint(s.Port), "-owner-code", "terminal")
	if o.Detached {
		// A file, not a pipe: once serve exits nobody reads a pipe, and the
		// board would die on its next log line.
		f, err := os.Create(s.LogPath())
		if err != nil {
			return fail(err)
		}
		defer f.Close()
		s.board.Stdout, s.board.Stderr = f, f
	} else {
		s.codes = &codeWatch{w: log}
		s.board.Stdout, s.board.Stderr = log, s.codes
	}
	s.board.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := s.board.Start(); err != nil {
		return fail(fmt.Errorf("start board: %v", err))
	}
	if err := waitHTTP(s.Base+"/", boardReadyBudget); err != nil {
		if o.Detached {
			err = fmt.Errorf("%v (log: %s)", err, s.LogPath())
		}
		return fail(fmt.Errorf("board on %s: %v", s.Base, err))
	}
	if o.Detached {
		return s, nil
	}
	if s.OwnerSession, err = s.signInOwner(); err != nil {
		return fail(fmt.Errorf("owner sign-in on %s: %v", s.Base, err))
	}
	return s, nil
}

// ownerCodeLine is the line the viewer prints with -owner-code terminal.
var ownerCodeLine = regexp.MustCompile(`Owner sign-in code: (\d{4})-(\d{4})`)

// codeWatch passes the board's stderr to the log and keeps the last sign-in
// code it printed.
type codeWatch struct {
	w    io.Writer
	mu   sync.Mutex
	buf  []byte
	code string
}

func (c *codeWatch) Write(p []byte) (int, error) {
	c.mu.Lock()
	c.buf = append(c.buf, p...)
	if m := ownerCodeLine.FindAllSubmatch(c.buf, -1); len(m) > 0 {
		last := m[len(m)-1]
		c.code = string(last[1]) + string(last[2])
	}
	if i := bytes.LastIndexByte(c.buf, '\n'); i >= 0 {
		c.buf = c.buf[i+1:]
	}
	c.mu.Unlock()
	return c.w.Write(p)
}

func (c *codeWatch) take() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	code := c.code
	c.code = ""
	return code
}

// signInOwner runs the owner's sign-in against the board over HTTP: ask for
// a code, read it off the board's stderr, type it in, keep the cookie.
func (s *Scenario) signInOwner() (string, error) {
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	post := func(path string, form url.Values) (*http.Response, error) {
		req, err := http.NewRequest(http.MethodPost, s.Base+path, strings.NewReader(form.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", s.Base)
		return client.Do(req)
	}
	resp, err := post("/owner/code", nil)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	var code string
	for deadline := time.Now().Add(5 * time.Second); code == "" && time.Now().Before(deadline); {
		if code = s.codes.take(); code == "" {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if code == "" {
		return "", fmt.Errorf("the board printed no sign-in code")
	}
	resp, err = post("/owner/pair", url.Values{"code": {code}})
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	for _, c := range resp.Cookies() {
		if c.Name == "iv_owner" && c.Value != "" {
			return c.Value, nil
		}
	}
	return "", fmt.Errorf("pair answered %d without a session", resp.StatusCode)
}

// buildHistory applies the spec's commits on main and creates its branches.
func (s *Scenario) buildHistory(src string) error {
	gitIn := func(dir string, args ...string) error {
		cmd := s.command("git", args...)
		cmd.Dir = dir
		if args[0] == "commit" || args[0] == "tag" {
			cmd.Env = append(cmd.Env, s.nextCommitDate()...)
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
		return nil
	}
	commit := func(dir string, c ScenarioCommit) error {
		if err := copyTree(filepath.Join(src, c.Files), dir); err != nil {
			return fmt.Errorf("commit %q: %v", c.Message, err)
		}
		msg := c.Message
		if msg == "" {
			msg = "scenario commit"
		}
		if err := gitIn(dir, "add", "-A"); err != nil {
			return err
		}
		return gitIn(dir, "commit", "-q", "-m", msg)
	}
	// Worktrees live inside the project: keep them out of its commits.
	if len(s.Spec.Branches) > 0 {
		if err := os.WriteFile(filepath.Join(s.Proj, ".git", "info", "exclude"), []byte(".worktrees/\n"), 0o644); err != nil {
			return err
		}
	}
	for _, c := range s.Spec.Commits {
		if err := commit(s.Proj, c); err != nil {
			return err
		}
	}
	for _, t := range s.Spec.Tags {
		ref := t.Ref
		if ref == "" {
			ref = "main"
		}
		if err := gitIn(s.Proj, "tag", "-a", t.Name, "-m", t.Name, ref); err != nil {
			return err
		}
	}
	for _, b := range s.Spec.Branches {
		from := b.From
		if from == "" {
			from = "main"
		}
		dir := s.Proj
		if b.Worktree {
			dir = filepath.Join(s.Proj, ".worktrees", strings.ReplaceAll(b.Name, "/", "-"))
			if err := gitIn(s.Proj, "worktree", "add", "-q", "-b", b.Name, dir, from); err != nil {
				return err
			}
		} else if err := gitIn(s.Proj, "checkout", "-q", "-b", b.Name, from); err != nil {
			return err
		}
		for _, c := range b.Commits {
			if err := commit(dir, c); err != nil {
				return err
			}
		}
		if b.Uncommitted != "" && b.Worktree {
			if err := copyTree(filepath.Join(src, b.Uncommitted), dir); err != nil {
				return err
			}
		}
		if !b.Worktree {
			if err := gitIn(s.Proj, "checkout", "-q", "main"); err != nil {
				return err
			}
		}
	}
	if s.Spec.Upstream != "" {
		return gitIn(s.Proj, "branch", "-q", "--set-upstream-to="+s.Spec.Upstream, "main")
	}
	return nil
}

// nextCommitDate pins a scenario commit's author and committer dates (an
// hour apart from 2026-10-01 12:00 UTC), so commit shas and the dates views
// show are the same on every run.
func (s *Scenario) nextCommitDate() []string {
	d := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC).Add(time.Duration(s.commits) * time.Hour).Format(time.RFC3339)
	s.commits++
	return []string{"GIT_AUTHOR_DATE=" + d, "GIT_COMMITTER_DATE=" + d}
}

// ConfigPath is the scenario's projects.yaml.
func (s *Scenario) ConfigPath() string { return filepath.Join(s.Root, "projects.yaml") }

// LogPath is a detached board's output (StartOpts.Detached).
func (s *Scenario) LogPath() string { return filepath.Join(s.Root, "board.log") }

// TmuxDir is the scenario's TMUX_TMPDIR: its tmux server's socket lives here.
func (s *Scenario) TmuxDir() string { return filepath.Join(s.Root, "tmux") }

// TmuxSocket is the scenario's tmux socket: where tmux puts its default
// socket under TmuxDir. The runner's own tmux calls name it with -S.
func (s *Scenario) TmuxSocket() string {
	return filepath.Join(s.TmuxDir(), "tmux-"+strconv.Itoa(os.Getuid()), "default")
}

// IssueFile is the path of an issue in the scenario project, found the way
// the board finds it (slugs lower-case the system folder).
func (s *Scenario) IssueFile(slug string) (string, error) {
	issues, err := tracker.LoadIssues(filepath.Join(s.Proj, "issues"))
	if err != nil {
		return "", err
	}
	for _, is := range issues {
		if is.Slug == slug {
			return is.FilePath, nil
		}
	}
	return "", fmt.Errorf("no issue %q in the scenario", slug)
}

// createdRe reads an issue's created: date.
var createdRe = regexp.MustCompile(`(?m)^created:\s*"?(\d{4}-\d{2}-\d{2})`)

// pinIssueTimes sets each issue file's mtime from its created: date. The
// board sorts issues by mtime, so copied fixtures would otherwise come out
// in whatever order the copy happened to write them.
func pinIssueTimes(dir string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		t := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
		if m := createdRe.FindSubmatch(data); m != nil {
			if c, err := time.Parse("2006-01-02", string(m[1])); err == nil {
				t = c.Add(12 * time.Hour)
			}
		}
		return os.Chtimes(p, t, t)
	})
}

// Env is the environment every scenario process runs with.
func (s *Scenario) Env() []string { return append([]string(nil), s.env...) }

// isolatedVars are dropped from the caller's environment: they would point a
// scenario process at the real config, the real tmux server, or tag it as a
// dispatched agent.
var isolatedVars = []string{
	"TMUX", "TMUX_PANE", "TMUX_TMPDIR", "HOME", "XDG_STATE_HOME", "XDG_CONFIG_HOME",
	"XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_RUNTIME_DIR", "ISSUE_VIEWER_CONFIG", "ISSUE_VIEWER_URL",
	"ISSUE_VIEWER_ISSUE_SLUG", "ISSUE_VIEWER_SERVER_PWD", "ISSUE_CLI_LOG", "AGENT_NAME",
	"AI_AGENT", "CLAUDECODE", "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE",
	"CLAUDE_CONFIG_DIR",
}

// scenarioEnv builds a scenario's environment from base: the isolated
// variables are replaced so nothing reaches ~/Work/projects.yaml, the user's
// state, or the default tmux socket.
func scenarioEnv(base []string, s *Scenario) []string {
	drop := map[string]bool{}
	for _, k := range isolatedVars {
		drop[k] = true
	}
	var env []string
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if drop[k] || strings.HasPrefix(k, "GIT_AUTHOR_") || strings.HasPrefix(k, "GIT_COMMITTER_") {
			continue
		}
		if k == "PATH" && s.bins != nil {
			kv = "PATH=" + s.bins.Dir + string(os.PathListSeparator) + strings.TrimPrefix(kv, "PATH=")
		}
		env = append(env, kv)
	}
	return append(env,
		"HOME="+filepath.Join(s.Root, "home"),
		"XDG_STATE_HOME="+filepath.Join(s.Root, "state"),
		"XDG_CONFIG_HOME="+filepath.Join(s.Root, "config"),
		"XDG_CACHE_HOME="+filepath.Join(s.Root, "cache"),
		"TMUX_TMPDIR="+s.TmuxDir(),
		"ISSUE_VIEWER_CONFIG="+s.ConfigPath(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=playtest", "GIT_AUTHOR_EMAIL=playtest@example.invalid",
		"GIT_COMMITTER_NAME=playtest", "GIT_COMMITTER_EMAIL=playtest@example.invalid",
		"TZ=UTC",
	)
}

// command builds a process that runs inside the scenario: its env, cwd in
// the project.
func (s *Scenario) command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Dir = s.Proj
	cmd.Env = s.Env()
	return cmd
}

// Run runs a command in the scenario and returns its combined output and
// exit code.
func (s *Scenario) Run(ctx context.Context, name string, args ...string) (string, int, error) {
	cmd := s.command(name, args...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return "", -1, err
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		code := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				return buf.String(), -1, err
			}
		}
		return buf.String(), code, nil
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		return buf.String(), -1, ctx.Err()
	}
}

// CLI runs the scenario's branch-built issue-cli.
func (s *Scenario) CLI(ctx context.Context, args ...string) (string, int, error) {
	return s.Run(ctx, s.bins.CLI, args...)
}

// Tmux runs tmux against the scenario's private server.
func (s *Scenario) Tmux(ctx context.Context, args ...string) (string, int, error) {
	if err := s.guardTmux(); err != nil {
		return "", -1, err
	}
	if err := os.MkdirAll(filepath.Dir(s.TmuxSocket()), 0o700); err != nil {
		return "", -1, err
	}
	return s.Run(ctx, "tmux", append([]string{"-S", s.TmuxSocket()}, args...)...)
}

// guardTmux refuses to talk to a tmux server whose socket dir is not this
// scenario's marked private dir.
func (s *Scenario) guardTmux() error {
	dir := s.TmuxDir()
	if !strings.HasPrefix(dir, s.Root+string(os.PathSeparator)) {
		return fmt.Errorf("tmux dir %s is outside the scenario", dir)
	}
	if _, err := os.Stat(filepath.Join(dir, markerName)); err != nil {
		return fmt.Errorf("tmux dir %s has no %s marker; refusing to touch it", dir, markerName)
	}
	return nil
}

// Expand replaces {project}, {base} and {dir} in a session string.
func (s *Scenario) Expand(v string) string {
	return strings.NewReplacer("{project}", s.Spec.Project.Slug, "{base}", s.Base, "{dir}", s.Proj).Replace(v)
}

// Stop kills the board and the private tmux server, then removes the temp
// dir unless keep is set.
func (s *Scenario) Stop(keep bool) {
	if s.board != nil && s.board.Process != nil {
		_ = syscall.Kill(-s.board.Process.Pid, syscall.SIGKILL)
		_ = s.board.Wait()
		s.board = nil
	}
	if s.guardTmux() == nil {
		if _, err := exec.LookPath("tmux"); err == nil {
			// Name the private socket and drop $TMUX: a Scenario built from
			// only a Root (stopRecord) has no env, and an inherited $TMUX
			// sends kill-server to the shared server (2026-10-31, 2026-11-01).
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			cmd := exec.CommandContext(ctx, "tmux", "-S", s.TmuxSocket(), "kill-server")
			for _, kv := range os.Environ() {
				if !strings.HasPrefix(kv, "TMUX=") && !strings.HasPrefix(kv, "TMUX_PANE=") {
					cmd.Env = append(cmd.Env, kv)
				}
			}
			_ = cmd.Run()
			cancel()
		}
	}
	if keep || s.Root == "" {
		return
	}
	if _, err := os.Stat(filepath.Join(s.Root, markerName)); err == nil {
		_ = os.RemoveAll(s.Root)
	}
}

// scenarioConfig is the staged projects.yaml: extra_config: on top, then
// the telemetry line, viewer_url and the one project's entry.
func scenarioConfig(spec *ScenarioSpec, base, proj, telemetryLine string) (string, error) {
	cfg := telemetryLine + fmt.Sprintf("viewer_url: %q\nprojects:\n  - name: %q\n    slug: %q\n    issues: %q\n    docs: %q\n    workdir: %q\n    terminal: \"none\"\n",
		base, spec.Project.Name, spec.Project.Slug, filepath.Join(proj, "issues"), filepath.Join(proj, "docs"), proj)
	if spec.Project.Repo != "" {
		cfg += fmt.Sprintf("    repo: %q\n", spec.Project.Repo)
	}
	if extra := strings.TrimRight(spec.Project.Extra, "\n"); extra != "" {
		for _, line := range strings.Split(extra, "\n") {
			cfg += "    " + line + "\n"
		}
	}
	if len(spec.ExtraConfig) > 0 {
		extra, err := yaml.Marshal(spec.ExtraConfig)
		if err != nil {
			return "", fmt.Errorf("extra_config: %v", err)
		}
		cfg = string(extra) + cfg
	}
	return cfg, nil
}

// pickPort is want when nothing listens on it, else a free port and want as
// the one that was taken. want 0 is any free port.
func pickPort(want int) (port, taken int, err error) {
	if want > 0 {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", want))
		if err == nil {
			l.Close()
			return want, 0, nil
		}
		taken = want
	}
	port, err = freePort()
	return port, taken, err
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// boardReadyBudget is how long a scenario board gets to answer. It answers
// in about a second on a quiet machine; under make validate beside other
// workers it has taken over 10 s. waitHTTP returns as soon as it answers.
const boardReadyBudget = time.Minute

// waitHTTP polls url until it answers below 500. Each request has its own
// short timeout, so a board that accepts but hangs can't outlast budget.
func waitHTTP(url string, budget time.Duration) error {
	deadline := time.Now().Add(budget)
	client := &http.Client{Timeout: 2 * time.Second}
	var last error
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
			last = fmt.Errorf("status %d", resp.StatusCode)
		} else {
			last = err
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("not ready after %s: %v", budget, last)
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		return copyFile(p, filepath.Join(dst, rel))
	})
}
