package main

import (
	"os"
	"sort"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// radioCommentWindow is how far back the Radio reads progress: and
// question: comments.
const radioCommentWindow = 7 * 24 * time.Hour

// radioKinds are the comment prefixes the Radio carries: what workers and
// leads report and ask on their issues.
var radioKinds = []string{"progress", "question"}

// radioComments is the newest progress: and question: comments on the
// project's issues from the last week, newest first, at most n: who said
// it, on which issue, how long ago. An issue file not written to in the
// window is not read.
func radioComments(p *tracker.Project, prefix string, issues []*tracker.Issue, now time.Time, n int) []WbMessage {
	type dated struct {
		at  time.Time
		msg WbMessage
	}
	var all []dated
	for _, issue := range issues {
		if info, err := os.Stat(issue.FilePath); err != nil || now.Sub(info.ModTime()) > radioCommentWindow {
			continue
		}
		comments, err := tracker.LoadComments(issue.FilePath)
		if err != nil {
			continue
		}
		for _, c := range comments {
			kind, text, ok := radioKind(c.Text)
			if !ok {
				continue
			}
			at, ok := tracker.CommentTime(c.Date)
			if !ok || now.Sub(at) > radioCommentWindow {
				continue
			}
			author := tracker.CommentAuthor(c)
			if author == "" {
				author = c.Source
			}
			initials, tone := radioVoice(p, author)
			all = append(all, dated{at, WbMessage{
				From: author, To: issue.Slug, URL: prefix + "/issue/" + issue.Slug, Initials: initials, Tone: tone,
				Kind: kind, When: agoText(now.Sub(at)), Text: clipRunes(text, 400),
			}})
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].at.After(all[j].at) })
	var out []WbMessage
	for i := 0; i < len(all) && i < n; i++ {
		out = append(out, all[i].msg)
	}
	return out
}

// radioKind is a Radio comment's kind and its text after the prefix; ok is
// false for any other comment.
func radioKind(text string) (kind, rest string, ok bool) {
	text = strings.TrimSpace(text)
	for _, k := range radioKinds {
		if len(text) > len(k) && strings.EqualFold(text[:len(k)+1], k+":") {
			return k, strings.TrimSpace(text[len(k)+1:]), true
		}
	}
	return "", "", false
}

// radioVoice is the avatar and tone for a comment's author: the CEO and
// department leads speak as leads, the owner as you, anyone else as an
// agent.
func radioVoice(p *tracker.Project, author string) (initials, tone string) {
	switch {
	case author == tracker.LeadSessionName(p):
		return "CEO", "ceo"
	case p.DepartmentOfLead(author) != nil:
		return strings.ToUpper(string([]rune(p.DepartmentOfLead(author).Name)[:1])) + "L", "ceo"
	case author == tracker.OwnerAuthor:
		return "You", "you"
	default:
		return "W", "agent"
	}
}
