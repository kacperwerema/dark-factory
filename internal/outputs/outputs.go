// Package outputs is the departments' outputs shelf: what each department
// made (posts, designs, pages, reports, invoices), with the issue or job
// that made it and the owner's review. Outputs are events in an append-only
// log with the work files, <issues>/.outputs/outputs.jsonl; Load folds them
// into the shelf. The CLI (issue-cli output, outputs), the company page, the
// shelf page and the station read the same list. See docs/departments.md.
package outputs

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/goals"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// Event ops and output states.
const (
	OpAdd     = "add"
	OpReview  = "review"
	OpPublish = "publish"

	StateReview    = "review"
	StateApproved  = "approved"
	StateRejected  = "rejected"
	StatePublished = "published"
)

// States are the states an output can be in, in shelf order.
var States = []string{StateReview, StateApproved, StateRejected, StatePublished}

// MaxFileBytes is the largest file output add copies onto the shelf.
const MaxFileBytes = 25 << 20

// StaleReview is how long an output may wait in review before lint warns.
const StaleReview = 14 * 24 * time.Hour

// kindIcons are the kinds with an icon; any other kind shows as written.
var kindIcons = map[string]string{
	"post": "📝", "design": "🎨", "site": "🌐", "page": "📄", "report": "📊", "invoice": "🧾",
}

// Kinds are the kinds with an icon, for help texts.
var Kinds = []string{"post", "design", "site", "page", "report", "invoice"}

// Icon is the kind's icon, 📦 for any other kind.
func Icon(kind string) string {
	if i, ok := kindIcons[strings.ToLower(strings.TrimSpace(kind))]; ok {
		return i
	}
	return "📦"
}

// Event is one line of the log.
type Event struct {
	Op   string `json:"op"`
	ID   string `json:"id"`
	Date string `json:"date"`
	// add
	Department string `json:"department,omitempty"`
	Title      string `json:"title,omitempty"`
	Kind       string `json:"kind,omitempty"`
	URL        string `json:"url,omitempty"`
	File       string `json:"file,omitempty"`
	Path       string `json:"path,omitempty"`
	Issue      string `json:"issue,omitempty"`
	Job        string `json:"job,omitempty"`
	Note       string `json:"note,omitempty"`
	// review
	State string `json:"state,omitempty"`
	Quote string `json:"quote,omitempty"`
	How   string `json:"how,omitempty"`
	By    string `json:"by,omitempty"`
}

// Output is an output with its events folded in.
type Output struct {
	ID string `json:"id"`
	// Department is the department's key (product, marketing); Name its name.
	Department string `json:"department"`
	Name       string `json:"department_name"`
	Title      string `json:"title"`
	Kind       string `json:"kind"`
	// One of URL, File (under FilesDir) and Path (from the project root).
	URL   string    `json:"url,omitempty"`
	File  string    `json:"file,omitempty"`
	Path  string    `json:"path,omitempty"`
	Issue string    `json:"issue,omitempty"`
	Job   string    `json:"job,omitempty"`
	Note  string    `json:"note,omitempty"`
	By    string    `json:"by,omitempty"`
	Added time.Time `json:"added"`
	State string    `json:"state"`
	// Reviewed is the newest review; Quote the owner's words.
	Reviewed   time.Time `json:"reviewed,omitempty"`
	ReviewedBy string    `json:"reviewed_by,omitempty"`
	Quote      string    `json:"quote,omitempty"`
	Published  time.Time `json:"published,omitempty"`
	// Missing: the file or path is gone.
	Missing bool `json:"missing,omitempty"`
}

// Icon is the output's kind icon.
func (o *Output) Icon() string { return Icon(o.Kind) }

// Where is what the output points at: the URL, the shelf file or the path.
func (o *Output) Where() string {
	switch {
	case o.URL != "":
		return o.URL
	case o.File != "":
		return filepath.ToSlash(filepath.Join(".outputs", "files", o.File))
	}
	return o.Path
}

// IsImage reports whether the output's file or path is an image.
func (o *Output) IsImage() bool {
	name := o.File
	if name == "" {
		name = o.Path
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg":
		return true
	}
	return false
}

// BadLine is a log line Load skipped.
type BadLine struct {
	Line int
	Err  string
}

