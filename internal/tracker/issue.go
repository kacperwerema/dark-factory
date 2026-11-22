package tracker

import (
	"bytes"

	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kacperwerema/dark-factory/internal/gitfile"
	"gopkg.in/yaml.v3"
)

// ExtraField holds a single unknown frontmatter field for display in the sidebar.
type ExtraField struct {
	Key    string
	Label  string
	Value  string
	Values []string
	IsURL  bool
	IsList bool
}

var knownFrontmatterFields = map[string]bool{
	"title": true, "status": true, "system": true, "version": true,
	"labels": true, "priority": true, "assignee": true,
	"human_approval": true, "approved_for": true, "queued_approval": true,
	"started_at": true, "done_at": true, "created": true,
	"number": true, "repo": true, "slug": true,
}

func formatFieldLabel(key string) string {
	words := strings.Split(key, "_")
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

func extractExtraFields(rawMap map[string]interface{}) []ExtraField {
	var keys []string
	for k := range rawMap {
		if !knownFrontmatterFields[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	var fields []ExtraField
	for _, k := range keys {
		ef := ExtraField{Key: k, Label: formatFieldLabel(k)}
		switch val := rawMap[k].(type) {
		case []interface{}:
			ef.IsList = true
			for _, item := range val {
				ef.Values = append(ef.Values, fmt.Sprintf("%v", item))
			}
		default:
			ef.Value = fmt.Sprintf("%v", val)
			ef.IsURL = strings.HasPrefix(ef.Value, "http://") || strings.HasPrefix(ef.Value, "https://")
		}
		fields = append(fields, ef)
	}
	return fields
}

type Issue struct {
	Title          string   `yaml:"title"`
	Status         string   `yaml:"status"`
	System         string   `yaml:"system"`
	Version        string   `yaml:"version"`
	Labels         []string `yaml:"labels"`
	Priority       string   `yaml:"priority"`
	Assignee       string   `yaml:"assignee"`
	HumanApproval  string   `yaml:"human_approval"`
	LegacyApproval string   `yaml:"approved_for"`
	QueuedApproval string   `yaml:"queued_approval"` // "approve and start": promoted when HumanApproval is consumed
	StartedAt      string   `yaml:"started_at"`
	DoneAt         string   `yaml:"done_at"`
	Created        string   `yaml:"created"`
	Number         int      `yaml:"number"`
	Repo           string   `yaml:"repo"`
	// SlugPin is the `slug:` field. When set it names the issue instead of
	// its title, so a retitle (update --title) keeps the slug and every
	// reference to it.
	SlugPin string `yaml:"slug"`

	// Type is the `type:` frontmatter value (type of work: feature, tweak, …).
	// Read from the raw map rather than decoded so a non-scalar `type:` in a
	// project without types can't fail the parse; it also stays in
	// ExtraFields, where projects without types have always shown it.
	Type string `yaml:"-"`

	// Computed fields
	Slug        string       `yaml:"-"`
	FilePath    string       `yaml:"-"`
	ModTime     time.Time    `yaml:"-"`
	BodyRaw     string       `yaml:"-"`
	GithubURL   string       `yaml:"-"`
	ExtraFields []ExtraField `yaml:"-"`

	// comments are the file's comments, parsed with the body; parsed says
	// they were, for an Issue built by hand. Read them through Comments.
	comments []Comment
	parsed   bool
	// memo is what is derived from this parse (decisions, refs), shared by
	// every copy of a cached issue (decision_memo.go).
	memo *issueMemo
}

// BodyHTML renders the body. Only the pages that show a body ask, so a load
// of the whole board doesn't pay for it (goldmark was 60% of a load).
func (i *Issue) BodyHTML() string {
	loadCounts.bodyRenders.Add(1)
	var buf bytes.Buffer
	if err := newIssueMarkdown().Convert([]byte(i.BodyRaw), &buf); err != nil {
		return "<pre>" + html.EscapeString(i.BodyRaw) + "</pre>"
	}
	return buf.String()
}

// Comments are the issue's comments as of when it was read; the slice is the
// caller's own. Where a fresher read is the point (after writing one), call
// LoadComments. An Issue not built by ParseIssue reads its file.
func (i *Issue) Comments() []Comment {
	if !i.parsed {
		comments, _ := LoadComments(i.FilePath)
		return comments
	}
	return slices.Clone(i.comments)
}

func ParseIssue(filename string, data []byte) (*Issue, error) {
	issue := &Issue{}
	rawBody, err := ParseFrontmatter(string(data), issue)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filename, err)
	}
	body, comments := ParseComments(rawBody)
	issue.BodyRaw = strings.TrimSpace(body)
	issue.comments, issue.parsed = comments, true
	issue.memo = &issueMemo{}

	issue.Slug = Slugify(issue.Title)
	if pin := Slugify(issue.SlugPin); pin != "" {
		issue.Slug = pin
	}

	issue.Status = strings.ToLower(strings.TrimSpace(issue.Status))
	issue.Priority = strings.ToLower(strings.TrimSpace(issue.Priority))
	issue.System = strings.TrimSpace(issue.System)
	if issue.HumanApproval == "" {
		issue.HumanApproval = strings.TrimSpace(issue.LegacyApproval)
	}
	if issue.Repo != "" && issue.Number > 0 {
		issue.GithubURL = fmt.Sprintf("https://github.com/%s/issues/%d", issue.Repo, issue.Number)
	}

	// Extract unknown frontmatter fields for display
	if strings.HasPrefix(string(data), "---") {
		parts := strings.SplitN(string(data)[3:], "\n---", 2)
		if len(parts) >= 1 {
			var rawMap map[string]interface{}
			if yaml.Unmarshal([]byte(parts[0]), &rawMap) == nil {
				issue.ExtraFields = extractExtraFields(rawMap)
				if t, ok := rawMap["type"].(string); ok {
					issue.Type = strings.TrimSpace(t)
				}
			}
		}
	}

	return issue, nil
}

const (
	issueLockRetryDelay = 25 * time.Millisecond
	issueLockTimeout    = 15 * time.Second
)

func issueLockPath(filePath string) string {
	return filePath + ".lock"
}

func withIssueLock(filePath string, fn func() error) error {
	lockPath := issueLockPath(filePath)
	deadline := time.Now().Add(issueLockTimeout)

	for {
		lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			fmt.Fprintf(lockFile, "pid=%d\ntime=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano))
			lockFile.Close()
			defer os.Remove(lockPath)
			return fn()
		}
		if !os.IsExist(err) {
			return fmt.Errorf("creating lock %s: %w", lockPath, err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for issue lock %s", filePath)
		}
		time.Sleep(issueLockRetryDelay)
	}
}

func writeFileAtomically(filePath string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(filePath)
	tmp, err := os.CreateTemp(dir, gitfile.TempPattern("", filepath.Base(filePath)))
	if err != nil {
		return fmt.Errorf("creating temp file for %s: %w", filePath, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("setting temp permissions for %s: %w", filePath, err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing temp file for %s: %w", filePath, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("syncing temp file for %s: %w", filePath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file for %s: %w", filePath, err)
	}
	if err := os.Rename(tmpPath, filePath); err != nil {
		return fmt.Errorf("replacing %s atomically: %w", filePath, err)
	}
	return nil
}

// warnOut is where LoadIssues reports a file it skips (tests swap it).
var warnOut io.Writer = os.Stderr

// warnedPaths holds the files already warned about: a process loads the
// board many times, and one warning per file is enough.
var warnedPaths sync.Map

// NotIssueFolder reports whether a folder under the issues dir holds no
// issues: dot folders keep the work files' logs and reports (.goals,
// .outputs, .jobs, .reports), underscore folders the evidence
// (_evidence/: READMEs, reports, shots).
func NotIssueFolder(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

func warnSkip(path string, err error) {
	if _, seen := warnedPaths.LoadOrStore(path, true); seen {
		return
	}
	fmt.Fprintf(warnOut, "warning: skipping %s: %v\n", path, err)
}

// LoadIssues reads the board. Parsed files are kept per folder and a load
// only stats them (path, size, mtime), so reading the board again costs a
// walk; a changed file is parsed again. The issues are the caller's own.
func LoadIssues(dir string) ([]*Issue, error) {
	return LoadIssuesWhere(dir, nil)
}

// LoadIssuesWhere is LoadIssues for the files whose content keep accepts
// (nil keeps all): a reader that needs a few issues skips parsing the rest.
func LoadIssuesWhere(dir string, keep func(data []byte) bool) ([]*Issue, error) {
	var issues []*Issue
	if keep == nil {
		loadCounts.boardWalks.Add(1)
	}
	known := cachedBoard(dir)
	files := make(map[string]cachedIssue, len(known))

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != dir && NotIssueFolder(d.Name()) {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}

		// Stat before reading: a write in between leaves an old stamp on
		// new content, which the next load sees as a change.
		info, err := d.Info()
		if err != nil {
			warnSkip(path, err)
			return nil
		}
		stamp := stampOf(info)
		cached, hit := known[path]
		hit = hit && cached.stamp.same(stamp)

		var data []byte
		readAt := time.Now()
		if hit {
			data = cached.data
		} else {
			loadCounts.fileReads.Add(1)
			if data, err = os.ReadFile(path); err != nil {
				warnSkip(path, err)
				return nil
			}
		}
		if keep != nil && !keep(data) {
			return nil
		}

		if !hit {
			cached = cachedIssue{stamp: stamp, data: data}
			if cached.issue, err = parseBoardFile(dir, path, d.Name(), data, info); err != nil {
				warnSkip(path, err)
				return nil
			}
		}
		// A link's stat says nothing about its target.
		if d.Type().IsRegular() && (hit || stamp.settled(readAt)) {
			files[path] = cached
		}
		issues = append(issues, copyIssue(cached.issue))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking directory %s: %w", dir, err)
	}
	if keep == nil {
		storeBoard(dir, files)
	}

	// Handle slug collisions
	seen := map[string]int{}
	for _, issue := range issues {
		count := seen[issue.Slug]
		seen[issue.Slug]++
		if count > 0 {
			issue.Slug = fmt.Sprintf("%s-%d", issue.Slug, count+1)
		}
	}

	sort.Slice(issues, func(i, j int) bool {
		return issues[i].ModTime.After(issues[j].ModTime)
	})

	return issues, nil
}

// parseBoardFile parses one file found under dir and sets what its place on
// the board decides: the folder in the slug and the system it implies.
func parseBoardFile(dir, path, name string, data []byte, info fs.FileInfo) (*Issue, error) {
	loadCounts.issueParses.Add(1)
	issue, err := ParseIssue(name, data)
	if err != nil {
		return nil, err
	}
	issue.FilePath = path
	issue.ModTime = info.ModTime()

	relDir := filepath.Dir(path)
	baseDir, _ := filepath.Rel(dir, relDir)
	if baseDir != "" && baseDir != "." {
		issue.Slug = strings.ToLower(baseDir) + "/" + issue.Slug
		// Auto-detect system from subdirectory if not set in frontmatter
		if issue.System == "" {
			issue.System = baseDir
		}
	}
	return issue, nil
}

type IssueUpdate struct {
	Title          *string           `json:"title,omitempty"`
	Status         *string           `json:"status,omitempty"`
	Priority       *string           `json:"priority,omitempty"`
	Version        *string           `json:"version,omitempty"`
	Assignee       *string           `json:"assignee,omitempty"`
	HumanApproval  *string           `json:"human_approval,omitempty"`
	QueuedApproval *string           `json:"queued_approval,omitempty"`
	StartedAt      *string           `json:"started_at,omitempty"`
	DoneAt         *string           `json:"done_at,omitempty"`
	Labels         []string          `json:"labels,omitempty"`
	Body           *string           `json:"body,omitempty"`
	ExtraFields    map[string]string `json:"extra_fields,omitempty"`
	// Slug pins the slug (slug:); "" removes the pin.
	Slug *string `json:"slug,omitempty"`
}

// BaseSlug is the issue's slug without its subdirectory prefix: the part a
// slug: pin or the file name holds.
func BaseSlug(issue *Issue) string {
	return issue.Slug[strings.LastIndex(issue.Slug, "/")+1:]
}

// RetitleUpdate builds the update that sets issue's title to title. By
// default the slug is kept: when the new title would give a different slug
// and the issue has no pin yet, the current slug is pinned with slug:.
// With rename the pin is removed so the slug follows the new title.
func RetitleUpdate(issue *Issue, title string, rename bool) IssueUpdate {
	u := IssueUpdate{Title: &title}
	switch {
	case rename:
		if issue.SlugPin != "" {
			empty := ""
			u.Slug = &empty
		}
	case issue.SlugPin == "" && Slugify(title) != BaseSlug(issue):
		pin := BaseSlug(issue)
		u.Slug = &pin
	}
	return u
}

func AppendIssueBody(body, text string) (string, bool, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return body, false, nil
	}

	textHeadings := findAllHeadings(text)

	// Auto-route: if text begins with a heading that already exists in body
	// and the rest of the text only contains deeper subheadings, treat this
	// as an append into that existing section.
	if len(textHeadings) > 0 && textHeadings[0].StartLine == 0 {
		lead := textHeadings[0]
		canAutoRoute := true
		for _, h := range textHeadings[1:] {
			if h.Level <= lead.Level {
				canAutoRoute = false
				break
			}
		}
		if owners := sectionsOwning(body, lead); canAutoRoute && len(owners) > 0 {
			if len(owners) > 1 {
				var labels []string
				for _, o := range owners {
					labels = append(labels, fmt.Sprintf("%s (line %d)", o.Line, o.StartLine+1))
				}
				return body, false, fmt.Errorf("multiple matching sections for %q: %s\n\nRerun with --section and --force to merge into the first matching section", lead.Line, strings.Join(labels, ", "))
			}
			_, remainder, _ := strings.Cut(text, "\n")
			nextBody, changed := appendContentToMatch(body, owners[0], stripLeadingHeading(remainder, lead.Key))
			return nextBody, changed, nil
		}
	}

	var duplicates []string
	seen := map[string]bool{}
	for _, match := range textHeadings {
		if len(sectionsOwning(body, match)) > 0 && !seen[match.Key] {
			duplicates = append(duplicates, strings.TrimSpace(match.Line))
			seen[match.Key] = true
		}
	}
	if len(duplicates) > 0 {
		return body, false, fmt.Errorf("append would introduce duplicate heading(s): %s\n\nUse --section to target an existing section instead", strings.Join(duplicates, ", "))
	}

	body = strings.TrimRight(body, "\n")
	if body == "" {
		return text + "\n", true, nil
	}
	return body + "\n" + text + "\n", true, nil
}

// CreateIssueFile creates a new issue markdown file and returns the file path and slug.
type CreateIssueOpts struct {
	Title    string
	Status   string
	System   string
	Version  string
	Priority string
	Labels   []string
	Body     string
	// Type is written as type: when set (projects with types: in workflow.yaml).
	Type string
	// Now dates the issue's created: line; zero is time.Now.
	Now time.Time
}

// CreatedLine is the created: frontmatter line every new issue gets: the
// date the criteria_proven gate reads (proofCovers).
func CreatedLine(now time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}
	return fmt.Sprintf("created: %q\n", now.Format("2006-01-02"))
}

func CreateIssueFile(issueDir, title, status, system, version string) (filePath, slug string, err error) {
	return CreateIssueFileOpts(issueDir, CreateIssueOpts{
		Title: title, Status: status, System: system, Version: version,
	})
}

func CreateIssueFileOpts(issueDir string, opts CreateIssueOpts) (filePath, slug string, err error) {
	if opts.Title == "" {
		return "", "", fmt.Errorf("title is required")
	}
	if opts.Status == "" {
		opts.Status = "idea"
	}

	if err := ValidSystemDir(opts.System); err != nil {
		return "", "", err
	}
	dir := issueDir
	if opts.System != "" {
		dir = filepath.Join(dir, opts.System)
		os.MkdirAll(dir, 0755)
	}

	slug = Slugify(opts.Title)
	filename := filepath.Join(dir, slug+".md")
	// Create never replaces an issue: a title that slugs to an existing
	// file is refused, as issue-cli create refuses it.
	if _, err := os.Lstat(filename); err == nil {
		return "", "", fmt.Errorf("%w: %s (use update to change it)", ErrIssueExists, slug)
	}

	var content strings.Builder
	content.WriteString("---\n")
	content.WriteString("title: " + yamlQuote(opts.Title) + "\n")
	content.WriteString("status: " + yamlQuote(opts.Status) + "\n")
	content.WriteString(CreatedLine(opts.Now))
	if opts.System != "" {
		content.WriteString("system: " + yamlQuote(opts.System) + "\n")
	}
	if opts.Version != "" {
		content.WriteString("version: " + yamlQuote(opts.Version) + "\n")
	}
	if opts.Priority != "" {
		content.WriteString("priority: " + yamlQuote(opts.Priority) + "\n")
	}
	if opts.Type != "" {
		content.WriteString("type: " + yamlQuote(opts.Type) + "\n")
	}
	if len(opts.Labels) > 0 {
		content.WriteString("labels:\n")
		for _, l := range opts.Labels {
			content.WriteString("  - " + yamlQuote(l) + "\n")
		}
	}
	content.WriteString("---\n")
	if err := CheckIssueFrontmatter(content.String()); err != nil {
		return "", "", fmt.Errorf("creating issue: %w", err)
	}
	if opts.Body != "" {
		content.WriteString("\n" + opts.Body + "\n")
	} else {
		content.WriteString("\n")
	}

	if err := writeFileAtomically(filename, []byte(content.String()), 0644); err != nil {
		return "", "", fmt.Errorf("creating issue: %w", err)
	}

	if opts.System != "" {
		slug = strings.ToLower(opts.System) + "/" + slug
	}

	return filename, slug, nil
}

// ErrIssueExists and ErrBadSystem are create's refusals of the request
// itself (an HTTP 409 and 400).
var (
	ErrIssueExists = errors.New("issue already exists")
	ErrBadSystem   = errors.New("bad system")
)

// ValidSystemDir refuses a system that isn't one plain folder name: a
// system is the subfolder an issue is written to, so "../x", "/abs", "a/b"
// or a dot-folder would write outside the issues tree
// (docs/API/security.md).
func ValidSystemDir(system string) error {
	if system == "" {
		return nil
	}
	if system != strings.TrimSpace(system) || strings.HasPrefix(system, ".") || strings.ContainsAny(system, "/\\:\x00\n\r") {
		return fmt.Errorf("%w: system %q is not one folder name (no /, \\, :, leading dot or surrounding spaces)", ErrBadSystem, system)
	}
	return nil
}

// yamlQuote writes s as a YAML double-quoted scalar: quotes, backslashes,
// newlines and control characters escaped, so a value can't end its line
// and add frontmatter keys.
func yamlQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// CollectSubdirSystems returns the names of immediate subdirectories in dir.
// This captures systems that exist as folders even if they contain no issues.
func CollectSubdirSystems(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var systems []string
	for _, e := range entries {
		if e.IsDir() {
			systems = append(systems, e.Name())
		}
	}
	return systems
}

func CollectFilterValues(issues []*Issue) (statuses, systems, priorities, labels, assignees []string) {
	statusSet := map[string]bool{}
	systemSet := map[string]bool{}
	prioritySet := map[string]bool{}
	labelSet := map[string]bool{}
	assigneeSet := map[string]bool{}

	for _, issue := range issues {
		if issue.Status != "" {
			statusSet[issue.Status] = true
		}
		if issue.System != "" {
			systemSet[issue.System] = true
		}
		if issue.Priority != "" {
			prioritySet[issue.Priority] = true
		}
		if issue.Assignee != "" {
			assigneeSet[issue.Assignee] = true
		}
		for _, l := range issue.Labels {
			labelSet[l] = true
		}
	}

	for k := range statusSet {
		statuses = append(statuses, k)
	}
	for k := range systemSet {
		systems = append(systems, k)
	}
	for k := range prioritySet {
		priorities = append(priorities, k)
	}
	for k := range labelSet {
		labels = append(labels, k)
	}
	for k := range assigneeSet {
		assignees = append(assignees, k)
	}

	sort.Strings(statuses)
	sort.Strings(systems)
	sort.Strings(priorities)
	sort.Strings(labels)
	sort.Strings(assignees)

	return
}

// HasCommentWithPrefix checks if any comment starts with the given prefix.
func HasCommentWithPrefix(comments []Comment, prefix string) bool {
	for _, c := range comments {
		if strings.HasPrefix(strings.ToLower(c.Text), strings.ToLower(prefix)) {
			return true
		}
	}
	return false
}
