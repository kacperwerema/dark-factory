package redrounds

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/critique"
)

// Failure keys name what failed in one red result, so a later round can
// tell whether the same failure came back: test:<name> (a go test --- FAIL
// line or a CI failure name), criterion:<id> (an unproven acceptance
// criterion), finding:<path> <text> (a critic's blocks finding) and
// output:<text> (any other failure, its whole text normalized).
const (
	kindTest      = "test"
	kindCriterion = "criterion"
	kindFinding   = "finding"
	kindOutput    = "output"
)

var (
	goFailRe     = regexp.MustCompile(`--- FAIL: (\S+)`)
	unprovenRe   = regexp.MustCompile(`(?m)^  ([A-Z]+[0-9]+) `)
	shaRe        = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
	digitsRe     = regexp.MustCompile(`[0-9]+`)
	nonWordRe    = regexp.MustCompile(`[^\p{L}\p{N}]+`)
	unprovenHead = "acceptance criteria are not proven:"
)

// FailureKeys reads the failures out of a red check's message: the failing
// go tests when it names any, the unproven criteria of the proof check,
// else the message itself as one output key (shas and numbers masked, so
// timings, line numbers and heads don't make the same failure look new).
func FailureKeys(text string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(k string) {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for _, m := range goFailRe.FindAllStringSubmatch(text, -1) {
		add(key(kindTest, strings.TrimSuffix(m[1], `\n`)))
	}
	if len(out) > 0 {
		return out
	}
	if i := strings.Index(text, unprovenHead); i >= 0 {
		for _, m := range unprovenRe.FindAllStringSubmatch(text[i:], -1) {
			add(key(kindCriterion, m[1]))
		}
		if len(out) > 0 {
			return out
		}
	}
	if n := normalizeOutput(text); n != "" {
		add(key(kindOutput, n))
	}
	return out
}

// TestKeys is the keys of failing test names (local CI's failures:).
func TestKeys(names []string) []string {
	var out []string
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, key(kindTest, n))
		}
	}
	return out
}

// FindingKey is a critic finding's key: its path and its text with case,
// spacing and punctuation folded. The line is left out: the same finding
// moves when the design above it changes. The key shows the file's name
// and the text's start; a hash of the whole path and text tells keys apart.
func FindingKey(f critique.Finding) string {
	text := f.Text
	if f.Malformed || strings.TrimSpace(text) == "" {
		text = f.Raw
	}
	norm := strings.TrimSpace(nonWordRe.ReplaceAllString(strings.ToLower(text), " "))
	path := strings.TrimSpace(f.Path)
	shown := norm
	if len(shown) > findingShown {
		shown = strings.ToValidUTF8(shown[:findingShown], "") + "…"
	}
	if path != "" {
		shown = filepath.Base(path) + " — " + shown
	}
	sum := sha256.Sum256([]byte(path + "\x00" + norm))
	return fmt.Sprintf("%s:%s #%s", kindFinding, shown, hex.EncodeToString(sum[:4]))
}

// findingShown is how much of a finding's text its key shows.
const findingShown = 80

func normalizeOutput(s string) string {
	s = strings.ToLower(s)
	s = shaRe.ReplaceAllString(s, "#")
	s = digitsRe.ReplaceAllString(s, "#")
	return strings.Join(strings.Fields(s), " ")
}

// keyShown is how much of a key's text is kept as written; a longer text
// is cut there and a hash of the whole is added, so two keys are equal
// exactly when their whole texts are.
const keyShown = 100

func key(kind, text string) string {
	if len(text) <= keyShown {
		return kind + ":" + text
	}
	sum := sha256.Sum256([]byte(text))
	cut := strings.ToValidUTF8(text[:keyShown], "")
	return fmt.Sprintf("%s:%s… #%s", kind, cut, hex.EncodeToString(sum[:4]))
}

// describeKey is a key in words: "failing test TestFoo", "finding
// docs/x.md the design …".
func describeKey(k string) string {
	kind, text, _ := strings.Cut(k, ":")
	switch kind {
	case kindTest:
		return "failing test " + text
	case kindCriterion:
		return "unproven criterion " + text
	case kindFinding:
		return "finding: " + stripHash(text)
	default:
		return "failure: " + stripHash(text)
	}
}

// stripHash drops a key's " #<hash>" end.
func stripHash(s string) string {
	if i := strings.LastIndex(s, " #"); i >= 0 && len(s)-i == 10 {
		return s[:i]
	}
	return s
}

// resultKeys is a result's failure keys: the recorded ones, else read from
// its Why (records from before failures were kept).
func resultKeys(r Result) []string {
	if len(r.Failures) > 0 {
		return r.Failures
	}
	if strings.TrimSpace(r.Why) == "" {
		return nil
	}
	return FailureKeys(r.Why)
}
