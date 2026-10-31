package validations

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCommandSucceeds_DisabledByDefault(t *testing.T) {
	err := CommandSucceeds(Action{Rule: "command_succeeds", Command: "true"}, sampleIssue(), Config{})
	if err == nil || !strings.Contains(err.Error(), "allow_shell") {
		t.Fatalf("expected allow_shell error, got %v", err)
	}
}

func TestCommandSucceeds_Pass(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell required")
	}
	if err := CommandSucceeds(Action{Rule: "command_succeeds", Command: "true"}, sampleIssue(), Config{AllowShell: true}); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

func TestCommandSucceeds_Fail(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell required")
	}
	err := CommandSucceeds(Action{Rule: "command_succeeds", Command: "echo nope >&2; false"}, sampleIssue(), Config{AllowShell: true})
	if err == nil || !strings.Contains(err.Error(), "exit") || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("expected exit+stderr in error, got %v", err)
	}
}

func TestCommandSucceeds_Templating(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell required")
	}
	cmd := `[ "{{slug}}" = "sample" ] && [ "{{number}}" = "42" ] && [ "{{repo}}" = "owner/repo" ]`
	if err := CommandSucceeds(Action{Rule: "command_succeeds", Command: cmd}, sampleIssue(), Config{AllowShell: true}); err != nil {
		t.Fatalf("expected templated pass, got %v", err)
	}
}

func TestCommandSucceeds_Timeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell required")
	}
	err := CommandSucceeds(Action{Rule: "command_succeeds", Command: "sleep 5", TimeoutSeconds: 1}, sampleIssue(), Config{AllowShell: true})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout, got %v", err)
	}
}

func TestCommandSucceeds_StubBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell required")
	}
	dir := t.TempDir()
	stub := filepath.Join(dir, "fakebin")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nif [ \"$1\" = \"--ok\" ]; then exit 0; fi\necho boom >&2\nexit 7\n"), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	if err := CommandSucceeds(Action{Rule: "command_succeeds", Command: "fakebin --ok"}, sampleIssue(), Config{AllowShell: true}); err != nil {
		t.Fatalf("stub --ok should succeed, got %v", err)
	}
	err := CommandSucceeds(Action{Rule: "command_succeeds", Command: "fakebin"}, sampleIssue(), Config{AllowShell: true})
	if err == nil || !strings.Contains(err.Error(), "exit 7") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected exit 7 + stderr, got %v", err)
	}
}

func TestCommandSucceeds_HintTemplated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell required")
	}
	a := Action{Rule: "command_succeeds", Command: "false", Hint: "fix it for {{slug}}"}
	err := CommandSucceeds(a, sampleIssue(), Config{AllowShell: true})
	if err == nil || !strings.Contains(err.Error(), "fix it for sample") {
		t.Fatalf("expected templated hint, got %v", err)
	}
}

// Frontmatter is attacker-writable (GitHub import, agents): a value with
// shell syntax reaches the command as one quoted word and runs nothing
// (docs/API/security.md).
func TestCommandSucceeds_TemplateValuesCannotRunCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell required")
	}
	marker := filepath.Join(t.TempDir(), "pwned")
	issue := sampleIssue()
	issue.System = "x;touch " + marker + ";$(touch " + marker + ")`touch " + marker + "`'"
	issue.Repo = "o/r"
	cmd := `test -n {{system}} && [ {{repo}} = o/r ]`
	if err := CommandSucceeds(Action{Rule: "command_succeeds", Command: cmd}, issue, Config{AllowShell: true}); err != nil {
		t.Fatalf("quoted template failed: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a frontmatter value ran a command")
	}
}
