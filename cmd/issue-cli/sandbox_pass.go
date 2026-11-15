package main

// The sandbox pass: the one place the worker guard lets an owner-only
// command through for a worker or agent session, and only on a marked
// sandbox config (the owner's design, cli/sandbox-marker-owner-only-commands-
// pass-on-a-sandbox-config-only-owner-approves-the-design D-2..D-6).
//
// A worker controls its own environment and can write any file under the
// temp dir, so the marker is a declaration, not a credential. What protects
// the real board is containment: the pass is given only when everything the
// command could change lies inside the one folder that holds the marker, and
// that folder is a temp folder outside every .worktrees/.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/viewerstate"
)

// sandboxMarkerName is the file lib.sh's sandbox_project writes beside the
// sandbox's projects.yaml.
const sandboxMarkerName = ".issue-cli-sandbox"

// sandboxMarker is the marker's content.
type sandboxMarker struct {
	Folder  string `json:"folder"`
	Created string `json:"created"`
	By      string `json:"by"`
}

// guardRule says what the sandbox pass does for one guarded verb.
type guardRule int

const (
	// sandboxPasses: refused for a worker or agent on the real board,
	// passed on a marked sandbox.
	sandboxPasses guardRule = iota + 1
	// neverInSandbox: refused for a worker or agent everywhere, because it
	// starts agents, spends tokens or reaches outside the machine (owner's
	// rule D-4: "for now we disallow it and see if this starts blocking us").
	neverInSandbox
)

// guardVerbs is every guarded verb and its rule. A guard site names its verb
// here; TestEveryWorkerGuardConsultsTheSandboxPass fails on a guard whose
// verb is missing. A new guarded command's rule is the owner's call, asked
// in that command's design (D-3: "let's ask always when building new").
var guardVerbs = map[string]guardRule{
	// Issues: a worker moves its own issue only.
	"claim":                  sandboxPasses,
	"start":                  sandboxPasses,
	"transition":             sandboxPasses,
	"close":                  sandboxPasses,
	"rounds --cannot-fix":    sandboxPasses,
	"decision challenge for": sandboxPasses,
	"approve":                sandboxPasses,
	// Owner boxes and decisions.
	"tick owner boxes":          sandboxPasses,
	"change owner boxes":        sandboxPasses,
	"edit owner boxes":          sandboxPasses,
	"change a hard decision":    sandboxPasses,
	"decide --scope":            sandboxPasses,
	"critique --override":       sandboxPasses,
	"department add":            sandboxPasses,
	"department remove":         sandboxPasses,
	"department update":         sandboxPasses,
	"project init on my config": sandboxPasses,
	"output review":             sandboxPasses,
	"job tick":                  sandboxPasses,
	"job run":                   sandboxPasses,
	"job auto":                  sandboxPasses,
	"yolo":                      sandboxPasses,
	"autostart":                 sandboxPasses,
	"queue":                     sandboxPasses,
	"agents rest":               sandboxPasses,
	"agents sweep":              sandboxPasses,
	"bench task add":            sandboxPasses,
	"bench task remove":         sandboxPasses,
	"bench holdout":             sandboxPasses,
	"bench budget":              sandboxPasses,
	// Never in a sandbox: these start agents, spend tokens or leave the
	// machine.
	"dispatch":           neverInSandbox,
	"lead spawn":         neverInSandbox,
	"lead message":       neverInSandbox,
	"queue next --start": neverInSandbox,
	"health review":      neverInSandbox,
	"postmortem start":   neverInSandbox,
	"experiment queue":   neverInSandbox,
	"bench run":          neverInSandbox,
	// prove --lead-run runs a lead link's command; refused until the owner
	// says otherwise (it arrived after the design, D-3: asked on the issue).
	"prove --lead-run": neverInSandbox,
	"bench nightly":    neverInSandbox,
}

// sandboxVerdict is the sandbox check's answer for this process.
type sandboxVerdict struct {
	// Pass: the config is a marked sandbox and every condition holds.
	Pass bool `json:"pass"`
	// Marked: a marker sits beside the config (it may still fail).
	Marked bool `json:"marked"`
	// Folder is the sandbox folder when marked.
	Folder string `json:"folder,omitempty"`
	// Why is the condition that failed, or what passed.
	Why string `json:"why"`
}

// sandboxTempRoots are the folders a sandbox may live under. $TMPDIR is not
// one: a worker sets it. A var so tests can name another root.
var sandboxTempRoots = func() []string {
	return []string{"/tmp", "/private/tmp", "/var/tmp", "/private/var/tmp", "/var/folders", "/private/var/folders"}
}

// sandboxCheck is the sandbox verdict for ctx, computed once.
func sandboxCheck(ctx *Context) sandboxVerdict {
	if ctx.sandbox == nil {
		v := computeSandbox(ctx)
		ctx.sandbox = &v
	}
	return *ctx.sandbox
}