// LogPath is the outputs log: with the work files (under the issues
// folder), so a private tracker keeps outputs private too.
func LogPath(p *tracker.Project) string {
	return filepath.Join(p.IssueDir, ".outputs", "outputs.jsonl")
}

// FilesDir is where output add copies files.
func FilesDir(p *tracker.Project) string {
	return filepath.Join(p.IssueDir, ".outputs", "files")
}

// DeptKey is a department's key in the log, as for goals.
func DeptKey(name string) string { return goals.DeptKey(name) }

// DepartmentName is the configured name for a key: Product, a department
// from projects.yaml, else the key as written (a department since removed).
func DepartmentName(p *tracker.Project, key string) string {
	if key == goals.ProductKey {
		return tracker.ProductName
	}
	for _, d := range p.Departments {
		if DeptKey(d.Name) == key {
			return d.Name
		}
	}
	return key
}

// Configured reports whether key is Product or a configured department.
func Configured(p *tracker.Project, key string) bool {
	if key == goals.ProductKey {
		return true
	}
	for _, d := range p.Departments {
		if DeptKey(d.Name) == key {
			return true
		}
	}
	return false
}

// Keys are Product's key and every configured department's.
func Keys(p *tracker.Project) []string {
	out := []string{goals.ProductKey}
	for _, d := range p.Departments {
		out = append(out, DeptKey(d.Name))
	}
	return out
}

// Append adds one event to the log, in one write.
func Append(p *tracker.Project, e Event) error {
	path := LogPath(p)
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

// Load folds the log into outputs, newest first, and the lines it skipped.
// A missing log is an empty shelf.
func Load(p *tracker.Project) ([]*Output, []BadLine) {
	f, err := os.Open(LogPath(p))
	if err != nil {
		return nil, nil
	}
	defer f.Close()
	byID := map[string]*Output{}
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
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			bad = append(bad, BadLine{Line: n, Err: err.Error()})
			continue
		}
		if err := apply(byID, e); err != nil {
			bad = append(bad, BadLine{Line: n, Err: err.Error()})
		}
	}
	out := make([]*Output, 0, len(byID))
	for _, o := range byID {
		o.Name = DepartmentName(p, o.Department)
		o.Missing = missing(p, o)
		out = append(out, o)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Added.Equal(out[j].Added) {
			return out[i].Added.After(out[j].Added)
		}
		return out[i].ID > out[j].ID
	})
	return out, bad
}

func apply(byID map[string]*Output, e Event) error {
	when, err := time.Parse(time.RFC3339, e.Date)
	if err != nil {
		return fmt.Errorf("date %q: %v", e.Date, err)
	}
	if e.ID == "" {
		return errors.New("no id")
	}
	switch e.Op {
	case OpAdd:
		if _, ok := byID[e.ID]; ok {
			return fmt.Errorf("output %s added twice", e.ID)
		}
		byID[e.ID] = &Output{
			ID: e.ID, Department: e.Department, Title: e.Title, Kind: e.Kind,
			URL: e.URL, File: e.File, Path: e.Path, Issue: e.Issue, Job: e.Job, Note: e.Note,
			By: e.By, Added: when, State: StateReview,
		}
	case OpReview:
		o, ok := byID[e.ID]
		if !ok {
			return fmt.Errorf("review of unknown output %s", e.ID)
		}
		if e.State != StateApproved && e.State != StateRejected {
			return fmt.Errorf("review state %q", e.State)
		}
		o.State, o.Reviewed, o.ReviewedBy, o.Quote = e.State, when, e.By, e.Quote
	case OpPublish:
		o, ok := byID[e.ID]
		if !ok {
			return fmt.Errorf("publish of unknown output %s", e.ID)
		}
		o.State, o.Published = StatePublished, when
		if e.URL != "" {
			o.URL = e.URL
		}
	default:
		return fmt.Errorf("unknown op %q", e.Op)
	}
	return nil
}

func missing(p *tracker.Project, o *Output) bool {
	var path string
	switch {
	case o.URL != "":
		return false
	case o.File != "":
		path = filepath.Join(FilesDir(p), filepath.FromSlash(o.File))
	case o.Path != "":
		path = filepath.Join(p.Root(), filepath.FromSlash(o.Path))
	default:
		return false
	}
	_, err := os.Stat(path)
	return err != nil
}

