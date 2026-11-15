package checks

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/standin"
)

func TestLibSandboxProjectLivesOutsideWorktrees(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	// A TMPDIR inside a .worktrees/ folder must not be used.
	wtTmp := filepath.Join(t.TempDir(), ".worktrees", "x", "tmp")
	os.MkdirAll(wtTmp, 0o755)
	r := runMachine(t, `
sandbox_project sbx
echo "DIR=$SANDBOX_PROJECT_DIR"
echo "CFG=$SANDBOX_CONFIG"
echo "STATE=$XDG_STATE_HOME VIEWER=$ISSUE_VIEWER_CONFIG"
cat "$SANDBOX_CONFIG"
git -C "$SANDBOX_PROJECT_DIR" log --oneline | wc -l
sic list
`, "TMPDIR="+wtTmp)
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.out)
	}
	if strings.Contains(r.out, "/.worktrees/") {
		t.Fatalf("the sandbox is under .worktrees/:\n%s", r.out)
	}
	contains(t, r.out, `slug: "sbx"`, "/sbx/issues", "workdir: ")
	dir := between(r.out, "DIR=", "\n")
	if _, err := os.Stat(dir); err == nil {
		t.Fatalf("the sandbox %s outlived the script", dir)
	}
	// Its own state dir: a viewer the script starts records itself there,
	// never in the shared viewer.json.
	if state := between(r.out, "STATE=", " "); state != filepath.Join(filepath.Dir(dir), "state") {
		t.Fatalf("XDG_STATE_HOME = %q, want the sandbox's own state dir next to %s", state, dir)
	}
}

func between(s, a, b string) string {
	i := strings.Index(s, a)
	if i < 0 {
		return ""
	}
	s = s[i+len(a):]
	if j := strings.Index(s, b); j >= 0 {
		return s[:j]
	}
	return s
}

func TestLibPrivateTmuxKillsOnlyItsOwnServer(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux")
	}
	// Another private server (standing in for the owner's) must survive.
	other, err := os.MkdirTemp("/tmp", "ckother-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(other)
	// Its socket is where TMUX_TMPDIR=other puts it, named with -S.
	sock := filepath.Join(other, "tmux-"+strconv.Itoa(os.Getuid()), "default")
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		t.Fatal(err)
	}
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "TMUX=") {
			env = append(env, kv)
		}
	}
	otherTmux := func(args ...string) error {
		c := exec.Command("tmux", append([]string{"-S", sock}, args...)...)
		c.Env = env
		return c.Run()
	}
	if err := otherTmux("new-session", "-d", "-s", "owner", "sleep 60"); err != nil {
		t.Skipf("tmux can't start here: %v", err)
	}
	defer func() {
		kill := exec.Command("tmux", "-S", sock, "kill-server")
		kill.Env = env
		_ = kill.Run()
	}()

	r := runMachine(t, `
private_tmux
echo "TMUXDIR=$TMUX_TMPDIR"
ptmux new-session -d -s sbx 'sleep 60'
ptmux has-session -t sbx && echo PRIVATE-UP
`, "TMUX_TMPDIR="+other)
	contains(t, r.out, "PRIVATE-UP")
	dir := between(r.out, "TMUXDIR=", "\n")
	if dir == other || dir == "" {
		t.Fatalf("private_tmux reused the caller's TMUX_TMPDIR: %q", dir)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatalf("the private tmux dir %s outlived the script", dir)
	}
	if err := otherTmux("has-session", "-t", "owner"); err != nil {
		t.Fatal("the other tmux server was killed")
	}
}

func TestLibSandboxProjectOptions(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	wf := filepath.Join(t.TempDir(), "wf.yaml")
	os.WriteFile(wf, []byte("statuses: [{name: idea}]\n"), 0o644)
	r := runMachine(t, `
sandbox_project sand --workflow `+wf+` --name Sand
cat "$SANDBOX_CONFIG"
cat "$SANDBOX_PROJECT_DIR/workflow.yaml"
ls "$SANDBOX_PROJECT_DIR"
`)
	contains(t, r.out, `name: "Sand"`, `slug: "sand"`, `terminal: "none"`, "statuses: [{name: idea}]", "issues")
}

func TestLibCleanPrivateTmuxGivesPanesTheStandIns(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux")
	}
	r := runMachine(t, `
stand_in claude
private_tmux --clean
ptmux new-session -d -s w 'sh -c "command -v claude > $CHECKS_STAND_IN_DIR/which.tmp; mv $CHECKS_STAND_IN_DIR/which.tmp $CHECKS_STAND_IN_DIR/which; sleep 5"'
tries=100
while [ ! -e "$CHECKS_STAND_IN_DIR/which" ] && [ "$tries" -gt 0 ]; do sleep 0.1; tries=$((tries - 1)); done
cat "$CHECKS_STAND_IN_DIR/which"
`)
	if !strings.Contains(r.out, "/bin/claude") || !strings.Contains(r.out, "ck-") {
		t.Fatalf("the pane didn't find the stand-in first:\n%s", r.out)
	}
}

