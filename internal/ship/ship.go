// Package ship lands an issue's work branch on local main: merge main into
// the branch, run the full check, fast-forward the main checkout, and when
// main moved meanwhile merge again and run the re-check before the next
// fast-forward. Its verdict is one line and an exit code, so a pipe can't
// hide a refused fast-forward.
package ship

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"github.com/kacperwerema/dark-factory/internal/gitfile"
	"github.com/kacperwerema/dark-factory/internal/testresults"
)

// Exit codes: landed and green, not landed, landed but the check after
// landing is red (2 stays issue-cli's usage error).
const (
	ExitLanded       = 0
	ExitNotLanded    = 1
	ExitLandedButRed = 3
)

// DefaultRounds is how many times ship merges main again after a refused
// fast-forward before it gives up.
const DefaultRounds = 3

// Options is one ship run.
type Options struct {
	Worktree string // the checkout on Branch, where merges and checks run
	MainDir  string // the main checkout, fast-forwarded in place
	Main     string // main's branch name
	Branch   string // the work branch, work/<slug>
	Slug     string // named in the merge subject: chore(merge): main into <slug>
	// Check is the full check, run in order with sh -c (make validate).
	Check []string
	// Recheck runs after a clean re-merge; nil means RecheckFor's rule.
	Recheck []string
	Rounds  int
	Out     io.Writer
	Now     func() time.Time
	// BeforeFF runs just before each fast-forward (tests move main there).
	BeforeFF func()
	// Prepare runs before each check command in Worktree (ci.PrepareInstall:
	// no install through a node_modules link): skip leaves the command out
	// as passed, note is said, err fails the command unrun.
	Prepare func(dir, command string) (skip bool, note string, err error)
}

// Result is what a run did: whether main holds the branch, why not, and
// one record per check naming the sha it ran on.
type Result struct {
	Landed  bool
	SHA     string   // main's head after a landing
	Reason  string   // why it did not land
	Records []string // "check PASS at abc1234 (make validate, 412s)"
	// AfterRed lists the red steps of the full check run on the landed tree
	// after a landing that came through a re-check; AfterRan says it ran.
	AfterRan bool
	AfterRed []string
	// AfterRedText is AfterRed as the verdict says it, the test step's
	// failing packages named ("test: internal/ci, internal/ship").
	AfterRedText string
}

// Verdict is the run's last line.
func (r Result) Verdict() string {
	switch {
	case !r.Landed:
		return "ship: NOT LANDED (" + r.Reason + ")"
	case r.AfterRan && r.AfterRed != nil:
		red := r.AfterRedText
		if red == "" {
			red = strings.Join(r.AfterRed, ", ")
		}
		return fmt.Sprintf("ship: LANDED %s, check after landing FAIL (%s)", gitdir.ShortSHA(r.SHA), red)
	}
	return "ship: LANDED " + gitdir.ShortSHA(r.SHA)
}

// ExitCode is the process exit code for the verdict.
func (r Result) ExitCode() int {
	switch {
	case !r.Landed:
		return ExitNotLanded
	case r.AfterRan && r.AfterRed != nil:
		return ExitLandedButRed
	}
	return ExitLanded
}

// MergeSubject is the merge commit's subject; the commit hook wants a
// conventional one and commits_reference_issue reads the bare slug.
func MergeSubject(slug string) string { return "chore(merge): main into " + slug }

type runner struct {
	o   Options
	out io.Writer
}

