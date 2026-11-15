package tracker

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"os"
	"path/filepath"
	"regexp"

	"strings"

	"time"

	"gopkg.in/yaml.v3"
)

// BodyHash fingerprints an issue body (as in Issue.BodyRaw) so a writer
// holding an older copy can be refused.
func BodyHash(body string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(body)))
	return hex.EncodeToString(sum[:8])
}

// ErrStaleBody marks a body write refused because the file's body is no
// longer the one the writer started from.
var ErrStaleBody = errors.New("issue body changed since it was read")

// UpdateIssueFrontmatterIfBody is UpdateIssueFrontmatter that first checks,
// under the issue lock, that the file's body still hashes to baseHash
// (ErrStaleBody otherwise). It returns the hash of the body as written.
func UpdateIssueFrontmatterIfBody(filePath, baseHash string, update IssueUpdate) (string, error) {
	var written string
	err := withIssueLock(filePath, func() error {
		current, err := readIssueBodyHash(filePath)
		if err != nil {
			return err
		}
		if current != baseHash {
			return ErrStaleBody
		}
		if err := updateIssueFrontmatterLocked(filePath, update); err != nil {
			return err
		}
		written, err = readIssueBodyHash(filePath)
		return err
	})
	return written, err
}

func readIssueBodyHash(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", filePath, err)
	}
	issue, err := ParseIssue(filepath.Base(filePath), data)
	if err != nil {
		return "", err
	}
	return BodyHash(issue.BodyRaw), nil
}

func UpdateIssueFrontmatter(filePath string, update IssueUpdate) error {
	return withIssueLock(filePath, func() error {
		return updateIssueFrontmatterLocked(filePath, update)
	})
}

// SetIssueType writes the issue's type: and, when status is non-empty, moves
// it to that status in the same locked write. Callers decide the status with
// WorkflowConfig.RetypeStatus first.
func SetIssueType(filePath, typ, status string) error {
	update := IssueUpdate{ExtraFields: map[string]string{"type": typ}}
	if status != "" {
		update.Status = &status
	}
	return UpdateIssueFrontmatter(filePath, update)
}

// RewriteIssueFile replaces the issue file with the given bytes after
// validating that they parse as a valid issue. The lock is held for the
// duration of validation + write, and the write is atomic — on any failure
// the file on disk is unchanged.
func RewriteIssueFile(filePath string, content []byte) error {
	if _, err := ParseIssue(filepath.Base(filePath), content); err != nil {
		return err
	}
	return withIssueLock(filePath, func() error {
		info, err := os.Stat(filePath)
		if err != nil {
			return fmt.Errorf("stat %s: %w", filePath, err)
		}
		return writeFileAtomically(filePath, content, info.Mode().Perm())
	})
}

// ProtectedFrontmatterFields lists keys that SetFrontmatterField refuses to touch
// because they are managed elsewhere (workflow transitions, claim/start, GitHub sync).
var ProtectedFrontmatterFields = map[string]bool{
	"title":           true,
	"status":          true,
	"human_approval":  true,
	"approved_for":    true,
	"queued_approval": true,
	"started_at":      true,
	"done_at":         true,
	"number":          true,
	"repo":            true,
	"created":         true,
	"labels":          true,
}

var frontmatterKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// yamlScalarEscaper keeps a double-quoted scalar on one line: the frontmatter
// writers edit line by line, so a raw newline would split the value.
var yamlScalarEscaper = strings.NewReplacer(
	`\`, `\\`,
	`"`, `\"`,
	"\n", `\n`,
	"\r", `\r`,
	"\t", `\t`,
)

func escapeYAMLScalar(s string) string {
	return yamlScalarEscaper.Replace(s)
}

// setFrontmatterScalar is the one writer for scalar frontmatter keys. The
// value is written as one escaped, double-quoted line in place of the key's
// old value, whatever shape that had.
func setFrontmatterScalar(fm, key, value string) string {
	return replaceFrontmatterKey(fm, key, []string{key + `: "` + escapeYAMLScalar(value) + `"`})
}

