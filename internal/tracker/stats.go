package tracker

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/jsonfile"
	"github.com/kacperwerema/dark-factory/internal/tokens"
)

// TransitionStat is one recorded workflow transition with its estimated token
// cost. ActualTokens is reserved for a future hybrid pass that records the
// measured agent-run token count; nil means "not yet captured".
type TransitionStat struct {
	From          string    `json:"from"`
	To            string    `json:"to"`
	TS            time.Time `json:"ts"`
	StaticTokens  int       `json:"static_tokens"`
	DynamicTokens int       `json:"dynamic_tokens"`
	ActualTokens  *int      `json:"actual_tokens"`

	// ApprovedAt is when the viewer recorded the human approval this
	// transition consumed. Nil when the transition needed no approval or the
	// approval was not set through the viewer.
	ApprovedAt *time.Time `json:"approved_at,omitempty"`

	// BranchHead is the work/<slug> branch head when the transition ran, so
	// the issue's commits stay reachable after the branch is deleted. Empty
	// without the branch.
	BranchHead string `json:"branch_head,omitempty"`
	// MergeSHA is set on the move to done: the project checkout's HEAD when
	// it contains BranchHead, i.e. the commit local CI tests after the merge.
	MergeSHA string `json:"merge_sha,omitempty"`
}

// ApprovalRecord is the most recent approval the viewer granted.
type ApprovalRecord struct {
	Status string    `json:"status"`
	At     time.Time `json:"at"`
}

// Dispatch is one launch of an agent on the issue by the viewer.
type Dispatch struct {
	At      time.Time `json:"at"`
	Session string    `json:"session"`
	Agent   string    `json:"agent"`
	// Model is the model passed with --model; "" when none was (the
	// CLI's own default, or a record from before models were recorded).
	Model string `json:"model,omitempty"`
	// BaseBranch and BaseSHA are what the work branch was cut from, set only
	// by the dispatch that created it (a redispatch reuses the branch).
	BaseBranch string `json:"base_branch,omitempty"`
	BaseSHA    string `json:"base_sha,omitempty"`
}

// DispatchBase returns the first dispatch that recorded a base: the one that
// cut the issue's work branch. ok is false when none did.
func DispatchBase(store StatsStore) (d Dispatch, ok bool) {
	for _, d := range store.Dispatches {
		if d.BaseSHA != "" {
			return d, true
		}
	}
	return Dispatch{}, false
}

// WorkerAgent is the agent CLI of the newest launch of issue slug's worker,
// "" when it was never dispatched or the record has no agent. Other launches
// on the issue (a post-mortem, say) sit in other sessions and don't count.
func WorkerAgent(store StatsStore, slug string) string {
	session := AgentSessionName(slug)
	for i := len(store.Dispatches) - 1; i >= 0; i-- {
		if d := store.Dispatches[i]; d.Session == session && d.Agent != "" {
			return d.Agent
		}
	}
	return ""
}

// Model labels for WorkerModel.
const (
	// ModelUnnamed is a worker started without --model: the CLI's or the
	// project's own default, or a record from before models were recorded.
	ModelUnnamed = "default"
	// ModelMixed is a worker whose launches ran on different models.
	ModelMixed = "mixed"
)

// WorkerModel is the model issue slug's worker ran on, over its launches:
// the one name they share, ModelMixed when they differ, ModelUnnamed when
// none passed --model; "" when the worker was never dispatched.
func WorkerModel(store StatsStore, slug string) string {
	session := AgentSessionName(slug)
	model, seen := "", false
	for _, d := range store.Dispatches {
		if d.Session != session {
			continue
		}
		m := strings.TrimSpace(d.Model)
		if m == "" {
			m = ModelUnnamed
		}
		switch {
		case !seen:
			model, seen = m, true
		case m != model:
			return ModelMixed
		}
	}
	return model
}

// RedRoundCount is how many red rounds the issue's recorded check results
// hold: the reds of one check at one branch head are one round (a retry
// without a commit is no fix round); a red with no head is its own.
func RedRoundCount(rounds []RoundResult) int {
	seen := map[string]bool{}
	n := 0
	for _, r := range rounds {
		if !r.Red {
			continue
		}
		if r.Head == "" {
			n++
			continue
		}
		if k := r.Check + "\x00" + r.Head; !seen[k] {
			seen[k] = true
			n++
		}
	}
	return n
}

// StatsStore is the per-issue workflow-stats sidecar persisted as
// <issue>.stats.json next to the markdown file.
type StatsStore struct {
	Transitions []TransitionStat `json:"transitions"`
	// LastApproval is scratch state consumed by the next transition row that
	// uses the approval. Sidecars written before v0.49.0 may also hold
	// pending_wait and per-row wait_started_at (from the removed --wait);
	// encoding/json ignores them on load and the next save drops them.
	LastApproval *ApprovalRecord `json:"last_approval,omitempty"`
	// Dispatches are the agent launches on this issue, oldest first; the
	// first is where the issue's agent work starts.
	Dispatches []Dispatch `json:"dispatches,omitempty"`
	// Rounds are the issue's check results, oldest first, that
	// internal/redrounds counts into red rounds.
	Rounds []RoundResult `json:"rounds,omitempty"`
	// Briefings are the briefings sent to the issue's agent, oldest first
	// (RecordBriefing): size and ids only.
	Briefings []Briefing `json:"briefings,omitempty"`
}

