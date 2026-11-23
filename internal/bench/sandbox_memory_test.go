package bench

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// memWorker records what the sandbox shows of memory and git, then solves
// the task like fixWorker.
const memWorker = `git for-each-ref --format='%(refname) %(objectname)' > refs.seen; git reflog 2>/dev/null > reflog.seen; ` +
	`echo "$XDG_DATA_HOME" > data.seen; cat ../tracker/issues/.memory/events.jsonl > project-mem.seen 2>/dev/null; ` +
	`cat "$XDG_DATA_HOME/ai-workflow-engine/memory/events.jsonl" > machine-mem.seen 2>/dev/null; ` + fixWorker

func TestTheSandboxHoldsMemoryAsOfTheBaseAndNoRefPastIt(t *testing.T) {
	f := newFixture(t)
	mustGit(t, f.repo, "tag", "v1", f.merged)
	agent := tracker.AgentSessionName(f.task.Slug)
	project := strings.Join([]string{
		`{"op":"add","id":"m-old","at":"2020-01-01T00:00:00Z","by":"agent-x","memory":{"id":"m-old","text":"old fact","kind":"fact","scope":"project"}}`,
		`{"op":"add","id":"m-late","at":"2100-01-01T00:00:00Z","by":"agent-x","memory":{"id":"m-late","text":"the answer: return a + b","kind":"fact","scope":"project"}}`,
		`{"op":"add","id":"m-own","at":"2020-01-02T00:00:00Z","by":"agent-y","memory":{"id":"m-own","text":"own issue's fact","kind":"fact","scope":"project","source":{"issue":"calc/fix-add"}}}`,
		`{"op":"update","id":"m-own","at":"2020-01-03T00:00:00Z","by":"agent-z","text":"own issue's fact, edited"}`,
		`{"op":"add","id":"m-agent","at":"2020-01-02T00:00:00Z","by":"` + agent + `","memory":{"id":"m-agent","text":"its agent's fact","kind":"fact","scope":"project"}}`,
		`{"op":"supersede","id":"m-old","at":"2020-01-04T00:00:00Z","by":"agent-z","target":"m-own"}`,
		`{"op":"pin","id":"m-old","at":"2100-01-02T00:00:00Z","by":"owner"}`,
		`not json`,
	}, "\n") + "\n"
	write(t, filepath.Join(f.proj.IssueDir, ".memory", "events.jsonl"), project)
	env := f.env(t)
	env.Keep = true
	write(t, env.MachineMemory, `{"op":"add","id":"m-mach","at":"2020-01-01T00:00:00Z","by":"agent-x","memory":{"id":"m-mach","text":"machine fact","kind":"fact","scope":"machine"}}`+"\n"+
		`{"op":"add","id":"m-mach2","at":"2100-01-01T00:00:00Z","by":"agent-x","memory":{"id":"m-mach2","text":"later machine fact","kind":"fact","scope":"machine"}}`+"\n")
	ownerData := filepath.Join(t.TempDir(), "owner-data")
	t.Setenv("XDG_DATA_HOME", ownerData)

	run, err := Execute(context.Background(), f.benchmark(memWorker, f.task), env, Options{})
	if err != nil {
		t.Fatal(err)
	}
	r := run.Tasks[0]
	if r.Outcome != OutcomePass {
		t.Fatalf("record = %+v", r)
	}
	dir := filepath.Join(env.TmpRoot, run.ID, "01")
	repo := filepath.Join(dir, "repo")

	// Project memory: only the old fact; the later one, the own issue's,
	// its agent's and the supersede onto the own one are left out.
	if got := seen(t, repo, "project-mem.seen"); got != strings.SplitN(project, "\n", 2)[0] {
		t.Errorf("project memory in the sandbox:\n%s", got)
	}
	if got := seen(t, repo, "machine-mem.seen"); !strings.Contains(got, "m-mach") || strings.Contains(got, "m-mach2") {
		t.Errorf("machine memory in the sandbox:\n%s", got)
	}
	if got := seen(t, repo, "data.seen"); got != filepath.Join(dir, "data") {
		t.Errorf("XDG_DATA_HOME = %q, want the sandbox's", got)
	}
	m := r.Memory
	if m == nil || m.Project != 1 || m.Machine != 1 || m.DroppedAfter != 4 || m.DroppedOwn != 4 {
		t.Fatalf("memory cut = %+v", m)
	}
	if !strings.Contains(m.Line(), "1 project, 1 machine (4 later and 4 of its own left out)") {
		t.Errorf("line = %q", m.Line())
	}
	// Counts only: no memory's text reaches run.json.
	data, err := os.ReadFile(filepath.Join(RunDir(run.Benchmark, run.ID), "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "old fact") || strings.Contains(string(data), "the answer") {
		t.Errorf("a memory's text is in run.json")
	}
	if _, err := os.Stat(ownerData); err == nil {
		t.Errorf("something was written under the owner's XDG_DATA_HOME")
	}

	// Git: one branch at base, no tag, no reflog naming a later commit.
	if got := seen(t, repo, "refs.seen"); got != "refs/heads/main "+f.base {
		t.Errorf("refs in the sandbox:\n%s", got)
	}
	if got := seen(t, repo, "reflog.seen"); strings.Contains(got, f.merged[:7]) {
		t.Errorf("the reflog names the merged commit:\n%s", got)
	}
}
