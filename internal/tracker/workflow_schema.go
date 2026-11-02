package tracker

import (
	"reflect"
	"strings"
)

// SchemaFieldDoc describes one YAML field in a workflow struct.
type SchemaFieldDoc struct {
	Name        string
	Type        string
	Description string
	Optional    bool
}

// SchemaSection groups fields for a single workflow type.
type SchemaSection struct {
	Path   string // e.g. "statuses[]"
	Title  string // struct name, e.g. "WorkflowStatus"
	Fields []SchemaFieldDoc
}

// SchemaNamedDoc describes a named value (action type or validation rule) that
// lives in a switch statement rather than a struct field.
type SchemaNamedDoc struct {
	Name        string
	Description string
}

// WorkflowActionTypes lists every action.type value honored by
// transitionActions / BuildTransitionPreview. Kept next to the switch so a new
// type must be documented here or reviewers will notice the drift.
var WorkflowActionTypes = []SchemaNamedDoc{
	{Name: "validate", Description: "Run a validation rule (see 'Validation rules'); blocks the transition on failure"},
	{Name: "require_human_approval", Description: "Require the issue to be human-approved for the target status before transitioning"},
	{Name: "append_section", Description: "Append a '## <title>' section with the given body; when a '## <title>' section already exists, add only the body's checkboxes it lacks (ticked boxes keep their ticks), and only if a section_checkboxes_checked (or all_checkboxes_checked) rule gates it: an ungated section that already has boxes is the author's and gets none. A new section whose id prefix a playtest session for the issue already uses (TP1, TP2, …) starts with one box per such step, so its own boxes number after them"},
	{Name: "inject_prompt", Description: "Inject prompt text into the agent's entry guidance on this transition"},
	{Name: "set_fields", Description: "Set or clear a frontmatter field (field=assignee|priority|status|human_approval, value=\"\" to clear)"},
}

