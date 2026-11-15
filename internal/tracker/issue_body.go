package tracker

import (
	"fmt"

	"os"

	"sort"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/shell"
)

// sectionsOwning returns the sections of body that a block opening with h
// belongs to: same title at h's own level, or, with none there, at a
// shallower one. A deeper heading of that title is a different section (a
// "### Clarify" inside "## Design" is not the "## Clarify" section).
func sectionsOwning(body string, h headingMatch) []headingMatch {
	var own, shallower []headingMatch
	for _, m := range findHeadingMatches(body, h.Key) {
		switch {
		case m.Level == h.Level:
			own = append(own, m)
		case m.Level < h.Level:
			shallower = append(shallower, m)
		}
	}
	if len(own) > 0 {
		return own
	}
	return shallower
}

func AppendIssueBodyToSection(body, section, text string, force bool) (string, bool, error) {
	text = stripLeadingHeading(text, section)
	matches := findHeadingMatches(body, section)
	if len(matches) > 1 && !force {
		var labels []string
		for _, match := range matches {
			labels = append(labels, fmt.Sprintf("%s (line %d)", match.Line, match.StartLine+1))
		}
		return body, false, fmt.Errorf("multiple matching sections for %q: %s\n\nRerun with --force to merge into the first matching section", section, strings.Join(labels, ", "))
	}
	if len(matches) > 1 {
		nextBody, changed := appendContentToMatch(body, matches[0], text)
		return nextBody, changed, nil
	}
	if len(matches) == 1 {
		nextBody, changed := appendContentToMatch(body, matches[0], text)
		return nextBody, changed, nil
	}

	nextBody, changed := appendToSection(body, section, text)
	return nextBody, changed, nil
}

// stripLeadingHeading drops text's first non-blank line when it is a heading
// titled section, so `append --section X` with a body that opens with "## X"
// does not write the heading a second time.
func stripLeadingHeading(text, section string) string {
	trimmed := strings.TrimLeft(text, " \t\r\n")
	first, rest, _ := strings.Cut(trimmed, "\n")
	if _, title, ok := parseHeadingLine(first); ok && normalizeHeadingKey(title) == normalizeHeadingKey(section) {
		return rest
	}
	return text
}

// ReplaceIssueBodySection replaces the content of an existing section with text.
// The heading line is preserved; everything between it and the next heading of
// equal or shallower depth is replaced. Errors if no matching section exists,
// or if multiple sections match and force is false.
func ReplaceIssueBodySection(body, section, text string, force bool) (string, bool, error) {
	matches := findHeadingMatches(body, section)
	if len(matches) == 0 {
		return body, false, fmt.Errorf("no section matching %q in issue body\n\nUse issue-cli append <slug> --section %s to create a new section", section, shell.Quote(section))
	}
	if len(matches) > 1 && !force {
		var labels []string
		for _, match := range matches {
			labels = append(labels, fmt.Sprintf("%s (line %d)", match.Line, match.StartLine+1))
		}
		return body, false, fmt.Errorf("multiple matching sections for %q: %s\n\nRerun with --force to replace the first matching section", section, strings.Join(labels, ", "))
	}
	nextBody, changed := replaceContentInMatch(body, matches[0], text)
	return nextBody, changed, nil
}

// UpdateIssueBody applies a body transformation while holding the issue lock.
// It reloads the current body under the lock so callers do not overwrite newer edits
// based on a stale pre-lock snapshot.
func UpdateIssueBody(filePath string, update func(body string) (string, bool, error)) (string, bool, error) {
	return UpdateIssueBodyWith(filePath, func(body string, comments []Comment) (string, []Comment, bool, error) {
		next, ok, err := update(body)
		return next, comments, ok, err
	})
}

// UpdateIssueBodyWith is UpdateIssueBody whose update also sees and returns
// the issue's comments, so a body change and the comment that records it
// land in one atomic write, or neither does.
func UpdateIssueBodyWith(filePath string, update func(body string, comments []Comment) (string, []Comment, bool, error)) (string, bool, error) {
	var updatedBody string
	var changed bool

	err := withIssueLock(filePath, func() error {
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

		fmRaw := parts[0]
		bodyWithComments := parts[1]
		body, existingComments := ParseComments(bodyWithComments)
		body = strings.TrimSpace(body)

		nextBody, nextComments, ok, err := update(body, existingComments)
		if err != nil {
			return err
		}
		if !ok {
			updatedBody = body
			return nil
		}

		updatedBody = nextBody
		changed = true

		var out strings.Builder
		out.WriteString("---")
		out.WriteString(fmRaw)
		out.WriteString("\n---")
		out.WriteString("\n")
		out.WriteString(nextBody)
		out.WriteString("\n")
		out.WriteString(SerializeComments(nextComments))

		info, err := os.Stat(filePath)
		if err != nil {
			return fmt.Errorf("stat %s: %w", filePath, err)
		}
		return writeFileAtomically(filePath, []byte(out.String()), info.Mode().Perm())
	})

	return updatedBody, changed, err
}

func updateIssueFrontmatterLocked(filePath string, update IssueUpdate) error {
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

	fmRaw := parts[0]
	body := parts[1]

	// set writes key, or removes it when value is "" and clearable; touched
	// names the keys for the error if the result doesn't parse.
	var touched []string
	set := func(key, value string, clearable bool) {
		touched = append(touched, key)
		if value == "" && clearable {
			fmRaw = removeFrontmatterKey(fmRaw, key)
		} else {
			fmRaw = setFrontmatterScalar(fmRaw, key, value)
		}
	}

	if update.Title != nil {
		set("title", *update.Title, false)
	}
	if update.Status != nil {
		set("status", *update.Status, false)
	}
	if update.Priority != nil {
		set("priority", *update.Priority, true)
	}
	if update.Version != nil {
		set("version", *update.Version, true)
	}
	if update.Assignee != nil {
		set("assignee", *update.Assignee, true)
	}
	if update.HumanApproval != nil {
		fmRaw = removeFrontmatterKey(fmRaw, "approved_for")
		set("human_approval", *update.HumanApproval, true)
	}
	if update.QueuedApproval != nil {
		set("queued_approval", *update.QueuedApproval, true)
	}
	if update.StartedAt != nil {
		set("started_at", *update.StartedAt, true)
	}
	if update.DoneAt != nil {
		set("done_at", *update.DoneAt, true)
	}
	if update.Labels != nil {
		touched = append(touched, "labels")
		if len(update.Labels) == 0 {
			fmRaw = removeFrontmatterKey(fmRaw, "labels")
		} else {
			fmRaw = setFrontmatterList(fmRaw, "labels", update.Labels)
		}
	}
	if update.Slug != nil {
		set("slug", *update.Slug, true)
	}

	if len(update.ExtraFields) > 0 {
		keys := make([]string, 0, len(update.ExtraFields))
		for k := range update.ExtraFields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if ProtectedFrontmatterFields[k] || !frontmatterKeyRe.MatchString(k) {
				continue
			}
			set(k, update.ExtraFields[k], true)
		}
	}

	if err := checkFrontmatter(fmRaw, touched); err != nil {
		return fmt.Errorf("%s: %w", filePath, err)
	}

	if update.Body != nil {
		_, existingComments := ParseComments(body)
		body = "\n" + *update.Body + "\n" + SerializeComments(existingComments)
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
}
