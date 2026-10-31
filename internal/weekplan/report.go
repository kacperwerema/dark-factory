package weekplan

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// The week report sits with the work files, so a private tracker keeps it
// private: <issues>/.reports/<box>/<week>.md (Settled rule "Business data
// sits with the work files"; #D-2 of this feature's issue).

// Dir is where box's reports are kept.
func Dir(p *tracker.Project, box string) string {
	return filepath.Join(p.IssueDir, ".reports", box)
}

// Path is box's report file for week key.
func Path(p *tracker.Project, box, key string) string {
	return filepath.Join(Dir(p, box), key+".md")
}

// Header is the report's first line, a JSON comment: what the room shows
// without recomputing a past week.
type Header struct {
	Box       string    `json:"box"`
	Week      string    `json:"week"`
	From      time.Time `json:"from"`
	To        time.Time `json:"to"`
	Counts    Counts    `json:"counts"`
	PlanIssue string    `json:"plan_issue,omitempty"`
	// Path is the file it was read from (not written).
	Path string `json:"-"`
}

const headerPrefix = "<!-- week: "

// Markdown is the week as markdown: what `issue-cli department week`
// prints and the body of the report.
func Markdown(w *Week) string {
	var b strings.Builder
	kind := "department"
	if w.Group {
		kind = "group"
	}
	fmt.Fprintf(&b, "# %s · week %s\n\n", w.Name, w.Key)
	fmt.Fprintf(&b, "The %s's week, looking back over %s – %s. Read from goals, the shelf, jobs, requests between departments and open issues; nothing here starts work.\n\n",
		kind, w.From.Format("2006-01-02"), w.To.Format("2006-01-02"))
	fmt.Fprintf(&b, "**%s**", w.Counts.Line())
	if w.Counts.WaitingReview > 0 {
		fmt.Fprintf(&b, " · %d waiting on the owner's review", w.Counts.WaitingReview)
	}
	b.WriteString("\n\n## Plan\n\n")
	if w.Plan != "" {
		fmt.Fprintf(&b, "From %s:\n\n%s\n\n", w.PlanIssue, w.Plan)
	} else {
		fmt.Fprintf(&b, "No week plan for %s yet. The group's weekly job writes it under `## %s` in its run issue (`issue-cli jobs %s`).\n\n", w.Key, PlanSection, w.Box)
	}
	for _, s := range w.Sections {
		writeSection(&b, s)
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func writeSection(b *strings.Builder, s *Section) {
	fmt.Fprintf(b, "## %s\n\n", s.Name)
	fmt.Fprintf(b, "Lead: `%s` · %s open · `issue-cli list --department %s`\n\n", s.Lead, plural(s.OpenIssues, "issue", "issues"), s.Box)

	b.WriteString("### Goals\n\n")
	if len(s.Goals) == 0 {
		fmt.Fprintf(b, "None configured: add `goals:` to %s in projects.yaml (`issue-cli goals`).\n\n", s.Name)
	}
	for _, g := range s.Goals {
		fmt.Fprintf(b, "- %s: %s", g.Name, g.Value)
		if g.Target != "" {
			fmt.Fprintf(b, " of %s", g.Target)
		}
		if g.Progress != nil {
			fmt.Fprintf(b, " (%d%%)", *g.Progress)
		}
		fmt.Fprintf(b, " · %s · `%s`\n", g.Move, g.Key)
	}
	if len(s.Goals) > 0 {
		b.WriteString("\n")
	}

	b.WriteString("### Outputs\n\n")
	if len(s.Review)+len(s.Reviewed)+len(s.Published) == 0 {
		fmt.Fprintf(b, "Nothing on the shelf this week (`issue-cli output add --issue <slug> …`).\n\n")
	} else {
		outputList(b, "Waiting on the owner's review", s.Review)
		outputList(b, "Reviewed this week", s.Reviewed)
		outputList(b, "Shipped (published) this week", s.Published)
	}

	b.WriteString("### Jobs\n\n")
	if len(s.Jobs) == 0 {
		fmt.Fprintf(b, "No recurring jobs (`issue-cli jobs %s` prints the config snippet).\n\n", s.Box)
	}
	for _, j := range s.Jobs {
		fmt.Fprintf(b, "- %s (`%s`), %s", j.Name, j.Key, j.Schedule)
		switch {
		case j.Error != "":
			fmt.Fprintf(b, " · error: %s", j.Error)
		case j.Due:
			fmt.Fprintf(b, " · due: %s", j.Verdict)
			if j.Reason != "" {
				fmt.Fprintf(b, " (%s)", j.Reason)
			}
		default:
			b.WriteString(" · not due")
		}
		if j.Last != "" {
			fmt.Fprintf(b, " · last run %s (%s)", j.Last, j.Status)
		}
		b.WriteString("\n")
	}
	if len(s.Jobs) > 0 {
		b.WriteString("\n")
	}

	b.WriteString("### Handoffs\n\n")
	if len(s.HandoffsIn)+len(s.HandoffsOut) == 0 {
		fmt.Fprintf(b, "None open (`issue-cli connections --kind request`).\n\n")
	} else {
		handoffList(b, "Asked of "+s.Name, "from", s.HandoffsIn)
		handoffList(b, s.Name+" asked", "to", s.HandoffsOut)
	}

	b.WriteString("### Blocked, and on whom\n\n")
	if len(s.Blocked) == 0 {
		b.WriteString("Nothing blocked.\n\n")
	}
	for _, l := range s.Blocked {
		fmt.Fprintf(b, "- %s (%s, %s): on %s · %s\n", l.Slug, l.Title, l.Status, l.On, l.Why)
	}
	if len(s.Blocked) > 0 {
		b.WriteString("\n")
	}
}

func outputList(b *strings.Builder, title string, list []OutputLine) {
	if len(list) == 0 {
		return
	}
	fmt.Fprintf(b, "%s:\n\n", title)
	for _, o := range list {
		fmt.Fprintf(b, "- %s %s (%s, %s) · %s", o.ID, o.Title, o.Kind, o.State, o.Date)
		if o.Issue != "" {
			fmt.Fprintf(b, " · %s", o.Issue)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

func handoffList(b *strings.Builder, title, dir string, list []HandoffLine) {
	if len(list) == 0 {
		return
	}
	fmt.Fprintf(b, "%s:\n\n", title)
	for _, h := range list {
		fmt.Fprintf(b, "- %s (%s, %s) %s %s", h.Slug, h.Title, h.Status, dir, h.Other)
		switch {
		case h.Waiting != "":
			fmt.Fprintf(b, " · waiting on %s to forward it", h.Waiting)
		case h.State != "" && !strings.HasPrefix(h.State, "at "):
			fmt.Fprintf(b, " · %s", h.State)
		}
		if h.Route != "" {
			fmt.Fprintf(b, " · route %s", h.Route)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

// Report is the report file's content: the header line, then Markdown.
func Report(w *Week) (string, error) {
	h := Header{Box: w.Box, Week: w.Key, From: w.From, To: w.To, Counts: w.Counts, PlanIssue: w.PlanIssue}
	raw, err := json.Marshal(h)
	if err != nil {
		return "", err
	}
	return headerPrefix + string(raw) + " -->\n" + Markdown(w), nil
}

// Write writes w's report, replacing the same week's (it is derived, like
// status.md); other weeks are never touched. It reports whether it replaced
// one.
func Write(p *tracker.Project, w *Week) (path string, replaced bool, err error) {
	content, err := Report(w)
	if err != nil {
		return "", false, err
	}
	path = Path(p, w.Box, w.Key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", false, err
	}
	_, statErr := os.Stat(path)
	replaced = statErr == nil
	tmp, err := os.CreateTemp(filepath.Dir(path), ".week-*.tmp")
	if err != nil {
		return "", false, err
	}
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", false, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", false, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return "", false, err
	}
	return path, replaced, nil
}

// ReadHeader reads a report's header line; an error when it has none.
func ReadHeader(path string) (*Header, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	line, _, _ := strings.Cut(string(data), "\n")
	if !strings.HasPrefix(line, headerPrefix) || !strings.HasSuffix(line, " -->") {
		return nil, fmt.Errorf("%s: no week header", path)
	}
	var h Header
	if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(line, headerPrefix), " -->")), &h); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	h.Path = path
	return &h, nil
}

// Latest is box's newest report's header, nil when there is none. A file
// without a readable header is skipped.
func Latest(p *tracker.Project, box string) *Header {
	keys := Weeks(p, box)
	for i := len(keys) - 1; i >= 0; i-- {
		if h, err := ReadHeader(Path(p, box, keys[i])); err == nil {
			return h
		}
	}
	return nil
}

// Weeks are box's report weeks, oldest first.
func Weeks(p *tracker.Project, box string) []string {
	entries, err := os.ReadDir(Dir(p, box))
	if err != nil {
		return nil
	}
	var keys []string
	for _, e := range entries {
		k := strings.TrimSuffix(e.Name(), ".md")
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") && ValidKey(k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}