// WorkflowValidationRules lists every validation rule recognized by checkRule
// or the validations sub-package. The first block is the legacy colon-string
// shorthand; the second block is the structured-action form (one validator
// per file under internal/tracker/validations/), which uses companion
// fields on transitions[].actions[] (field, values, pattern, section, etc.).
var WorkflowValidationRules = []SchemaNamedDoc{
	{Name: "body_not_empty", Description: "Issue body must contain non-whitespace content"},
	{Name: "has_checkboxes", Description: "Body must contain at least one '- [ ]' or '- [x]' checkbox anywhere"},
	{Name: "section_has_checkboxes: <Title>", Description: "Named '## <Title>' section must contain at least one checkbox"},
	{Name: "criteria_checkboxes: <Title>", Description: "Every list item in '## <Title>' is a checkbox, and none still has the text the workflow's append_section put there"},
	{Name: "criteria_proven: since <YYYY-MM-DD>", Description: "Every Acceptance Criteria box has a proof link that passes its check (issue-cli prove); issues created before the date, and issues without criteria, pass"},
	{Name: "commits_reference_issue", Description: "One of the work branch's own commits references the issue: its subject names the slug (chore(merge): main into <slug>) or a 'Refs: <slug>' line in its message does"},
	{Name: "has_output: <kind>", Description: "The outputs shelf has an output for the issue (issue-cli output add --issue <slug>) of the given kind (any kind when omitted) that isn't rejected and whose file is still there"},
	{Name: "critique", Description: "The newest fresh-critic run from the current status (issue-cli critique <slug>) has no blocks finding, didn't fail, or the owner overrode it; the critique: block says how the critic starts"},
	{Name: "decisions_cited: since <YYYY-MM-DD>", Description: "'## Design' cites every hard decision in force for the issue at its status (<slug>#D-n), or sets one aside with a line 'n/a <ref>: <why>'; the issue's own decisions, soft ones and issues created before the date pass. Under YOLO a hold passes and posts a yolo-hold: comment for review"},
	{Name: "decision_unchallenged: since <YYYY-MM-DD>", Description: "No open challenge this issue made on a decision (issue-cli decision challenge) waits for the owner's answer; it holds only the challenger's own issue, never one that cites or touches the decision. Under YOLO a hold passes and posts a yolo-hold: comment for review"},
	{Name: "citations_current: since <YYYY-MM-DD>", Description: "No decision ref the body cites (outside its own '## Decisions' and '(replaces …)') is replaced or expired; the refusal names the replacer. Issues created before the date pass"},
	{Name: "decisions_respected: since <YYYY-MM-DD>", Description: "When the work branch's own changes (three-dot diff from its merge-base with main) touch the paths: of an active hard decision, whatever its scope or at:, the body cites that ref (an 'n/a <ref>' line doesn't count) and the issue has no open challenge of its own on it. No recorded base or branch: passes with a warning. Issues created before the date pass. Under YOLO a hold passes and posts a yolo-hold: comment for review"},
	{Name: "has_assignee", Description: "Issue must have a non-empty assignee"},
	{Name: "all_checkboxes_checked", Description: "Every checkbox in the body must be checked"},
	{Name: "section_checkboxes_checked: <Title>", Description: "'## <Title>' section must exist with at least one checkbox, and every checkbox in it must be checked"},
	{Name: "has_test_plan", Description: "Body must contain '## Test Plan' with '### Automated' and '### Manual' subsections"},
	{Name: "has_comment_prefix: <prefix>", Description: "At least one comment must start with the given prefix (e.g. 'tests:', 'docs:')"},
	{Name: "approved_for: <status>", Description: "Issue must be human-approved for the given status"},
	{Name: "human_approval: <status>", Description: "Alias for 'approved_for: <status>'"},

	// Structured validators — populate companion fields on the action.
	{Name: "field_present", Description: "Frontmatter key (action.field) exists on the issue"},
	{Name: "field_not_empty", Description: "Frontmatter key (action.field) exists and has a non-blank value"},
	{Name: "field_in", Description: "Frontmatter key (action.field) value is one of action.values"},
	{Name: "field_matches", Description: "Frontmatter key (action.field) value matches Go RE2 regex action.pattern"},
	{Name: "has_label", Description: "Issue labels contain the name in action.field"},
	{Name: "has_any_label", Description: "Issue has at least one label"},
	{Name: "has_pr_url", Description: "Frontmatter \"pr\" is a github pull request URL"},
	{Name: "linked_issue_in_status", Description: "Issue referenced by action.ref_key (frontmatter key holding a slug) has status action.linked_status"},
	{Name: "has_section", Description: "Body contains a '## <action.section>' heading"},
	{Name: "section_min_length", Description: "Section '## <action.section>' has at least action.min non-whitespace chars"},
	{Name: "section_max_length", Description: "Section '## <action.section>' has at most action.max non-whitespace chars (missing section passes)"},
	{Name: "no_todo_markers", Description: "Body contains no whole-word TODO or FIXME markers"},
	{Name: "file_exists", Description: "File action.path, relative to the project root, exists and isn't empty; templated with {{slug}}/{{number}}/{{repo}}/{{system}}/{{department}}; runs no shell"},
	{Name: "command_succeeds", Description: "Shell command in action.command exits 0; templated with {{slug}}/{{number}}/{{repo}}/{{system}}; requires top-level allow_shell=true"},
}