// Run lands o.Branch on o.Main. It never pushes, forces or skips hooks.
func Run(o Options) Result {
	if o.Out == nil {
		o.Out = io.Discard
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Rounds <= 0 {
		o.Rounds = DefaultRounds
	}
	r := &runner{o: o, out: o.Out}
	if reason := r.preflight(); reason != "" {
		return Result{Reason: reason}
	}
	var res Result
	needFull := true // the tree at HEAD has no full PASS yet
	for round := 0; ; round++ {
		merged, reason := r.mergeMain()
		if reason != "" {
			res.Reason = reason
			return res
		}
		head := r.head()
		switch {
		case PassedAt(o.Worktree, head):
			r.say("check already passed at %s: not run again", gitdir.ShortSHA(head))
			res.Records = append(res.Records, fmt.Sprintf("check PASS at %s (remembered)", gitdir.ShortSHA(head)))
			needFull = false
		case round == 0 || !merged:
			rec, red, _ := r.check("check", o.Check, head)
			res.Records = append(res.Records, rec)
			if red != "" {
				res.Reason = fmt.Sprintf("check FAIL (%s) at %s", red, gitdir.ShortSHA(head))
				return res
			}
			RecordPass(o.Worktree, head)
			needFull = false
		default:
			cmds := o.Recheck
			if cmds == nil {
				cmds = RecheckFor(o.Worktree, o.Main, o.Check)
			}
			rec, red, _ := r.check("re-check", cmds, head)
			res.Records = append(res.Records, rec)
			if red != "" {
				res.Reason = fmt.Sprintf("re-check FAIL (%s) at %s", red, gitdir.ShortSHA(head))
				return res
			}
			needFull = !slices.Equal(cmds, o.Check)
			if !needFull {
				RecordPass(o.Worktree, head)
			}
		}
		if o.BeforeFF != nil {
			o.BeforeFF()
		}
		r.say("fast-forwarding %s in %s to %s", o.Main, o.MainDir, gitdir.ShortSHA(head))
		_, ffErr := r.git(o.MainDir, "merge", "--ff-only", o.Branch)
		if ffErr == nil {
			if !r.isAncestor(head, "refs/heads/"+o.Main) {
				res.Reason = "fast-forward reported success but " + o.Main + " does not contain " + gitdir.ShortSHA(head)
				return res
			}
			res.Landed, res.SHA = true, r.rev("refs/heads/"+o.Main)
			res.Records = append(res.Records, fmt.Sprintf("%s fast-forwarded to %s", o.Main, gitdir.ShortSHA(res.SHA)))
			break
		}
		if r.isAncestor("refs/heads/"+o.Main, head) {
			res.Reason = "ff refused: " + firstLine(ffErr.Error())
			return res
		}
		if round+1 >= o.Rounds {
			res.Reason = fmt.Sprintf("main moved %d times", round+1)
			return res
		}
		r.say("%s moved to %s while checking: merging it again (round %d of %d)", o.Main, gitdir.ShortSHA(r.rev("refs/heads/"+o.Main)), round+2, o.Rounds)
	}
	if needFull {
		r.say("landed after a re-check: running the full check on the landed tree")
		rec, red, steps := r.check("check after landing", o.Check, res.SHA)
		res.Records = append(res.Records, rec)
		res.AfterRan = true
		if red != "" {
			res.AfterRed, res.AfterRedText = steps, red
		} else {
			RecordPass(o.Worktree, res.SHA)
		}
	}
	return res
}

// Plan is what Run would do, one line each, without running anything.
func Plan(o Options) []string {
	r := &runner{o: o, out: io.Discard}
	var lines []string
	if reason := r.preflight(); reason != "" {
		return []string{"refuses: " + reason}
	}
	head := r.head()
	if r.isAncestor("refs/heads/"+o.Main, head) {
		lines = append(lines, fmt.Sprintf("%s is already in %s: no merge", o.Main, o.Branch))
	} else {
		lines = append(lines, fmt.Sprintf("merge %s into %s: %q", o.Main, o.Branch, MergeSubject(o.Slug)))
	}
	if PassedAt(o.Worktree, head) && r.isAncestor("refs/heads/"+o.Main, head) {
		lines = append(lines, fmt.Sprintf("check already passed at %s: skipped", gitdir.ShortSHA(head)))
	} else {
		lines = append(lines, "check: "+strings.Join(o.Check, " && "))
	}
	lines = append(lines, fmt.Sprintf("fast-forward: git -C %s merge --ff-only %s", o.MainDir, o.Branch))
	re := o.Recheck
	if re == nil {
		re = RecheckFor(o.Worktree, o.Main, o.Check)
	}
	lines = append(lines, fmt.Sprintf("if %s moves: merge it again, re-check: %s (up to %d rounds), then the full check on the landed tree", o.Main, strings.Join(re, " && "), max(o.Rounds, 1)))
	return lines
}

func (r *runner) say(format string, a ...any) {
	fmt.Fprintf(r.out, "ship: "+format+"\n", a...)
}

// preflight is "" when the run may start, else why not.
func (r *runner) preflight() string {
	o := r.o
	if b, _ := r.git(o.Worktree, "symbolic-ref", "--quiet", "--short", "HEAD"); b != o.Branch {
		if b == "" {
			b = "a detached HEAD"
		}
		return fmt.Sprintf("%s is on %s, not %s", o.Worktree, b, o.Branch)
	}
	if _, err := r.git(o.Worktree, "rev-parse", "-q", "--verify", "MERGE_HEAD"); err == nil {
		return "a merge is in progress in " + o.Worktree + ": commit or abort it first"
	}
	if st, _ := r.git(o.Worktree, "status", "--porcelain", "--untracked-files=no"); st != "" {
		return "uncommitted changes in " + o.Worktree + ": commit them first"
	}
	if b, _ := r.git(o.MainDir, "symbolic-ref", "--quiet", "--short", "HEAD"); b != o.Main {
		if b == "" {
			b = "a detached HEAD"
		}
		return fmt.Sprintf("the main checkout %s has %s checked out, not %s: stop and ask the lead which branch this issue ships into", o.MainDir, b, o.Main)
	}
	if len(o.Check) == 0 {
		return "no check to run: pass --check \"<cmd>\" or set ci.run in workflow.yaml"
	}
	return ""
}

// mergeMain merges main into the branch when it isn't there yet. merged
// says a merge commit was made; reason is set on a conflict or failure,
// with the merge left in progress for the worker to resolve.
func (r *runner) mergeMain() (merged bool, reason string) {
	o := r.o
	if r.isAncestor("refs/heads/"+o.Main, "HEAD") {
		return false, ""
	}
	r.say("merging %s (%s) into %s", o.Main, gitdir.ShortSHA(r.rev("refs/heads/"+o.Main)), o.Branch)
	_, err := r.git(o.Worktree, "merge", "--no-ff", "--no-edit", "-m", MergeSubject(o.Slug), "refs/heads/"+o.Main)
	if err == nil {
		return true, ""
	}
	files, _ := r.git(o.Worktree, "diff", "--name-only", "--diff-filter=U")
	if files != "" {
		return false, "conflict in " + strings.Join(strings.Fields(files), ", ") + ": resolve, commit, run ship again"
	}
	return false, "merge failed: " + firstLine(err.Error())
}

var verdictLine = regexp.MustCompile(`^validate: (PASS|FAIL(?: \((.*)\))?)\s*$`)
var progressLine = regexp.MustCompile(`^validate: `)

// check runs cmds in order in the worktree, its output in a log under the
// git dir, echoing make validate's step lines. red is "" on a pass, else
// the red steps with the test step's failing packages named (or the
// command and exit code when there is no verdict); steps are the red step
// names alone. On red it says the first tmuxguard leftover line, so a
// leftover file reads apart from a failing test without the log.
func (r *runner) check(label string, cmds []string, sha string) (record, red string, steps []string) {
	o := r.o
	start := o.Now()
	logPath := LogPath(o.Worktree, sha, label)
	_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
	logf, err := os.Create(logPath)
	if err != nil {
		logf = nil
	}
	var from int64
	noVerdict := false
cmds:
	for _, c := range cmds {
		r.say("%s at %s: %s (log %s)", label, gitdir.ShortSHA(sha), c, logPath)
		if logf != nil {
			from, _ = logf.Seek(0, io.SeekCurrent)
		}
		verdict, redSteps, code := r.runOne(c, logf)
		switch {
		case verdict == "PASS" && code == 0, verdict == "" && code == 0:
			continue
		case verdict == "FAIL" && redSteps != "":
			steps = strings.Split(redSteps, ", ")
			red = redSteps
		default:
			red = fmt.Sprintf("%s exited %d", c, code)
			steps, noVerdict = []string{red}, true
		}
		break cmds
	}
	if logf != nil {
		_ = logf.Close()
	}
	if red != "" {
		text := readFrom(logPath, from)
		rt := testresults.ParseRedText(text, testresults.GoModule(o.Worktree))
		if len(rt.Packages) > 0 {
			if slices.Contains(steps, "test") {
				red = testresults.NameTestStep(steps, rt.Packages)
			} else if noVerdict {
				red += ": " + testresults.PackageList(rt.Packages)
			}
		}
		if rt.Leftover != "" {
			r.say("%s", rt.Leftover)
		}
		for _, h := range MainFixHints(o.Worktree, o.Main, text) {
			r.say("%s", h)
		}
	}
	secs := int(o.Now().Sub(start).Seconds())
	state := "PASS"
	if red != "" {
		state = "FAIL (" + red + ")"
	}
	return fmt.Sprintf("%s %s at %s (%s, %ds)", label, state, gitdir.ShortSHA(sha), strings.Join(cmds, " && "), secs), red, steps
}

// runOne runs one command, writing its output to logf and echoing the
// `validate: ` lines; verdict is "PASS", "FAIL" or "" (no verdict line).
func (r *runner) runOne(command string, logf *os.File) (verdict, steps string, code int) {
	if r.o.Prepare != nil {
		skip, note, err := r.o.Prepare(r.o.Worktree, command)
		if note != "" {
			r.say("%s", note)
			if logf != nil {
				fmt.Fprintln(logf, note)
			}
		}
		if err != nil {
			r.say("%s", err)
			if logf != nil {
				fmt.Fprintln(logf, err)
			}
			return "", "", 1
		}
		if skip {
			return "", "", 0
		}
	}
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = r.o.Worktree
	cmd.Env = os.Environ()
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for sc.Scan() {
			line := sc.Text()
			if logf != nil {
				fmt.Fprintln(logf, line)
			}
			if m := verdictLine.FindStringSubmatch(line); m != nil {
				verdict, steps = "PASS", ""
				if strings.HasPrefix(m[1], "FAIL") {
					verdict, steps = "FAIL", m[2]
				}
			}
			if progressLine.MatchString(line) {
				fmt.Fprintln(r.out, "  "+line)
			}
		}
		_, _ = io.Copy(io.Discard, pr)
	}()
	err := cmd.Run()
	_ = pw.Close()
	<-done
	if err != nil {
		code = 1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
	}
	if verdict == "" && code != 0 {
		r.say("%s printed no validate: verdict line: its exit code %d decides", command, code)
	}
	return verdict, steps, code
}

