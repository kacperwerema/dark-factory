package clibug

import (
	"os/exec"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaybeFixedLabel marks a cli-bug whose quoted error main changed after the
// reporter's build: the bug may already be fixed there.
const MaybeFixedLabel = "maybe-fixed"

// minQuoteWords is how many words a quoted span needs to count as an error
// message; shorter spans are names, flags or ids.
const minQuoteWords = 3

// commandPrefixes start a quoted span that is a command line, not an error:
// two reports quoting the same command are not the same bug.
var commandPrefixes = []string{"issue-cli ", "./issue-cli ", "go ", "git ", "make ", "./", "$ "}

// QuotedErrors are the error messages a report quotes: spans in backticks,
// double quotes, or single quotes bounded by non-letters, with 3 words or
// more, that are not command lines. In the order they appear, without
// repeats (by Normalize).
func QuotedErrors(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, span := range quotedSpans(text) {
		span = strings.TrimSpace(span)
		if len(strings.Fields(span)) < minQuoteWords || isCommand(span) {
			continue
		}
		key := NormalizeTitle(span)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, span)
	}
	return out
}

func isCommand(span string) bool {
	low := strings.ToLower(span)
	for _, p := range commandPrefixes {
		if strings.HasPrefix(low, p) {
			return true
		}
	}
	return false
}

// quotedSpans finds the spans between matching quote marks. A backtick or
// double quote pairs with the next one of its kind; a single quote opens
// only after a non-letter and closes only before one, so "doesn't" is not a
// quote.
func quotedSpans(text string) []string {
	var spans []string
	for i := 0; i < len(text); {
		c := text[i]
		if c != '`' && c != '"' && c != '\'' {
			i++
			continue
		}
		if c == '\'' && i > 0 && isWordByte(text, i-1) {
			i++
			continue
		}
		end := -1
		for j := i + 1; j < len(text); j++ {
			if text[j] == '\n' && c != '`' {
				break
			}
			if text[j] != c {
				continue
			}
			if c == '\'' && j+1 < len(text) && isWordByte(text, j+1) {
				continue
			}
			end = j
			break
		}
		if end < 0 {
			i++
			continue
		}
		spans = append(spans, text[i+1:end])
		i = end + 1
	}
	return spans
}

func isWordByte(text string, i int) bool {
	r, _ := utf8.DecodeRuneInString(text[i:])
	if r == utf8.RuneError {
		// Inside a multi-byte rune: look back for its start.
		for k := i; k >= 0 && k > i-4; k-- {
			if utf8.RuneStart(text[k]) {
				r, _ = utf8.DecodeRuneInString(text[k:])
				break
			}
		}
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// SameError reports whether two reports quote the same error message.
func SameError(a, b []string) bool {
	keys := map[string]bool{}
	for _, q := range a {
		keys[NormalizeTitle(q)] = true
	}
	for _, q := range b {
		if keys[NormalizeTitle(q)] {
			return true
		}
	}
	return false
}

// ReportSection is the text of the body's "## Report" section ("" without
// one): what report-bug wrote from the first report.
func ReportSection(body string) string {
	lines := strings.Split(body, "\n")
	var out []string
	in := false
	for _, l := range lines {
		if strings.HasPrefix(l, "## ") {
			if in {
				break
			}
			in = strings.TrimSpace(l) == "## Report"
			continue
		}
		if in {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// literalSplit cuts a quoted error at the parts a printed message fills in:
// quotes, colons, printf verbs and digits.
var literalSplit = regexp.MustCompile(`["'` + "`" + `:]|%[a-zA-Z]|\d+`)

// minLiteral is the shortest literal part searched on its own.
const minLiteral = 12

// SearchTerms are the strings to look for in main's diffs for one quoted
// error: the quote itself and its longest literal part, so a quote of a
// printed message finds the format string that printed it.
func SearchTerms(quote string) []string {
	quote = strings.TrimSpace(quote)
	terms := []string{quote}
	longest := ""
	for _, part := range literalSplit.Split(quote, -1) {
		part = strings.TrimSpace(part)
		if len(part) > len(longest) {
			longest = part
		}
	}
	if len(longest) >= minLiteral && longest != quote {
		terms = append(terms, longest)
	}
	return terms
}

// pseudoSHA is the commit in a Go pseudo-version
// (v0.59.0-rc.1.0.20261031120000-0123456789ab).
var pseudoSHA = regexp.MustCompile(`\d{14}-([0-9a-f]{12})$`)

// BuildRef is the git revision a build's version names: a `git describe`
// stamp as is, the commit of a Go pseudo-version, without -dirty/+dirty.
// "" when the version names no commit (dev, unknown, empty, odd).
func BuildRef(version string) string {
	v := strings.TrimSpace(version)
	v = strings.TrimSuffix(v, "+dirty")
	v = strings.TrimSuffix(v, "-dirty")
	if v == "" || v == "dev" || v == "unknown" || v == "(devel)" || strings.ContainsAny(v, " :~^") || strings.HasPrefix(v, "-") {
		return ""
	}
	if m := pseudoSHA.FindStringSubmatch(v); m != nil {
		return m[1]
	}
	return v
}

// Commit is one commit on main that touched a quoted error.
type Commit struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
}

// maxFixedSince caps the commits listed per report.
const maxFixedSince = 5

// FixedSince lists main's commits since the build (version) whose diff adds
// or removes one of quotes' search terms, each term's newest first, at most 5. nil when
// the version names no commit git knows in root, or nothing matches. Git
// failures are silent: a report always files. A var so tests stub it.
var FixedSince = func(root, branch, version string, quotes []string) []Commit {
	ref := BuildRef(version)
	if ref == "" || root == "" || len(quotes) == 0 || strings.HasPrefix(branch, "-") {
		return nil
	}
	rng := ref + "^{commit}.." + branch
	if err := exec.Command("git", "-C", root, "rev-parse", "--verify", "--quiet", ref+"^{commit}").Run(); err != nil {
		return nil
	}
	var out []Commit
	seen := map[string]bool{}
	for _, q := range quotes {
		for _, term := range SearchTerms(q) {
			b, err := exec.Command("git", "-C", root, "log", "--format=%h%x09%s", "--max-count", "5", "-S"+term, rng, "--").Output()
			if err != nil {
				continue
			}
			for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
				sha, subject, ok := strings.Cut(line, "\t")
				if !ok || seen[sha] {
					continue
				}
				seen[sha] = true
				out = append(out, Commit{SHA: sha, Subject: subject})
			}
		}
	}
	if len(out) > maxFixedSince {
		out = out[:maxFixedSince]
	}
	return out
}

// MaybeFixedBlock is the markdown that lists commits in an issue body or a
// report: comment.
func MaybeFixedBlock(version, branch string, commits []Commit) string {
	if len(commits) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Maybe fixed since " + version + ": " + branch + " has commits since that build that touch the quoted error:\n")
	for _, c := range commits {
		b.WriteString("- " + c.SHA + " " + c.Subject + "\n")
	}
	return b.String()
}