// StatsSidecarPath returns the stats sidecar path for an issue markdown file.
// Mirrors the convention used by SidecarPath for the data-table sidecar:
// foo/42.md → foo/42.stats.json.
func StatsSidecarPath(issuePath string) string {
	if strings.HasSuffix(issuePath, ".md") {
		return strings.TrimSuffix(issuePath, ".md") + ".stats.json"
	}
	return issuePath + ".stats.json"
}

// LoadStats reads the stats sidecar for an issue. A missing file yields an
// empty store with no error — callers should treat "no records" and "no
// sidecar" the same.
func LoadStats(issuePath string) (StatsStore, error) {
	path := StatsSidecarPath(issuePath)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return StatsStore{}, nil
		}
		return StatsStore{}, fmt.Errorf("reading %s: %w", path, err)
	}
	var store StatsStore
	if err := json.Unmarshal(data, &store); err != nil {
		return StatsStore{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	return store, nil
}

var statsCache FileCache[StatsStore]

// StatsOf is LoadStats for a reader that won't change the store, shared with
// other readers and parsed once per version of the sidecar: a page reads one
// per issue, several times over.
func StatsOf(issuePath string) (StatsStore, error) {
	info, err := os.Stat(StatsSidecarPath(issuePath))
	if err != nil {
		if os.IsNotExist(err) {
			return StatsStore{}, nil
		}
		return StatsStore{}, err
	}
	return statsCache.Get(StatsSidecarPath(issuePath), info, func() (StatsStore, error) { return LoadStats(issuePath) })
}

// SaveStats writes the stats sidecar through jsonfile.Write (aside, then
// renamed). Last-write-wins; the rename guarantees no half-written file on disk.
func SaveStats(issuePath string, store StatsStore) error {
	path := StatsSidecarPath(issuePath)
	if store.Transitions == nil {
		store.Transitions = []TransitionStat{}
	}
	if err := jsonfile.Write(path, store, 0o644); err != nil {
		return fmt.Errorf("writing stats store: %w", err)
	}
	return nil
}

// AppendTransitionStat loads, appends, and saves the stats sidecar in one shot.
// Errors from the load path propagate; missing-file is not an error.
func AppendTransitionStat(issuePath string, stat TransitionStat) error {
	store, err := LoadStats(issuePath)
	if err != nil {
		return err
	}
	store.Transitions = append(store.Transitions, stat)
	return SaveStats(issuePath, store)
}

// appendTransitionStatLocked appends stat, folding in the viewer's timestamp
// for the consumed approval, then clears that scratch state. Caller must hold
// the issue lock.
func appendTransitionStatLocked(issuePath string, stat TransitionStat, consumedApproval string) error {
	store, err := LoadStats(issuePath)
	if err != nil {
		return err
	}
	if la := store.LastApproval; la != nil && consumedApproval != "" && strings.EqualFold(la.Status, consumedApproval) {
		at := la.At
		stat.ApprovedAt = &at
	}
	if consumedApproval != "" {
		store.LastApproval = nil
	}
	store.Transitions = append(store.Transitions, stat)
	return SaveStats(issuePath, store)
}

// RecordApproval stores the viewer's approval timestamp so the transition that
// consumes it can report ApprovedAt. An empty status (approval toggled off)
// clears the record.
func RecordApproval(issuePath, status string, at time.Time) error {
	return withIssueLock(issuePath, func() error {
		store, err := LoadStats(issuePath)
		if err != nil {
			return err
		}
		if status == "" {
			if store.LastApproval == nil {
				return nil
			}
			store.LastApproval = nil
		} else {
			store.LastApproval = &ApprovalRecord{Status: status, At: at.UTC()}
		}
		return SaveStats(issuePath, store)
	})
}

// StaticTransitionCost returns the approximate token count of the workflow
// scaffolding the bot reads when transitioning from→to: every action body the
// transition runs (validate rules, append_section bodies, inject_prompt
// prompts), the legacy template appended on entry to the target status, and
// the target status's entry-guidance prompt.
//
// Pure function — no I/O, no issue context. Used both by the Stats tab's
// reference table and as the floor for the dynamic cost recorded at
// transition time.
func StaticTransitionCost(wf *WorkflowConfig, from, to string) int {
	if wf == nil {
		return 0
	}

	total := 0
	for _, action := range wf.transitionActions(from, to) {
		switch action.Type {
		case "validate":
			total += tokens.Estimate(action.Rule)
		case "append_section":
			total += tokens.Estimate(action.Title) + tokens.Estimate(action.Body)
		case "inject_prompt":
			total += tokens.Estimate(action.Prompt)
		case "require_human_approval":
			total += tokens.Estimate(action.Status)
		}
	}

	total += tokens.Estimate(wf.TemplateForStatus(to))
	total += tokens.Estimate(wf.StatusPrompt(to))

	return total
}

// DynamicTransitionCost returns the static cost plus the issue context the
// bot reads at transition time: the issue body and the joined comment bodies
// captured at the moment of transition.
func DynamicTransitionCost(wf *WorkflowConfig, from, to, issueBody string, comments []Comment) int {
	cost := StaticTransitionCost(wf, from, to)
	cost += tokens.Estimate(issueBody)
	for _, c := range comments {
		cost += tokens.Estimate(c.Text)
	}
	return cost
}

// RecordDispatch appends one agent launch to the issue's stats sidecar.
func RecordDispatch(issuePath string, d Dispatch) error {
	return withIssueLock(issuePath, func() error {
		store, err := LoadStats(issuePath)
		if err != nil {
			return err
		}
		d.At = d.At.UTC()
		store.Dispatches = append(store.Dispatches, d)
		return SaveStats(issuePath, store)
	})
}
