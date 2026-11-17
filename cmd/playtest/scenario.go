package main

import (
	"bytes"

	"fmt"
	"io"
	"io/fs"

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
	// Untracked are paths in the project kept out of its git repository
	// (written to .git/info/exclude before the first commit): files a view
	// treats differently when git does not track them, such as a
	// department's workflows/ file the hall edits in place.
	Untracked []string `yaml:"untracked"`
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
//
// Kind "retro" writes retrospectives instead (generateRetros): Count files
// in the project root's retros/, as issue-cli retrospective writes them.
type ScenarioGenerate struct {
	// Kind is "issue" (the default) or "retro".
	Kind    string `yaml:"kind"`
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
// Commits, then its Tags (Ref defaults to the branch: a release branch's
// candidate tags on commits main lacks), then Uncommitted overlaid and left
// uncommitted (worktree only).
type ScenarioBranch struct {
	Name        string           `yaml:"name"`
	From        string           `yaml:"from"`
	Worktree    bool             `yaml:"worktree"`
	Commits     []ScenarioCommit `yaml:"commits"`
	Tags        []ScenarioTag    `yaml:"tags"`
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
		if g.Kind != "" && g.Kind != "issue" && g.Kind != "retro" {
			return nil, fmt.Errorf("%s: generate[%d]: kind %q: want issue (the default) or retro", filepath.Join(dir, "scenario.yaml"), i, g.Kind)
		}
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
		if g.Kind == "retro" {
			continue
		}
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

// retroEpoch is a generated retro's first date when its entry has no
// created:.
var retroEpoch = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

// generateRetros writes the spec's kind: retro entries into dir (the
// project's retros/). Retro i of an entry is dated i-1 hours after created:
// (12:00 UTC that day), in its Date line and its mtime alike, so the
// newest-first order is the same on every run and the highest number is the
// newest. Its Issue line is <dir>/<the title's slug>, System the dir.
func generateRetros(dir string, gens []ScenarioGenerate, now time.Time) error {
	for _, g := range gens {
		if g.Kind != "retro" {
			continue
		}
		start := retroEpoch
		if g.Created != "" {
			d, err := time.Parse("2006-01-02", createdDate(g.Created, now))
			if err != nil {
				return fmt.Errorf("retro created %q: want YYYY-MM-DD or -Nd", g.Created)
			}
			start = d.Add(12 * time.Hour)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		for i := 1; i <= g.Count; i++ {
			title := fmt.Sprintf(g.Title, i)
			at := start.Add(time.Duration(i-1) * time.Hour)
			slug := generatedName(title)
			if g.Dir != "" {
				slug = strings.ToLower(g.Dir) + "/" + slug
			}
			var b strings.Builder
			fmt.Fprintf(&b, "# Workflow Retrospective\n\nIssue: %s\nTitle: %s\nStatus: %s\n", slug, title, g.Status)
			if g.Dir != "" {
				fmt.Fprintf(&b, "System: %s\n", g.Dir)
			}
			fmt.Fprintf(&b, "Date: %s\n", at.Format(time.RFC3339))
			if extra := strings.TrimRight(g.Extra, "\n"); extra != "" {
				b.WriteString(extra + "\n")
			}
			body := strings.TrimSpace(g.Body)
			if body == "" {
				body = fmt.Sprintf("Tooling/workflow friction: generated retro %d.", i)
			}
			b.WriteString("\n" + body + "\n")
			p := filepath.Join(dir, at.Format("20060102-150405")+"-"+strings.ReplaceAll(slug, "/", "-")+".md")
			if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
				return err
			}
			if err := os.Chtimes(p, at, at); err != nil {
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

	bins  *Binaries
	board *exec.Cmd
	// forgetStop drops the signal stop hook (stop.go) once Stop has run.
	forgetStop func()
	commits    int // scenario commits made so far (pins their dates)
	log        io.Writer
	env        []string
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
	if err := generateRetros(filepath.Join(s.Proj, "retros"), spec.Generate, time.Now()); err != nil {
		return fail(fmt.Errorf("scenario %s: generate: %v", name, err))
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
		if args[0] == "add" && len(spec.Untracked) > 0 {
			if err := writeExclude(s.Proj, spec.Untracked...); err != nil {
				return fail(err)
			}
		}
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
	if !o.Detached {
		// A runner stopped by a signal takes its board and tmux server
		// along; make serve's detached board outlives it on purpose.
		s.forgetStop = atStop(func() { s.Stop(false) })
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

// ownerCodeText in a type action's text is the sign-in code the board
// printed last (a page asked for it: the in-place sign-in dialog).
const ownerCodeText = "{owner_code}"

// awaitOwnerCode waits up to budget for the board to print a sign-in code
// and takes it.
func (s *Scenario) awaitOwnerCode(budget time.Duration) (string, error) {
	if s.codes == nil {
		return "", fmt.Errorf("this board's sign-in codes go to its log, not the runner")
	}
	for deadline := time.Now().Add(budget); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if code := s.codes.take(); code != "" {
			return code, nil
		}
	}
	return "", fmt.Errorf("the board printed no sign-in code")
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
	code, err := s.awaitOwnerCode(5 * time.Second)
	if err != nil {
		return "", err
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
		if err := writeExclude(s.Proj, ".worktrees/"); err != nil {
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
		for _, t := range b.Tags {
			ref := t.Ref
			if ref == "" {
				ref = b.Name
			}
			if err := gitIn(s.Proj, "tag", "-a", t.Name, "-m", t.Name, ref); err != nil {
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