// Find is the output with id, or the one whose id ends with it (the short
// form, a1b2); an unknown or ambiguous id is an error naming the newest.
func Find(list []*Output, id string) (*Output, error) {
	id = strings.TrimSpace(id)
	var hits []*Output
	for _, o := range list {
		if o.ID == id {
			return o, nil
		}
		if id != "" && strings.HasSuffix(o.ID, "-"+id) {
			hits = append(hits, o)
		}
	}
	if len(hits) == 1 {
		return hits[0], nil
	}
	if len(hits) > 1 {
		return nil, fmt.Errorf("output %q matches %d outputs; use the full id", id, len(hits))
	}
	var newest []string
	for i, o := range list {
		if i == 5 {
			break
		}
		newest = append(newest, o.ID+" ("+o.Title+")")
	}
	if len(newest) == 0 {
		return nil, fmt.Errorf("no output %q: the shelf is empty", id)
	}
	return nil, fmt.Errorf("no output %q; newest: %s", id, strings.Join(newest, ", "))
}

// NewID is a fresh id, o-<yyyymmdd>-<4 hex>, not in list.
func NewID(list []*Output, now time.Time) string {
	taken := map[string]bool{}
	for _, o := range list {
		taken[o.ID] = true
	}
	for {
		b := make([]byte, 2)
		_, _ = rand.Read(b)
		id := "o-" + now.UTC().Format("20060102") + "-" + hex.EncodeToString(b)
		if !taken[id] {
			return id
		}
	}
}

// AddInput is what output add records. Exactly one of URL, File (a local
// file to copy) and Path (from the project root) is set.
type AddInput struct {
	Department, Title, Kind string
	URL, File, Path         string
	Issue, Job, Note, By    string
}

// Add checks the input, copies a file onto the shelf and records the
// output in review.
func Add(p *tracker.Project, in AddInput, now time.Time) (*Output, error) {
	in.Title, in.Kind = strings.TrimSpace(in.Title), strings.ToLower(strings.TrimSpace(in.Kind))
	if in.Title == "" {
		return nil, errors.New("an output needs --title")
	}
	if in.Kind == "" {
		return nil, fmt.Errorf("an output needs --kind (%s, or any word)", strings.Join(Kinds, ", "))
	}
	set := 0
	for _, s := range []string{in.URL, in.File, in.Path} {
		if strings.TrimSpace(s) != "" {
			set++
		}
	}
	if set != 1 {
		return nil, errors.New("give exactly one of --url (a link), --file (a file to copy onto the shelf) or --path (a file in the repo)")
	}
	if !Configured(p, in.Department) {
		return nil, fmt.Errorf("no department %q (departments: %s)", in.Department, strings.Join(Keys(p), ", "))
	}
	list, _ := Load(p)
	id := NewID(list, now)
	e := Event{
		Op: OpAdd, ID: id, Date: now.UTC().Format(time.RFC3339), Department: in.Department,
		Title: in.Title, Kind: in.Kind, Issue: in.Issue, Job: strings.TrimSpace(in.Job),
		Note: strings.TrimSpace(in.Note), By: in.By,
	}
	switch {
	case in.URL != "":
		if err := CheckURL(in.URL); err != nil {
			return nil, err
		}
		e.URL = strings.TrimSpace(in.URL)
	case in.Path != "":
		rel, err := repoPath(p, in.Path)
		if err != nil {
			return nil, err
		}
		e.Path = rel
	default:
		rel, err := copyFile(p, in.Department, id, in.File)
		if err != nil {
			return nil, err
		}
		e.File = rel
	}
	if err := Append(p, e); err != nil {
		return nil, fmt.Errorf("recording the output: %w", err)
	}
	list, _ = Load(p)
	return Find(list, id)
}

// CheckURL accepts an http(s) URL with a host.
func CheckURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("--url must be an http:// or https:// link with a host, got %q", raw)
	}
	return nil
}