func computeSandbox(ctx *Context) sandboxVerdict {
	cfg := ctx.Config.Path
	if cfg == "" {
		cfg = ctx.ConfigPath
	}
	if cfg == "" && ctx.Project != nil {
		cfg = ctx.Project.ConfigPath
	}
	if cfg == "" {
		return sandboxVerdict{Why: "no config file"}
	}
	dir := sandboxResolve(filepath.Dir(cfg))
	m, err := readSandboxMarker(dir)
	if err != nil {
		return sandboxVerdict{Why: fmt.Sprintf("no %s beside %s", sandboxMarkerName, cfg)}
	}
	v := sandboxVerdict{Marked: true, Folder: dir}
	fail := func(format string, a ...any) sandboxVerdict {
		v.Why = fmt.Sprintf(format, a...)
		return v
	}
	if why := ctx.Config.Why; why != "--config" && why != "$ISSUE_VIEWER_CONFIG" {
		return fail("the config came from %s, not --config or $ISSUE_VIEWER_CONFIG", valueOr(why, "nowhere named"))
	}
	if m.Folder == "" || sandboxResolve(m.Folder) != dir {
		return fail("the marker names %q, not its own folder %s (a stale or copied marker)", m.Folder, dir)
	}
	if !sandboxUnder(dir, sandboxTempRoots()) {
		return fail("%s is not under a temp folder (%s)", dir, strings.Join(sandboxTempRoots(), ", "))
	}
	if strings.Contains(filepath.ToSlash(dir)+"/", "/.worktrees/") {
		return fail("%s is under a .worktrees/ folder", dir)
	}
	for _, real := range ownerConfigs() {
		if sameFile(cfg, real) {
			return fail("%s is the owner's config", cfg)
		}
	}
	for _, env := range []string{"XDG_STATE_HOME", "XDG_CONFIG_HOME"} {
		val := strings.TrimSpace(os.Getenv(env))
		if val == "" {
			return fail("$%s is not set to a folder in the sandbox", env)
		}
		if !sandboxUnder(sandboxResolve(val), []string{dir}) {
			return fail("$%s %s is outside %s", env, val, dir)
		}
	}
	projects := ctx.AllProjects
	if len(projects) == 0 && ctx.Project != nil {
		projects = []tracker.Project{*ctx.Project}
	}
	if len(projects) == 0 {
		return fail("the config has no project")
	}
	for i := range projects {
		for _, p := range sandboxProjectPaths(&projects[i], dir) {
			if !sandboxUnder(sandboxResolve(p.path), []string{dir}) {
				return fail("project %s: %s %s is outside %s", projects[i].Slug, p.what, p.path, dir)
			}
		}
	}
	v.Pass = true
	v.Why = "marked sandbox " + dir
	return v
}

type namedPath struct{ what, path string }

// sandboxProjectPaths is every path of p a command could write through:
// issues, docs, workflow, workdir, the tracker repo, department files and
// the git folder a workdir's .git file points at (a worktree of a real repo
// would write into that repo).
func sandboxProjectPaths(p *tracker.Project, dir string) []namedPath {
	root := p.WorkDir
	if root == "" {
		root = dir
	}
	abs := func(s string) string {
		if s == "" || filepath.IsAbs(s) {
			return s
		}
		return filepath.Join(root, s)
	}
	out := []namedPath{
		{"issues", abs(p.IssueDir)}, {"docs", abs(p.DocsDir)}, {"workflow", abs(p.WorkflowFile)},
		{"workdir", p.WorkDir}, {"tracker_git_dir", abs(p.TrackerGitDir)},
	}
	if p.Company != nil {
		out = append(out, namedPath{"company prompt", abs(p.Company.Prompt)})
	}
	deps := append([]tracker.Department{}, p.Departments...)
	if p.Product != nil {
		deps = append(deps, *p.Product)
	}
	for _, d := range deps {
		out = append(out, namedPath{"department " + d.Name + " workflow", abs(d.Workflow)}, namedPath{"department " + d.Name + " prompt", abs(d.Prompt)})
	}
	for _, d := range []string{p.WorkDir, p.IssueDir} {
		if g := gitFileTarget(abs(d)); g != "" {
			out = append(out, namedPath{"git folder of " + d, g})
		}
	}
	var kept []namedPath
	for _, n := range out {
		if n.path != "" {
			kept = append(kept, n)
		}
	}
	return kept
}

// gitFileTarget is the gitdir a .git file in dir points at ("" when dir has
// a .git folder or none). A relative gitdir is resolved against dir.
func gitFileTarget(dir string) string {
	if dir == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(dir, ".git"))
	if err != nil {
		return ""
	}
	g, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !ok {
		return ""
	}
	g = strings.TrimSpace(g)
	if !filepath.IsAbs(g) {
		g = filepath.Join(dir, g)
	}
	return g
}

