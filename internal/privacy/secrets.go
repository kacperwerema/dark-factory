package privacy

// The secret scan: a key that would go into a repo. Two kinds of hit:
//
//   - a line holding a known key shape (secretKinds): ElevenLabs, Stripe
//     live, GitHub, AWS, Slack, OpenAI/Anthropic, a PEM private key, a JWT;
//   - a file named .env or .env.* (envFile), except the example names
//     (.env.example, .env.sample, .env.template), whose lines are checked
//     like any other file's.
//
// A hit names the file, the line and the kind, never the value. A match
// that looks like a placeholder (few distinct characters, or EXAMPLE in
// it) passes, so docs and examples can show a key's shape.
//
// Four places run it: make leak-check over the tracked tree
// (TestTrackedFilesCarryNoSecrets), the pre-push guard over what a push
// sends (PushSecrets, every project with the hook), the commit-msg hook
// over the staged diff (ScanStagedSecrets), and release publish over the
// release branch's tree (Publish). Keys live in Keychain or
// in a vendor CLI's own file in the home directory: docs/departments.md,
// Secrets.

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// secretKind is one key shape.
type secretKind struct {
	what   string // shown in a hit: the kind and its prefix, never a value
	re     *regexp.Regexp
	prefix int // bytes of the match that are the fixed prefix, left out of the placeholder test
}

var secretKinds = []secretKind{
	{"an ElevenLabs key (sk_…)", regexp.MustCompile(`\bsk_[A-Za-z0-9]{32,}`), 3},
	{"a Stripe live key (sk_live_… / rk_live_…)", regexp.MustCompile(`\b[sr]k_live_[A-Za-z0-9]{16,}`), 8},
	{"a GitHub token (ghp_…)", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}`), 4},
	{"a GitHub token (github_pat_…)", regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{40,}`), 11},
	{"an AWS access key (AKIA…)", regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`), 4},
	{"a Slack token (xox…)", regexp.MustCompile(`\bxox[abprs]-[0-9]{6,}-[A-Za-z0-9-]{8,}`), 5},
	{"an OpenAI or Anthropic key (sk-proj-… / sk-ant-…)", regexp.MustCompile(`\bsk-(?:proj|ant|svcacct)-[A-Za-z0-9_-]{20,}`), 7},
	{"a private key block (-----BEGIN … PRIVATE KEY-----)", regexp.MustCompile(`-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY(?: BLOCK)?-----`), -1},
	{"a JWT (eyJ….eyJ….…)", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`), 3},
}

// secretPrefilter is git grep's ERE: every line a kind above could hit.
const secretPrefilter = `sk_|k_live_|gh[pousr]_|github_pat_|AKIA|ASIA|xox[abprs]-|sk-(proj|ant|svcacct)-|PRIVATE KEY|eyJ`

// placeholder reports whether a key-shaped value is a stand-in: fewer than
// 8 distinct characters after its prefix (sk_xxxx…, AKIA0000…) or the word
// EXAMPLE (AWS's documented AKIAIOSFODNN7EXAMPLE).
func placeholder(value string, prefix int) bool {
	if prefix < 0 {
		return false // a PEM header is the hit itself
	}
	if strings.Contains(strings.ToUpper(value), "EXAMPLE") {
		return true
	}
	seen := map[rune]bool{}
	for _, r := range value[prefix:] {
		if r != '_' && r != '-' && r != '.' {
			seen[r] = true
		}
	}
	return len(seen) < 8
}

// SecretsInText lists the kinds of key one line holds, each once.
func SecretsInText(text string) []string {
	var out []string
	for _, k := range secretKinds {
		for _, m := range k.re.FindAllString(text, -1) {
			if !placeholder(m, k.prefix) {
				out = append(out, k.what)
				break
			}
		}
	}
	return out
}

// exampleEnvSuffixes are the .env.<suffix> names a repo may track: a
// template with the variable names and empty values.
var exampleEnvSuffixes = map[string]bool{"example": true, "sample": true, "template": true}

// EnvFile reports whether a repo path is an env file that must stay
// untracked: base name .env or .env.<anything> but the example names.
func EnvFile(p string) bool {
	base := path.Base(p)
	if base == ".env" {
		return true
	}
	if !strings.HasPrefix(base, ".env.") {
		return false
	}
	return !exampleEnvSuffixes[strings.ToLower(strings.TrimPrefix(base, ".env."))]
}

const envFileWhat = "an env file (.env / .env.*): keep it untracked (.gitignore), commit .env.example with empty values"

