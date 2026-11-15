package bench

import (
	"bytes"
	"context"
	"encoding/json"

	"fmt"

	"os"
	"os/exec"
	"path/filepath"
	"regexp"

	"strconv"
	"strings"

	"time"

	"github.com/kacperwerema/dark-factory/internal/tokens"
)

// score puts the merged commit's hidden test files over the worker's, runs
// the hidden Go tests and the task's check.
func score(ctx context.Context, sb *sandbox, t Task, rec *TaskRecord) {
	for _, h := range t.Hidden {
		data, err := git(sb.repo, "show", t.Merged+":"+h)
		if err != nil {
			continue
		}
		dst := filepath.Join(sb.work, filepath.FromSlash(h))
		_ = os.MkdirAll(filepath.Dir(dst), 0o755)
		_ = os.WriteFile(dst, []byte(data), 0o644)
	}
	rec.HiddenTotal = len(t.HiddenTests)
	if rec.HiddenTotal > 0 {
		rec.HiddenPassed = len(goHidden(ctx, sb.work, t))
	}
	rec.CheckOK = true
	if t.Check != "" {
		rec.CheckOK, _ = shell(ctx, sb.work, t.Check, nil, 30*time.Minute)
	}
}

// goHidden runs the hidden Go tests in their packages and returns the ones
// that passed; a test that never ran (the package does not build) failed.
func goHidden(ctx context.Context, dir string, t Task) map[string]bool {
	return goHiddenEnv(ctx, dir, t, nil)
}

// goHiddenEnv is goHidden with the go command's environment (nil: this
// process's).
func goHiddenEnv(ctx context.Context, dir string, t Task, env []string) map[string]bool {
	pkgs := map[string]bool{}
	for _, h := range t.Hidden {
		if strings.HasSuffix(h, "_test.go") {
			pkgs["./"+filepath.ToSlash(filepath.Dir(h))] = true
		}
	}
	var quoted []string
	for _, name := range t.HiddenTests {
		quoted = append(quoted, regexp.QuoteMeta(name))
	}
	args := []string{"test", "-json", "-count=1", "-run", "^(" + strings.Join(quoted, "|") + ")$"}
	for p := range pkgs {
		args = append(args, p)
	}
	tctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(tctx, "go", args...)
	cmd.Dir = dir
	cmd.Env = env
	out, _ := cmd.Output()
	want := map[string]bool{}
	for _, n := range t.HiddenTests {
		want[n] = true
	}
	passed := map[string]bool{}
	for _, l := range bytes.Split(out, []byte("\n")) {
		var ev struct{ Action, Test string }
		if json.Unmarshal(l, &ev) == nil && ev.Action == "pass" && want[ev.Test] {
			passed[ev.Test] = true
		}
	}
	return passed
}

var judgeScore = regexp.MustCompile(`(?im)^\s*SCORE:\s*([0-5])\b`)

var judgeReason = regexp.MustCompile(`(?im)^\s*REASON:\s*(.+)$`)

// JudgePrompt is what the judge reads: the issue, the known good diff and
// the worker's diff, each cut to limit bytes.
func JudgePrompt(issue, merged, worker string) string {
	cut := cutDiff
	return "You judge one benchmark task. Compare the WORKER DIFF with the KNOWN GOOD DIFF for the ISSUE: does it do what the issue asks, as well as the known good one? Different code that meets the issue is fine.\n" +
		"Answer with exactly two lines:\nSCORE: <0-5>  (5 = as good or better, 0 = nothing useful)\nREASON: <one line>\n\n" +
		"## ISSUE\n\n" + cut(issue) + "\n\n## KNOWN GOOD DIFF\n\n" + cut(merged) + "\n\n## WORKER DIFF\n\n" + cut(worker) + "\n"
}

// diffLimit caps each text the judge and a diff eval read.
const diffLimit = 60000

// cutDiff cuts s to diffLimit bytes.
func cutDiff(s string) string {
	if len(s) > diffLimit {
		return s[:diffLimit] + "\n… (cut)\n"
	}
	return s
}

func judge(ctx context.Context, b *Benchmark, t Task, sb *sandbox, workerDiff string, rec *TaskRecord) {
	issue, _ := os.ReadFile(SnapshotPath(t))
	merged, _ := git(sb.repo, "diff", t.Base, t.Merged)
	score, reason, u := runJudge(ctx, b, sb.dir, JudgePrompt(string(issue), merged, workerDiff))
	addUsage(&rec.Usage, u)
	if score < 0 {
		rec.JudgeReason = reason
		return
	}
	rec.Judge, rec.JudgeReason = score, reason
}

// judgeText is the judge's answer: a Claude Code result's text with its
// usage priced, else the output as it is.
func judgeText(b *Benchmark, out []byte) (string, Usage) {
	var res struct {
		Result string `json:"result"`
	}
	if json.Unmarshal(bytes.TrimSpace(out), &res) != nil || res.Result == "" {
		return string(out), Usage{}
	}
	u, ok := ParseResult(out)
	if !ok {
		return res.Result, Usage{}
	}
	Cost(&u, b.Prices)
	return res.Result, u
}

// runWorker runs command in dir through sh with prompt on stdin, in its own
// process group so a stop ends every process it started.
func runWorker(ctx context.Context, dir, command, prompt string, env []string) (stdout, stderr []byte, err error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = strings.NewReader(prompt)
	var out bytes.Buffer
	errTail := &tailBuffer{max: 4096}
	cmd.Stdout = &out
	cmd.Stderr = errTail
	ownGroup(cmd)
	cmd.WaitDelay = 10 * time.Second
	err = cmd.Run()
	return out.Bytes(), errTail.buf, err
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-t.max:]...)
	}
	return len(p), nil
}

// shell runs command in dir and reports whether it exited 0, with its
// combined output.
func shell(ctx context.Context, dir, command string, env []string, limit time.Duration) (bool, string) {
	sctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	cmd := exec.CommandContext(sctx, "sh", "-c", command)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	ownGroup(cmd)
	cmd.WaitDelay = 10 * time.Second
	out, err := cmd.CombinedOutput()
	return err == nil, string(out)
}

func diffLines(dir, base string) int {
	_, _ = git(dir, "add", "-A", "-N")
	out, err := git(dir, "diff", "--numstat", base)
	if err != nil {
		return 0
	}
	n := 0
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 {
			a, _ := strconv.Atoi(f[0])
			d, _ := strconv.Atoi(f[1])
			n += a + d
		}
	}
	return n
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func okText(ok bool) string {
	if ok {
		return "green"
	}
	return "red"
}

func tokensText(u Usage) string {
	if !u.Known {
		return "tokens unknown"
	}
	s := fmt.Sprintf("%s tokens", tokens.Format(u.Tokens()))
	if u.USDKnown {
		s += fmt.Sprintf(" $%.2f", u.USD)
	}
	return s
}

func wallText(sec float64) string {
	return (time.Duration(sec) * time.Second).Round(time.Second).String()
}
