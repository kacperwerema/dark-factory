package clilog

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRedactKeepsStructureAndHidesText(t *testing.T) {
	args := []string{
		"--project", "issue-viewer", "--json", "comment", "cli/foo",
		"--text", "progress: secret plan", "--how", "lead chat",
		"--to", "in progress", "--field", "waiting=design review",
		"--body=- [ ] one", "--dry-run", "check", "D3", "AC1",
		"report-bug", "it broke when I typed hunter2 here",
	}
	got := Redact(args)
	want := []string{
		"--project", "issue-viewer", "--json", "comment", "cli/foo",
		"--text", Placeholder("progress: secret plan"), "--how", "lead chat",
		"--to", "in progress", "--field", "waiting=" + Placeholder("design review"),
		"--body=" + Placeholder("- [ ] one"), "--dry-run", "check", "D3", "AC1",
		"report-bug", Placeholder("it broke when I typed hunter2 here"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Redact:\n got %q\nwant %q", got, want)
	}
}

func TestRedactIsIdempotent(t *testing.T) {
	args := []string{"comment", "x", "--text", "a b", "--field", "k=v w", "free text here"}
	once := Redact(args)
	if twice := Redact(once); !reflect.DeepEqual(once, twice) {
		t.Fatalf("second pass changed args:\n%q\n%q", once, twice)
	}
}

func TestRedactValueThatLooksLikeAFlag(t *testing.T) {
	got := Redact([]string{"append", "x", "--body", "--secret", "--section", "Design"})
	want := []string{"append", "x", "--body", Placeholder("--secret"), "--section", "Design"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRedactUnknownFlagHidesValue(t *testing.T) {
	got := Redact([]string{"show", "x", "--brand-new", "token123"})
	if got[3] != Placeholder("token123") {
		t.Fatalf("unknown flag value kept: %q", got)
	}
}

func TestRedactLongPositional(t *testing.T) {
	long := strings.Repeat("a", maxKeptPositional+1)
	got := Redact([]string{"show", long})
	if got[1] != Placeholder(long) {
		t.Fatalf("long positional kept: %q", got[1])
	}
}

func TestPlaceholderStable(t *testing.T) {
	p := Placeholder("héllo")
	if p != Placeholder("héllo") || !IsPlaceholder(p) || !strings.Contains(p, "n=5 ") {
		t.Fatalf("placeholder %q", p)
	}
	if Placeholder("a") == Placeholder("b") {
		t.Fatal("different text, same placeholder")
	}
}

// TestRedactCallAsksIsBool: the flags isBool names take no value, so the
// token after one stays a positional; any other flag hides its value.
func TestRedactCallAsksIsBool(t *testing.T) {
	isBool := func(name string) bool { return name == "brand-new-switch" }
	got := RedactCall([]string{"show", "--brand-new-switch", "cli/foo", "--other", "v", "--brand-new-switch=false"}, isBool)
	want := []string{"show", "--brand-new-switch", "cli/foo", "--other", Placeholder("v"), "--brand-new-switch=false"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RedactCall:\n got %q\nwant %q", got, want)
	}
	if got := RedactCall([]string{"--yes", "cli/foo"}, nil); got[1] != Placeholder("cli/foo") {
		t.Fatalf("nil isBool must take the next token as the value: %q", got)
	}
}

func TestAppendAndReadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".agent-logs", FileName)
	exit, dur := 1, int64(1500)
	rec := Record{TS: "2026-10-23T10:00:00Z", PID: 1, PPID: 2, Args: []string{"show", "x"}, Exit: &exit, ErrClass: "validation", DurMS: &dur}
	if err := Append(path, rec); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".1", []byte(`{"ts":"2026-10-22T09:00:00Z","pid":3,"ppid":4,"args":["next"]}`+"\nnot json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var got []Record
	Read(path, func(r Record) { got = append(got, r) })
	if len(got) != 2 || got[0].Args[0] != "next" || got[0].Exit != nil || *got[1].Exit != 1 || got[1].ErrClass != "validation" {
		t.Fatalf("read %+v", got)
	}
	if !got[1].End().Equal(got[1].Start().Add(1500 * time.Millisecond)) {
		t.Fatalf("End %v Start %v", got[1].End(), got[1].Start())
	}
	if !got[0].End().Equal(got[0].Start()) {
		t.Fatal("old record End should equal Start")
	}
}

func TestStripGlobalAndFlagValue(t *testing.T) {
	args := []string{"--config", "c.yaml", "--project", "p", "--json", "transition", "x", "--to=done"}
	if got := StripGlobal(args); !reflect.DeepEqual(got, []string{"transition", "x", "--to=done"}) {
		t.Fatalf("StripGlobal %q", got)
	}
	if FlagValue(args, "--to") != "done" || FlagValue(args, "--project") != "p" || FlagValue(args, "--x") != "" {
		t.Fatal("FlagValue")
	}
}
