package tracker

import (
	"fmt"
	"math"
	"strings"

	"gopkg.in/yaml.v3"
)

// Budget is a department's budget: what it may spend per period, in USD
// (estimated from token prices) or in tokens. It is shown as "x of y" next
// to the department's spend and never blocks anything (factory/department-
// budgets-…#D-3, N-18). A budget that doesn't parse keeps Err, never fails
// loading projects.yaml; lint's departments check names it.
type Budget struct {
	USD    float64 `yaml:"usd" json:"usd,omitempty"`
	Tokens int64   `yaml:"tokens" json:"tokens,omitempty"`
	// Period is week (from Monday, the default) or month.
	Period string `yaml:"period" json:"period"`
	// Err is why the budget can't be used; "" for a good one.
	Err string `yaml:"-" json:"error,omitempty"`
}

// BudgetShape is the budget: shape, for lint's and the CLI's hints.
const BudgetShape = "budget: {usd: 50, period: week} or {tokens: 20000000, period: month}"

// UnmarshalYAML reads any node: a mapping with usd or tokens and a period;
// anything else becomes a Budget with Err set.
func (b *Budget) UnmarshalYAML(n *yaml.Node) error {
	*b = Budget{}
	if n.Kind != yaml.MappingNode {
		b.Err = fmt.Sprintf("budget: is not a mapping (want %s)", BudgetShape)
		return nil
	}
	var raw struct {
		USD    *float64 `yaml:"usd"`
		Tokens *float64 `yaml:"tokens"`
		Period string   `yaml:"period"`
	}
	known := map[string]bool{"usd": true, "tokens": true, "period": true}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if k := n.Content[i].Value; !known[k] {
			b.Err = fmt.Sprintf("budget: unknown key %q (want %s)", k, BudgetShape)
			return nil
		}
	}
	if err := n.Decode(&raw); err != nil {
		b.Err = fmt.Sprintf("budget: usd and tokens must be numbers (want %s)", BudgetShape)
		return nil
	}
	b.Period = strings.ToLower(strings.TrimSpace(raw.Period))
	if b.Period == "" {
		b.Period = "week"
	}
	switch {
	case raw.USD == nil && raw.Tokens == nil:
		b.Err = fmt.Sprintf("budget: has neither usd nor tokens (want %s)", BudgetShape)
	case raw.USD != nil && raw.Tokens != nil:
		b.Err = "budget: has both usd and tokens; pick one"
	case raw.USD != nil && (*raw.USD <= 0 || math.IsInf(*raw.USD, 0) || math.IsNaN(*raw.USD)):
		b.Err = "budget: usd must be more than 0"
	case raw.Tokens != nil && (*raw.Tokens < 1 || *raw.Tokens > math.MaxInt64/2 || *raw.Tokens != math.Trunc(*raw.Tokens)):
		b.Err = "budget: tokens must be a whole number more than 0"
	case b.Period != "week" && b.Period != "month":
		b.Err = fmt.Sprintf("budget: period %q: use week or month", raw.Period)
	}
	if raw.USD != nil {
		b.USD = *raw.USD
	}
	if raw.Tokens != nil {
		b.Tokens = int64(*raw.Tokens)
	}
	return nil
}
