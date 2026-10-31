package outputs

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// Comments on an output: the owner's notes on a whole document, a heading or
// a passage, Google-Docs-style, with replies and resolve. They live beside
// the outputs log in <issues>/.outputs/comments.jsonl, never in the output's
// file. The reader (shelf_read.go) writes them for the owner; agents read
// them with issue-cli output comments and answer with output reply.

// Comment ops.
const (
	OpComment = "comment"
	OpReply   = "reply"
	OpResolve = "resolve"
	OpReopen  = "reopen"
)

// Limits on what a comment carries.
const (
	MaxQuoteRunes   = 500
	MaxCommentRunes = 4000
	anchorContext   = 30
)

// Where a thread sits in the version shown (Locate).
const (
	PlaceWhole   = "whole"   // on the whole document
	PlaceHere    = "here"    // the quoted passage is in the text
	PlaceHeading = "heading" // the passage is gone or there was none; its heading is there
	PlaceGone    = "gone"    // neither the passage nor its heading is there
)

// Anchor is what a comment is on. No Quote and no Heading: the whole
// document. Heading alone: that heading. Quote: that passage, with the text
// just before and after it to tell repeats apart, and the heading above it.
type Anchor struct {
	Heading     string `json:"heading,omitempty"`
	HeadingText string `json:"heading_text,omitempty"`
	Quote       string `json:"quote,omitempty"`
	Prefix      string `json:"prefix,omitempty"`
	Suffix      string `json:"suffix,omitempty"`
}

// Whole reports whether the anchor is the whole document.
func (a Anchor) Whole() bool { return a.Quote == "" && a.Heading == "" }

// CommentEvent is one line of the comments log.
type CommentEvent struct {
	Op     string `json:"op"`
	Thread string `json:"thread"`
	Output string `json:"output"`
	Date   string `json:"date"`
	By     string `json:"by,omitempty"`
	Text   string `json:"text,omitempty"`
	// Version is the output's version the comment was written on.
	Version int     `json:"version,omitempty"`
	Anchor  *Anchor `json:"anchor,omitempty"`
}

// Message is one comment or reply in a thread.
type Message struct {
	By      string    `json:"by"`
	Date    time.Time `json:"date"`
	Text    string    `json:"text"`
	Version int       `json:"version,omitempty"`
}

// Thread is a comment with its replies.
type Thread struct {
	ID       string    `json:"id"`
	Output   string    `json:"output"`
	Anchor   Anchor    `json:"anchor"`
	Version  int       `json:"version"`
	Messages []Message `json:"messages"`
	Resolved bool      `json:"resolved,omitempty"`
	// ResolvedAt and ResolvedBy are the newest resolve.
	ResolvedAt time.Time `json:"resolved_at,omitempty"`
	ResolvedBy string    `json:"resolved_by,omitempty"`
}

// CommentsPath is the comments log, beside the outputs log.
func CommentsPath(p *tracker.Project) string {
	return filepath.Join(p.IssueDir, ".outputs", "comments.jsonl")
}

// LoadComments folds the comments log into threads per output id, each in
// the order they were opened, and the lines it skipped. A missing log is no
// comments.
func LoadComments(p *tracker.Project) (map[string][]*Thread, []BadLine) {
	f, err := os.Open(CommentsPath(p))
	if err != nil {
		return map[string][]*Thread{}, nil
	}
	defer f.Close()
	out := map[string][]*Thread{}
	byID := map[string]*Thread{}
	var bad []BadLine
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e CommentEvent
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			bad = append(bad, BadLine{Line: n, Err: err.Error()})
			continue
		}
		if err := applyComment(out, byID, e); err != nil {
			bad = append(bad, BadLine{Line: n, Err: err.Error()})
		}
	}
	return out, bad
}

func applyComment(out map[string][]*Thread, byID map[string]*Thread, e CommentEvent) error {
	when, err := time.Parse(time.RFC3339, e.Date)
	if err != nil {
		return fmt.Errorf("date %q: %v", e.Date, err)
	}
	if e.Thread == "" || e.Output == "" {
		return errors.New("no thread or output")
	}
	key := e.Output + "/" + e.Thread
	t := byID[key]
	if e.Op == OpComment {
		if t != nil {
			return fmt.Errorf("thread %s opened twice", e.Thread)
		}
		t = &Thread{ID: e.Thread, Output: e.Output, Version: e.Version}
		if e.Anchor != nil {
			t.Anchor = *e.Anchor
		}
		t.Messages = []Message{{By: e.By, Date: when, Text: e.Text, Version: e.Version}}
		byID[key] = t
		out[e.Output] = append(out[e.Output], t)
		return nil
	}
	if t == nil {
		return fmt.Errorf("%s on unknown thread %s", e.Op, e.Thread)
	}
	switch e.Op {
	case OpReply:
		t.Messages = append(t.Messages, Message{By: e.By, Date: when, Text: e.Text, Version: e.Version})
	case OpResolve:
		t.Resolved, t.ResolvedAt, t.ResolvedBy = true, when, e.By
	case OpReopen:
		t.Resolved = false
	default:
		return fmt.Errorf("unknown op %q", e.Op)
	}
	return nil
}

