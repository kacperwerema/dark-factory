package main

import (
	"time"

	"github.com/michal-franc/issue-viewer/internal/connections"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// Handoffs in the rooms (issue-cli handoff, internal/connections/handoff.go):
// each room's Handoffs block lists what it asked others for (Asked), what
// others asked of it (Inbox) and, for HQ and a group, what waits for its
// forward (To forward), each with its state. The connection lines carry the
// same items with their state (connections_view.go).

// roomHandoffsMax is how many rows each list shows before "+N more".
const roomHandoffsMax = 3

// WbHandoffs is a room's Handoffs block; nil when nothing was handed off
// to or from the room.
type WbHandoffs struct {
	Forward, Asked, Inbox             []WbHandoffRow
	MoreForward, MoreAsked, MoreInbox int
	// OutURL and InURL are the connections drill-in filtered on the box.
	OutURL, InURL string
}

// WbHandoffRow is one handoff on a room's list.
type WbHandoffRow struct {
	Slug, Title, URL string
	// Other is the box at the other end: who it went to (Asked) or came
	// from (Inbox, To forward).
	Other string
	State string
	Word  string
	// Tone is the chip's colour: green done, red declined, idle parked.
	Tone string
}

func handoffTone(state string) string {
	switch state {
	case connections.StateDone:
		return "green"
	case connections.StateDeclined:
		return "red"
	case connections.StateParked:
		return "idle"
	}
	return ""
}

// roomHandoffs builds every box's block from the project's handoffs.
func roomHandoffs(p *tracker.Project, prefix string, issues []*tracker.Issue, now time.Time) map[string]*WbHandoffs {
	all := connections.ListHandoffs(p, issues, func(i *tracker.Issue) []tracker.Comment {
		c, _ := tracker.LoadComments(i.FilePath)
		return c
	}, connections.HandoffFilter{}, now)
	out := map[string]*WbHandoffs{}
	get := func(box string) *WbHandoffs {
		b := out[box]
		if b == nil {
			b = &WbHandoffs{OutURL: connURL(prefix, box, "", connections.Request), InURL: connURL(prefix, "", box, connections.Request)}
			out[box] = b
		}
		return b
	}
	row := func(h connections.Handoff, other string) WbHandoffRow {
		return WbHandoffRow{Slug: h.Slug, Title: h.Title, URL: prefix + "/issue/" + h.Slug, Other: connections.BoxName(p, other), State: h.State, Word: h.Word, Tone: handoffTone(h.State)}
	}
	add := func(list *[]WbHandoffRow, more *int, r WbHandoffRow) {
		if len(*list) < roomHandoffsMax {
			*list = append(*list, r)
		} else {
			*more++
		}
	}
	for _, h := range all {
		a := get(h.From)
		add(&a.Asked, &a.MoreAsked, row(h, h.To))
		in := get(h.To)
		add(&in.Inbox, &in.MoreInbox, row(h, h.From))
		if h.State == connections.StateWaiting && h.Via != "" {
			f := get(h.Via)
			add(&f.Forward, &f.MoreForward, row(h, h.From))
		}
	}
	return out
}

// addRoomHandoffs sets a room's block, and its group children's.
func addRoomHandoffs(room *WbRoomDept, blocks map[string]*WbHandoffs) {
	room.Handoffs = blocks[room.Box]
	for i := range room.Children {
		addRoomHandoffs(&room.Children[i], blocks)
	}
}

// handoffListView is one list of a room's Handoffs block, for the
// wb/handoff-list template.
type handoffListView struct {
	Key, Label string
	Rows       []WbHandoffRow
	More       int
	URL        string
}

func handoffList(key, label string, rows []WbHandoffRow, more int, url string) handoffListView {
	return handoffListView{Key: key, Label: label, Rows: rows, More: more, URL: url}
}