// RecheckFor is the re-check after a clean re-merge, the rule of
// workflow/shipping-when-main-moves-during-make-validate-a-clean-re-merge-passes-on-build-vet-and-focused-tests-full-validate-on-the-landed-tree:
// go build and go vet over the module, and go test for each package folder
// the branch touches (./<dir>/...). Without a go.mod it is the full check.
func RecheckFor(worktree, main string, full []string) []string {
	if _, err := os.Stat(filepath.Join(worktree, "go.mod")); err != nil {
		return full
	}
	cmds := []string{"go build ./...", "go vet ./..."}
	if pkgs := touchedPackages(worktree, main); len(pkgs) > 0 {
		cmds = append(cmds, "go test "+strings.Join(pkgs, " "))
	}
	return cmds
}

// touchedPackages is ./<dir>/... for each folder holding a .go file the
// branch changes against main, sorted; files deleted from the tree are
// left out when their folder is gone.
func touchedPackages(worktree, main string) []string {
	out, err := gitOut(worktree, "diff", "--name-only", "refs/heads/"+main+"...HEAD")
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var pkgs []string
	for _, f := range strings.Fields(out) {
		if !strings.HasSuffix(f, ".go") {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(f))
		if fi, err := os.Stat(filepath.Join(worktree, dir)); err != nil || !fi.IsDir() {
			continue
		}
		p := "./" + dir + "/..."
		if dir == "." {
			p = "."
		}
		if !seen[p] {
			seen[p] = true
			pkgs = append(pkgs, p)
		}
	}
	slices.Sort(pkgs)
	return pkgs
}

