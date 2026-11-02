package workflowupgrade

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Paths of the files catalog-check reads, relative to the repository root.
const (
	TemplatesPath = "cmd/issue-cli/templates/workflow"
	ChangelogPath = "cmd/issue-cli/CHANGELOG.md"
)

// PromptSource is the sources: value of a feature that is prompt text only
// (a template or snippet convention): catalog-check doesn't watch it, the
// documentation box does.
const PromptSource = "@prompt"

// Branch is what catalog-check knows about a work branch: the files it
// changes since the merge-base, their content on each side, and the
// `Catalog: n/a <id>[,<id>] <why>` lines on its own commits.
type Branch struct {
	Changed []string
	// Base and Head return a file's content; ok is false when it doesn't
	// exist on that side.
	Base, Head func(path string) (data []byte, ok bool)
	// HeadDir lists the file names in a folder at the head (the catalog's
	// entry files, the bundled templates).
	HeadDir func(dir string) []string
	// HeadFiles lists every file of the head (tracked and new): the sources
	// check. nil skips it.
	HeadFiles func() []string
	Trailers  []Trailer
}

// Trailer is the value of one Catalog: line and the commit it is on.
type Trailer struct {
	Commit string `json:"commit"`
	Value  string `json:"value"`
}

// TrailerForm is the one form of Catalog: line catalog-check reads.
const TrailerForm = "Catalog: n/a <id>[,<id>...] <why>"

// BadTrailer is a Catalog: line that marks nothing: it isn't an n/a, names
// no entry id, or names an entry the catalog doesn't have.
type BadTrailer struct {
	Trailer
	Problem string `json:"problem"`
}

// CheckEntry is one catalog entry whose sources the branch changes.
type CheckEntry struct {
	ID      string   `json:"id"`
	Version string   `json:"version"`
	Files   []string `json:"files"`
	// State is "bumped" (version newer than the last release), "n/a" (a
	// Catalog: n/a trailer names it) or "stale".
	State string `json:"state"`
	Why   string `json:"why,omitempty"`
}

// CheckTemplate is a bundled template the branch changes.
type CheckTemplate struct {
	Name  string `json:"name"`
	Stamp string `json:"stamp"`
	State string `json:"state"` // "bumped" or "stale"
}

// CheckReport is the result of CheckBranch.
type CheckReport struct {
	LastRelease string          `json:"last_release"`
	NextRelease string          `json:"next_release"`
	Entries     []CheckEntry    `json:"entries"`
	Templates   []CheckTemplate `json:"templates"`
	BadTrailers []BadTrailer    `json:"bad_trailers"`
	// Problems are the catalog's own mistakes at the head, each with its
	// fix: CatalogProblems, and a bundled template whose features: record
	// isn't what a scan records (RecordDrift).
	Problems []string `json:"problems"`
}

// OK reports whether every touched entry and template is bumped or marked
// n/a, and every Catalog: line is one catalog-check reads.
func (r *CheckReport) OK() bool {
	if len(r.BadTrailers) > 0 || len(r.Problems) > 0 {
		return false
	}
	for _, e := range r.Entries {
		if e.State == "stale" {
			return false
		}
	}
	for _, t := range r.Templates {
		if t.State == "stale" {
			return false
		}
	}
	return true
}

var releaseHeading = regexp.MustCompile(`(?m)^## (v\d+\.\d+\.\d+)\b`)

// LastRelease is the newest `## vX.Y.Z` heading in a changelog.
func LastRelease(changelog []byte) (string, bool) {
	m := releaseHeading.FindSubmatch(changelog)
	if m == nil {
		return "", false
	}
	return string(m[1]), true
}

// NextRelease is the release after last: its minor version bumped.
func NextRelease(last string) string {
	v := ParseVersion(last)
	if v == nil {
		return ""
	}
	return fmt.Sprintf("v%d.%d.0", v[0], v[1]+1)
}

