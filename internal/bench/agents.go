package bench

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/routing"
	"github.com/kacperwerema/dark-factory/internal/tokens"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// MinRoutingTasks is how many tasks both agents ran a type needs before a
// routing line says anything about it.
const MinRoutingTasks = 3

// Routing verdicts.
const (
	RoutingContradicted = "contradicted"
	RoutingSupported    = "supported"
	RoutingNoDifference = "no difference"
	RoutingTooFew       = "too few tasks"
	RoutingNotRun       = "not run"
)

// AllTypes is the Type of a table's rows over every type.
const AllTypes = "all"

// AgentRow is one agent on one type (AllTypes: every type). Means are per
// scored task; Tokens over TokensKnown tasks, USD over Priced ones, so a task
// with no count or price is left out, never counted as 0.
type AgentRow struct {
	Agent  string `json:"agent"`
	Type   string `json:"type"`
	Scored int    `json:"scored"`
	Passed int    `json:"passed"`
	// NotScored are the tasks the agent never finished for a reason of its
	// own or the run's (error, budget): a login error is no lost task.
	NotScored   int     `json:"not_scored,omitempty"`
	PassRate    float64 `json:"pass_rate"`
	WallSec     float64 `json:"wall_sec"`
	Tokens      int64   `json:"tokens"`
	TokensKnown int     `json:"tokens_known"`
	USD         float64 `json:"usd"`
	Priced      int     `json:"priced"`
}

// PassText is "4/5 80%", "–" when nothing was scored.
func (r AgentRow) PassText() string {
	if r.Scored == 0 {
		return "–"
	}
	return fmt.Sprintf("%d/%d %.0f%%", r.Passed, r.Scored, r.PassRate*100)
}

// WallText is the mean time per task.
func (r AgentRow) WallText() string {
	if r.Scored == 0 {
		return "–"
	}
	return (time.Duration(r.WallSec) * time.Second).Round(time.Second).String()
}

// TokensText is the mean tokens per task.
func (r AgentRow) TokensText() string {
	switch {
	case r.Scored == 0:
		return "–"
	case r.TokensKnown == 0:
		return "unknown"
	case r.TokensKnown < r.Scored:
		return fmt.Sprintf("%s (%d of %d read)", tokens.Format(r.Tokens), r.TokensKnown, r.Scored)
	}
	return tokens.Format(r.Tokens)
}

// CostText is the mean cost per task: "cost unknown" with no price, never a
// guess or $0.00.
func (r AgentRow) CostText() string {
	switch {
	case r.Scored == 0:
		return "–"
	case r.Priced == 0:
		return "cost unknown"
	case r.Priced < r.Scored:
		return fmt.Sprintf("$%.2f (%d of %d priced)", r.USD, r.Priced, r.Scored)
	}
	return fmt.Sprintf("$%.2f", r.USD)
}

// agentRecord is a record with the agent it ran on.
type agentRecord struct {
	TaskRecord
	agent string
}

// AgentTable is pass rate, time, tokens and cost per agent and per type over
// one or two runs' non-holdout tasks: the holdout stays in the overall
// passed n/m.
type AgentTable struct {
	Agents []string   `json:"agents"`
	Types  []string   `json:"types"`
	Rows   []AgentRow `json:"rows"`
	// Unpriced are the models of scored tasks with no cost.
	Unpriced []string `json:"unpriced,omitempty"`

	records []agentRecord
}

// Row is the table's row for agent on typ.
func (t *AgentTable) Row(agent, typ string) (AgentRow, bool) {
	for _, r := range t.Rows {
		if r.Agent == agent && r.Type == typ {
			return r, true
		}
	}
	return AgentRow{}, false
}

// recordAgent is the agent a record ran on: its own in a run on two agents,
// else the run's; "" in a run from before agents were recorded.
func recordAgent(r *Run, t TaskRecord) string {
	if t.Agent != "" {
		return t.Agent
	}
	if a := r.Setup.Agents(); len(a) == 1 {
		return a[0]
	}
	return ""
}

// typeName is a record's type for the table.
func typeName(t TaskRecord) string {
	if t.Type == "" {
		return "untyped"
	}
	return t.Type
}

