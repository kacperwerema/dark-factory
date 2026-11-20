// Package leadstate keeps a lead's compact state file: what the lead needs
// to go on (live workers, the queue head, the owner's open asks, today's
// decisions and memories, the background shells it waits on, its notes) in
// a few kilobytes, so a fresh lead session can start from it instead of a
// long conversation (docs/patterns/lead-session.md#the-state-file).
//
// The file is rendered, never edited: the board part comes from the tick on
// every run and the lead's own part (notes, asks, watches) from a JSON
// store beside it.
package leadstate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/gitfile"
	"github.com/kacperwerema/dark-factory/internal/jsonfile"
	"github.com/kacperwerema/dark-factory/internal/viewerstate"
)

// RefillLine: at this many open queue items or fewer the lead's tick says
// the queue wants refilling (the lead's "refill when down to about 5").
const RefillLine = 5

// HeldLong: a start held this long makes the lead's tick say so under
// --watch even when each hold on the way clears by itself.
const HeldLong = time.Hour

// MaxBytes caps the rendered file: about 4 k tokens at 4 bytes a token.
const MaxBytes = 16 * 1024

// lineMax caps one rendered line, so one long note can't eat the file.
const lineMax = 240

// Kinds of note.
const (
	KindNote     = "note"
	KindAsk      = "ask"
	KindTomorrow = "tomorrow"
)

// Note is one thing the lead wrote down: a note, an owner ask (the owner's
// words, open until done) or something to check tomorrow.
type Note struct {
	ID   int       `json:"id"`
	Kind string    `json:"kind"`
	At   time.Time `json:"at"`
	Text string    `json:"text"`
	Done bool      `json:"done,omitempty"`
}

// Watch is a background shell the lead waits on: lead tick --watch or
// lead watch registers itself, and a rotate hands the commands over.
type Watch struct {
	PID     int       `json:"pid"`
	Command string    `json:"command"`
	Started time.Time `json:"started"`
}

// Store is the lead's own part of the state, kept as <lead>.json.
type Store struct {
	Notes   []Note  `json:"notes,omitempty"`
	Watches []Watch `json:"watches,omitempty"`
	NextID  int     `json:"next_id,omitempty"`
	// Tick is the last lead tick --watch this lead ran: its command and,
	// once it exited on its own, when. A tick killed with its session
	// leaves its watch behind with a dead pid instead.
	Tick *TickSeen `json:"tick,omitempty"`
	// Handover is the last rotate's: what the fresh session's first prompt
	// carries while it is fresh.
	Handover *Handover `json:"handover,omitempty"`
	// Held is what the running lead tick --watch --exit-on-news holds for
	// its next news, saved on each tick: a tick that dies without a word
	// (SIGKILL) leaves it here, and the lead's next tick says it.
	Held *HeldLines `json:"held,omitempty"`
}

// HeldLines are the lines a tick held and had not said yet, and its pid.
type HeldLines struct {
	PID   int       `json:"pid"`
	At    time.Time `json:"at"`
	Lines []string  `json:"lines"`
}

// TickSeen is the lead's last background tick: the command it ran and when
// it ended (zero while it runs, or when it died without exiting).
type TickSeen struct {
	Command string    `json:"command"`
	Ended   time.Time `json:"ended,omitempty"`
	// Signal names the signal that stopped it ("SIGTERM"), "" when it
	// exited on its own.
	Signal string `json:"signal,omitempty"`
	// PID is the stopped tick's pid, with Signal.
	PID int `json:"pid,omitempty"`
}

// TickGrace is how long after a tick exits on news its lead may go
// without one: the lead acts on the news, then starts the tick again.
const TickGrace = 10 * time.Minute

// IsTick reports whether a watch command is a lead tick --watch (the lead
// command's alias ceo too).
func IsTick(command string) bool {
	f := strings.Fields(command)
	for i := 0; i+1 < len(f); i++ {
		if (f[i] == "lead" || f[i] == "ceo") && f[i+1] == "tick" {
			for _, a := range f[i+2:] {
				if a == "--watch" || a == "-watch" || a == "--watch=true" || a == "-watch=true" {
					return true
				}
			}
			return false
		}
	}
	return false
}

