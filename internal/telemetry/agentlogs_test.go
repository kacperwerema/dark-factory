package telemetry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The first log line under .agent-logs/ ignores the folder in git, nested
// paths included; a .gitignore already there is kept, and a log outside
// .agent-logs gets none.
func TestAppendLineIgnoresAgentLogs(t *testing.T) {
	root := t.TempDir()
	if err := AppendLine(filepath.Join(root, ".agent-logs", "agent-x", "agent-x.clilog"), []byte("{}\n"), 0); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, ".agent-logs", ".gitignore"))
	if err != nil || !strings.Contains(string(b), "\n*\n") {
		t.Fatalf(".agent-logs/.gitignore = %q, %v", b, err)
	}
	os.WriteFile(filepath.Join(root, ".agent-logs", ".gitignore"), []byte("mine\n"), 0o644)
	AppendLine(filepath.Join(root, ".agent-logs", "clilog.jsonl"), []byte("{}\n"), 0)
	if b, _ := os.ReadFile(filepath.Join(root, ".agent-logs", ".gitignore")); string(b) != "mine\n" {
		t.Errorf("an existing .gitignore was rewritten: %q", b)
	}
	other := filepath.Join(root, "state", "issue-cli")
	AppendLine(filepath.Join(other, "clilog.jsonl"), []byte("{}\n"), 0)
	if _, err := os.Stat(filepath.Join(other, ".gitignore")); err == nil {
		t.Error("a log outside .agent-logs got a .gitignore")
	}
}