// setFrontmatterList writes key as a YAML block list of quoted values.
func setFrontmatterList(fm, key string, values []string) string {
	lines := []string{key + ":"}
	for _, v := range values {
		lines = append(lines, `  - "`+escapeYAMLScalar(v)+`"`)
	}
	return replaceFrontmatterKey(fm, key, lines)
}

func removeFrontmatterKey(fm, key string) string {
	return replaceFrontmatterKey(fm, key, nil)
}

// replaceFrontmatterKey puts lines where key's first occurrence was and drops
// every occurrence, continuation lines included. A missing key is appended,
// or left missing when lines is nil.
func replaceFrontmatterKey(fm, key string, lines []string) string {
	fmLines := strings.Split(fm, "\n")
	spans := frontmatterKeySpans(fmLines, key)
	if len(spans) == 0 {
		if lines == nil {
			return fm
		}
		// Keep the frontmatter's own ending: a newline added here would
		// show as a blank line before the closing ---.
		trimmed := strings.TrimRight(fm, "\n")
		return trimmed + "\n" + strings.Join(lines, "\n") + fm[len(trimmed):]
	}
	var out []string
	prev := 0
	for i, sp := range spans {
		out = append(out, fmLines[prev:sp[0]]...)
		if i == 0 {
			out = append(out, lines...)
		}
		prev = sp[1]
	}
	out = append(out, fmLines[prev:]...)
	return strings.Join(out, "\n")
}

// frontmatterKeySpans returns the [start, end) line ranges of key at the top
// level: its own line plus the lines that continue its value (indented list
// items, block scalars and nested maps, `- ` items at column 0, blank lines
// inside a block), up to the next top-level line. Blank lines at the end of
// a span stay outside it.
func frontmatterKeySpans(lines []string, key string) [][2]int {
	var spans [][2]int
	for i := 0; i < len(lines); i++ {
		rest, ok := strings.CutPrefix(lines[i], key+":")
		if !ok || (rest != "" && rest[0] != ' ' && rest[0] != '\t') {
			continue
		}
		end := i + 1
		for j := i + 1; j < len(lines); j++ {
			l := lines[j]
			if strings.TrimSpace(l) == "" {
				continue
			}
			if l[0] != ' ' && l[0] != '\t' && l != "-" && !strings.HasPrefix(l, "- ") {
				break
			}
			end = j + 1
		}
		spans = append(spans, [2]int{i, end})
		i = end - 1
	}
	return spans
}

// checkFrontmatter refuses frontmatter the YAML parser can't read, so a line
// edit that went wrong never reaches disk. keys names what the edit changed.
func checkFrontmatter(fm string, keys []string) error {
	var probe Issue
	if err := yaml.Unmarshal([]byte(fm), &probe); err != nil {
		if len(keys) == 0 {
			return fmt.Errorf("not written: frontmatter would not parse: %w", err)
		}
		return fmt.Errorf("not written: frontmatter would not parse after setting %s: %w", strings.Join(keys, ", "), err)
	}
	return nil
}

// CheckIssueFrontmatter is checkFrontmatter for a whole issue file that a
// writer built by hand; call it before writing the file.
func CheckIssueFrontmatter(content string) error {
	if !strings.HasPrefix(content, "---") {
		return fmt.Errorf("not written: missing frontmatter delimiter")
	}
	fm, _, ok := strings.Cut(content[3:], "\n---")
	if !ok {
		return fmt.Errorf("not written: missing closing frontmatter delimiter")
	}
	return checkFrontmatter(fm, nil)
}