// WorkflowSchemaSections returns the YAML schema for workflow.yaml, derived by
// reflecting the struct tags on the workflow types. Adding a new `yaml:"..."`
// field to any of these structs surfaces it here automatically, so the CLI
// docs cannot drift from the parser.
func WorkflowSchemaSections() []SchemaSection {
	return []SchemaSection{
		{Path: "(top level)", Title: "WorkflowConfig", Fields: schemaFieldsOf(reflect.TypeOf(WorkflowConfig{}))},
		{Path: "features", Title: "FeatureRecord", Fields: schemaFieldsOf(reflect.TypeOf(FeatureRecord{}))},
		{Path: "statuses[]", Title: "WorkflowStatus", Fields: schemaFieldsOf(reflect.TypeOf(WorkflowStatus{}))},
		{Path: "transitions[]", Title: "WorkflowTransition", Fields: schemaFieldsOf(reflect.TypeOf(WorkflowTransition{}))},
		{Path: "transitions[].actions[]", Title: "WorkflowAction", Fields: schemaFieldsOf(reflect.TypeOf(WorkflowAction{}))},
		{Path: "transitions[].fields[]", Title: "WorkflowField", Fields: schemaFieldsOf(reflect.TypeOf(WorkflowField{}))},
		{Path: "board", Title: "WorkflowBoardConfig", Fields: schemaFieldsOf(reflect.TypeOf(WorkflowBoardConfig{}))},
		{Path: "systems[<name>]", Title: "WorkflowOverlay", Fields: schemaFieldsOf(reflect.TypeOf(WorkflowOverlay{}))},
		{Path: "types[<name>]", Title: "WorkflowType", Fields: schemaFieldsOf(reflect.TypeOf(WorkflowType{}))},
		{Path: "actions[]", Title: "CustomAction", Fields: schemaFieldsOf(reflect.TypeOf(CustomAction{}))},
		{Path: "leads", Title: "LeadsConfig", Fields: schemaFieldsOf(reflect.TypeOf(LeadsConfig{}))},
		{Path: "scoring", Title: "ScoringConfig", Fields: schemaFieldsOf(reflect.TypeOf(ScoringConfig{}))},
		{Path: "scoring.formula", Title: "ScoringFormula", Fields: schemaFieldsOf(reflect.TypeOf(ScoringFormula{}))},
		{Path: "scoring.formula.due_date", Title: "ScoringDueDate", Fields: schemaFieldsOf(reflect.TypeOf(ScoringDueDate{}))},
		{Path: "scoring.formula.age", Title: "ScoringAge", Fields: schemaFieldsOf(reflect.TypeOf(ScoringAge{}))},
		{Path: "workbench", Title: "WorkbenchConfig", Fields: schemaFieldsOf(reflect.TypeOf(WorkbenchConfig{}))},
		{Path: "workbench.systems[<name>]", Title: "WorkbenchSystem", Fields: schemaFieldsOf(reflect.TypeOf(WorkbenchSystem{}))},
		{Path: "workbench.systems[<name>].families[<name>]", Title: "WorkbenchFamily", Fields: schemaFieldsOf(reflect.TypeOf(WorkbenchFamily{}))},
		{Path: "workbench.metrics", Title: "WorkbenchMetrics", Fields: schemaFieldsOf(reflect.TypeOf(WorkbenchMetrics{}))},
		{Path: "workbench.balance", Title: "WorkbenchBalance", Fields: schemaFieldsOf(reflect.TypeOf(WorkbenchBalance{}))},
		{Path: "workbench.pages[]", Title: "WorkbenchPage", Fields: schemaFieldsOf(reflect.TypeOf(WorkbenchPage{}))},
		{Path: "ci", Title: "CIConfig", Fields: schemaFieldsOf(reflect.TypeOf(CIConfig{}))},
		{Path: "ci.run[]", Title: "CICommand", Fields: schemaFieldsOf(reflect.TypeOf(CICommand{}))},
		{Path: "critique", Title: "CritiqueConfig", Fields: schemaFieldsOf(reflect.TypeOf(CritiqueConfig{}))},
		{Path: "health", Title: "HealthConfig", Fields: schemaFieldsOf(reflect.TypeOf(HealthConfig{}))},
	}
}

func schemaFieldsOf(t reflect.Type) []SchemaFieldDoc {
	var out []SchemaFieldDoc
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		yamlTag := f.Tag.Get("yaml")
		if yamlTag == "" || yamlTag == "-" {
			continue
		}
		parts := strings.Split(yamlTag, ",")
		name := strings.TrimSpace(parts[0])
		if name == "" {
			continue
		}
		optional := false
		for _, p := range parts[1:] {
			if strings.TrimSpace(p) == "omitempty" {
				optional = true
			}
		}
		out = append(out, SchemaFieldDoc{
			Name:        name,
			Type:        schemaTypeName(f.Type),
			Description: f.Tag.Get("desc"),
			Optional:    optional,
		})
	}
	return out
}

func schemaTypeName(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Ptr:
		return schemaTypeName(t.Elem())
	case reflect.Slice, reflect.Array:
		return "list<" + schemaTypeName(t.Elem()) + ">"
	case reflect.Map:
		return "map<" + schemaTypeName(t.Key()) + "," + schemaTypeName(t.Elem()) + ">"
	case reflect.Struct:
		return t.Name()
	case reflect.Bool:
		return "bool"
	case reflect.String:
		return "string"
	default:
		if t.Kind() >= reflect.Int && t.Kind() <= reflect.Int64 {
			return "int"
		}
		if t.Kind() >= reflect.Uint && t.Kind() <= reflect.Uint64 {
			return "int"
		}
		if t.Kind() == reflect.Float32 || t.Kind() == reflect.Float64 {
			return "float"
		}
		return t.Kind().String()
	}
}
