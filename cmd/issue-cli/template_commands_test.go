package main

import (
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/cmd/issue-cli/templates"
)

// templateCommandRe finds an issue-cli command written in a bundled template
// and its arguments, up to the end of the code span, parenthesis, sentence
// or line it sits in.
var templateCommandRe = regexp.MustCompile("issue-cli((?: [^`();\n]*)?)")

var templateFlagRe = regexp.MustCompile(`(?:^|\s)--([a-z][a-z0-9-]*)`)

// Every issue-cli command a bundled template tells an agent to run uses only
// flags that command accepts. A foundation's publish step said `output
// publish <id> --path ...`; the command only takes --url, so the agent's
// call failed with "Valid flags: --url".
func TestBundledTemplatesNameOnlyFlagsTheCommandAccepts(t *testing.T) {
	isolateTelemetryEnv(t)
	withCwd(t, t.TempDir())
	issueDir := t.TempDir()
	global := []string{"project", "json", "help"}
	flagsOf := map[string][]string{}

	err := fs.WalkDir(templates.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := templates.FS.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range templateCommandRe.FindAllStringSubmatch(string(data), -1) {
			words := strings.Fields(m[1])
			for len(words) >= 2 && words[0] == "--project" {
				words = words[2:]
			}
			if len(words) == 0 {
				continue
			}
			cmd := lookupCommand(words[0])
			if cmd == nil {
				continue // prose ("issue-cli is …"), not a command
			}
			sub := ""
			if len(words) > 1 && slices.Contains(cmd.Subcommands, words[1]) {
				sub = words[1]
			}
			key := words[0] + " " + sub
			valid, seen := flagsOf[key]
			if !seen {
				valid, _ = introspectFlags(cmd, sub, issueDir)
				flagsOf[key] = valid
			}
			for _, f := range templateFlagRe.FindAllStringSubmatch(strings.Join(words, " "), -1) {
				if !slices.Contains(valid, f[1]) && !slices.Contains(global, f[1]) {
					t.Errorf("%s: `issue-cli%s` uses --%s; %s accepts %v", path, m[1], f[1], strings.TrimSpace(key), valid)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(flagsOf) == 0 {
		t.Fatal("found no issue-cli commands in the bundled templates")
	}
}
