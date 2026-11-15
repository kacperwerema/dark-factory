// Package doclinks finds relative markdown links that point at no file: the
// link check make validate runs over the repo's tracked markdown
// (make link-check). It checks the path only, not the #fragment, and skips
// links inside fenced code blocks and inline code. A path has to match the
// names on disk exactly, so a link that only a Mac's case-insensitive disk
// finds fails on the Mac too.
package doclinks

import (
	"bufio"
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Broken is one relative link whose target does not exist.
type Broken struct {
	File   string // the markdown file, as given
	Line   int    // 1-indexed
	Target string // the link as written, fragment dropped
	Note   string // why a target the disk finds still fails; "" when it is simply missing
}

func (b Broken) String() string {
	s := fmt.Sprintf("%s:%d: %s", b.File, b.Line, b.Target)
	if b.Note != "" {
		s += " (" + b.Note + ")"
	}
	return s
}

// linkRe matches an inline link or image target: ](target) or
// ](target "title").
var linkRe = regexp.MustCompile(`\]\(\s*<?([^)\s>]+)>?(?:\s+"[^"]*")?\s*\)`)

var inlineCodeRe = regexp.MustCompile("`+[^`]*`+")

// Check reads each file (a path relative to root) and returns the relative
// links in it whose target is missing on disk or spelled in another case.
func Check(root string, files []string) ([]Broken, error) {
	var out []Broken
	listed := map[string][]os.DirEntry{}
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			return nil, err
		}
		for _, l := range Links(data) {
			rel := filepath.Join(filepath.Dir(f), filepath.FromSlash(l.Target))
			if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
				out = append(out, Broken{File: f, Line: l.Line, Target: l.Target})
				continue
			}
			note, err := spelledOtherwise(root, rel, listed)
			if err != nil {
				return nil, err
			}
			if note != "" {
				out = append(out, Broken{File: f, Line: l.Line, Target: l.Target, Note: note})
			}
		}
	}
	return out, nil
}

// spelledOtherwise walks rel, a path that exists under root, down from root
// and says how the first name that is not in its directory's listing differs:
// the disk may match case and Unicode normalization loosely, the listing is
// what Linux and git see. It returns "" when every name is there. A path that
// leaves root is not walked: the names above root are not the repo's. listed
// keeps each directory's listing for the next link.
func spelledOtherwise(root, rel string, listed map[string][]os.DirEntry) (string, error) {
	if rel == "." || !filepath.IsLocal(rel) {
		return "", nil
	}
	dir := root
	for _, name := range strings.Split(rel, string(filepath.Separator)) {
		entries, ok := listed[dir]
		if !ok {
			var err error
			if entries, err = os.ReadDir(dir); err != nil {
				return "", err
			}
			listed[dir] = entries
		}
		exact, fold := false, ""
		for _, e := range entries {
			if e.Name() == name {
				exact = true
				break
			}
			if strings.EqualFold(e.Name(), name) {
				fold = e.Name()
			}
		}
		switch {
		case exact:
			dir = filepath.Join(dir, name)
		case fold != "":
			return "case differs, on disk: " + fold, nil
		default:
			return "spelled differently on disk: " + name, nil
		}
	}
	return "", nil
}

// Link is one relative link found in a markdown file, its path unescaped.
type Link struct {
	Line   int
	Target string
}

// Links returns the relative links of a markdown text, outside code: no
// scheme (http:, mailto:), no absolute path, no bare #fragment.
func Links(data []byte) []Link {
	var out []Link
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	fence := ""
	n := 0
	for sc.Scan() {
		n++
		line := sc.Text()
		trim := strings.TrimLeft(line, " \t>")
		if fence != "" {
			if strings.HasPrefix(trim, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			fence = trim[:3]
			continue
		}
		line = inlineCodeRe.ReplaceAllString(line, "")
		for _, m := range linkRe.FindAllStringSubmatch(line, -1) {
			target, _, _ := strings.Cut(m[1], "#")
			if target == "" || strings.HasPrefix(target, "/") || strings.Contains(target, ":") {
				continue
			}
			if dec, err := url.PathUnescape(target); err == nil {
				target = dec
			}
			out = append(out, Link{Line: n, Target: target})
		}
	}
	return out
}
