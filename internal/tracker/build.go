package tracker

import (
	"fmt"
	"os"
)

// BuildVersion is the running program's version (issue-cli's or the
// viewer's main.Version, `git describe` at make install); "" when unset.
// Errors about a workflow newer than the build name it.
var BuildVersion = ""

// UnknownRuleError is a validate rule this build doesn't know. Most often
// the workflow file is newer than the installed program (main added the
// rule after this build), so it names the build and the fix.
type UnknownRuleError struct {
	Rule    string
	Version string
}

func (e *UnknownRuleError) Error() string {
	v := e.Version
	if v == "" {
		v = "dev"
	}
	where := ""
	if exe, err := os.Executable(); err == nil {
		where = ", " + exe
	}
	return fmt.Sprintf("unknown validation rule: %s — this build (%s%s) doesn't know it, so the workflow file is likely newer than the build: "+
		"the installed build is behind Dark Factory's main.\n"+
		"Fix: in your Dark Factory checkout (the dark-factory repo, not this project), pull main and rebuild:\n\n  git pull && make install\n\n"+
		"make install puts issue-cli and issue-viewer in $(go env GOBIN) or $(go env GOPATH)/bin, which must come first on PATH; restart a running viewer. "+
		"Without a checkout, install the newest release instead (docs/getting-started.md).\n"+
		"(Misspelled? The rules this build knows: issue-cli process schema)", e.Rule, v, where)
}