// CheckBranch asks for a version bump on every catalog entry whose sources
// the branch changes, and a stamp bump on every bundled template it changes.
// The catalog and templates are read from the branch head, so the check
// sees the bumps the branch itself makes.
func CheckBranch(b Branch, lastRelease string) (*CheckReport, error) {
	rep := &CheckReport{LastRelease: lastRelease, NextRelease: NextRelease(lastRelease), Entries: []CheckEntry{}, Templates: []CheckTemplate{}, BadTrailers: []BadTrailer{}, Problems: []string{}}
	entries, err := RepoCatalog(Repo{Read: b.Head, Dir: b.HeadDir})
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, e := range entries {
		known[e.ID] = true
	}
	na, bad := naTrailers(b.Trailers, known)
	rep.BadTrailers = append(rep.BadTrailers, bad...)
	newer := func(v string) bool { return CompareVersions(v, lastRelease) > 0 }
	c := &checker{b: b, consts: map[string]map[string]string{}}

	for _, e := range entries {
		var files []string
		for _, f := range b.Changed {
			if !Exempt(f) && c.sourceChanged(e, f) {
				files = append(files, f)
			}
		}
		if len(files) == 0 {
			continue
		}
		ce := CheckEntry{ID: e.ID, Version: e.Version, Files: files, State: "stale"}
		switch why, marked := na[e.ID]; {
		case newer(e.Version):
			ce.State = "bumped"
		case marked:
			ce.State, ce.Why = "n/a", why
		}
		rep.Entries = append(rep.Entries, ce)
	}

	for _, f := range b.Changed {
		if path.Dir(f) != TemplatesPath || path.Ext(f) != ".yaml" {
			continue
		}
		data, ok := b.Head(f)
		if !ok {
			continue
		}
		if old, ok := b.Base(f); ok && bytes.Equal(withoutRecord(old), withoutRecord(data)) {
			continue // only its features: record moved: scan's output, nothing to adopt
		}
		name := strings.TrimSuffix(path.Base(f), ".yaml")
		ct := CheckTemplate{Name: name, Stamp: ReadStamp(data), State: "stale"}
		if n, v, ok := ParseStamp(ct.Stamp); ok && n == name && newer(v) {
			ct.State = "bumped"
		}
		rep.Templates = append(rep.Templates, ct)
	}

	var files []string
	if b.HeadFiles != nil {
		files = b.HeadFiles()
	}
	rep.Problems = append(rep.Problems, CatalogProblems(entries, files, b.Head)...)
	if b.HeadDir != nil {
		for _, f := range b.HeadDir(TemplatesPath) {
			if path.Ext(f) != ".yaml" {
				continue
			}
			data, ok := b.Head(TemplatesPath + "/" + f)
			if !ok {
				continue
			}
			drift, err := RecordDrift(entries, f, data)
			if err != nil {
				return nil, err
			}
			if drift != "" {
				rep.Problems = append(rep.Problems, fmt.Sprintf("%s/%s\nFix: issue-cli workflow scan --template %s", TemplatesPath, drift, strings.TrimSuffix(f, ".yaml")))
			}
		}
	}
	return rep, nil
}

// withoutRecord is a workflow file with its features: record emptied, to
// tell a change of the record alone from one to the workflow.
func withoutRecord(data []byte) []byte {
	if _, ok := ReadRecord(data); !ok {
		return data
	}
	out, err := WriteRecord(data, tracker.FeatureRecord{})
	if err != nil {
		return data
	}
	return out
}

var naTrailer = regexp.MustCompile(`(?i)^n/a(?:\s+([^\s,]+(?:\s*,\s*[^\s,]+)*)(?:\s+(.*))?)?$`)

var naColon = regexp.MustCompile(`(?i)^n/a\s*:`)

// CheckTrailers returns the Catalog: values that mark nothing, with why:
// the same reading catalog-check does, for a message before it is
// committed (issue-cli workflow commit-msg). known nil skips the question
// whether an id is a catalog entry; every other form is still checked.
func CheckTrailers(values []Trailer, known map[string]bool) []BadTrailer {
	_, bad := naTrailers(values, known)
	return bad
}

// EntryIDs is the set of entry ids in the catalog of the checkout repo
// reads (the split catalog included).
func EntryIDs(repo Repo) (map[string]bool, error) {
	entries, err := RepoCatalog(repo)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, e := range entries {
		known[e.ID] = true
	}
	return known, nil
}