// ownerConfigs are the owner's real configs whatever this process's XDG
// variables say: the default projects.yaml and the config viewer.json names,
// under the home folder from HOME and from the user database.
func ownerConfigs() []string {
	homes := map[string]bool{}
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		homes[h] = true
	}
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		homes[u.HomeDir] = true
	}
	var out []string
	if p := userConfigPath(); p != "" {
		out = append(out, p)
	}
	if s, err := viewerstate.Read(); err == nil && s.Config != "" {
		out = append(out, s.Config)
	}
	for h := range homes {
		out = append(out, filepath.Join(h, ".config", "ai-workflow-engine", "projects.yaml"))
		if s, err := readViewerState(filepath.Join(h, ".local", "state", "ai-workflow-engine", "viewer.json")); err == nil && s.Config != "" {
			out = append(out, s.Config)
		}
	}
	sort.Strings(out)
	return out
}

func readViewerState(path string) (viewerstate.State, error) {
	var s viewerstate.State
	b, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(b, &s)
	return s, err
}

func readSandboxMarker(dir string) (sandboxMarker, error) {
	var m sandboxMarker
	b, err := os.ReadFile(filepath.Join(dir, sandboxMarkerName))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("%s: %w", sandboxMarkerName, err)
	}
	return m, nil
}

// sandboxResolve makes p absolute and resolves symlinks in its longest
// existing prefix, so a path that doesn't exist yet compares by its real
// folder.
func sandboxResolve(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	p = filepath.Clean(p)
	rest := ""
	for dir := p; ; dir = filepath.Dir(dir) {
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(r, rest)
		}
		if filepath.Dir(dir) == dir {
			return p
		}
		rest = filepath.Join(filepath.Base(dir), rest)
	}
}

// sandboxUnder reports whether the resolved path p lies in one of roots.
func sandboxUnder(p string, roots []string) bool {
	for _, root := range roots {
		r := sandboxResolve(root)
		if p == r || strings.HasPrefix(p, r+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// guardWho is whoAmI for a guard: a worker or lead whose verb passes on a
// marked sandbox comes back as the owner's shell (RoleUnmanaged), and the
// pass is said on stderr and in the clilog. Elsewhere it is whoAmI.
func guardWho(ctx *Context, issues []*tracker.Issue, verb string) Identity {
	me := whoAmI(ctx.Project, issues)
	if me.Role == tracker.RoleUnmanaged {
		return me
	}
	if sandboxPass(ctx, verb, string(me.Role)) {
		return Identity{Role: tracker.RoleUnmanaged, Signal: "sandbox pass (was " + me.Signal + ")"}
	}
	return me
}

// guardAgent is agentSession for a guard: "" (the owner's shell) when the
// verb passes on a marked sandbox.
func guardAgent(ctx *Context, verb string) string {
	who := agentSession(ctx)
	if who == "" {
		return ""
	}
	if sandboxPass(ctx, verb, who) {
		return ""
	}
	return who
}

// guardNever marks a never-in-sandbox guard's refusal: on a marked sandbox
// it says the refusal is the owner's rule and records it, so the owner sees
// when it bites (D-4). It returns nothing; the caller refuses as before.
func guardNever(ctx *Context, verb string) {
	if ctx == nil || ctx.Project == nil {
		return
	}
	if v := sandboxCheck(ctx); v.Marked {
		ctx.sandboxRefused = true
		fmt.Fprintf(ctx.stderr(), "sandbox: %s stays refused in a sandbox (owner's rule, D-4); tell the owner if this blocks a check\n", verb)
	}
}

// sandboxPass decides verb for a worker or agent session (who) and says so.
func sandboxPass(ctx *Context, verb, who string) bool {
	if ctx == nil || ctx.Project == nil {
		return false
	}
	rule, known := guardVerbs[verb]
	if !known {
		// A guard with no rule never passes; the inventory test names it.
		return false
	}
	if rule == neverInSandbox {
		guardNever(ctx, verb)
		return false
	}
	v := sandboxCheck(ctx)
	if !v.Pass {
		if v.Marked || sandboxUnder(sandboxResolve(filepath.Dir(valueOr(ctx.Config.Path, ctx.ConfigPath))), sandboxTempRoots()) {
			fmt.Fprintf(ctx.stderr(), "not a marked sandbox: %s\n", v.Why)
		}
		return false
	}
	ctx.sandboxPassed = true
	fmt.Fprintf(ctx.stderr(), "sandbox: the worker guard passed %s for %s (%s)\n", verb, who, v.Why)
	return true
}

func (c *Context) stderr() interface{ Write([]byte) (int, error) } {
	if c.Stderr != nil {
		return c.Stderr
	}
	return os.Stderr
}
