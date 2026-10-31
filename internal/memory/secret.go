package memory

import (
	"math"
	"regexp"
	"strings"
)

// secretRule is one kind of secret a memory may not hold.
type secretRule struct {
	kind string
	re   *regexp.Regexp
	// ok, when set, lets a match through: a reference (env:VAR) is not a
	// secret.
	ok func(match []string) bool
}

var secretRules = []secretRule{
	{kind: "private key", re: regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY`)},
	{kind: "API key (sk-…)", re: regexp.MustCompile(`\b(?:sk|rk|pk)-[A-Za-z0-9_-]{20,}`)},
	{kind: "GitHub token", re: regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{20,})`)},
	{kind: "Slack token", re: regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`)},
	{kind: "AWS access key", re: regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{kind: "Google API key", re: regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`)},
	{kind: "JWT", re: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)},
	{kind: "bearer token", re: regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{16,}`)},
	{kind: "password in a URL", re: regexp.MustCompile(`://[^/\s:@]+:[^/\s@]+@`)},
	{
		kind: "password or token value",
		re:   regexp.MustCompile(`(?i)\b(?:password|passwd|pwd|passphrase|secret|token|api[_-]?key|access[_-]?key|client[_-]?secret|auth)\s*[:=]\s*["']?([^\s"',;]{4,})`),
		ok:   func(m []string) bool { return isReference(m[1]) },
	},
}

// isReference is a value that names a secret instead of holding it:
// env:VAR, keychain:service (Settled rule "Secrets are references"), a
// placeholder (<token>, ***, $VAR, ${VAR}).
func isReference(v string) bool {
	low := strings.ToLower(v)
	switch {
	case strings.HasPrefix(low, "env:"), strings.HasPrefix(low, "keychain:"):
		return true
	case strings.HasPrefix(v, "<") || strings.HasPrefix(v, "$") || strings.HasPrefix(v, "{{"):
		return true
	case strings.Trim(v, "*x.") == "":
		return true
	}
	return false
}

// tokenRe splits text into candidate opaque tokens for the entropy check.
var tokenRe = regexp.MustCompile(`[A-Za-z0-9+=_\-]{32,}`)

// FindSecret names the kind of secret text seems to hold, or "" for none.
// It errs on the side of refusing: a memory is one fact in words, so a long
// random-looking string has no place in it anyway. It never returns the
// matched text.
func FindSecret(text string) string {
	for _, r := range secretRules {
		for _, m := range r.re.FindAllStringSubmatch(text, -1) {
			if r.ok != nil && r.ok(m) {
				continue
			}
			return r.kind
		}
	}
	for _, tok := range tokenRe.FindAllString(text, -1) {
		if looksRandom(tok) {
			return "long random-looking string (a key or token?)"
		}
	}
	return ""
}

// looksRandom: mixed upper case, lower case and digits with high entropy.
// Pure hex (commit SHAs) and slugs (lower case and dashes) pass.
func looksRandom(tok string) bool {
	var upper, lower, digit bool
	for _, c := range tok {
		switch {
		case c >= 'A' && c <= 'Z':
			upper = true
		case c >= 'a' && c <= 'z':
			lower = true
		case c >= '0' && c <= '9':
			digit = true
		}
	}
	if !(upper && lower && digit) {
		return false
	}
	return entropy(tok) >= 4.0
}

// entropy is the Shannon entropy of s in bits per character.
func entropy(s string) float64 {
	counts := map[rune]int{}
	n := 0
	for _, c := range s {
		counts[c]++
		n++
	}
	var h float64
	for _, k := range counts {
		p := float64(k) / float64(n)
		h -= p * math.Log2(p)
	}
	return h
}