// checkSettableKey refuses keys set-meta may not write: malformed ones and
// those managed elsewhere (see ProtectedFrontmatterFields).
func checkSettableKey(key string) error {
	if key == "" {
		return fmt.Errorf("key is required")
	}
	if !frontmatterKeyRe.MatchString(key) {
		return fmt.Errorf("invalid key %q: must start with a lowercase letter and contain only lowercase letters, digits, or underscores", key)
	}
	if ProtectedFrontmatterFields[key] {
		return fmt.Errorf("field %q is managed by the workflow and cannot be set via set-meta", key)
	}
	return nil
}

// SetFrontmatterField writes or clears a single scalar frontmatter key.
// Protected keys (see ProtectedFrontmatterFields) are refused. Pass clear=true
// to remove the field; value is ignored in that case.
func SetFrontmatterField(filePath, key, value string, clear bool) error {
	if err := checkSettableKey(key); err != nil {
		return err
	}
	return editFrontmatterKey(filePath, key, func(fm string) string {
		if clear {
			return removeFrontmatterKey(fm, key)
		}
		return setFrontmatterScalar(fm, key, value)
	})
}

// SetFrontmatterList writes key as a YAML list of values, replacing whatever
// it held. Protected keys are refused as in SetFrontmatterField.
func SetFrontmatterList(filePath, key string, values []string) error {
	if err := checkSettableKey(key); err != nil {
		return err
	}
	return editFrontmatterKey(filePath, key, func(fm string) string {
		return setFrontmatterList(fm, key, values)
	})
}

// createdKeyRe finds a created: line in raw frontmatter.
var createdKeyRe = regexp.MustCompile(`(?m)^created:`)

// SetCreated dates an undated issue: created is protected, but an issue
// without one may get it once (set-meta, the backfill). An existing date is
// never changed.
func SetCreated(filePath, date string) error {
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return fmt.Errorf("created must be a YYYY-MM-DD date, got %q", date)
	}
	return editFrontmatterKeyErr(filePath, "created", func(fm string) (string, error) {
		if createdKeyRe.MatchString(fm) {
			return "", fmt.Errorf("%s already has created:; an issue's date is never changed", filePath)
		}
		return setFrontmatterScalar(fm, "created", date), nil
	})
}

func editFrontmatterKey(filePath, key string, edit func(fm string) string) error {
	return editFrontmatterKeyErr(filePath, key, func(fm string) (string, error) { return edit(fm), nil })
}

func editFrontmatterKeyErr(filePath, key string, edit func(fm string) (string, error)) error {
	return withIssueLock(filePath, func() error {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("reading %s: %w", filePath, err)
		}

		content := string(data)
		if !strings.HasPrefix(content, "---") {
			return fmt.Errorf("no frontmatter in %s", filePath)
		}

		parts := strings.SplitN(content[3:], "\n---", 2)
		if len(parts) < 2 {
			return fmt.Errorf("invalid frontmatter in %s", filePath)
		}

		fmRaw, err := edit(parts[0])
		if err != nil {
			return err
		}
		body := parts[1]
		if err := checkFrontmatter(fmRaw, []string{key}); err != nil {
			return fmt.Errorf("%s: %w", filePath, err)
		}

		var out strings.Builder
		out.WriteString("---")
		out.WriteString(fmRaw)
		out.WriteString("\n---")
		out.WriteString(body)

		info, err := os.Stat(filePath)
		if err != nil {
			return fmt.Errorf("stat %s: %w", filePath, err)
		}
		return writeFileAtomically(filePath, []byte(out.String()), info.Mode().Perm())
	})
}

// DeleteIssue removes the issue markdown file and its comment sidecar if present.
func DeleteIssue(filePath string) error {
	if err := os.Remove(filePath); err != nil {
		return fmt.Errorf("deleting %s: %w", filePath, err)
	}
	// Also remove comment sidecar if it exists
	commentFile := strings.TrimSuffix(filePath, ".md") + ".comments.yaml"
	os.Remove(commentFile) // ignore error — may not exist
	return nil
}
