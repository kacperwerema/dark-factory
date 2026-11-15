package tracker

import "strings"

// SplitRule splits a validate rule into its name and argument at the first
// colon, both trimmed: "has_comment_prefix: tests:" is has_comment_prefix
// and "tests:". Every reader of a rule string goes through it, so a name
// means the same to the transition, lint, the catalog and the viewer
// (TestRuleSplitHasOneSource).
func SplitRule(rule string) (name, arg string) {
	name, arg, _ = strings.Cut(rule, ":")
	return strings.TrimSpace(name), strings.TrimSpace(arg)
}

// ruleDoc is rule's entry in WorkflowValidationRules, by the name before
// its argument.
func ruleDoc(rule string) (SchemaNamedDoc, bool) {
	name, _ := SplitRule(rule)
	for _, r := range WorkflowValidationRules {
		if known, _ := SplitRule(r.Name); known == name {
			return r, true
		}
	}
	return SchemaNamedDoc{}, false
}

// KnownRule reports whether this build runs rule, with or without its
// argument. WorkflowValidationRules is the one list; TestRuleRegistryIsComplete
// keeps it in step with validations.Registry and checkRule.
func KnownRule(rule string) bool {
	_, ok := ruleDoc(rule)
	return ok
}

// ruleTakesSince reports whether rule's registry entry takes a "since
// <YYYY-MM-DD>" argument.
func ruleTakesSince(rule string) bool {
	r, ok := ruleDoc(rule)
	return ok && strings.Contains(r.Name, ": since <")
}