// TickState is what a lead's next call says about its background tick.
type TickState struct {
	// Missing: no tick runs and none ended within TickGrace.
	Missing bool
	// DeadPID is the pid of a tick watch whose process is gone without
	// a word (killed, or the session that ran it ended), 0 when none is.
	DeadPID int
	// Signal names the signal a stopped tick said it was stopped by, ""
	// when none was; PID is that tick's.
	Signal string
	PID    int
	// Command is the last tick's command, "" when none is known.
	Command string
	// Ended is when the last tick exited on its own, zero when unknown.
	Ended time.Time
}

// TickStatus reads whether the lead has a live background tick: a tick
// watch whose process runs is live; one whose process is gone (killed, or
// its session ended) and one stopped by a signal are said at once; a tick
// that exited on its own is given TickGrace before it counts as missing.
func (s Store) TickStatus(alive func(pid int) bool, now time.Time) TickState {
	var st TickState
	if s.Tick != nil {
		st.Command, st.Ended = s.Tick.Command, s.Tick.Ended
		st.Signal, st.PID = s.Tick.Signal, s.Tick.PID
	}
	for _, w := range s.Watches {
		if !IsTick(w.Command) {
			continue
		}
		if alive(w.PID) {
			return TickState{Command: w.Command}
		}
		st.DeadPID, st.Command = w.PID, w.Command
	}
	if st.DeadPID == 0 && st.Signal == "" && !st.Ended.IsZero() && now.Sub(st.Ended) < TickGrace {
		return st
	}
	st.Missing = true
	return st
}

// Handover is what a rotate or a flush close leaves for the fresh session:
// when, the note and digest (a flush: its summary) it wrote, the state as it
// was, and the watches the old session ran (they die with it, so the fresh
// one starts them again).
type Handover struct {
	At time.Time `json:"at"`
	// By is what closed the old session: "" or ByRotate for lead rotate,
	// ByFlush for the nightly lead flush.
	By      string  `json:"by,omitempty"`
	Note    string  `json:"note"`
	Digest  string  `json:"digest"`
	State   string  `json:"state"`
	Watches []Watch `json:"watches,omitempty"`
	// Since is where the next rotate's digest starts.
	Since time.Time `json:"since"`
}

// Handover.By values.
const (
	ByRotate = "rotate"
	ByFlush  = "flush"
)

// HandoverFresh is how long after a rotate a spawn counts as its fresh
// session: the finisher waits for the old turn to end (30 minutes at most)
// and then starts it.
const HandoverFresh = 2 * time.Hour

// Dir is where state files live, "" without a state dir.
func Dir() string {
	d := viewerstate.Dir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "lead")
}

// Path is lead's state file (<lead>.md), "" without a state dir.
func Path(lead string) string {
	if d := Dir(); d != "" {
		return filepath.Join(d, lead+".md")
	}
	return ""
}

func storePath(lead string) string {
	if d := Dir(); d != "" {
		return filepath.Join(d, lead+".json")
	}
	return ""
}

// Load reads lead's store; a missing one is empty.
func Load(lead string) (Store, error) {
	var s Store
	p := storePath(lead)
	if p == "" {
		return s, nil
	}
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return s, nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("%s: %w", p, err)
	}
	return s, nil
}

