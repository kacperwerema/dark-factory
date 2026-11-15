package main

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// parentFlagHelp is the --parent flag's usage on create and update.
const parentFlagHelp = "the epic this issue belongs to (an epic on the board: type epic, or already a parent); writes parent:"

// checkParent resolves --parent <epic> to the epic's slug as the board
// spells it. It refuses a slug that is no issue, an issue that is no epic
// (typed epic, or named as parent by another issue: tracker.IsEpic), and
// self, the issue being updated ("" on create).
func checkParent(issues []*tracker.Issue, value, self string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("--parent needs an epic's slug (to clear it: issue-cli set-meta <slug> --key parent --clear)")
	}
	epic := tracker.FindIssue(issues, value)
	if epic == nil {
		return "", fmt.Errorf("--parent %q: no such issue on the board (find epics with issue-cli list --type epic)", value)
	}
	if self != "" && strings.EqualFold(epic.Slug, self) {
		return "", fmt.Errorf("--parent %q: an issue can't be its own parent", value)
	}
	if !tracker.IsEpic(epic, issues) {
		return "", fmt.Errorf("--parent %q: %s is not an epic (type: epic, or the parent of another issue); an epic is made with issue-cli set-type %s epic", value, epic.Slug, epic.Slug)
	}
	return epic.Slug, nil
}