func TestLibSandboxYoloOnWritesTheSandboxWindowOnly(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	r := runMachine(t, `
sandbox_project sbx
sandbox_yolo_on
echo "DIR=$SANDBOX_PROJECT_DIR"
cat "$SANDBOX_PROJECT_DIR/.yolo/sbx.json"
`)
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.out)
	}
	contains(t, r.out, `"on": true`, `"forever": true`, `"by": "owner"`)
	var st struct {
		On      bool      `json:"on"`
		Since   time.Time `json:"since"`
		Forever bool      `json:"forever"`
	}
	raw := r.out[strings.Index(r.out, "{"):]
	if err := json.Unmarshal([]byte(raw), &st); err != nil || !st.On || !st.Forever || st.Since.IsZero() {
		t.Fatalf("not a YOLO window the engine reads (%v): %s", err, raw)
	}

	r = runMachine(t, "sandbox_yolo_on\necho NOT-REACHED\n")
	if r.code == 0 || strings.Contains(r.out, "NOT-REACHED") {
		t.Fatalf("sandbox_yolo_on ran without a sandbox:\n%s", r.out)
	}
	contains(t, r.out, "needs sandbox_project first")
}

func TestLibSeedDepartmentAppliesTheDryRunPlan(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	// The stand-in issue-cli answers the dry run with a plan, and writes the
	// workflow file workflow init --out asks for. A real run never happens.
	dir := t.TempDir()
	bin := filepath.Join(dir, "issue-cli")
	calls := filepath.Join(dir, "calls.txt")
	body := `echo "$*" >> "${0%/*}/calls.txt"
case "$*" in
*"init --template development") mkdir -p issues docs && echo "statuses: []" > workflow.yaml ;;
*"department add marketing --dry-run")
	cat << 'JSON'
{"name":"Marketing","key":"marketing","files":[
 {"path":"workflows/marketing.yaml","state":"write"},
 {"path":"departments/marketing/role.md","state":"write"},
 {"path":"departments/marketing/notes.md","state":"keep"}],
 "entry":"- name: Marketing\n  prompt: departments/marketing/role.md\n  workflow: workflows/marketing.yaml\n"}
JSON
	;;
*"department add nope --dry-run") echo "no template nope" >&2; exit 1 ;;
*"workflow init --template marketing --out workflows/marketing.yaml") echo "statuses: [{name: brief}]" > workflows/marketing.yaml ;;
esac
`
	standin.Write(t, bin, body)
	tmpl := filepath.Join(t.TempDir(), "departments")
	os.MkdirAll(filepath.Join(tmpl, "marketing"), 0o755)
	os.WriteFile(filepath.Join(tmpl, "marketing", "role.md"), []byte("# Marketing role\n"), 0o644)
	r := runMachine(t, `
sandbox_project sbx
seed_department marketing
cat "$SANDBOX_CONFIG"
cat "$SANDBOX_PROJECT_DIR/workflows/marketing.yaml" "$SANDBOX_PROJECT_DIR/departments/marketing/role.md"
ls "$SANDBOX_PROJECT_DIR/departments/marketing"
`, "SANDBOX_ISSUE_CLI="+bin, "SEED_DEPARTMENT_TEMPLATES="+tmpl)
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.out)
	}
	contains(t, r.out, "    terminal: \"none\"\n    departments:\n      - name: Marketing\n        prompt: departments/marketing/role.md\n",
		"statuses: [{name: brief}]", "# Marketing role")
	if strings.Contains(r.out, "notes.md") {
		t.Errorf("a file the plan keeps was written:\n%s", r.out)
	}
	data, _ := os.ReadFile(calls)
	if strings.Contains(string(data), "department add marketing\n") {
		t.Fatalf("seed_department ran department add itself:\n%s", data)
	}

	r = runMachine(t, "sandbox_project sbx\nseed_department nope\necho NOT-REACHED\n", "SANDBOX_ISSUE_CLI="+bin)
	if r.code == 0 || strings.Contains(r.out, "NOT-REACHED") {
		t.Fatalf("seed_department went on after a failed dry run:\n%s", r.out)
	}
	contains(t, r.out, "no template nope")
}

// TMPDIR ends in a slash on macOS (/var/folders/…/T/) and /var links to
// /private/var: sandbox_dir's path is the resolved one, as issue-cli and a
// viewer print it.
func TestLibSandboxDirPathIsResolved(t *testing.T) {
	tmp := t.TempDir()
	r := runMachine(t, `
sandbox_dir
echo "SBX=$SANDBOX"
echo "PHYS=$(cd "$SANDBOX" && pwd -P)"
`, "TMPDIR="+tmp+"/")
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.out)
	}
	sbx := between(r.out, "SBX=", "\n")
	if strings.Contains(sbx, "//") {
		t.Fatalf("sandbox_dir path has a double slash: %q", sbx)
	}
	if phys := between(r.out, "PHYS=", "\n"); sbx != phys {
		t.Fatalf("sandbox_dir path %q, want the resolved %q", sbx, phys)
	}
	resolved, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(sbx) != resolved {
		t.Fatalf("sandbox_dir path %q, want it under %q", sbx, resolved)
	}
}

