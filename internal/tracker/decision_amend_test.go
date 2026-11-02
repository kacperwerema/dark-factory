package tracker

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const amendFixture = `---
title: "Gold"
status: "in design"
---
## Decisions

- **D-1** · 2026-10-01 · chat: Gold resets each stage, "yes reset it"
  - **D-2** · 2026-10-02 · chat: Shops open at stage 2, "shops at 2" (also considered: stage 1) (replaces economy/old#D-4)
- **D-3** · 2026-10-03 · owner: Merge into main only, "only main" {scope: project; hard; paths: cmd/**}

` + "```" + `
- **D-2** · 2026-10-02 · chat: a fenced copy stays put
` + "```" + `
`

func writeAmendFixture(t *testing.T) (dir, file string) {
	t.Helper()
	dir = t.TempDir()
	file = filepath.Join(dir, "gold.md")
	if err := os.WriteFile(file, []byte(amendFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, file
}

// readOnlyAfterPending makes the issue's folder read-only once the pending
// event is in the log, so the atomic write can't land; restore undoes it
// and clears the lock file the failed write could not remove.
func readOnlyAfterPending(t *testing.T, file string) (restore func()) {
	t.Helper()
	folder := filepath.Dir(file)
	afterDecisionPending = func() {
		if err := os.Chmod(folder, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	restore = func() {
		afterDecisionPending = nil
		os.Chmod(folder, 0o755)
		os.Remove(issueLockPath(file))
	}
	t.Cleanup(restore)
	return restore
}

func setScope(s string) func(Decision) (DecisionAttrs, error) {
	return func(d Decision) (DecisionAttrs, error) {
		a := d.DecisionAttrs
		a.Scope = s
		return a, nil
	}
}

func agentWrite(dir string) DecisionWrite {
	return DecisionWrite{IssueDir: dir, Actor: DecisionActor{Name: "agent-x", Role: ActorAgent}}
}

// TestAmendKeepsLineBytes: --amend D-2 --scope system:Workflow changes only
// that line's attribute group; every other byte of the file stays, and the
// log has an amended event with the old group (AC12).
func TestAmendKeepsLineBytes(t *testing.T) {
	dir, file := writeAmendFixture(t)
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.Local)
	before, after, err := AmendDecisionAs(file, "economy/gold", "D-2", setScope("system:Workflow"), now, agentWrite(dir))
	if err != nil {
		t.Fatal(err)
	}
	if before.Scope != ScopeIssue || after.Scope != "system:Workflow" || after.Ref != "economy/gold#D-2" {
		t.Fatalf("before %+v after %+v", before.DecisionAttrs, after.DecisionAttrs)
	}
	got, _ := os.ReadFile(file)
	oldLine := `  - **D-2** · 2026-10-02 · chat: Shops open at stage 2, "shops at 2" (also considered: stage 1) (replaces economy/old#D-4)`
	newLine := `  - **D-2** · 2026-10-02 · chat: Shops open at stage 2, "shops at 2" (also considered: stage 1) (replaces economy/old#D-4) {scope: system:Workflow}`
	if want := strings.Replace(amendFixture, oldLine, newLine, 1); string(got) != want {
		t.Fatalf("file changed beyond the group:\n%q\nwant\n%q", got, want)
	}
	if !strings.HasPrefix(newLine, strings.TrimRight(oldLine, " ")) {
		t.Fatal("the line before the group must be byte-identical")
	}
	// Change the group again: only the group's bytes move.
	if _, _, err := AmendDecisionAs(file, "economy/gold", "D-3", func(d Decision) (DecisionAttrs, error) {
		a := d.DecisionAttrs
		a.At = []string{"in progress"}
		return a, nil
	}, now, agentWrite(dir)); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(file)
	if !strings.Contains(string(got), "- **D-3** · 2026-10-03 · owner: Merge into main only, \"only main\" {scope: project; hard; at: in progress; paths: cmd/**}\n") {
		t.Fatalf("D-3 = %s", got)
	}
	evs, err := ReadDecisionLog(dir)
	if err != nil || len(evs) != 2 {
		t.Fatalf("log %v %+v", err, evs)
	}
	if e := evs[0]; e.Event != DecisionAmended || e.Ref != "economy/gold#D-2" || e.Old != "" || e.New != "{scope: system:Workflow}" || e.By != "agent-x" {
		t.Fatalf("event %+v", e)
	}
	if e := evs[1]; e.Old != "{scope: project; hard; paths: cmd/**}" {
		t.Fatalf("old group not kept: %+v", e)
	}
	// Same group again: nothing changed, nothing logged.
	if _, _, err := AmendDecisionAs(file, "economy/gold", "D-2", setScope("system:Workflow"), now, agentWrite(dir)); !errors.Is(err, ErrNothingChanged) {
		t.Fatalf("err = %v", err)
	}
	if all, _ := ReadDecisionLogAll(dir); len(all) != 4 {
		t.Fatalf("a no-op amend logged: %d events", len(all))
	}
}

// TestDecisionSpliceFailureLogsFailed: when the write fails (the issue's
// folder is read-only, so the atomic rename can't land), the log has
// pending then failed and no done, and the line is unchanged (AC9).
func TestDecisionSpliceFailureLogsFailed(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("needs a non-root unix user for a read-only folder")
	}
	logDir := t.TempDir()
	_, file := writeAmendFixture(t)
	restore := readOnlyAfterPending(t, file)
	_, _, err := AmendDecisionAs(file, "economy/gold", "D-1", setScope("project"), time.Now(), agentWrite(logDir))
	restore()
	if err == nil {
		t.Fatal("the write should fail on a read-only folder")
	}
	all, _ := ReadDecisionLogAll(logDir)
	if len(all) != 2 || all[0].Op != DecisionOpPending || all[1].Op != DecisionOpFailed || all[0].Write != all[1].Write || all[1].Error == "" {
		t.Fatalf("log = %+v", all)
	}
	if done, _ := ReadDecisionLog(logDir); len(done) != 0 {
		t.Fatalf("a failed write counts: %+v", done)
	}
	got, _ := os.ReadFile(file)
	if string(got) != amendFixture {
		t.Fatal("the line changed although the write failed")
	}
}

// TestDecisionLogFailedPendingWritesNothing: when the pending append
// fails, the issue file is not touched.
func TestDecisionLogFailedPendingWritesNothing(t *testing.T) {
	logDir := t.TempDir()
	if err := os.WriteFile(DecisionLogDir(logDir), []byte("a file, not a folder"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, file := writeAmendFixture(t)
	if _, _, err := AmendDecisionAs(file, "economy/gold", "D-1", setScope("project"), time.Now(), agentWrite(logDir)); err == nil {
		t.Fatal("want the log error")
	}
	if got, _ := os.ReadFile(file); string(got) != amendFixture {
		t.Fatal("the line changed although the log could not take the pending event")
	}
}

// TestDecisionWritesConcurrent: two concurrent amends on different lines of
// one issue and 20 concurrent appends lose nothing (AC11).
func TestDecisionWritesConcurrent(t *testing.T) {
	dir, file := writeAmendFixture(t)
	var wg sync.WaitGroup
	errs := make(chan error, 30)
	for _, id := range []string{"D-1", "D-2"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, _, err := AmendDecisionAs(file, "economy/gold", id, setScope("type:bugfix"), time.Now(), agentWrite(dir))
			errs <- err
		}(id)
	}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- AppendDecisionEvents(dir, []DecisionEvent{{Write: fmt.Sprintf("w%02d", i), Op: DecisionOpDone, Event: DecisionRecorded, Ref: fmt.Sprintf("x/y#D-%d", i)}})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(file)
	ds := ParseDecisions("economy/gold", string(data))
	if len(ds) != 3 || ds[0].Scope != "type:bugfix" || ds[1].Scope != "type:bugfix" {
		t.Fatalf("an amend was lost: %+v", ds)
	}
	all, _ := ReadDecisionLogAll(dir)
	if len(all) != 24 {
		t.Fatalf("log has %d events, want 24", len(all))
	}
	seen := map[string]bool{}
	for _, e := range all {
		seen[e.Write+e.Op] = true
	}
	for i := 0; i < 20; i++ {
		if !seen[fmt.Sprintf("w%02d", i)+DecisionOpDone] {
			t.Fatalf("append %d lost", i)
		}
	}
}

// TestDecisionLogHoldsNoQuote: the log sits under the issues dir and holds
// no decision text or quote, only refs, groups, names and comment ids; a
// relay's quote is in the issue file's comment (AC15).
func TestDecisionLogHoldsNoQuote(t *testing.T) {
	dir, file := writeAmendFixture(t)
	now := time.Now()
	w := DecisionWrite{IssueDir: dir, Actor: DecisionActor{Name: "lead-x", Role: ActorLeadRelay}, Relay: &DecisionRelay{Lead: "lead-x", Quote: "make it hard, secret words"}}
	if _, err := AddDecisionAs(file, "economy/gold", NewDecision{Text: "Never ship on Fridays", How: "chat", Quote: "fridays are off", Attrs: DecisionAttrs{Scope: "project", Hard: true}, Replaces: []string{"economy/gold#D-1"}}, now, w); err != nil {
		t.Fatal(err)
	}
	if _, _, err := AmendDecisionAs(file, "economy/gold", "D-3", setScope("system:Ops"), now, w); err != nil {
		t.Fatal(err)
	}
	if _, err := ConfirmDecisionAs(file, "economy/gold", "D-2", "", true, now, DecisionWrite{IssueDir: dir, Actor: DecisionActor{Name: "owner", Role: ActorOwnerBrowser}}); err != nil {
		t.Fatal(err)
	}
	logPath := DecisionLogPath(dir)
	if rel, err := filepath.Rel(dir, logPath); err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("log %s is outside the issues dir %s", logPath, dir)
	}
	raw, _ := os.ReadFile(logPath)
	for _, words := range []string{"secret words", "fridays are off", "Never ship", "yes reset it", "shops at 2", "Shops open", "only main", "Gold resets"} {
		if strings.Contains(string(raw), words) {
			t.Errorf("the log holds %q:\n%s", words, raw)
		}
	}
	evs, _ := ReadDecisionLog(dir)
	kinds := []string{}
	for _, e := range evs {
		kinds = append(kinds, e.Event)
	}
	if strings.Join(kinds, ",") != "recorded,replaced,amended,amended" {
		t.Fatalf("events %v", kinds)
	}
	if evs[0].Comment == 0 || evs[0].Role != ActorLeadRelay {
		t.Fatalf("relay event %+v", evs[0])
	}
	cs, _ := LoadComments(file)
	if len(cs) != 2 || cs[0].ID != evs[0].Comment || !strings.Contains(cs[0].Text, `"make it hard, secret words"`) {
		t.Fatalf("comments %+v", cs)
	}
	if p, ok := ParseDecisionRelayComment(cs[1].Text); !ok || p.Ref != "economy/gold#D-3" || p.Lead != "lead-x" || !strings.HasPrefix(p.Action, "amended {scope: project; hard; paths: cmd/**} → {scope: system:Ops; hard; paths: cmd/**}") {
		t.Fatalf("relay comment %q → %+v", cs[1].Text, p)
	}
}

// TestRelayCommentInSameWrite: a relayed amend whose write fails leaves
// neither the line change nor the comment; one that lands has both, and
// its done event names the comment.
func TestRelayCommentInSameWrite(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores the read-only folder")
	}
	logDir := t.TempDir()
	_, file := writeAmendFixture(t)
	w := DecisionWrite{IssueDir: logDir, Actor: DecisionActor{Name: "lead-x", Role: ActorLeadRelay}, Relay: &DecisionRelay{Lead: "lead-x", Quote: "drop the paths", YOLO: true}}
	drop := func(d Decision) (DecisionAttrs, error) { a := d.DecisionAttrs; a.Paths = nil; return a, nil }
	restore := readOnlyAfterPending(t, file)
	_, _, err := AmendDecisionAs(file, "economy/gold", "D-3", drop, time.Now(), w)
	restore()
	if err == nil {
		t.Fatal("want the write to fail")
	}
	if got, _ := os.ReadFile(file); string(got) != amendFixture {
		t.Fatal("a failed relay left a change")
	}
	if _, _, err := AmendDecisionAs(file, "economy/gold", "D-3", drop, time.Now(), w); err != nil {
		t.Fatal(err)
	}
	cs, _ := LoadComments(file)
	evs, _ := ReadDecisionLog(logDir)
	if len(cs) != 1 || len(evs) != 1 || evs[0].Comment != cs[0].ID || !strings.HasSuffix(cs[0].Text, "(YOLO)") {
		t.Fatalf("comments %+v events %+v", cs, evs)
	}
	all, _ := ReadDecisionLogAll(logDir)
	if ops := []string{all[0].Op, all[1].Op, all[2].Op, all[3].Op}; strings.Join(ops, ",") != "pending,failed,pending,done" {
		t.Fatalf("ops %v", ops)
	}
}
