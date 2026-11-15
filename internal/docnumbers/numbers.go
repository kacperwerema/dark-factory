// Package docnumbers is the docs' Numbers table: the numbers people quote,
// read from the constants that hold them. docs/numbers.md is generated from
// it; TestDocNumbersInSync fails when that page is stale, and
// TestDocNumbersCopiesAgree fails when a sentence elsewhere quotes another
// value than the constant's.
package docnumbers

import (
	"fmt"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/health"
	"github.com/kacperwerema/dark-factory/internal/memguard"
	"github.com/kacperwerema/dark-factory/internal/timeline"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/viewer"
)

//go:generate go run ../../tools/doc-numbers ../../docs/numbers.md

// Page is the generated page's path from the repository root.
const Page = "docs/numbers.md"

// Number is one row of the table.
type Number struct {
	Name    string
	Value   int
	Unit    string // "min", " days", "%", or "" for a count
	Source  string // the constant, as code names it
	Meaning string
	// Copies are the sentences that quote the number elsewhere, as regular
	// expressions whose one group is the number. Each must still match
	// somewhere, and every match must read Value.
	Copies []string
}

// Text is the value as the table shows it: "15 min", "50%", "12".
func (n Number) Text() string {
	switch n.Unit {
	case "":
		return fmt.Sprint(n.Value)
	case "%":
		return fmt.Sprintf("%d%%", n.Value)
	}
	return fmt.Sprintf("%d %s", n.Value, n.Unit)
}

func minutes(d time.Duration) int { return int(d / time.Minute) }
func days(d time.Duration) int    { return int(d / (24 * time.Hour)) }

// Numbers is the table, in the order the page shows it.
func Numbers() []Number {
	return []Number{
		{
			Name: "Idle gap", Value: minutes(timeline.IdleAfter), Unit: "min", Source: "timeline.IdleAfter",
			Meaning: "With no agent call for this long, an agent counts as idle, or as waiting on you in a status only you can move on",
			Copies: []string{
				`(\d+) min or more`,
				`less than (\d+) min apart`,
				`no issue-cli call for (\d+) minutes`,
				`call in the last (\d+) minutes`,
				`ended (\d+) minutes ago or more`,
				`any other gap of (\d+) m`,
				`the same (\d+) min as`,
			},
		},
		{
			Name: "Timeline window", Value: days(timeline.Window), Unit: "days", Source: "timeline.Window",
			Meaning: "The project aggregate reads issues done this recently",
			Copies:  []string{`every issue done in the last (\d+) days`},
		},
		{
			Name: "Health trend", Value: health.TrendDays, Unit: "days", Source: "health.TrendDays",
			Meaning: "How far back the score's trend components compare",
		},
		{
			Name: "Churn window", Value: health.ChurnDays, Unit: "days", Source: "health.ChurnDays",
			Meaning: "The shape family's churn and hotspots read git this far back",
			Copies:  []string{`shape\.churn\.big_share\{window=(\d+)d\}`},
		},
		{
			Name: "Shots stay full size", Value: tracker.HealthDefaultShrinkDays, Unit: "days", Source: "tracker.HealthDefaultShrinkDays",
			Meaning: "After an issue is done this long its playtest shots shrink",
			Copies:  []string{`shrinks shots of issues done (\d+)\+ days ago`},
		},
		{
			Name: "Decisions brief cap", Value: tracker.DefaultDecisionBriefCap, Source: "tracker.DefaultDecisionBriefCap",
			Meaning: "Lines the decisions-in-force block carries in start, show and the briefings",
			Copies:  []string{`brief_cap:? \((\d+)\)`, `briefings carries \(default (\d+)\)`, "brief_cap:` in workflow\\.yaml, (\\d+) when unset"},
		},
		{
			Name: "Red-round cap", Value: tracker.DefaultMaxRedRounds, Source: "tracker.DefaultMaxRedRounds",
			Meaning: "Fix rounds a check may stay red before it becomes a question for you, when max_red_rounds is unset",
			Copies: []string{
				`max_red_rounds[^\n]{0,30}\((\d+) when unset`,
				`(?m)^\s*max_red_rounds: (\d+)`,
				`project-wide like ci:; (\d+) when unset`,
				`At max_red_rounds \(workflow\.yaml, (\d+) when unset`,
			},
		},
		{
			Name: "mem-guard start limit", Value: memguard.StartMax, Unit: "%", Source: "memguard.StartMax",
			Meaning: "`mem-guard check` refuses at or over this much memory used",
		},
		{
			Name: "mem-guard pause", Value: memguard.PauseAt, Unit: "%", Source: "memguard.PauseAt",
			Meaning: "The watchdog pauses the largest heavy process",
		},
		{
			Name: "mem-guard resume", Value: memguard.ResumeAt, Unit: "%", Source: "memguard.ResumeAt",
			Meaning: "The watchdog resumes; optional work (a benchmark check, an embed) waits for this too",
			Copies: []string{
				`reading of (\d+)% or more`,
				`At (\d+)% or more`,
				`at (\d+)% or more`,
				`stops at (\d+)%`,
			},
		},
		{
			Name: "mem-guard hard limit", Value: memguard.HardLimit, Unit: "%", Source: "memguard.HardLimit",
			Meaning: "Memory use is never to exceed this",
		},
		{
			Name: "Auto-start memory line", Value: autostart.DefaultBelow, Unit: "%", Source: "autostart.DefaultBelow",
			Meaning: "Auto-start, the queue and the nightly loop start only while memory used is under this",
			Copies: []string{
				`[Mm]emory (?:is )?under (\d+)%`,
				`--below` + "`" + ` \(default (\d+)%\)`,
			},
		},
		{
			Name: "Auto-start gap", Value: autostart.DefaultGapMinutes, Unit: "min", Source: "autostart.DefaultGapMinutes",
			Meaning: "Time between two auto-starts, across the machine",
		},
		{
			Name: "Health snapshot check", Value: minutes(viewer.HealthCheckInterval), Unit: "min", Source: "viewer.HealthCheckInterval",
			Meaning: "How often the viewer looks for a project with no snapshot from today",
			Copies: []string{
				`looks every (\d+) minutes`,
				`health snapshot: every (\d+) min`,
			},
		},
		{
			Name: "Tracker sync", Value: minutes(viewer.TrackerSyncInterval), Unit: "min", Source: "viewer.TrackerSyncInterval",
			Meaning: "How often the viewer commits, pulls and pushes each private tracker",
			Copies: []string{
				`syncs (?:it|each tracker) every (\d+) minutes`,
				`sync every (\d+) min`,
				`tracker sync: every (\d+) min`,
				`pushes it every (\d+) minutes`,
				`the (\d+)-minute watcher`,
			},
		},
	}
}

// Render is docs/numbers.md.
func Render() string {
	var b strings.Builder
	b.WriteString(`---
title: "Numbers"
order: 91
---

# Numbers

The numbers the docs and pages quote, each read from the constant that holds it.

This page is generated: run ` + "`go generate ./internal/docnumbers/...`" + ` after changing a constant, and don't edit it by hand (` + "`TestDocNumbersInSync`" + `). A sentence elsewhere that quotes one of these numbers has to agree with it (` + "`TestDocNumbersCopiesAgree`" + `).

| Number | Value | What it is | Source |
|---|---|---|---|
`)
	for _, n := range Numbers() {
		fmt.Fprintf(&b, "| %s | %s | %s | `%s` |\n", n.Name, n.Text(), n.Meaning, n.Source)
	}
	return b.String()
}