// repoPath is path relative to the project root; it must exist inside it.
func repoPath(p *tracker.Project, path string) (string, error) {
	root := p.Root()
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, path)
	}
	rel, err := filepath.Rel(root, filepath.Clean(abs))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("--path %q is outside the project (%s); use --file to copy it onto the shelf", path, root)
	}
	if _, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("--path %q: %v", path, err)
	}
	return filepath.ToSlash(rel), nil
}

// copyFile copies src to FilesDir/<dept>/<id>-<name> and returns that path
// relative to FilesDir.
func copyFile(p *tracker.Project, dept, id, src string) (string, error) {
	st, err := os.Stat(src)
	if err != nil {
		return "", fmt.Errorf("--file %q: %v", src, err)
	}
	if st.IsDir() {
		return "", fmt.Errorf("--file %q is a folder; give one file", src)
	}
	if st.Size() > MaxFileBytes {
		return "", fmt.Errorf("--file %q is %d MB, over the shelf's %d MB: put it somewhere and pass --url", src, st.Size()>>20, MaxFileBytes>>20)
	}
	rel := filepath.ToSlash(filepath.Join(dept, id+"-"+filepath.Base(src)))
	dst := filepath.Join(FilesDir(p), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return "", err
	}
	return rel, out.Close()
}

// Review records the owner's approve or reject. A published output can't
// be reviewed again.
func Review(p *tracker.Project, id, state, quote, how, by string, now time.Time) (*Output, error) {
	if state != StateApproved && state != StateRejected {
		return nil, fmt.Errorf("review is approve or reject, got %q", state)
	}
	if strings.TrimSpace(quote) == "" {
		return nil, errors.New("a review needs the owner's words (--quote)")
	}
	list, _ := Load(p)
	o, err := Find(list, id)
	if err != nil {
		return nil, err
	}
	if o.State == StatePublished {
		return nil, fmt.Errorf("output %s is already published", o.ID)
	}
	e := Event{Op: OpReview, ID: o.ID, Date: now.UTC().Format(time.RFC3339), State: state, Quote: strings.TrimSpace(quote), How: how, By: by}
	if err := Append(p, e); err != nil {
		return nil, fmt.Errorf("recording the review: %w", err)
	}
	list, _ = Load(p)
	return Find(list, o.ID)
}

// Publish marks an approved output published, with the link it went out at.
func Publish(p *tracker.Project, id, link, by string, now time.Time) (*Output, error) {
	list, _ := Load(p)
	o, err := Find(list, id)
	if err != nil {
		return nil, err
	}
	if o.State != StateApproved {
		return nil, fmt.Errorf("output %s is %s, not approved: the owner approves it first (issue-cli output review %s approve --quote \"<the owner's words>\")", o.ID, o.State, o.ID)
	}
	if link != "" {
		if err := CheckURL(link); err != nil {
			return nil, err
		}
	}
	if err := Append(p, Event{Op: OpPublish, ID: o.ID, Date: now.UTC().Format(time.RFC3339), URL: strings.TrimSpace(link), By: by}); err != nil {
		return nil, fmt.Errorf("recording the publish: %w", err)
	}
	list, _ = Load(p)
	return Find(list, o.ID)
}

// Filter is which outputs Select keeps; empty fields keep all.
type Filter struct {
	Department, State, Issue string
}

// Select is the outputs that match f, in the list's order.
func Select(list []*Output, f Filter) []*Output {
	var out []*Output
	for _, o := range list {
		if f.Department != "" && o.Department != f.Department {
			continue
		}
		if f.State != "" && o.State != f.State {
			continue
		}
		if f.Issue != "" && !strings.EqualFold(o.Issue, f.Issue) {
			continue
		}
		out = append(out, o)
	}
	return out
}

// Count is one department's shelf at a glance.
type Count struct {
	ToReview, Week, Total int
}

// Counts is the shelf at a glance per department key: in review, made
// this week (since Monday) and in all.
func Counts(list []*Output, now time.Time) map[string]Count {
	week := goals.PeriodStart("week", now)
	out := map[string]Count{}
	for _, o := range list {
		c := out[o.Department]
		c.Total++
		if o.State == StateReview {
			c.ToReview++
		}
		if !o.Added.Before(week) {
			c.Week++
		}
		out[o.Department] = c
	}
	return out
}
