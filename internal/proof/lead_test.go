package proof

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func TestLeadCommandAllowlist(t *testing.T) {
	const slug = "sys/prove-me"
	ok := []struct {
		cmd  string
		env  []string
		args []string
	}{
		{"issue-cli check-script sys/prove-me --yes", nil, []string{"check-script", slug, "--yes"}},
		{"check-script sys/prove-me", nil, []string{"check-script", slug}},
		{"SANDBOX_KEEP=1 issue-cli check-script sys/prove-me --yes --cli ./issue-cli", []string{"SANDBOX_KEEP=1"}, []string{"check-script", slug, "--yes", "--cli=./issue-cli"}},
		{"issue-cli check-script 'sys/prove-me' --cli=bin/issue-cli", nil, []string{"check-script", slug, "--cli=bin/issue-cli"}},
		{"issue-cli prove sys/prove-me --run", nil, []string{"prove", slug, "--run"}},
	}
	for _, c := range ok {
		got, err := ParseLeadCommand(slug, c.cmd)
		if err != nil {
			t.Errorf("%q refused: %v", c.cmd, err)
			continue
		}
		if !reflect.DeepEqual(got.Env, c.env) || !reflect.DeepEqual(got.Args, c.args) {
			t.Errorf("%q = %+v, want env %v args %v", c.cmd, got, c.env, c.args)
		}
	}
	refused := []string{
		"",
		"issue-cli check-script other/issue --yes",                // another issue's slug
		"issue-cli dispatch sys/prove-me",                         // not allowed
		"issue-cli approve sys/prove-me",                          // not allowed
		"issue-cli check-script sys/prove-me --yes; rm -rf ~",     // shell syntax
		"issue-cli check-script sys/prove-me --yes && true",       // shell syntax
		"issue-cli check-script $(whoami) --yes",                  // substitution
		"issue-cli check-script \"sys/prove-me$X\" --yes",         // $ in double quotes
		"issue-cli check-script sys/prove-me --new x",             // flag not allowed
		"issue-cli check-script sys/prove-me --cli /usr/bin/evil", // --cli outside the worktree
		"issue-cli check-script sys/prove-me --cli ../../evil",    // --cli outside the worktree
		"issue-cli check-script sys/prove-me --project other",     // the runner names the project
		"issue-cli --project p check-script sys/prove-me --yes",   // the runner names the project
		"issue-cli --project=p check-script sys/prove-me",         // the runner names the project
		"PATH=/tmp issue-cli check-script sys/prove-me --yes",     // env that changes the program
		"DYLD_INSERT_LIBRARIES=x issue-cli check-script sys/prove-me",
		"GIT_DIR=/x issue-cli check-script sys/prove-me",
		"ISSUE_VIEWER_LEAD_SCOPE=project issue-cli check-script sys/prove-me",
		"lower=1 issue-cli check-script sys/prove-me",
		"issue-cli prove sys/prove-me", // prove only with --run
		"issue-cli prove sys/prove-me --run --lead-run",
		"bash scripts/checks/x.sh",
		"issue-cli check-script sys/prove-me 'unclosed",
	}
	for _, cmd := range refused {
		if got, err := ParseLeadCommand(slug, cmd); err == nil {
			t.Errorf("%q accepted as %+v, want refused", cmd, got)
		} else if !strings.Contains(err.Error(), "check-script "+slug) {
			t.Errorf("%q refusal doesn't name the allowed forms: %v", cmd, err)
		}
	}
}

func saveLead(t *testing.T, f fixture, cmd string, linked time.Time, runs ...tracker.LeadRun) {
	t.Helper()
	store := tracker.DataStore{
		Proofs:   map[string][]tracker.ProofLink{"AC3": {{Kind: tracker.ProofLead, Ref: cmd, At: linked, Criterion: "A value proves it"}}},
		LeadRuns: runs,
	}
	if err := tracker.SaveData(f.issue.FilePath, store); err != nil {
		t.Fatal(err)
	}
}

