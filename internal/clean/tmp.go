package clean

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TmpName is one name shape the factory gives an entry in $TMPDIR: Prefix,
// then the random part os.MkdirTemp / os.CreateTemp put in for "*" (decimal
// digits), or Chrome for Testing's letters and digits (Alnum), then Suffix.
// Only a name with that random part matches: "pt-" takes pt-123456, never
// pt-anything.
type TmpName struct {
	Prefix, Suffix string
	Alnum          bool
}

// FactoryTmp are the names the factory's code and its test binaries make
// in $TMPDIR (2026-11-21: 6.8 GB of them, 4.5 GB cmd/playtest's pt-bins-*).
// A new os.MkdirTemp("", …) or CreateTemp("", …) in the engine adds its
// pattern here; TestFactoryTmpNamesEveryTempPattern holds the list to the
// code.
var FactoryTmp = []TmpName{
	{Prefix: "pt-bins-"}, {Prefix: "pt-serve-"}, {Prefix: "pt-serve-test-"}, {Prefix: "pt-src-"}, {Prefix: "pt-"},
	{Prefix: "dark-factory-playtest-", Alnum: true}, // a playtest Chrome's profile: <prefix><pid>-<n>
	{Prefix: "com.google.chrome.for.testing.", Alnum: true},
	{Prefix: "go-build"},
	{Prefix: "dft-"}, // a test binary's private TMPDIR (internal/tmuxguard)
	{Prefix: "testbin-"}, {Prefix: "test-shards-cover-"},
	// make's cover profiles, one per worktree path (Makefile CLI_COVER_PROFILE):
	// a removed worktree's stay behind.
	{Prefix: "dark-factory-cover-", Suffix: ".out"}, {Prefix: "dark-factory-cover-", Suffix: ".guard.out"},
	{Prefix: "dark-factory-cover-", Suffix: ".guard.out.merged"},
	{Prefix: "issue-cli-help-"}, {Prefix: "issue-cli-introspect-"}, {Prefix: "issue-cli-test-xdg-"},
	{Prefix: "issue-viewer-test-xdg-"}, {Prefix: "agentrun-test-xdg-"},
	{Prefix: "prove-run-results-"}, {Prefix: "check-script-"}, {Prefix: "experiment-revert-"},
	{Prefix: "awe-bench-verify-"},
	{Prefix: "issue-nudge-", Suffix: ".txt"}, {Prefix: "issue-approval-", Suffix: ".txt"},
	{Prefix: "issue-edit-", Suffix: ".md"}, {Prefix: "issue-edit-status-", Suffix: ".txt"},
}

// Match reports whether name has this shape.
func (n TmpName) Match(name string) bool {
	rest, ok := strings.CutPrefix(name, n.Prefix)
	if !ok {
		return false
	}
	if rest, ok = strings.CutSuffix(rest, n.Suffix); !ok || rest == "" {
		return false
	}
	for _, c := range rest {
		switch {
		case c >= '0' && c <= '9':
		case n.Alnum && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-' || c == '_'):
		default:
			return false
		}
	}
	return true
}

// tmpTakes reports whether the tmp area may take name: one of FactoryTmp,
// or a name starting with one of extra (another project's prefixes, from
// clean.tmp_prefixes) with something after it.
func tmpTakes(name string, extra []string) bool {
	for _, n := range FactoryTmp {
		if n.Match(name) {
			return true
		}
	}
	for _, p := range extra {
		if len(name) > len(p) && strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// Tmp clears the factory's leftovers in dir ($TMPDIR): an entry goes when
// its name is the factory's (FactoryTmp) or starts with one of extra, the
// newest thing in it was written before now-maxAge, and inUse (the entries
// a live process holds, procs.InUseUnder) doesn't name it. Everything else
// stays: another program's files, a live test's folder, a young one. A nil
// inUse means it isn't known (lsof failed): nothing goes. dry reports
// without deleting.
func Tmp(dir string, extra []string, maxAge time.Duration, inUse map[string]bool, now time.Time, dry bool) Area {
	a := Area{Name: "tmp"}
	if dir == "" {
		a.Note = "no TMPDIR"
		return a
	}
	if inUse == nil {
		a.Note = "left alone: which entries are open is unknown"
		return a
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		a.Err = err.Error()
		return a
	}
	cutoff := now.Add(-maxAge)
	removed, kept := 0, 0
	for _, e := range entries {
		name := e.Name()
		if !tmpTakes(name, extra) {
			continue
		}
		path := filepath.Join(dir, name)
		newest, size := treeAge(path)
		if inUse[name] || !newest.Before(cutoff) {
			kept++
			continue
		}
		if !dry {
			if err := os.RemoveAll(path); err != nil {
				kept++
				continue
			}
		}
		removed++
		a.FreedBytes += size
	}
	a.Note = fmt.Sprintf("%d entries removed, %d kept (in use or younger than %s)", removed, kept, hoursText(maxAge))
	return a
}

// treeAge is the newest mtime under path (path's own included, links not
// followed) and the bytes its files hold.
func treeAge(path string) (time.Time, int64) {
	var newest time.Time
	var size int64
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if t := info.ModTime(); t.After(newest) {
			newest = t
		}
		if d.Type().IsRegular() {
			size += info.Size()
		}
		return nil
	})
	return newest, size
}

func hoursText(d time.Duration) string {
	return fmt.Sprintf("%d h", int(d/time.Hour))
}