// ScanSecrets scans the tracked files of the repo at dir (rev "") or the
// tree at rev: key-shaped lines and env files. Binary files are skipped.
func ScanSecrets(dir, rev string) ([]Leak, error) {
	lines, err := grepLines(dir, rev, []string{"-E", "-e", secretPrefilter})
	if err != nil {
		return nil, err
	}
	var leaks []Leak
	for _, l := range lines {
		for _, what := range SecretsInText(l.text) {
			leaks = append(leaks, Leak{Path: l.path, Line: l.line, What: what})
		}
	}
	var out string
	if rev == "" {
		out, err = git(dir, "-c", "core.quotePath=false", "ls-files")
	} else {
		out, err = git(dir, "-c", "core.quotePath=false", "ls-tree", "-r", "--name-only", rev)
	}
	if err != nil {
		return nil, err
	}
	for _, p := range lines0(out) {
		if EnvFile(p) {
			leaks = append(leaks, Leak{Path: p, Line: 1, What: envFileWhat})
		}
	}
	sortLeaks(leaks)
	return leaks, nil
}

func lines0(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func sortLeaks(leaks []Leak) {
	sort.SliceStable(leaks, func(i, j int) bool {
		if leaks[i].Path != leaks[j].Path {
			return leaks[i].Path < leaks[j].Path
		}
		return leaks[i].Line < leaks[j].Line
	})
}

// diffSecrets runs the key patterns over the lines a unified diff (-U0)
// adds, and the env-file rule over the files it adds or changes.
func diffSecrets(diff string, changed []string) []Leak {
	var leaks []Leak
	addedLines(diff, func(p string, line int, text string) {
		for _, what := range SecretsInText(text) {
			leaks = append(leaks, Leak{Path: p, Line: line, What: what})
		}
	})
	for _, p := range changed {
		if EnvFile(p) {
			leaks = append(leaks, Leak{Path: p, Line: 1, What: envFileWhat})
		}
	}
	return leaks
}

// ScanStagedSecrets is the secret scan over what the commit being made
// adds: the staged diff's added lines and its added or changed files.
func ScanStagedSecrets(root string) ([]Leak, error) {
	diff, err := git(root, "-c", "core.quotePath=false", "diff", "--cached", "-U0", "--no-color", "--no-ext-diff", "--no-renames", "--diff-filter=d")
	if err != nil {
		return nil, err
	}
	names, err := git(root, "-c", "core.quotePath=false", "diff", "--cached", "--name-only", "--no-renames", "--diff-filter=AM")
	if err != nil {
		return nil, err
	}
	leaks := diffSecrets(diff, lines0(names))
	sortLeaks(leaks)
	return leaks, nil
}

// PushSecrets is the secret scan over what a push sends: each commit the
// remote lacks, its added lines and its added or changed files. A file the
// push doesn't touch is not read, so an old one never blocks a push
// (make leak-check covers the whole tree). When the guard can't tell what
// the remote has (a first push to an empty or unrelated remote: no old
// tip, no tracking refs, no ls-remote tip this repo holds), it scans the
// pushed tip's tree instead, as make leak-check does, rather than every
// commit since the root: a stand-in an old commit held and the tree no
// longer does is not refused. Hits are deduplicated.
func PushSecrets(root, remote string, refs []PushRef) ([]Leak, error) {
	seen := map[string]bool{}
	var leaks []Leak
	add := func(ls []Leak) {
		for _, l := range ls {
			if k := l.String(); !seen[k] {
				seen[k] = true
				leaks = append(leaks, l)
			}
		}
	}
	base := remoteBase(root, remote)
	for _, ref := range refs {
		commits, known, err := pushedCommits(root, base, ref)
		if err != nil {
			return nil, err
		}
		if !known && len(commits) > 0 {
			tree, err := ScanSecrets(root, ref.LocalSHA)
			if err != nil {
				return nil, err
			}
			add(tree)
			continue
		}
		for _, c := range commits {
			diff, err := git(root, "-c", "core.quotePath=false", "diff-tree", "-p", "-U0", "-r", "--root", "--no-commit-id", "--no-color", "--no-ext-diff", "--no-renames", "--diff-filter=d", c)
			if err != nil {
				return nil, err
			}
			names, err := git(root, "-c", "core.quotePath=false", "diff-tree", "--name-only", "-r", "--root", "--no-commit-id", "--no-renames", "--diff-filter=AM", c)
			if err != nil {
				return nil, err
			}
			add(diffSecrets(diff, lines0(names)))
		}
	}
	sortLeaks(leaks)
	return leaks, nil
}

// SecretRefusal is the refusal for secret hits: each file:line and kind
// (10 at most), never a value, then what to do. what says who refuses
// ("secret guard: this push", "commit refused").
func SecretRefusal(what string, leaks []Leak) string {
	if len(leaks) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %d secret(s) would go into the repo:", what, len(leaks))
	for i, l := range leaks {
		if i == 10 {
			fmt.Fprintf(&b, "\n  … and %d more", len(leaks)-10)
			break
		}
		b.WriteString("\n  " + l.String())
	}
	b.WriteString("\nA key never goes in a file in a repo: keep it in Keychain (keychain:<service> in a connector) or in the vendor CLI's own file in your home directory (docs/departments.md#secrets). Remove it from the commit (git rm --cached <file>, git commit --amend, or rebase for an older commit), add .env and .env.* to .gitignore, and rotate any key that was ever pushed.")
	return b.String()
}
