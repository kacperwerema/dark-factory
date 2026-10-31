package tracker

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

const commentMarkerStart = "<!-- issue-viewer-comments"
const commentMarkerEnd = "-->"

type Comment struct {
	ID    int `json:"id"`
	Block int `json:"block"`
	// Date is when the comment was made: RFC3339 since v0.54, the day
	// ("2006-01-02") before. Read it through CommentDay / CommentTime.
	Date   string `json:"date"`
	Text   string `json:"text"`
	Status string `json:"status"`
	Source string `json:"source"`
	// Author is who wrote it: a session name (lead-<project>, agent-<slug>,
	// a department lead) or "owner" from the viewer. Empty on comments from
	// before v0.57 and from a shell with no session; read it through
	// CommentAuthor.
	Author string `json:"author,omitempty"`
	// Edits are the comment's earlier versions, oldest first: each the
	// text it had and when that text was replaced. EditComment adds one
	// per edit; Text is always the current version and Date stays when the
	// comment was first made.
	Edits []CommentEdit `json:"edits,omitempty"`
}

// CommentEdit is one earlier version of a comment.
type CommentEdit struct {
	Date string `json:"date"`
	Text string `json:"text"`
}

// LatestCommentBy is the newest comment author wrote (ok false when none).
func LatestCommentBy(comments []Comment, author string) (Comment, bool) {
	if author == "" {
		return Comment{}, false
	}
	for i := len(comments) - 1; i >= 0; i-- {
		if CommentAuthor(comments[i]) == author {
			return comments[i], true
		}
	}
	return Comment{}, false
}

// EditComment replaces comment id's text, keeping the old text in its Edits
// dated now. check runs under the issue lock on the comments as they are
// and may refuse the edit.
func EditComment(filePath string, id int, text string, now time.Time, check func(comments []Comment, c Comment) error) (Comment, error) {
	var edited Comment
	err := withIssueLock(filePath, func() error {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("reading %s: %w", filePath, err)
		}
		_, comments := ParseComments(string(data))
		for i := range comments {
			if comments[i].ID != id {
				continue
			}
			if check != nil {
				if err := check(comments, comments[i]); err != nil {
					return err
				}
			}
			c := &comments[i]
			c.Edits = append(c.Edits, CommentEdit{Date: now.Format(time.RFC3339), Text: c.Text})
			c.Text = text
			edited = *c
			return saveCommentsLocked(filePath, comments)
		}
		return fmt.Errorf("no comment #%d", id)
	})
	return edited, err
}

// OwnerAuthor is the author of every comment the viewer writes: the owner
// at the browser.
const OwnerAuthor = "owner"

// recordedBy is the suffix approval: and dispatch: comments name their
// recorder with.
const recordedBy = " — recorded by "

// CommentAuthor is who wrote c: its author, else the name an older comment
// gives after " — recorded by ", else "".
func CommentAuthor(c Comment) string {
	if c.Author != "" {
		return c.Author
	}
	if i := strings.LastIndex(c.Text, recordedBy); i >= 0 {
		if name := strings.TrimSpace(c.Text[i+len(recordedBy):]); name != "" && !strings.ContainsAny(name, " \n") {
			return name
		}
	}
	return ""
}

// CommentDay is the "2006-01-02" day of a comment date in either stored
// form; other values come back unchanged.
func CommentDay(date string) string {
	date = strings.TrimSpace(date)
	if len(date) > 10 && date[10] == 'T' {
		return date[:10]
	}
	return date
}

// CommentTime parses a comment date in either stored form. A day-only date
// is midnight local time; ok is false when date is neither.
func CommentTime(date string) (time.Time, bool) {
	date = strings.TrimSpace(date)
	if t, err := time.Parse(time.RFC3339, date); err == nil {
		return t, true
	}
	if t, err := time.ParseInLocation("2006-01-02", date, time.Local); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// ParseComments extracts comments from the bottom of a markdown file.
func ParseComments(content string) (body string, comments []Comment) {
	idx := strings.LastIndex(content, commentMarkerStart)
	if idx == -1 {
		return content, nil
	}

	body = strings.TrimRight(content[:idx], "\n")

	block := content[idx+len(commentMarkerStart):]
	endIdx := strings.Index(block, commentMarkerEnd)
	if endIdx == -1 {
		return body, nil
	}

	block = strings.TrimSpace(block[:endIdx])
	scanner := bufio.NewScanner(strings.NewReader(block))
	nextID := 1
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var c Comment
		if err := json.Unmarshal([]byte(line), &c); err == nil {
			if c.Status == "" {
				c.Status = "open"
			}
			if c.ID == 0 {
				c.ID = nextID
			}
			if c.ID >= nextID {
				nextID = c.ID + 1
			}
			comments = append(comments, c)
		}
	}

	return body, comments
}

// SerializeComments renders comments as an HTML comment block.
func SerializeComments(comments []Comment) string {
	if len(comments) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n\n")
	b.WriteString(commentMarkerStart)
	b.WriteString("\n")
	for _, c := range comments {
		data, _ := json.Marshal(c)
		b.Write(data)
		b.WriteString("\n")
	}
	b.WriteString(commentMarkerEnd)
	return b.String()
}

func SaveComments(filePath string, comments []Comment) error {
	return withIssueLock(filePath, func() error {
		return saveCommentsLocked(filePath, comments)
	})
}

func saveCommentsLocked(filePath string, comments []Comment) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", filePath, err)
	}

	body, _ := ParseComments(string(data))
	result := body + SerializeComments(comments)
	info, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("stat %s: %w", filePath, err)
	}
	return writeFileAtomically(filePath, []byte(result), info.Mode().Perm())
}

func NextCommentID(comments []Comment) int {
	max := 0
	for _, c := range comments {
		if c.ID > max {
			max = c.ID
		}
	}
	return max + 1
}

func AddComment(filePath string, block int, text, source string) error {
	return AddCommentBy(filePath, block, text, source, "")
}

// AddCommentBy is AddComment signed by author ("" for none).
func AddCommentBy(filePath string, block int, text, source, author string) error {
	return withIssueLock(filePath, func() error {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("reading %s: %w", filePath, err)
		}

		_, existing := ParseComments(string(data))
		c := Comment{
			ID:     NextCommentID(existing),
			Block:  block,
			Date:   time.Now().Format(time.RFC3339),
			Text:   text,
			Status: "open",
			Source: source,
			Author: author,
		}
		existing = append(existing, c)
		return saveCommentsLocked(filePath, existing)
	})
}

func ToggleComment(filePath string, commentID int) error {
	return withIssueLock(filePath, func() error {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("reading %s: %w", filePath, err)
		}

		_, comments := ParseComments(string(data))
		for i := range comments {
			if comments[i].ID == commentID {
				if comments[i].Status == "done" {
					comments[i].Status = "open"
				} else {
					comments[i].Status = "done"
				}
				break
			}
		}

		return saveCommentsLocked(filePath, comments)
	})
}

func DeleteComment(filePath string, commentID int) error {
	return withIssueLock(filePath, func() error {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("reading %s: %w", filePath, err)
		}

		_, comments := ParseComments(string(data))
		var filtered []Comment
		for _, c := range comments {
			if c.ID != commentID {
				filtered = append(filtered, c)
			}
		}

		return saveCommentsLocked(filePath, filtered)
	})
}

func LoadComments(filePath string) ([]Comment, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", filePath, err)
	}

	_, comments := ParseComments(string(data))
	return comments, nil
}