// naTrailers parses `Catalog: n/a <id>[,<id>...] <why>` values into id → why,
// and returns the values that mark nothing as bad.
func naTrailers(values []Trailer, known map[string]bool) (map[string]string, []BadTrailer) {
	out := map[string]string{}
	var bad []BadTrailer
	for _, t := range values {
		t.Value = strings.TrimSpace(t.Value)
		if t.Value == "" {
			continue
		}
		m := naTrailer.FindStringSubmatch(t.Value)
		switch {
		case m == nil && naColon.MatchString(t.Value):
			bad = append(bad, BadTrailer{t, "a colon after n/a: write " + TrailerForm + ", no colon"})
			continue
		case m == nil:
			bad = append(bad, BadTrailer{t, "only n/a is read in a Catalog: line: a bumped entry needs no line (issue-cli workflow catalog-bump <id>), an unchanged one is " + TrailerForm})
			continue
		case m[1] == "":
			bad = append(bad, BadTrailer{t, "names no entry: the entry id comes right after n/a"})
			continue
		}
		var ids, unknown, colon []string
		for _, id := range strings.Split(m[1], ",") {
			id = strings.TrimSpace(id)
			if bare := strings.TrimRight(id, ":"); bare != id && bare != "" {
				colon = append(colon, bare)
				continue
			}
			if known == nil || known[id] {
				ids = append(ids, id)
			} else {
				unknown = append(unknown, fmt.Sprintf("%q", id))
			}
		}
		if len(colon) > 0 {
			bad = append(bad, BadTrailer{t, fmt.Sprintf("a colon after the entry id %q: write \"Catalog: n/a %s <why>\", no colon (with it the line marks nothing)", colon[0], strings.Join(colon, ","))})
			continue
		}
		if len(unknown) > 0 {
			bad = append(bad, BadTrailer{t, "no catalog entry " + strings.Join(unknown, ", ") + " (the entry id comes right after n/a)"})
			continue
		}
		for _, id := range ids {
			out[id] = strings.TrimSpace(m[2])
		}
	}
	return out, bad
}

// checker holds what one CheckBranch run reads more than once.
type checker struct {
	b      Branch
	consts map[string]map[string]string // package folder → string consts
}

// sourceChanged reports whether the changed file f is one of the entry's
// sources and the change is the entry's own (attribute.go): for a
// `file.go#Symbol` source the symbol's code (narrowed to the entry's rule
// cases) differs; for a bundled template the parts its detect: sees
// differ; for a TextSources file more than its text changed.
func (c *checker) sourceChanged(e Entry, f string) bool {
	b := c.b
	for _, s := range e.Sources {
		if s == PromptSource {
			continue
		}
		glob, sym, hasSym := strings.Cut(s, "#")
		if !MatchSource(glob, f) {
			continue
		}
		before, _ := b.Base(f)
		after, _ := b.Head(f)
		switch {
		case hasSym:
			rules := ruleNames(e)
			consts := map[string]string{}
			if len(rules) > 0 {
				consts = c.packageConsts(path.Dir(f))
			}
			if ruleSymbolSource(before, sym, rules, consts) != ruleSymbolSource(after, sym, rules, consts) {
				return true
			}
		case isTemplate(f):
			was, okB := detectRegion(before, e.Detect)
			is, okA := detectRegion(after, e.Detect)
			if !okB || !okA || was != is {
				return true
			}
		case TextSource(f) && textOnlyChange(before, after):
		default:
			return true
		}
	}
	return false
}

// MatchSource reports whether the repo-relative slash path p matches a
// sources: glob. A `**` segment matches any number of segments; other
// segments use path.Match. Globs are anchored at the repository root.
func MatchSource(glob, p string) bool {
	return matchSegments(strings.Split(strings.Trim(glob, "/"), "/"), strings.Split(p, "/"))
}

func matchSegments(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			for i := 0; i <= len(segs); i++ {
				if matchSegments(pat[1:], segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		if ok, _ := path.Match(pat[0], segs[0]); !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}

// symbolSource is the code of the top-level Go declarations named sym (a
// func or method name, a type, a const or a var), doc comments left out.
// "" when the file doesn't parse or has no such symbol.
func symbolSource(src []byte, sym string) string {
	decls := SymbolDecls(src, sym)
	var b strings.Builder
	for _, d := range decls {
		b.WriteString(d)
		b.WriteByte('\n')
	}
	return b.String()
}

// SymbolDecls returns the source of each top-level declaration named sym.
func SymbolDecls(src []byte, sym string) []string {
	if len(src) == 0 {
		return nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, parser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	text := func(n ast.Node) string {
		return string(bytes.TrimSpace(src[fset.Position(n.Pos()).Offset:fset.Position(n.End()).Offset]))
	}
	var out []string
	for _, d := range file.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Name.Name == sym {
				out = append(out, text(d))
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name.Name == sym {
						out = append(out, text(s))
					}
				case *ast.ValueSpec:
					for _, n := range s.Names {
						if n.Name == sym {
							out = append(out, text(s))
						}
					}
				}
			}
		}
	}
	sort.Strings(out)
	return out
}
