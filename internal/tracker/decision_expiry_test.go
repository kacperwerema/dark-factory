package tracker

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeExpiryIssue(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\ntitle: \""+filepath.Base(name)+"\"\nstatus: \"done\"\n---\n\n## Decisions\n\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestStateOnExpired: expired the day after expires:, not on it; a
// replaced one stays replaced.
func TestStateOnExpired(t *testing.T) {
	dir := t.TempDir()
	writeExpiryIssue(t, dir, "a", "- **D-1** · 2026-10-01 · chat: X, \"x\" {hard; expires: 2026-10-30}\n"+
		"- **D-2** · 2026-10-01 · chat: Y {expires: 2026-10-31}\n"+
		"- **D-3** · 2026-10-01 · chat: Z, \"z\" {hard; expires: 2026-10-01}\n"+
		"- **D-4** · 2026-10-02 · chat: W (replaces a#D-3)\n")
	issues, _ := LoadIssues(dir)
	x := BuildDecisionIndex(issues, nil)
	today := time.Date(2026, 10, 31, 12, 0, 0, 0, time.Local)
	for ref, want := range map[string]string{"a#D-1": DecisionExpired, "a#D-2": DecisionActive, "a#D-3": DecisionReplaced, "a#D-4": DecisionActive} {
		if got := x.Lookup(ref).StateOn(today); got != want {
			t.Errorf("%s = %s, want %s", ref, got, want)
		}
	}
	if !x.Lookup("a#D-1").WaitsOnOwner(today) || x.Lookup("a#D-3").WaitsOnOwner(today) {
		t.Fatal("WaitsOnOwner")
	}
}

// TestExpiredHardSkipsOtherFiles: only files with an expires: or a
// replaces are parsed; the list is the hard, expired, open ones.
func TestExpiredHardSkipsOtherFiles(t *testing.T) {
	dir := t.TempDir()
	writeExpiryIssue(t, dir, "Sim/a", "- **D-1** · 2026-10-01 · chat: X, \"x\" {scope: project; hard; expires: 2026-10-20}\n"+
		"- **D-2** · 2026-10-01 · chat: Y, \"y\" {hard; expires: 2026-10-10; ended: 2026-10-12}\n"+
		"- **D-3** · 2026-10-01 · chat: Z {expires: 2026-10-10}\n"+
		"- **D-4** · 2026-10-01 · chat: V, \"v\" {hard; expires: 2026-10-05}\n")
	writeExpiryIssue(t, dir, "Sim/b", "- **D-1** · 2026-10-02 · chat: New, \"n\" (replaces sim/a#D-4)\n")
	// A file that would not parse as an issue is never read past the filter.
	if err := os.WriteFile(filepath.Join(dir, "junk.md"), []byte("no frontmatter **D-1**"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ExpiredHard(dir, time.Date(2026, 10, 31, 0, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Ref != "sim/a#D-1" || got[0].Since.Format("2006-01-02") != "2026-10-21" {
		t.Fatalf("ExpiredHard = %+v", got)
	}
}

// TestLetGoAndRenewWriteOnlyTheGroup: the events, the line and refusals.
func TestLetGoAndRenewWriteOnlyTheGroup(t *testing.T) {
	dir := t.TempDir()
	line := "- **D-1** · 2026-10-01 · chat: X, \"x\" (also considered: y) {scope: project; hard; expires: 2026-10-20}"
	path := writeExpiryIssue(t, dir, "a", line+"\n")
	now := time.Date(2026, 10, 31, 8, 0, 0, 0, time.Local)
	w := DecisionWrite{IssueDir: dir, Actor: DecisionActor{Name: "owner", Role: ActorOwnerShell}}
	_, after, err := RenewDecisionAs(path, "a", "D-1", "2026-12-31", nil, now, w)
	if err != nil || after.Raw != "- **D-1** · 2026-10-01 · chat: X, \"x\" (also considered: y) {scope: project; hard; expires: 2026-12-31}" {
		t.Fatalf("renew: %v %q", err, after.Raw)
	}
	if _, _, err := LetGoDecisionAs(path, "a", "D-1", nil, now, w); err != ErrNotExpired {
		t.Fatalf("let go of one in force: %v", err)
	}
	if _, _, err := RenewDecisionAs(path, "a", "D-1", "2026-12-31", nil, now, w); err != ErrNothingChanged {
		t.Fatalf("same date: %v", err)
	}
	later := time.Date(2027, 1, 2, 8, 0, 0, 0, time.Local)
	_, after, err = LetGoDecisionAs(path, "a", "D-1", nil, later, w)
	if err != nil || after.Ended != "2027-01-02" {
		t.Fatalf("let go: %v %+v", err, after)
	}
	if _, _, err := RenewDecisionAs(path, "a", "D-1", "2027-12-31", nil, later, w); err != ErrDecisionEnded {
		t.Fatalf("renew after let go: %v", err)
	}
	evs, _ := ReadDecisionLog(dir)
	if len(evs) != 2 || evs[0].Event != DecisionRenewedEvent || evs[1].Event != DecisionExpiredEvent ||
		evs[1].New != "{scope: project; hard; expires: 2026-12-31; ended: 2027-01-02}" {
		t.Fatalf("log = %+v", evs)
	}
}
