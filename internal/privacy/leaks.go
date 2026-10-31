package privacy

// The leak scan: tracked files that would carry this machine's private data
// into a public tree. Two kinds of hit:
//
//   - a user home path (/Users/<name>/, /home/<name>/, C:\Users\<name>\, and
//     Claude Code's encoded -Users-<name>- folder names) or a Claude Code
//     scratchpad path (/claude-<uid>/). These patterns always run.
//   - a name from the owner's private-names list, kept outside the repo
//     (PrivateNames), so the names themselves never reach it.
//
// A test input that needs a fake home path uses a placeholder user name
// (placeholderUsers). To exempt another one, add it there: a made-up name,
// never a real one. TestTrackedFilesCarryNoLeaks runs the scan over this
// repo in make validate, and release publish refuses a tree with a hit.

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// placeholderUsers are the user names test inputs and docs may put in a home
// path. Compared without case.
var placeholderUsers = map[string]bool{
	"a": true, "u": true, "h": true, "x": true, "me": true, "you": true,
	"owner": true, "someone": true, "user": true, "username": true, "name": true,
	"example": true, "alice": true, "bob": true, "runner": true, "test": true, "foo": true,
}

// Leak is one hit of the scan.
type Leak struct {
	Path string // relative to the repo root
	Line int
	What string // what kind of hit, without echoing a private name
}

func (l Leak) String() string {
	return fmt.Sprintf("%s:%d: %s", l.Path, l.Line, l.What)
}

var (
	// git grep's prefilter (ERE): every line a pattern below could hit.
	leakPrefilter = `/Users/|/home/|-Users-|-home-|/claude-[0-9]|Users\\`

	// The character before the path must not continue a word or a variable
	// ($stage/home/…, news/home/2025 are not home paths).
	homeRe    = regexp.MustCompile(`(?:^|[^A-Za-z0-9_.}$\-])(/Users/|/home/)([A-Za-z0-9._-]+)`)
	encodedRe = regexp.MustCompile(`(?:^|[/"'\s=:])-(Users|home)-([A-Za-z0-9._]+)-`)
	winRe     = regexp.MustCompile(`[A-Za-z]:\\{1,2}Users\\{1,2}([A-Za-z0-9._-]+)`)
	scratchRe = regexp.MustCompile(`/claude-[0-9]+/`)
)

func realUser(name string) bool {
	if name == "" || strings.HasPrefix(name, ".") {
		return false
	}
	return !placeholderUsers[strings.ToLower(name)]
}

// pathLeaks lists the path hits on one line.
func pathLeaks(text string) []string {
	var out []string
	for _, m := range homeRe.FindAllStringSubmatch(text, -1) {
		if realUser(m[2]) {
			out = append(out, "a user home path ("+m[1]+m[2]+"/): use a placeholder user such as /home/someone/")
		}
	}
	for _, m := range encodedRe.FindAllStringSubmatch(text, -1) {
		if realUser(m[2]) {
			out = append(out, "an encoded user home path (-"+m[1]+"-"+m[2]+"-): use a placeholder user")
		}
	}
	for _, m := range winRe.FindAllStringSubmatch(text, -1) {
		if realUser(m[1]) {
			out = append(out, `a user home path (C:\Users\`+m[1]+`\): use a placeholder user`)
		}
	}
	if scratchRe.MatchString(text) {
		out = append(out, "a Claude Code scratchpad path (/claude-<uid>/): use a neutral path such as /tmp/…")
	}
	return out
}

// nameAt reports whether name occurs in text as a whole word, without case.
func nameAt(text, name string) bool {
	lt, ln := strings.ToLower(text), strings.ToLower(name)
	for i := 0; ; {
		j := strings.Index(lt[i:], ln)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(ln)
		if !wordRune(lt, start-1) && !wordRune(lt, end) {
			return true
		}
		i = start + 1
	}
}

func wordRune(s string, i int) bool {
	if i < 0 || i >= len(s) {
		return false
	}
	r := rune(s[i])
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// ScanLeaks scans the tracked files of the repo at dir: the working tree's
// tracked files when rev is "", else the tree at rev. Binary files are
// skipped. names is the private-names list (PrivateNames).
func ScanLeaks(dir, rev string, names []string) ([]Leak, error) {
	var leaks []Leak
	lines, err := grepLines(dir, rev, []string{"-E", "-e", leakPrefilter})
	if err != nil {
		return nil, err
	}
	for _, l := range lines {
		for _, what := range pathLeaks(l.text) {
			leaks = append(leaks, Leak{Path: l.path, Line: l.line, What: what})
		}
	}
	var pats []string
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			pats = append(pats, "-e", n)
		}
	}
	if len(pats) > 0 {
		lines, err := grepLines(dir, rev, append([]string{"-i", "-F"}, pats...))
		if err != nil {
			return nil, err
		}
		for _, l := range lines {
			for i, n := range names {
				if strings.TrimSpace(n) != "" && nameAt(l.text, strings.TrimSpace(n)) {
					leaks = append(leaks, Leak{Path: l.path, Line: l.line, What: fmt.Sprintf("a private name (line %d of the private-names list)", i+1)})
				}
			}
		}
	}
	return leaks, nil
}

type grepLine struct {
	path string
	line int
	text string
}

// grepLines runs git grep -I -n -z with the given pattern args over the
// tracked files (rev "") or a revision's tree.
func grepLines(dir, rev string, patArgs []string) ([]grepLine, error) {
	args := append([]string{"-c", "core.quotePath=false", "grep", "-I", "-n", "-z", "--no-color"}, patArgs...)
	if rev != "" {
		args = append(args, rev)
	}
	args = append(args, "--")
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return nil, nil // no match
		}
		return nil, fmt.Errorf("git grep in %s: %w", dir, err)
	}
	var res []grepLine
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		// path\0line\0text (with -z, the line number ends in \0 too)
		parts := strings.SplitN(sc.Text(), "\x00", 3)
		if len(parts) != 3 {
			continue
		}
		path := parts[0]
		if rev != "" {
			path = strings.TrimPrefix(path, rev+":")
		}
		n, _ := strconv.Atoi(parts[1])
		res = append(res, grepLine{path: path, line: n, text: parts[2]})
	}
	return res, sc.Err()
}

// PrivateNamesFile is where the owner's private-names list lives:
// $XDG_CONFIG_HOME/ai-workflow-engine/private-names.txt (default
// ~/.config/…). One name per line; # starts a comment.
func PrivateNamesFile() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "ai-workflow-engine", "private-names.txt")
}

// PrivateNames reads the private-names list. path is "" when there is no
// list, and then only the path patterns run.
func PrivateNames() (names []string, path string) {
	p := PrivateNamesFile()
	data, err := os.ReadFile(p)
	if p == "" || err != nil {
		return nil, ""
	}
	for _, l := range strings.Split(string(data), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			names = append(names, l)
		}
	}
	return names, p
}

// leakError is the refusal publish gives on a hit.
func leakError(where string, leaks []Leak) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%d private-data hit(s) in %s's tree would go public (the leak scan: user home paths, scratchpad paths, names from %s):", len(leaks), where, valueOr(PrivateNamesFile(), "the private-names list"))
	for i, l := range leaks {
		if i == 10 {
			fmt.Fprintf(&b, "\n  … and %d more", len(leaks)-10)
			break
		}
		b.WriteString("\n  " + l.String())
	}
	b.WriteString("\nFix them on main, pick the commit, cut a new rc")
	return errors.New(b.String())
}
