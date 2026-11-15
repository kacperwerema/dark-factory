// Package issuetrace builds an issue's trace (internal/trace) from what the
// timeline loads (internal/timeline) and the issue's Claude Code
// transcripts (internal/bench's readers). It sits apart from timeline
// because bench imports timeline through proof.
package issuetrace

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/kacperwerema/dark-factory/internal/bench"
	"github.com/kacperwerema/dark-factory/internal/clilog"
	"github.com/kacperwerema/dark-factory/internal/timeline"
	"github.com/kacperwerema/dark-factory/internal/trace"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// IssueTrace is an issue's trace and what it was built from.
type IssueTrace struct {
	Spans []trace.Span
	// Read and Missing are as on Sources, plus the transcripts.
	Read    []string
	Missing []timeline.Missing
}

// Load builds issue's trace (internal/trace) from what is recorded: the
// stats sidecar (transitions, dispatches, briefings), the clilogs Load
// reads and the Claude Code transcripts of sessions run in the issue's
// worktree (the folders under projectsDir, "" for ~/.claude/projects),
// the same sessions internal/spend counts as the issue's. It reads only.
func Load(proj *tracker.Project, issue *tracker.Issue, cache *timeline.Cache, projectsDir string, now time.Time) IssueTrace {
	src := timeline.Load(proj, issue, cache)
	return From(proj, issue, src, projectsDir, now)
}

// From is Load over sources already loaded.
func From(proj *tracker.Project, issue *tracker.Issue, src timeline.Sources, projectsDir string, now time.Time) IssueTrace {
	out := IssueTrace{Read: src.Read, Missing: src.Missing}
	stats, _ := tracker.StatsOf(issue.FilePath)
	wf := proj.LoadWorkflowForIssue(issue)
	var statuses []string
	if wf != nil {
		for _, s := range wf.Statuses {
			statuses = append(statuses, s.Name)
		}
	}
	calls := make([]clilog.Record, 0, len(src.Calls))
	for _, c := range src.Calls {
		calls = append(calls, c.Record)
	}
	if projectsDir == "" {
		projectsDir = spendProjectsDir()
	}
	msgs, sessions := issueMessages(projectsDir, filepath.Join(timeline.ProjectRoot(proj), ".worktrees", issue.Slug))
	if sessions == 0 {
		out.Missing = append(out.Missing, timeline.Missing{Name: "Transcripts", Detail: "no Claude Code transcript of a session in the issue's worktree under " + projectsDir + ". Tokens per stage are unknown (a Codex worker's are never split by stage)."})
	} else {
		out.Read = append(out.Read, fmt.Sprintf("transcripts (%d sessions, %d messages)", sessions, len(msgs)))
	}
	if len(stats.Briefings) == 0 {
		out.Missing = append(out.Missing, timeline.Missing{Name: "Briefings", Detail: "no briefing recorded in the stats sidecar (dispatches, reprompts and starts record one from v0.63). Briefing sizes are unknown."})
	}
	end := now
	open := !proj.IssueClosed(issue)
	if !open {
		if t, ok := timeline.DoneAt(src.Transitions, wf); ok {
			end = t
		} else if n := len(src.Transitions); n > 0 {
			end = src.Transitions[n-1].TS
		}
	}
	out.Spans = trace.Build(trace.In{
		Transitions: src.Transitions,
		Briefings:   stats.Briefings,
		Dispatches:  stats.Dispatches,
		Calls:       calls,
		Messages:    msgs,
		Statuses:    statuses,
		Status:      issue.Status,
		Start:       timeline.LifeStart(src),
		End:         end,
		Open:        open,
		CallsKnown:  len(src.Calls) > 0,
		TokensKnown: sessions > 0,
	})
	return out
}

// Write writes issue's trace file, <issues>/.traces/<slug>.jsonl, and
// returns its path.
func Write(proj *tracker.Project, issue *tracker.Issue, projectsDir string, now time.Time) (string, error) {
	t := Load(proj, issue, nil, projectsDir, now)
	path := trace.Path(proj.IssueDir, issue.Slug)
	return path, trace.Write(path, t.Spans)
}

func spendProjectsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// transcriptCache keeps each transcript file's messages (numbers only) by
// path, size and mtime: the Timeline tab reads them on every render.
var transcriptCache tracker.FileCache[[]bench.Message]

// issueMessages is every counted message of the sessions run in worktree,
// oldest first, each API message once, and how many sessions there were.
func issueMessages(projectsDir, worktree string) ([]trace.Message, int) {
	var out []trace.Message
	sessions := 0
	seen := map[string]bool{}
	for _, folder := range bench.TranscriptFolders(projectsDir, worktree) {
		files := bench.SessionFiles(folder)
		tops := make([]string, 0, len(files))
		for top := range files {
			tops = append(tops, top)
		}
		sort.Strings(tops)
		for _, top := range tops {
			sessions++
			for _, f := range files[top] {
				info, err := os.Stat(f)
				if err != nil {
					continue
				}
				msgs, _ := transcriptCache.Get(f, info, func() ([]bench.Message, error) {
					var ms []bench.Message
					bench.EachMessage(f, nil, func(m bench.Message) {
						if m.Counted {
							m.Wrote = nil
							ms = append(ms, m)
						}
					})
					return ms, nil
				})
				for _, m := range msgs {
					if m.ID != "" {
						if seen[m.ID] {
							continue
						}
						seen[m.ID] = true
					}
					out = append(out, trace.Message{At: m.Timestamp, Session: m.SessionID, Model: m.Model,
						Input: m.Input, Output: m.Output, CacheRead: m.CacheRead, CacheWrite: m.CacheWrite})
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, sessions
}