func appendComment(p *tracker.Project, e CommentEvent) error {
	path := CommentsPath(p)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// cleanText is a comment's text, trimmed; empty or too long is an error.
func cleanText(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("a comment needs text")
	}
	if utf8.RuneCountInString(text) > MaxCommentRunes {
		return "", fmt.Errorf("a comment is at most %d characters", MaxCommentRunes)
	}
	return text, nil
}

func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

func newThreadID(taken []*Thread) string {
	for {
		b := make([]byte, 2)
		_, _ = rand.Read(b)
		id := "c-" + hex.EncodeToString(b)
		free := true
		for _, t := range taken {
			free = free && t.ID != id
		}
		if free {
			return id
		}
	}
}

// AddComment opens a thread on output id (full id) at version, anchored at
// a. The quote is cut to MaxQuoteRunes and its context to 30 characters.
func AddComment(p *tracker.Project, id string, a Anchor, text, by string, version int, now time.Time) (*Thread, error) {
	text, err := cleanText(text)
	if err != nil {
		return nil, err
	}
	a.Quote = cut(strings.TrimSpace(FoldSpace(a.Quote)), MaxQuoteRunes)
	a.Prefix = lastRunes(FoldSpace(a.Prefix), anchorContext)
	a.Suffix = cut(FoldSpace(a.Suffix), anchorContext)
	a.Heading, a.HeadingText = strings.TrimSpace(a.Heading), strings.TrimSpace(FoldSpace(a.HeadingText))
	if a.Quote == "" {
		a.Prefix, a.Suffix = "", ""
	}
	all, _ := LoadComments(p)
	e := CommentEvent{Op: OpComment, Thread: newThreadID(all[id]), Output: id, Date: now.UTC().Format(time.RFC3339Nano), By: by, Text: text, Version: version}
	if !a.Whole() {
		e.Anchor = &a
	}
	if err := appendComment(p, e); err != nil {
		return nil, fmt.Errorf("recording the comment: %w", err)
	}
	return FindThread(p, id, e.Thread)
}

// Reply adds a message to a thread.
func Reply(p *tracker.Project, id, thread, text, by string, version int, now time.Time) (*Thread, error) {
	text, err := cleanText(text)
	if err != nil {
		return nil, err
	}
	t, err := FindThread(p, id, thread)
	if err != nil {
		return nil, err
	}
	if err := appendComment(p, CommentEvent{Op: OpReply, Thread: t.ID, Output: id, Date: now.UTC().Format(time.RFC3339Nano), By: by, Text: text, Version: version}); err != nil {
		return nil, fmt.Errorf("recording the reply: %w", err)
	}
	return FindThread(p, id, t.ID)
}

// Resolve resolves a thread, or reopens it.
func Resolve(p *tracker.Project, id, thread, by string, reopen bool, now time.Time) (*Thread, error) {
	t, err := FindThread(p, id, thread)
	if err != nil {
		return nil, err
	}
	op := OpResolve
	if reopen {
		op = OpReopen
	}
	if err := appendComment(p, CommentEvent{Op: op, Thread: t.ID, Output: id, Date: now.UTC().Format(time.RFC3339Nano), By: by}); err != nil {
		return nil, fmt.Errorf("recording the %s: %w", op, err)
	}
	return FindThread(p, id, t.ID)
}

// FindThread is thread on output id; an unknown one is an error naming the
// output's threads.
func FindThread(p *tracker.Project, id, thread string) (*Thread, error) {
	all, _ := LoadComments(p)
	thread = strings.TrimSpace(thread)
	var ids []string
	for _, t := range all[id] {
		if t.ID == thread || t.ID == "c-"+thread {
			return t, nil
		}
		ids = append(ids, t.ID)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no thread %q: output %s has no comments", thread, id)
	}
	return nil, fmt.Errorf("no thread %q on output %s (threads: %s)", thread, id, strings.Join(ids, ", "))
}

// OpenCount is how many of threads are not resolved.
func OpenCount(threads []*Thread) int {
	n := 0
	for _, t := range threads {
		if !t.Resolved {
			n++
		}
	}
	return n
}

// FoldSpace folds every run of white space into one space.
func FoldSpace(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	if space && b.Len() > 0 {
		b.WriteByte(' ')
	}
	return b.String()
}

func lastRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}

// Locate says where t sits in a version whose text (the rendered document,
// as plain text) is text and whose heading ids are headings, and the byte
// offset in the folded text it sits at (-1 for the whole document, or when
// gone). The passage is looked for with its context first, then alone; a
// heading anchor falls back to the heading.
func Locate(t *Thread, text string, headings map[string]int) (string, int) {
	a := t.Anchor
	if a.Whole() {
		return PlaceWhole, -1
	}
	if a.Quote != "" {
		folded := FoldSpace(text)
		if i := strings.Index(folded, a.Prefix+a.Quote+a.Suffix); a.Prefix+a.Suffix != "" && i >= 0 {
			return PlaceHere, i + len(a.Prefix)
		}
		if i := strings.Index(folded, a.Quote); i >= 0 {
			return PlaceHere, i
		}
	}
	if at, ok := headings[a.Heading]; ok && a.Heading != "" {
		return PlaceHeading, at
	}
	return PlaceGone, -1
}