// LogPath is where a check's output goes: <git dir>/ship/<sha>-<label>.log.
func LogPath(worktree, sha, label string) string {
	return filepath.Join(stateDir(worktree), gitdir.ShortSHA(sha)+"-"+strings.ReplaceAll(label, " ", "-")+".log")
}

func stateDir(worktree string) string {
	gd, err := gitOut(worktree, "rev-parse", "--absolute-git-dir")
	if err != nil || gd == "" {
		gd = filepath.Join(worktree, ".git")
	}
	return filepath.Join(gd, "ship")
}

func passedPath(worktree string) string { return filepath.Join(stateDir(worktree), "passed") }

// PassedAt reports whether the full check passed on sha in this worktree.
func PassedAt(worktree, sha string) bool {
	if sha == "" {
		return false
	}
	b, err := os.ReadFile(passedPath(worktree))
	if err != nil {
		return false
	}
	return slices.Contains(strings.Fields(string(b)), sha)
}

// RecordPass remembers a full check's PASS at sha (the last few only).
func RecordPass(worktree, sha string) {
	p := passedPath(worktree)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	old, _ := os.ReadFile(p)
	shas := append(strings.Fields(string(old)), sha)
	if len(shas) > 20 {
		shas = shas[len(shas)-20:]
	}
	_ = gitfile.WriteAtomic(p, []byte(strings.Join(shas, "\n")+"\n"), 0o644)
}

func (r *runner) head() string { return r.rev("HEAD") }

func (r *runner) rev(ref string) string {
	s, _ := r.git(r.o.Worktree, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return s
}

func (r *runner) isAncestor(a, b string) bool {
	_, err := r.git(r.o.Worktree, "merge-base", "--is-ancestor", a, b)
	return err == nil
}

func (r *runner) git(dir string, args ...string) (string, error) { return gitOut(dir, args...) }

// gitOut runs git in dir; the error carries git's stderr.
func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return strings.TrimSpace(stdout.String()), errors.New(msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// readFrom is the file's content from offset on, "" when it can't be read.
func readFrom(path string, offset int64) string {
	b, err := os.ReadFile(path)
	if err != nil || offset > int64(len(b)) {
		return ""
	}
	return string(b[offset:])
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}
