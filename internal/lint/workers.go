package lint

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/connectors"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// unknownKeptSkills names each workers: skills: name that is neither a
// personal or project skill on disk nor one Claude Code listed in the
// project's transcripts: its workers keep nothing by it. Quiet when no
// transcript lists anything yet (a fresh machine: nothing to compare with).
func unknownKeptSkills(skills []tracker.WorkerSkill, root, where string) []Finding {
	listed := connectors.ListedSkills(root)
	if len(listed) == 0 || len(skills) == 0 {
		return nil
	}
	known := append(connectors.OwnerSkills(root, ""), listed...)
	sort.Strings(known)
	known = slices.Compact(known)
	var out []Finding
	for _, s := range skills {
		if s.Skill == "" || slices.Contains(known, s.Skill) {
			continue
		}
		out = append(out, Finding{
			Where:   where,
			Message: fmt.Sprintf("workers: skills: keeps %q, which no skill folder holds and Claude Code has not listed: its workers keep nothing by it", s.Skill),
			Fix:     "name a skill as Claude Code lists it (synced ones as anthropic-skills:<name>): " + strings.Join(known, ", "),
		})
	}
	return out
}

// checkWorkers reads a workflow file's workers: block (docs/agent-dispatch.md
// "Worker setup"): a setup: other than lean or full, a server with no name,
// a type the file does not define, a plugin that is not name@marketplace,
// a tool workers: tools: cannot keep, a server the owner's Claude Code
// config does not define (the launch then starts the worker without it),
// and a kept skill nothing lists.
func checkWorkers(env *Env) []Finding {
	wf := env.Workflow
	if wf == nil || wf.Workers == nil {
		return nil
	}
	where := env.where(env.lineOf("workers:"))
	var out []Finding
	for _, p := range wf.WorkersProblems() {
		out = append(out, Finding{Where: where, Message: p, Fix: "see docs/agent-dispatch.md, Worker setup"})
	}
	if wf.WorkerSetupFull() {
		return out
	}
	root := ""
	if env.Project != nil && (env.Project.WorkDir != "" || env.Project.IssueDir != "") {
		root = env.Project.Root()
	} else if env.File != "" { // a lone file (lint.File): its folder
		root = filepath.Dir(env.File)
	}
	out = append(out, unknownKeptSkills(wf.Workers.Skills, root, where)...)
	if len(wf.Workers.MCP) == 0 {
		return out
	}
	owner := connectors.OwnerMCPServers(root, "")
	var have []string
	for name := range owner {
		have = append(have, name)
	}
	sort.Strings(have)
	for _, m := range wf.Workers.MCP {
		if m.Server == "" {
			continue
		}
		if _, ok := owner[m.Server]; !ok {
			known := "none"
			if len(have) > 0 {
				known = strings.Join(have, ", ")
			}
			out = append(out, Finding{
				Where:   where,
				Message: fmt.Sprintf("workers: mcp: names %q, which the owner's Claude Code config does not define: its workers start without it", m.Server),
				Fix:     "add it with claude mcp add, or name one of: " + known,
			})
		}
	}
	return out
}
