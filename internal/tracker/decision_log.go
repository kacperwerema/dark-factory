package tracker

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
	"syscall"
	"time"
)

// The decisions audit log: <issues>/.decisions/log.jsonl, one JSON event
// per line, appended under <issues>/.decisions/log.lock and never
// rewritten. Every D-n write appends its events as "pending" while it holds
// the issue's lock (so the ref and the old group are the ones it splices),
// then the same events as "done", or "failed" when the write did not land.
// Readers count only writes that have a done. The log holds refs,
// attribute groups, names and comment ids: never a decision's text or
// quote, which stay in the issue file.

// Decision log events.
const (
	DecisionRecorded  = "recorded"
	DecisionAmended   = "amended"
	DecisionConfirmed = "confirmed"
	// A replaced decision's event is DecisionReplaced ("replaced"), a
	// briefing's DecisionInjected ("injected").

	// DecisionHeld is a ledger rule holding a real transition on a ref
	// (refused, or passed under YOLO): RecordHolds.
	DecisionHeld = "held"
	// A challenge to a decision and its resolution (issue-cli decision
	// challenge / resolve): ref is the decision, issue the challenger's.
	DecisionChallenged = "challenged"
	DecisionResolved   = "resolved"

	DecisionOpPending = "pending"
	DecisionOpDone    = "done"
	DecisionOpFailed  = "failed"
)

// Who made a decision write.
const (
	ActorOwnerShell   = "owner-shell"   // the owner's own terminal (no lead or worker session)
	ActorOwnerBrowser = "owner-browser" // the viewer, signed in as the owner
	ActorLeadRelay    = "lead-relay"    // a lead relaying the owner's words (--quote)
	ActorAgent        = "agent"         // a worker or lead acting on its own (soft decisions)
)

