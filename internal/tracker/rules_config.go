package tracker

// ProjectRule is one entry of workflow.yaml's rules: (docs/workflow.md,
// Rules): what a project adds to the machine's rules.md.
type ProjectRule struct {
	ID      string `yaml:"id,omitempty" desc:"Names the rule; a machine rule's key here needs narrows: too"`
	Text    string `yaml:"text" desc:"The rule, one sentence or two"`
	Narrows string `yaml:"narrows,omitempty" desc:"The [key] of the machine rule this one narrows; the machine rule stays in every briefing"`
}