// scored reports whether the task counts: it ran to an outcome that says
// something about the agent.
func scored(t TaskRecord) bool {
	switch t.Outcome {
	case OutcomePass, OutcomeFail, OutcomeTimeout:
		return true
	}
	return false
}

// ByAgent builds the table over runs; for one task on one agent the later
// run's record wins. nil when the runs hold under two agents; a record with
// no agent known is left out.
func ByAgent(runs ...*Run) *AgentTable {
	merged := map[string]agentRecord{}
	for _, r := range runs {
		for _, t := range r.Tasks {
			a := recordAgent(r, t)
			if a == "" {
				continue
			}
			merged[t.Task+"@"+a] = agentRecord{TaskRecord: t, agent: a}
		}
	}
	agents, types := map[string]bool{}, map[string]bool{}
	var keys []string
	for k, rec := range merged {
		agents[rec.agent] = true
		types[typeName(rec.TaskRecord)] = true
		keys = append(keys, k)
	}
	if len(agents) < 2 {
		return nil
	}
	sort.Strings(keys)
	t := &AgentTable{Agents: sortedSet(agents), Types: sortedSet(types)}
	unpriced := map[string]bool{}
	for _, k := range keys {
		rec := merged[k]
		t.records = append(t.records, rec)
		if scored(rec.TaskRecord) && !rec.Usage.USDKnown && rec.Usage.Model != "" {
			unpriced[rec.Usage.Model] = true
		}
	}
	t.Unpriced = sortedSet(unpriced)
	for _, typ := range append([]string{AllTypes}, t.Types...) {
		for _, a := range t.Agents {
			t.Rows = append(t.Rows, t.row(a, typ))
		}
	}
	return t
}