// Save writes lead's store.
func (s Store) Save(lead string) error {
	p := storePath(lead)
	if p == "" {
		return fmt.Errorf("no state dir: set XDG_STATE_HOME or HOME")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return jsonfile.Write(p, s, 0o644)
}

// Add records a note of kind and returns its id.
func (s *Store) Add(kind, text string, now time.Time) int {
	if s.NextID == 0 {
		for _, n := range s.Notes {
			s.NextID = max(s.NextID, n.ID)
		}
	}
	s.NextID++
	s.Notes = append(s.Notes, Note{ID: s.NextID, Kind: kind, At: now, Text: strings.TrimSpace(text)})
	return s.NextID
}

// Done closes note id (an ask or a tomorrow item; a note is dropped).
func (s *Store) Done(id int) error {
	for i := range s.Notes {
		if s.Notes[i].ID != id {
			continue
		}
		if s.Notes[i].Kind == KindNote {
			s.Notes = append(s.Notes[:i], s.Notes[i+1:]...)
			return nil
		}
		s.Notes[i].Done = true
		return nil
	}
	return fmt.Errorf("no note #%d (issue-cli lead state lists them)", id)
}

// Fold takes the plain notes written before today (local calendar days, not
// 24-hour spans) and the done asks and tomorrow items out of the store, and
// returns the plain notes by day ("2006-01-02") for the daily summary.
func (s *Store) Fold(now time.Time) map[string][]Note {
	today := now.Local().Format("2006-01-02")
	out := map[string][]Note{}
	var keep []Note
	for _, n := range s.Notes {
		day := n.At.Local().Format("2006-01-02")
		switch {
		case n.Done && day < today:
		case n.Kind == KindNote && day < today:
			out[day] = append(out[day], n)
		default:
			keep = append(keep, n)
		}
	}
	s.Notes = keep
	return out
}

// Register adds a watch, replacing one with the same pid; a tick is
// remembered as the lead's last tick.
func (s *Store) Register(w Watch) {
	s.Unregister(w.PID)
	s.Watches = append(s.Watches, w)
	if IsTick(w.Command) {
		s.Tick = &TickSeen{Command: w.Command}
	}
}

// Ended drops the watch with pid as one that exited on its own: a tick
// records when, so the lead's next call knows it ended on news and not
// with its session.
func (s *Store) Ended(pid int, now time.Time) {
	for _, w := range s.Watches {
		if w.PID == pid && IsTick(w.Command) {
			s.Tick = &TickSeen{Command: w.Command, Ended: now}
		}
	}
	s.Unregister(pid)
}

// Stopped drops the tick watch with pid as one a signal stopped: the
// lead's next call says so at once, naming sig. A pid no longer among the
// watches (a newer tick stopped it and took its place) changes nothing
// else.
func (s *Store) Stopped(pid int, sig string, now time.Time) {
	for _, w := range s.Watches {
		if w.PID == pid && IsTick(w.Command) {
			s.Tick = &TickSeen{Command: w.Command, Ended: now, Signal: sig, PID: pid}
		}
	}
	s.Unregister(pid)
}

// Unregister drops the watch with pid.
func (s *Store) Unregister(pid int) {
	var keep []Watch
	for _, w := range s.Watches {
		if w.PID != pid {
			keep = append(keep, w)
		}
	}
	s.Watches = keep
}

// Prune drops the watches whose process is gone and reports whether any went.
func (s *Store) Prune(alive func(pid int) bool) bool {
	var keep []Watch
	for _, w := range s.Watches {
		if alive(w.PID) {
			keep = append(keep, w)
		}
	}
	changed := len(keep) != len(s.Watches)
	s.Watches = keep
	return changed
}

// Worker is one live worker session.
type Worker struct {
	Session string
	Slug    string
	Status  string
	// Last is the first line of its newest progress comment.
	Last string
}

// QueueItem is one open queue item.
type QueueItem struct {
	ID    int
	Label string
	Quote string
}

// Ref is a link to a record kept elsewhere: a decision ref or a memory id,
// with a few words of what it says.
type Ref struct {
	ID   string
	Text string
}

// View is the board part of the state, read by the caller.
type View struct {
	Lead    string
	Project string
	At      time.Time
	Workers []Worker
	// Queue is the open items in order; QueueOpen how many are open,
	// Refill the line under which the tick asks for more.
	Queue      []QueueItem
	QueueOpen  int
	Refill     int
	QueueState string
	Decisions  []Ref
	Memories   []Ref
	// OwnerSteps are the open owner steps, one line each (ownerstep.Line):
	// the owner's hand steps a fresh lead must not lose.
	OwnerSteps []string
	// Commands are the issue-cli calls the sections point at, with
	// --project already in them.
	Commands Commands
}

// Commands are the commands a section's "+N more" line and hints name.
type Commands struct {
	Workers, Queue, Status, Decisions, Memories, Note, Ask, Done, History, OwnerSteps string
}

// caps per section; a longer list ends with a "+N more" line.
const (
	capWorkers   = 20
	capQueue     = 5
	capRefs      = 12
	capAsks      = 15
	capTomorrow  = 12
	capWatches   = 10
	capNotesLast = 30
)

// Render is the state file: the view and the store, under MaxBytes. When
// it would pass the cap, the oldest notes go first, then each line is cut.
func Render(v View, s Store) string {
	notes := s.Notes
	for {
		out := render(v, s, notes)
		if len(out) <= MaxBytes {
			return out
		}
		if i := oldestPlain(notes); i >= 0 {
			notes = append(append([]Note{}, notes[:i]...), notes[i+1:]...)
			continue
		}
		cut := out[:MaxBytes-40]
		if i := strings.LastIndex(cut, "\n"); i > 0 {
			cut = cut[:i+1]
		}
		return cut + "(cut at the 16 KB cap)\n"
	}
}

func oldestPlain(notes []Note) int {
	for i, n := range notes {
		if n.Kind == KindNote {
			return i
		}
	}
	return -1
}

func render(v View, s Store, notes []Note) string {
	var b strings.Builder
	at := v.At.Local().Format("2006-01-02 15:04")
	fmt.Fprintf(&b, "# Lead state: %s (%s)\n\nRendered %s by issue-cli lead state; never edit it, it is rewritten on every tick.\n", v.Lead, v.Project, at)

	b.WriteString("\n## Workers\n\n")
	if len(v.Workers) == 0 {
		b.WriteString("None live.\n")
	}
	for i, w := range v.Workers {
		if i == capWorkers {
			more(&b, len(v.Workers)-i, v.Commands.Workers)
			break
		}
		line := fmt.Sprintf("- %s · %s", w.Session, valueOr(w.Status, "?"))
		if w.Last != "" {
			line += " · " + w.Last
		}
		writeLine(&b, line)
	}

	b.WriteString("\n## Queue\n\n")
	if v.QueueState != "" {
		writeLine(&b, v.QueueState)
	}
	fmt.Fprintf(&b, "%d open (refill at %d).\n", v.QueueOpen, v.Refill)
	for i, q := range v.Queue {
		if i == capQueue {
			more(&b, len(v.Queue)-i, v.Commands.Queue)
			break
		}
		line := fmt.Sprintf("- #%d %s", q.ID, q.Label)
		if q.Quote != "" {
			line += fmt.Sprintf(" (owner: %q)", q.Quote)
		}
		writeLine(&b, line)
	}

	b.WriteString("\n## Owner asks\n\n")
	asks := open(notes, KindAsk)
	if len(asks) == 0 {
		b.WriteString("None open.\n")
	}
	list(&b, asks, capAsks, v.Commands.Note)
	ownerSteps(&b, v, capAsks)
	if v.Commands.Status != "" {
		fmt.Fprintf(&b, "Gates and questions that wait on the owner: %s\n", v.Commands.Status)
	}

	b.WriteString("\n## Today\n\n")
	if len(v.Decisions) == 0 && len(v.Memories) == 0 {
		b.WriteString("No decision or lead memory recorded today.\n")
	}
	refs(&b, v.Decisions, v.Commands.Decisions)
	refs(&b, v.Memories, v.Commands.Memories)

	b.WriteString("\n## Watches\n\n")
	if len(s.Watches) == 0 {
		b.WriteString("None running.\n")
	}
	for i, w := range s.Watches {
		if i == capWatches {
			more(&b, len(s.Watches)-i, "")
			break
		}
		writeLine(&b, fmt.Sprintf("- since %s (pid %d): %s", w.Started.Local().Format("01-02 15:04"), w.PID, w.Command))
	}

	b.WriteString("\n## Tomorrow\n\n")
	tom := open(notes, KindTomorrow)
	if len(tom) == 0 {
		b.WriteString("Nothing noted.\n")
	}
	list(&b, tom, capTomorrow, v.Commands.Note)

	b.WriteString("\n## Notes\n\n")
	plain := open(notes, KindNote)
	if len(plain) == 0 {
		b.WriteString("None today.\n")
	}
	if len(plain) > capNotesLast {
		fmt.Fprintf(&b, "(%d older notes in the store)\n", len(plain)-capNotesLast)
		plain = plain[len(plain)-capNotesLast:]
	}
	for _, n := range plain {
		writeLine(&b, fmt.Sprintf("- #%d %s %s", n.ID, n.At.Local().Format("15:04"), n.Text))
	}

	if c := v.Commands; c.Note != "" {
		fmt.Fprintf(&b, "\nAdd: %s · close an ask: %s · older context: %s\n", c.Note, c.Done, c.History)
	}
	return b.String()
}

func open(notes []Note, kind string) []Note {
	var out []Note
	for _, n := range notes {
		if n.Kind == kind && !n.Done {
			out = append(out, n)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

func list(b *strings.Builder, notes []Note, limit int, cmd string) {
	for i, n := range notes {
		if i == limit {
			more(b, len(notes)-i, cmd)
			return
		}
		writeLine(b, fmt.Sprintf("- #%d %s %q", n.ID, n.At.Local().Format("01-02 15:04"), n.Text))
	}
}

func refs(b *strings.Builder, rs []Ref, cmd string) {
	for i, r := range rs {
		if i == capRefs {
			more(b, len(rs)-i, cmd)
			return
		}
		writeLine(b, "- "+r.ID+": "+r.Text)
	}
}

func more(b *strings.Builder, n int, cmd string) {
	if cmd == "" {
		fmt.Fprintf(b, "+%d more\n", n)
		return
	}
	fmt.Fprintf(b, "+%d more: %s\n", n, cmd)
}

func writeLine(b *strings.Builder, s string) {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if r := []rune(s); len(r) > lineMax {
		s = string(r[:lineMax-1]) + "…"
	}
	b.WriteString(s)
	b.WriteString("\n")
}

func valueOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// FoldInto appends notes to the summary file at path under ## Lead notes,
// making the file or the heading when missing.
func FoldInto(path string, notes []Note) error {
	if len(notes) == 0 {
		return nil
	}
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	text := strings.TrimRight(string(old), "\n")
	if !strings.Contains(text, "\n## Lead notes") && !strings.HasPrefix(text, "## Lead notes") {
		if text != "" {
			text += "\n\n"
		}
		text += "## Lead notes"
		text += "\n"
	} else {
		text += "\n"
	}
	for _, n := range notes {
		text += fmt.Sprintf("- %s %s\n", n.At.Local().Format("15:04"), strings.ReplaceAll(n.Text, "\n", " "))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return gitfile.WriteAtomic(path, []byte(text), 0o644)
}

// ownerSteps writes the open owner steps, at most max, with the command
// that prints their bodies.
func ownerSteps(b *strings.Builder, v View, max int) {
	for i, l := range v.OwnerSteps {
		if i == max {
			more(b, len(v.OwnerSteps)-i, v.Commands.OwnerSteps)
			return
		}
		writeLine(b, "- step "+l)
	}
	if len(v.OwnerSteps) > 0 && v.Commands.OwnerSteps != "" {
		writeLine(b, "- the steps' exact text: "+v.Commands.OwnerSteps)
	}
}

// noteCaps keep the handoff note under the flush's 80 lines.
const (
	noteWorkers = 15
	noteQueue   = 5
	noteAsks    = 10
	noteRefs    = 8
	noteNext    = 8
)

// HandoffNote is the handoff note a rotate writes from the state: the
// sections the flush's note has (tracker.CheckHandoffNote), short;
// stateCmd is the command that prints the state file.
func HandoffNote(v View, s Store, stateCmd string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Handoff: %s, %s (rotate)\n", v.Lead, v.At.Local().Format("2006-01-02 15:04"))
	b.WriteString("\n## State\n\n")
	if len(v.Workers) == 0 {
		b.WriteString("No worker live.\n")
	}
	for i, w := range v.Workers {
		if i == noteWorkers {
			more(&b, len(v.Workers)-i, v.Commands.Workers)
			break
		}
		writeLine(&b, fmt.Sprintf("- %s · %s", w.Session, valueOr(w.Status, "?")))
	}
	for _, w := range s.Watches {
		writeLine(&b, "- watch: "+w.Command)
	}
	b.WriteString("\n## Queue\n\n")
	fmt.Fprintf(&b, "%d open.\n", v.QueueOpen)
	for i, q := range v.Queue {
		if i == noteQueue {
			break
		}
		writeLine(&b, fmt.Sprintf("- #%d %s", q.ID, q.Label))
	}
	b.WriteString("\n## Waiting on the owner\n\n")
	asks := open(s.Notes, KindAsk)
	if len(asks) == 0 {
		b.WriteString("No open ask noted.\n")
	}
	for i, n := range asks {
		if i == noteAsks {
			more(&b, len(asks)-i, "")
			break
		}
		writeLine(&b, fmt.Sprintf("- %q", n.Text))
	}
	ownerSteps(&b, v, noteAsks)
	if v.Commands.Status != "" {
		writeLine(&b, "- gates and questions: "+v.Commands.Status)
	}
	b.WriteString("\n## Rules in force\n\n")
	all := append(append([]Ref{}, v.Decisions...), v.Memories...)
	for i, r := range all {
		if i == noteRefs {
			more(&b, len(all)-i, v.Commands.Decisions)
			break
		}
		writeLine(&b, "- "+r.ID+": "+r.Text)
	}
	if v.Commands.Memories != "" {
		writeLine(&b, "- the lead's rules: "+v.Commands.Memories)
	}
	b.WriteString("\n## How to resume\n\n")
	writeLine(&b, "- Read the state file first: "+stateCmd)
	b.WriteString("- Start the watches in the state file again (Bash run_in_background).\n")
	if v.Commands.History != "" {
		writeLine(&b, "- Older context: "+v.Commands.History)
	}
	b.WriteString("\n## Next\n\n")
	tom := open(s.Notes, KindTomorrow)
	for i, n := range tom {
		if i == noteNext {
			break
		}
		writeLine(&b, fmt.Sprintf("%d. %s", i+1, n.Text))
	}
	fmt.Fprintf(&b, "%d. Keep the tick running.\n", min(len(tom), noteNext)+1)
	return b.String()
}

// FreshHandover is lead's last handover while a spawn still counts as its
// fresh session, else nil.
func FreshHandover(lead string, now time.Time) *Handover {
	s, err := Load(lead)
	if err != nil || s.Handover == nil || now.Sub(s.Handover.At) > HandoverFresh || now.Before(s.Handover.At.Add(-time.Minute)) {
		return nil
	}
	return s.Handover
}

// FromFlush reports a handover the nightly lead flush left.
func (h *Handover) FromFlush() bool { return h != nil && h.By == ByFlush }

// HandoverSection is the part of a fresh lead's first prompt after a
// rotate or a flush close: the state file as it was, the watches to start
// again and where older context is (historyCmd, with --project in it). ""
// when no handover is fresh. After a flush the note and summary come before
// it (tracker.LeadHandoffSection), so it heads its own section.
func HandoverSection(lead, historyCmd string, now time.Time) string {
	h := FreshHandover(lead, now)
	if h == nil {
		return ""
	}
	var b strings.Builder
	if h.FromFlush() {
		b.WriteString("\n\n## Your state file\n\n")
		fmt.Fprintf(&b, "You are a fresh session of %s, started by the nightly lead flush at %s after it closed the last one. Your state file (%s) as it was:\n\n", lead, h.At.Local().Format("15:04"), Path(lead))
	} else {
		b.WriteString("\n\n## Where the last lead left off\n\n")
		fmt.Fprintf(&b, "You are a fresh session of %s, started by issue-cli lead rotate at %s. Your state file (%s) as it was:\n\n", lead, h.At.Local().Format("15:04"), Path(lead))
	}
	b.WriteString(strings.TrimSpace(h.State))
	b.WriteString("\n")
	if len(h.Watches) > 0 {
		b.WriteString("\n## Restart these watches\n\nThe old session's background shells ended with it. Start each again with Bash run_in_background:\n\n")
		for _, w := range h.Watches {
			fmt.Fprintf(&b, "- %s\n", w.Command)
		}
	}
	if h.FromFlush() {
		fmt.Fprintf(&b, "\nThe handoff note is %s, the summary of the day's chat %s (both above). Look older context up with %s.\n", h.Note, h.Digest, historyCmd)
	} else {
		fmt.Fprintf(&b, "\nThe handoff note is %s, the digest of the old chat %s. Don't read them whole: look up what you need with %s.\n", h.Note, h.Digest, historyCmd)
	}
	return b.String()
}