// sandbox_dir alone keeps a viewer or issue-cli the script starts off the
// shared state and config; a later sandbox_dir keeps the first sandbox's,
// sandbox_project moves both into its own; ticking the real issue gets the
// caller's back.
func TestLibSandboxDirIsolatesState(t *testing.T) {
	r := runMachine(t, `
sandbox_dir
echo "SBX=$SANDBOX"
echo "STATE=$XDG_STATE_HOME"
echo "CONF=$XDG_CONFIG_HOME"
[ -d "$XDG_STATE_HOME" ] && [ -d "$XDG_CONFIG_HOME" ] && echo DIRS=ok
other=; sandbox_dir other
echo "STATE2=$XDG_STATE_HOME"
_ck_real sh -c 'echo "REAL=$XDG_STATE_HOME $XDG_CONFIG_HOME"'
`, "XDG_CONFIG_HOME=/real/config")
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.out)
	}
	sbx := between(r.out, "SBX=", "\n")
	if got := between(r.out, "STATE=", "\n"); got != sbx+"/state" {
		t.Errorf("XDG_STATE_HOME = %q, want %q", got, sbx+"/state")
	}
	if got := between(r.out, "CONF=", "\n"); got != sbx+"/config" {
		t.Errorf("XDG_CONFIG_HOME = %q, want %q", got, sbx+"/config")
	}
	contains(t, r.out, "DIRS=ok", "STATE2="+sbx+"/state\n", "REAL=/real/state /real/config")

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	p := runMachine(t, `
sandbox_dir
sandbox_project sbx
echo "PDIR=$SANDBOX_PROJECT_DIR"
echo "STATE=$XDG_STATE_HOME"
echo "CONF=$XDG_CONFIG_HOME"
`, "XDG_CONFIG_HOME=/real/config")
	if p.code != 0 {
		t.Fatalf("exit %d\n%s", p.code, p.out)
	}
	dir := filepath.Dir(between(p.out, "PDIR=", "\n"))
	contains(t, p.out, "STATE="+dir+"/state\n", "CONF="+dir+"/config\n")
}

func TestLibSandboxProjectSetsSandboxAndAddsASecondProject(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	r := runMachine(t, `
sandbox_project one
echo "SANDBOX=$SANDBOX"
echo "DIR=$SANDBOX_PROJECT_DIR"
sandbox_project --add two --name "Second"
echo "ADDED=$SANDBOX_ADDED_PROJECT AT=$SANDBOX_ADDED_DIR"
echo "STILL=$SANDBOX_PROJECT"
git -C "$SANDBOX_ADDED_DIR" log --oneline | wc -l | tr -d ' '
cat "$SANDBOX_CONFIG"
`)
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.out)
	}
	sandbox, dir := between(r.out, "SANDBOX=", "\n"), between(r.out, "DIR=", "\n")
	if sandbox == "" || sandbox != filepath.Dir(dir) {
		t.Fatalf("$SANDBOX = %q, want SANDBOX_PROJECT_DIR's parent (%s)\n%s", sandbox, filepath.Dir(dir), r.out)
	}
	contains(t, r.out, "ADDED=two AT="+sandbox+"/two\n", "STILL=one\n",
		`slug: "one"`, `slug: "two"`, `name: "Second"`, sandbox+"/two/issues")
	if strings.Count(r.out, "projects:") != 1 {
		t.Fatalf("want one projects: list holding both:\n%s", r.out)
	}
}

func TestLibSandboxGitRepoIgnoresWorktreesAndPushes(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	r := runMachine(t, `
sandbox_git_repo
echo "REPO=$SANDBOX_REPO"
cat "$SANDBOX_REPO/.gitignore"
mkdir -p "$SANDBOX_REPO/.worktrees/x" && echo wt > "$SANDBOX_REPO/.worktrees/x/f" && echo a > "$SANDBOX_REPO/a"
git -C "$SANDBOX_REPO" add -A && git -C "$SANDBOX_REPO" commit -q -m two && git -C "$SANDBOX_REPO" push -q
echo "PUSH=$?"
echo "FILES=$(git -C "$SANDBOX_REPO" ls-files | tr '\n' ' ')"
echo "REMOTE=$(git --git-dir="$SANDBOX_REMOTE" rev-list --count main)"
`)
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.out)
	}
	contains(t, r.out, ".worktrees/\n", "PUSH=0\n", "FILES=.gitignore a \n", "REMOTE=2\n")
	if repo := between(r.out, "REPO=", "\n"); strings.Contains(repo, "/.worktrees/") || repo == "" {
		t.Fatalf("repo %q", repo)
	}
}