func (t *AgentTable) row(agent, typ string) AgentRow {
	r := AgentRow{Agent: agent, Type: typ}
	var wall, usd float64
	var spent int64
	for _, rec := range t.records {
		if rec.agent != agent || (typ != AllTypes && typeName(rec.TaskRecord) != typ) || rec.Outcome == OutcomeBroken {
			continue
		}
		if !scored(rec.TaskRecord) {
			r.NotScored++
			continue
		}
		r.Scored++
		if rec.Outcome == OutcomePass {
			r.Passed++
		}
		wall += rec.WallSec + rec.ScoreWallSec
		if rec.Usage.Known {
			r.TokensKnown++
			spent += rec.Usage.Tokens()
		}
		if rec.Usage.USDKnown {
			r.Priced++
			usd += rec.Usage.USD
		}
	}
	if r.Scored > 0 {
		r.PassRate = float64(r.Passed) / float64(r.Scored)
		r.WallSec = wall / float64(r.Scored)
	}
	if r.TokensKnown > 0 {
		r.Tokens = spent / int64(r.TokensKnown)
	}
	if r.Priced > 0 {
		r.USD = usd / float64(r.Priced)
	}
	return r
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// RoutingCheck is what the numbers say of one routing rule on one type.
type RoutingCheck struct {
	// Rule is the rule in words, routing.Route's why ("type bugfix →
	// claude", "default → claude").
	Rule    string `json:"rule"`
	Type    string `json:"type"`
	Agent   string `json:"agent"`
	Other   string `json:"other,omitempty"`
	Verdict string `json:"verdict"`
	Why     string `json:"why"`
}

// Line is the check in one line.
func (c RoutingCheck) Line() string { return c.Rule + " " + c.Verdict + ": " + c.Why }

// CheckRouting asks cfg what each type in the table routes to, as a golden
// task starts (DefaultStart), and compares that agent with each other one on
// the tasks both ran. contradicted: the other passes more, or the same at
// CostStep less cost (tokens when a side has no cost) and wall time under
// WallStep more. supported: the same the other way. It only reports.
func CheckRouting(cfg *tracker.RoutingConfig, t *AgentTable) []RoutingCheck {
	if t == nil {
		return nil
	}
	var out []RoutingCheck
	for _, typ := range t.Types {
		s := routing.Subject{Status: DefaultStart}
		if typ != "untyped" {
			s.Type = typ
		}
		for _, rec := range t.records {
			if typeName(rec.TaskRecord) == typ {
				if rec.Workflow != "" && rec.Workflow != ProductWorkflow {
					s.Department = rec.Workflow
				}
				break
			}
		}
		agent, rule := routing.Route(cfg, s)
		if typ != "untyped" && !strings.HasPrefix(rule, "type ") {
			rule = "type " + typ + " (" + rule + ")"
		}
		if !slices.Contains(t.Agents, agent) {
			out = append(out, RoutingCheck{Rule: rule, Type: typ, Agent: agent, Verdict: RoutingNotRun, Why: agent + " has no task in these runs"})
			continue
		}
		for _, other := range t.Agents {
			if other != agent {
				out = append(out, t.checkPair(rule, typ, agent, other))
			}
		}
	}
	return out
}

func (t *AgentTable) checkPair(rule, typ, agent, other string) RoutingCheck {
	c := RoutingCheck{Rule: rule, Type: typ, Agent: agent, Other: other}
	byTask := map[string]TaskRecord{}
	for _, rec := range t.records {
		if rec.agent == other && typeName(rec.TaskRecord) == typ && scored(rec.TaskRecord) {
			byTask[rec.Task] = rec.TaskRecord
		}
	}
	var n, passA, passO int
	var wallA, wallO, usdA, usdO float64
	var tokA, tokO int64
	allPriced, allCounted := true, true
	for _, rec := range t.records {
		o, ok := byTask[rec.Task]
		if rec.agent != agent || typeName(rec.TaskRecord) != typ || !scored(rec.TaskRecord) || !ok {
			continue
		}
		n++
		if rec.Outcome == OutcomePass {
			passA++
		}
		if o.Outcome == OutcomePass {
			passO++
		}
		wallA += rec.WallSec + rec.ScoreWallSec
		wallO += o.WallSec + o.ScoreWallSec
		allPriced = allPriced && rec.Usage.USDKnown && o.Usage.USDKnown
		allCounted = allCounted && rec.Usage.Known && o.Usage.Known
		usdA += rec.Usage.USD
		usdO += o.Usage.USD
		tokA += rec.Usage.Tokens()
		tokO += o.Usage.Tokens()
	}
	passes := fmt.Sprintf("%s passed %d/%d, %s %d/%d", other, passO, n, agent, passA, n)
	switch {
	case n < MinRoutingTasks:
		c.Verdict, c.Why = RoutingTooFew, fmt.Sprintf("%d both ran, %d needed", n, MinRoutingTasks)
		return c
	case passO > passA:
		c.Verdict, c.Why = RoutingContradicted, passes
		return c
	case passA > passO:
		c.Verdict, c.Why = RoutingSupported, passes
		return c
	}
	word, a, o := "cost", usdA, usdO
	if !allPriced {
		word, a, o = "tokens", float64(tokA), float64(tokO)
	}
	if !allPriced && !allCounted {
		c.Verdict, c.Why = RoutingNoDifference, fmt.Sprintf("both passed %d/%d, cost and tokens not known for every task", passA, n)
		return c
	}
	same := fmt.Sprintf("both passed %d/%d", passA, n)
	switch {
	case o <= a*(1-CostStep) && wallO < wallA*(1+WallStep):
		c.Verdict, c.Why = RoutingContradicted, fmt.Sprintf("%s, %s used %.0f%% less %s", same, other, (1-o/a)*100, word)
	case a <= o*(1-CostStep) && wallA < wallO*(1+WallStep):
		c.Verdict, c.Why = RoutingSupported, fmt.Sprintf("%s, %s used %.0f%% less %s", same, agent, (1-a/o)*100, word)
	default:
		c.Verdict, c.Why = RoutingNoDifference, same
	}
	return c
}

// NewestTwoAgents is benchmark's newest finished run on two or more agents,
// nil without one: a later one-agent nightly doesn't hide it.
func NewestTwoAgents(benchmark string) *Run {
	runs := Runs(benchmark)
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].Finished() && len(runs[i].Setup.Agents()) > 1 {
			return runs[i]
		}
	}
	return nil
}