// DecisionEvent is one line of the decisions audit log.
type DecisionEvent struct {
	Write   string `json:"write"`
	Op      string `json:"op"`
	Event   string `json:"event"`
	Ref     string `json:"ref"`
	At      string `json:"at"`
	By      string `json:"by,omitempty"`
	Role    string `json:"role,omitempty"`
	Old     string `json:"old,omitempty"`
	New     string `json:"new,omitempty"`
	ByRef   string `json:"by_ref,omitempty"`
	Comment int    `json:"comment,omitempty"`
	Error   string `json:"error,omitempty"`
	// An injected event's briefing (start, dispatch, reprompt, lead,
	// department-lead), the briefed issue ("" for a lead) and every ref
	// the block carried, shown or folded.
	Briefing string   `json:"briefing,omitempty"`
	Issue    string   `json:"issue,omitempty"`
	Refs     []string `json:"refs,omitempty"`
	// A held event's rule (decisions_cited, citations_current…), the
	// transition it held (From → To) and Passed when YOLO let it through.
	Rule   string `json:"rule,omitempty"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	Passed bool   `json:"passed,omitempty"`
	// A challenged event's reason and proposal, one line each: the
	// challenger's words, never the decision's text or quote.
	Why     string `json:"why,omitempty"`
	Propose string `json:"propose,omitempty"`
}

// DecisionActor is who runs a decision write.
type DecisionActor struct {
	Name string
	Role string
}

// DecisionRelay is a lead relaying the owner's words for an owner act: the
// write posts a decision: comment on the issue in the same atomic write.
type DecisionRelay struct {
	Lead  string
	Quote string
	YOLO  bool
}

// DecisionWrite says where a decision write is logged and who runs it. An
// empty IssueDir logs nothing (tests and fixtures).
type DecisionWrite struct {
	IssueDir string
	Actor    DecisionActor
	Relay    *DecisionRelay
	// OwnerWords are the owner's own words on an act they ran themselves
	// (their shell or browser: renew, let go): without a Relay the write
	// posts them as a decision: comment, so the line's quote stays as is.
	OwnerWords string
}

// DecisionLogDir is <issues>/.decisions.
func DecisionLogDir(issueDir string) string { return filepath.Join(issueDir, ".decisions") }

// DecisionLogPath is <issues>/.decisions/log.jsonl.
func DecisionLogPath(issueDir string) string {
	return filepath.Join(DecisionLogDir(issueDir), "log.jsonl")
}

// DecisionLogWait is how long an append waits for another writer.
var DecisionLogWait = 10 * time.Second

// AppendDecisionEvents appends events to the log under its lock.
func AppendDecisionEvents(issueDir string, evs []DecisionEvent) error {
	if len(evs) == 0 {
		return nil
	}
	dir := DecisionLogDir(issueDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "log.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close()
	deadline := time.Now().Add(DecisionLogWait)
	for {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return err
		}
		if time.Now().After(deadline) {
			return errors.New("the decisions log is being written by another issue-cli; try again")
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	var b strings.Builder
	for _, ev := range evs {
		line, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	f, err := os.OpenFile(DecisionLogPath(issueDir), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(b.String()); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// ReadDecisionLogAll is every event in the log, in order, whatever its op.
func ReadDecisionLogAll(issueDir string) ([]DecisionEvent, error) {
	f, err := os.Open(DecisionLogPath(issueDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []DecisionEvent
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var ev DecisionEvent
		if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Write != "" {
			out = append(out, ev)
		}
	}
	return out, sc.Err()
}

// ReadDecisionLog is the done events: the writes that landed, in order.
func ReadDecisionLog(issueDir string) ([]DecisionEvent, error) {
	all, err := ReadDecisionLogAll(issueDir)
	var out []DecisionEvent
	for _, ev := range all {
		if ev.Op == DecisionOpDone {
			out = append(out, ev)
		}
	}
	return out, err
}

func newWriteID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// decisionChange is what one D-n write does to an issue: the new body, the
// log events (ref, old and new filled) and, for a relayed owner act, what
// the decision: comment says happened to which ref.
type decisionChange struct {
	Body        string
	Events      []DecisionEvent
	RelayRef    string
	RelayAction string
}

// writeDecision runs one D-n write: under the issue's lock it computes the
// change, appends its pending events, adds the relay comment, and writes
// body and comments atomically; then it appends done (or failed).
func writeDecision(filePath string, w DecisionWrite, now time.Time, change func(body string) (decisionChange, error)) error {
	id := newWriteID()
	var logged []DecisionEvent
	_, _, err := UpdateIssueBodyWith(filePath, func(body string, comments []Comment) (string, []Comment, bool, error) {
		c, err := change(body)
		if err != nil {
			return "", nil, false, err
		}
		commentID := 0
		var cm *Comment
		switch {
		case c.RelayRef == "":
		case w.Relay != nil:
			cm = &Comment{Text: DecisionRelayComment(c.RelayRef, c.RelayAction, *w.Relay), Author: w.Relay.Lead}
		case oneLine(w.OwnerWords) != "":
			cm = &Comment{Text: DecisionOwnerComment(c.RelayRef, c.RelayAction, w.OwnerWords), Author: OwnerAuthor}
		}
		if cm != nil {
			cm.ID, cm.Date, cm.Status, cm.Source = NextCommentID(comments), now.Format(time.RFC3339), "open", "cli"
			comments = append(comments, *cm)
			commentID = cm.ID
		}
		evs := make([]DecisionEvent, len(c.Events))
		for i, ev := range c.Events {
			ev.Write, ev.At, ev.By, ev.Role, ev.Comment = id, now.Format(time.RFC3339), w.Actor.Name, w.Actor.Role, commentID
			evs[i] = ev
		}
		if w.IssueDir != "" {
			if err := AppendDecisionEvents(w.IssueDir, withOp(evs, DecisionOpPending, "")); err != nil {
				return "", nil, false, fmt.Errorf("decisions log: %w", err)
			}
			logged = evs
		}
		if afterDecisionPending != nil {
			afterDecisionPending()
		}
		return c.Body, comments, true, nil
	})
	if len(logged) > 0 {
		op, msg := DecisionOpDone, ""
		if err != nil {
			op, msg = DecisionOpFailed, err.Error()
		}
		if lerr := AppendDecisionEvents(w.IssueDir, withOp(logged, op, msg)); lerr != nil && err == nil {
			err = fmt.Errorf("the decision is written, but its done event is not in the decisions log: %w", lerr)
		}
	}
	return err
}

// afterDecisionPending runs between the pending event and the write; tests
// use it to make the write fail there.
var afterDecisionPending func()

func withOp(evs []DecisionEvent, op, msg string) []DecisionEvent {
	out := make([]DecisionEvent, len(evs))
	for i, ev := range evs {
		ev.Op, ev.Error = op, msg
		out[i] = ev
	}
	return out
}

// DecisionRelayPrefix starts the comment a lead's relayed owner act posts.
const DecisionRelayPrefix = "decision:"

// DecisionRelayComment is the decision: comment for a relayed owner act:
//
//	decision: <ref> <action>, "<owner's words>" — relayed by <lead>[ (YOLO)]
func DecisionRelayComment(ref, action string, r DecisionRelay) string {
	text := fmt.Sprintf("%s %s %s, %q — relayed by %s", DecisionRelayPrefix, ref, action, oneLine(r.Quote), r.Lead)
	if r.YOLO {
		text += " (YOLO)"
	}
	return text
}

// DecisionOwnerComment is the decision: comment for an act the owner ran
// themselves with their words (no relay, so no YOLO review entry):
//
//	decision: <ref> <action>, "<owner's words>" — the owner
func DecisionOwnerComment(ref, action, words string) string {
	return fmt.Sprintf("%s %s %s, %q — the owner", DecisionRelayPrefix, ref, action, oneLine(words))
}

// ParsedDecisionRelay is a parsed decision: comment.
type ParsedDecisionRelay struct {
	Ref    string
	Action string
	Lead   string
	YOLO   bool
}

// ParseDecisionRelayComment reads a decision: comment; ok false for any
// other text.
func ParseDecisionRelayComment(text string) (ParsedDecisionRelay, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(text), DecisionRelayPrefix+" ")
	if !ok {
		return ParsedDecisionRelay{}, false
	}
	var p ParsedDecisionRelay
	if p.YOLO = strings.HasSuffix(rest, " (YOLO)"); p.YOLO {
		rest = strings.TrimSuffix(rest, " (YOLO)")
	}
	i := strings.LastIndex(rest, " — relayed by ")
	if i < 0 {
		return ParsedDecisionRelay{}, false
	}
	p.Lead = rest[i+len(" — relayed by "):]
	rest = rest[:i]
	ref, action, _ := strings.Cut(rest, " ")
	if !strings.Contains(ref, "#D-") {
		return ParsedDecisionRelay{}, false
	}
	p.Ref = ref
	if q := strings.LastIndex(action, `, "`); q >= 0 {
		action = action[:q]
	}
	p.Action = action
	return p, true
}