func TestLeadLinkWaitsOnTheLeadUntilARunPasses(t *testing.T) {
	f := newFixture(t, "TestFoo")
	cmd := "issue-cli check-script " + f.issue.Slug + " --yes"
	saveLead(t, f, cmd, t0.Add(3*time.Hour))

	r := Collect(f.root, f.issue, nil)
	c := criterion(t, r, "AC3")
	if c.Proven() || len(c.Links) != 1 || !c.Links[0].Lead || c.Links[0].Check.State != LeadWaitsPrefix+cmd {
		t.Fatalf("AC3 before a run = %+v, want waits on the lead", c.Links)
	}
	if w := r.WaitsOnLead(); len(w) != 1 || w[0].ID != "AC3" || w[0].Command != cmd {
		t.Errorf("WaitsOnLead = %+v", w)
	}
	msg := GateError(r).Error()
	for _, want := range []string{"Waits on the lead: AC3 " + cmd, "no question: comment needed", "issue-cli prove " + f.issue.Slug + " --lead-run"} {
		if !strings.Contains(msg, want) {
			t.Errorf("gate error lacks %q:\n%s", want, msg)
		}
	}

	saveLead(t, f, cmd, t0.Add(3*time.Hour), tracker.LeadRun{Command: cmd, At: t0.Add(4 * time.Hour), OK: false, Exit: 1})
	c = criterion(t, Collect(f.root, f.issue, nil), "AC3")
	if c.Proven() || !strings.Contains(c.Links[0].Check.State, "last run failed") {
		t.Errorf("AC3 after a failed run = %+v", c.Links)
	}

	saveLead(t, f, cmd, t0.Add(3*time.Hour),
		tracker.LeadRun{Command: cmd, At: t0.Add(4 * time.Hour), OK: false, Exit: 1},
		tracker.LeadRun{Command: cmd, At: t0.Add(5 * time.Hour), OK: true, By: "lead-x"})
	r = Collect(f.root, f.issue, nil)
	if c := criterion(t, r, "AC3"); !c.Proven() || !strings.Contains(c.Links[0].Check.State, "the lead ran it by lead-x") {
		t.Errorf("AC3 after a passing run = %+v, want proven", c.Links)
	}
	if w := r.WaitsOnLead(); len(w) != 0 {
		t.Errorf("WaitsOnLead after a pass = %+v", w)
	}
	ticked, err := TickProven(f.issue.FilePath, r)
	if err != nil || !containsString(ticked, "AC3") {
		t.Errorf("TickProven = %v, %v; want AC3 ticked", ticked, err)
	}
}

func TestLeadLinkStaleAfterNewCommit(t *testing.T) {
	f := newFixture(t, "TestFoo")
	cmd := "issue-cli check-script " + f.issue.Slug + " --yes"
	branch := "work/" + f.issue.Slug
	gitAt(t, f.root, t0, "checkout", "-q", branch)
	old := gitAt(t, f.root, t0, "rev-parse", "HEAD")
	saveLead(t, f, cmd, t0.Add(3*time.Hour), tracker.LeadRun{Command: cmd, At: t0.Add(5 * time.Hour), Head: old, OK: true})
	if c := criterion(t, Collect(f.root, f.issue, nil), "AC3"); !c.Proven() {
		t.Fatalf("AC3 = %+v, want proven before the new commit", c.Links)
	}
	head := commitFile(t, f.root, t0.Add(6*time.Hour), "pkg/bar.go", "package pkg\n", "more code")
	gitAt(t, f.root, t0, "checkout", "-q", "main")
	c := criterion(t, Collect(f.root, f.issue, nil), "AC3")
	if c.Proven() || !strings.Contains(c.Links[0].Check.State, "ran before the last change") {
		t.Errorf("AC3 after a new code commit = %+v, want waits on the lead (stale)", c.Links)
	}
	// A run recorded with an early clock at the new head still holds it.
	saveLead(t, f, cmd, t0.Add(3*time.Hour), tracker.LeadRun{Command: cmd, At: t0.Add(5 * time.Hour), Head: head, OK: true})
	if c := criterion(t, Collect(f.root, f.issue, nil), "AC3"); !c.Proven() {
		t.Errorf("AC3 with a run at the new head = %+v, want proven", c.Links)
	}
}

func TestPendingLeadReadsTheSidecarAlone(t *testing.T) {
	cmd := "issue-cli check-script s --yes"
	link := func(at time.Time) map[string][]tracker.ProofLink {
		return map[string][]tracker.ProofLink{"AC1": {{Kind: tracker.ProofLead, Ref: cmd, At: at}, {Kind: tracker.ProofTest, Ref: "TestX"}}}
	}
	cases := []struct {
		name  string
		store tracker.DataStore
		want  string
	}{
		{"never ran", tracker.DataStore{Proofs: link(t0)}, "never ran"},
		{"failed", tracker.DataStore{Proofs: link(t0), LeadRuns: []tracker.LeadRun{{Command: cmd, At: t0.Add(time.Hour)}}}, "last run failed"},
		{"linked after", tracker.DataStore{Proofs: link(t0.Add(2 * time.Hour)), LeadRuns: []tracker.LeadRun{{Command: cmd, At: t0.Add(time.Hour), OK: true}}}, "linked after its last run"},
		{"passed", tracker.DataStore{Proofs: link(t0), LeadRuns: []tracker.LeadRun{{Command: cmd, At: t0.Add(time.Hour), OK: true}}}, ""},
	}
	for _, c := range cases {
		got := PendingLead(c.store)
		switch {
		case c.want == "" && len(got) != 0:
			t.Errorf("%s: %+v, want nothing pending", c.name, got)
		case c.want != "" && (len(got) != 1 || got[0].ID != "AC1" || got[0].Command != cmd || !strings.HasPrefix(got[0].State, c.want)):
			t.Errorf("%s: %+v, want AC1 %q", c.name, got, c.want)
		}
	}
}
