package partner

import (
	"fmt"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The gate bridge: the owner answers a partner's gate in Dark Factory, the
// answer is a D-n on the issue whose ref names the stage
// (partner-approved:<stage> or partner-sent-back:<stage>), and the worker
// writes the partner's own approval only quoting that D-n. An adapter that
// can list the partner's approvals (Approver) lets Unpaired flag a gated
// stage approved at the partner with no owner decision behind it
// (docs/departments.md#the-owners-answer).

func init() {
	tracker.PartnerApprovalHow = ApprovalHow
}

// Ref prefixes of the owner's answer to a partner's gate.
const (
	ApprovedRefPrefix = "partner-approved:"
	SentBackRefPrefix = "partner-sent-back:"
)

// askLayout stamps which ask an answer answers: the stage's checkpoint
// time, in UTC, in the ref after an @.
const askLayout = "20060102T150405Z"

// ApprovedRef is the ref an owner's approval of stage carries; asked is
// the checkpoint time of the ask it answers (zero: none known).
func ApprovedRef(stage string, asked time.Time) string {
	return ApprovedRefPrefix + askKey(stage, asked)
}

// SentBackRef is the ref an owner's send-back of stage carries.
func SentBackRef(stage string, asked time.Time) string {
	return SentBackRefPrefix + askKey(stage, asked)
}

func askKey(stage string, asked time.Time) string {
	stage = strings.TrimSpace(stage)
	if asked.IsZero() {
		return stage
	}
	return stage + "@" + asked.UTC().Format(askLayout)
}

// parseAsk splits "pitch@20261101T102000Z" into the stage and the ask's
// time (zero without one).
func parseAsk(key string) (string, time.Time) {
	stage, at, ok := strings.Cut(key, "@")
	if !ok {
		return stage, time.Time{}
	}
	t, _ := time.Parse(askLayout, at)
	return stage, t
}

// IsAnswerRef reports whether ref is an owner's answer to a partner gate:
// recording one is the owner's act.
func IsAnswerRef(ref string) bool {
	ref = strings.TrimSpace(ref)
	return strings.HasPrefix(ref, ApprovedRefPrefix) || strings.HasPrefix(ref, SentBackRefPrefix)
}

// Approval is one stage of a partner project with its gate flags.
type Approval struct {
	Stage string
	// Gated: the partner asks for the human's approval at this stage.
	Gated bool
	// Approved: the partner's own approval flag is set on the stage.
	Approved bool
	// At is the stage's checkpoint time; zero when unknown.
	At time.Time
}

// Approver is an adapter that can list a project's gated stages and the
// approvals written at the partner, and says in its own words how a worker
// writes one.
type Approver interface {
	// Approvals lists project's stages that have a checkpoint, in the
	// pipeline's order. A project that isn't there is nil with no error.
	Approvals(cfg tracker.Partner, dir, project string) ([]Approval, error)
	// ApprovalHow is how the worker writes the partner's approval of stage,
	// quoting the owner's decision ref.
	ApprovalHow(stage, ref string) string
}

// ErrNoApprovals is an adapter that can't list approvals: nothing to pair.
var ErrNoApprovals = fmt.Errorf("the partner's adapter can't list its approvals")

// Approvals reads issue's partner approvals through its adapter.
func Approvals(cfg tracker.Partner, root string, issue *tracker.Issue) ([]Approval, error) {
	id, err := ProjectID(issue)
	if err != nil {
		return nil, err
	}
	a, err := For(cfg)
	if err != nil {
		return nil, err
	}
	ap, ok := a.(Approver)
	if !ok {
		return nil, ErrNoApprovals
	}
	dir := cfg.Dir(root)
	if dir == "" || !isDir(dir) {
		return nil, fmt.Errorf("partner %s: path %s is missing", cfg.Label(), dir)
	}
	return ap.Approvals(cfg, dir, id)
}

// ApprovalHow is cfg's adapter's words for writing the approval of stage
// quoting ref; a generic line for an adapter that has none.
func ApprovalHow(cfg tracker.Partner, stage, ref string) string {
	if a, err := For(cfg); err == nil {
		if ap, ok := a.(Approver); ok {
			return ap.ApprovalHow(stage, ref)
		}
	}
	return fmt.Sprintf("write %s's approval of %s the way its guide says, quoting %s", cfg.Label(), stage, ref)
}

// Answer is the owner's answer to one stage on the issue.
type Answer struct {
	Stage string
	// Asked is the checkpoint time of the ask it answers; zero when the
	// ref names none.
	Asked time.Time
	// Approved, else sent back.
	Approved bool
	Ref      string // the decision's ref, "<slug>#D-n"
	Num      int
	Quote    string
}

// Answers are the owner's answers to partner gates among decisions, in
// file order. Deferred decisions don't count.
func Answers(decisions []tracker.Decision) []Answer {
	var out []Answer
	for _, d := range decisions {
		if d.Deferred {
			continue
		}
		for _, ref := range d.Refs {
			ref = strings.TrimSpace(ref)
			a := Answer{Ref: d.Ref, Num: d.Num, Quote: d.Quote}
			var key string
			switch {
			case strings.HasPrefix(ref, ApprovedRefPrefix):
				key, a.Approved = strings.TrimPrefix(ref, ApprovedRefPrefix), true
			case strings.HasPrefix(ref, SentBackRefPrefix):
				key = strings.TrimPrefix(ref, SentBackRefPrefix)
			default:
				continue
			}
			a.Stage, a.Asked = parseAsk(key)
			out = append(out, a)
		}
	}
	return out
}

// AnswerTo is the owner's newest answer to the ask of stage written at
// asked (the awaiting checkpoint's time); ok is false when that ask has no
// answer yet. An answer naming no time answers any ask of its stage.
func AnswerTo(answers []Answer, stage string, asked time.Time) (Answer, bool) {
	var best Answer
	found := false
	for _, a := range answers {
		if a.Stage != stage || (!a.Asked.IsZero() && !asked.IsZero() && !a.Asked.Equal(asked.UTC().Truncate(time.Second))) {
			continue
		}
		if !found || a.Num > best.Num {
			best, found = a, true
		}
	}
	return best, found
}

// Unpaired are the gated stages approved at the partner with no owner
// approval among decisions: the worker wrote the partner's approval
// without the owner's D-n. Any approve decision for the stage pairs it
// (the partner re-writes the checkpoint when it records the approval, so
// the ask's time can't be matched here).
func Unpaired(approvals []Approval, decisions []tracker.Decision) []Approval {
	approved := map[string]bool{}
	for _, a := range Answers(decisions) {
		if a.Approved {
			approved[a.Stage] = true
		}
	}
	var out []Approval
	for _, a := range approvals {
		if a.Gated && a.Approved && !approved[a.Stage] {
			out = append(out, a)
		}
	}
	return out
}

// UnpairedLine words one unpaired approval for the pages, lint and
// issue-cli.
func UnpairedLine(cfg tracker.Partner, a Approval) string {
	return fmt.Sprintf("%s: %s approved at the partner with no owner decision (%s)", cfg.Label(), a.Stage, ApprovedRefPrefix+a.Stage)
}

// Bridge is the gate bridge's view of one issue: the owner's answer to the
// stage awaiting them, if any, and the approvals at the partner with no
// owner decision.
type Bridge struct {
	// Answered is the owner's answer to the awaiting stage's ask: it then
	// waits on the worker, not the owner.
	Answered *Answer
	// Unpaired are UnpairedLine's words for each unpaired approval.
	Unpaired []string
}

// BridgeOf reads issue's bridge state for st, the status Read returned.
// An adapter without Approver has no Unpaired.
func BridgeOf(cfg tracker.Partner, root string, issue *tracker.Issue, st Status) Bridge {
	var b Bridge
	if st.State == StateNone || st.State == "" {
		return b
	}
	decisions := issue.Decisions()
	if st.Awaiting() {
		if a, ok := AnswerTo(Answers(decisions), st.Stage, st.Since); ok {
			b.Answered = &a
		}
	}
	if approvals, err := Approvals(cfg, root, issue); err == nil {
		for _, a := range Unpaired(approvals, decisions) {
			b.Unpaired = append(b.Unpaired, UnpairedLine(cfg, a))
		}
	}
	return b
}

// AnsweredLine is Line for a stage the owner answered, which waits on the
// worker: "OpenMontage: pitch · approved by you" or "… · sent back by you".
func AnsweredLine(cfg tracker.Partner, stage string, a Answer) string {
	if a.Approved {
		return cfg.Label() + ": " + stage + " · approved by you"
	}
	return cfg.Label() + ": " + stage + " · sent back by you"
}
