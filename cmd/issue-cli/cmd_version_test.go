package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestVersionFlagSpellings: --version, -version and -v run version, with no
// config anywhere (a fresh install).
func TestVersionFlagSpellings(t *testing.T) {
	withVersion(t, "v0.60.0")
	t.Chdir(t.TempDir())
	for _, arg := range []string{"version", "--version", "-version", "-v"} {
		var out, errw bytes.Buffer
		if err := run([]string{arg}, strings.NewReader(""), &out, &errw); err != nil {
			t.Fatalf("issue-cli %s: %v (stderr %q)", arg, err, errw.String())
		}
		if out.String() != "issue-cli v0.60.0\n" {
			t.Errorf("issue-cli %s printed %q", arg, out.String())
		}
	}
}
