package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// notGuards are functions that read the worker identity without refusing an
// owner-only command, each with why. Everything else that tests for a worker
// (tracker.RoleWorker) or an agent session (agentSession) is a guard and must
// ask the sandbox pass (guardWho, guardAgent, guardNever, sandboxPass, or a
// helper that does: ownIssueOnly, refuseWorker, ownerOnly).
var notGuards = map[string]string{
	"agentSession":         "the identity itself",
	"whoAmI":               "the identity itself",
	"commentAuthor":        "signs comments",
	"isOwnWorker":          "keeps a worker's own assignee on its transition",
	"guardWho":             "the pass",
	"runOwnerStepAdd":      "anyone adds an owner step; the worker only picks the default issue",
	"guardAgent":           "the pass",
	"dueStandingJobs":      "lists the jobs a tick may start, for none to a worker; the start goes through startScheduledRun under queue next --start, whose worker refusal is queueStart's",
	"runBenchShow":         "hides holdout tasks from agents in a listing; nothing is changed",
	"flushRun":             "lead flush: narrows a worker to its own flush run, which only a human's start creates",
	"nightlyWords":         "bench nightly: never in a sandbox (guardNever), and a worker starts only its own run",
	"connectorCaller":      "connectors.Allowed decides by caller; connector exec/check start nothing the sandbox could contain, so they stay refused",
	"requestedByFor":       "attributes a request to the asking box",
	"runDecisionChallenge": "defaults --for to the worker's issue; ownIssueOnly is its guard",
	"runDecideExpiry":      "acts on ownerActFor's identity (guardWho \"change a hard decision\")",
	"runDecideAmend":       "acts on ownerActFor's identity (guardWho \"change a hard decision\")",
	"askerBox":             "attributes a handoff to the asking box",
	"runLeadWhoami":        "reports the identity",
	"memoryCaller":         "picks what memories a caller sees",
	"checkRoute":           "declared routes between departments, the same for any worker; not an owner-only command",
	"leadTickQueueSweep":   "lead tick's queue-low sweep: does nothing for a worker (refuses nothing); the tick's start keeps queueStart's worker refusal",
	"leadTickYoloMoves":    "lead tick's YOLO moves: does nothing for a worker (refuses nothing); each move is runTransition, which keeps its own-issue guard",
}

// guardHelpers ask the sandbox pass themselves.
var guardHelpers = map[string]bool{"guardWho": true, "guardAgent": true, "guardNever": true, "sandboxPass": true, "ownIssueOnly": true, "refuseWorker": true, "ownerOnly": true, "workerScopeRefusal": true, "ownerActFor": true}

// TestEveryWorkerGuardConsultsTheSandboxPass: each guard asks the sandbox
// pass under a verb in guardVerbs, and every verb there has a guard. A new
// guard's rule (sandboxPasses or neverInSandbox) is the owner's call, asked
// in that command's design (D-3).
func TestEveryWorkerGuardConsultsTheSandboxPass(t *testing.T) {
	fset := token.NewFileSet()
	files, _ := filepath.Glob("*.go")
	used := map[string]bool{}
	var unguarded []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			checksWorker, asks := false, false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.SelectorExpr:
					if id, ok := x.X.(*ast.Ident); ok && id.Name == "tracker" && x.Sel.Name == "RoleWorker" {
						checksWorker = true
					}
				case *ast.CallExpr:
					id, ok := x.Fun.(*ast.Ident)
					if !ok {
						return true
					}
					if id.Name == "agentSession" {
						checksWorker = true
					}
					if guardHelpers[id.Name] {
						asks = true
						for _, verb := range guardVerbArgs(id.Name, x) {
							used[verb] = true
							if _, ok := guardVerbs[verb]; !ok {
								t.Errorf("%s: %s(%q): the verb has no rule in guardVerbs (sandbox_pass.go). Whether it passes on a marked sandbox is the owner's call: ask in this command's design (Clarify), don't pick it yourself", fset.Position(x.Pos()), id.Name, verb)
							}
						}
					}
				}
				return true
			})
			if !checksWorker || asks || fn.Name.Name == "guardNever" || fn.Name.Name == "sandboxPass" {
				continue
			}
			if _, ok := notGuards[fn.Name.Name]; ok {
				continue
			}
			unguarded = append(unguarded, fset.Position(fn.Pos()).String()+" "+fn.Name.Name)
		}
	}
	sort.Strings(unguarded)
	for _, u := range unguarded {
		t.Errorf("%s tests for a worker or agent but never asks the sandbox pass: call guardWho/guardAgent with its verb (and add the verb to guardVerbs: the owner decides whether it passes on a sandbox), or list it in notGuards with why", u)
	}
	for verb := range guardVerbs {
		if !used[verb] {
			t.Errorf("guardVerbs has %q but no guard asks for it", verb)
		}
	}
	for name := range notGuards {
		if !strings.Contains(allSource(t), "func "+name+"(") && !strings.Contains(allSource(t), ") "+name+"(") {
			t.Errorf("notGuards names %s, which no longer exists", name)
		}
	}
}

// guardVerbArgs are the guard verbs a call names: the literal verb argument,
// with ownerOnly's "bench " prefix.
func guardVerbArgs(fn string, call *ast.CallExpr) []string {
	idx := map[string]int{"guardWho": 2, "guardAgent": 1, "guardNever": 1, "sandboxPass": 1, "ownIssueOnly": 3, "refuseWorker": 1, "ownerOnly": 1}[fn]
	if fn == "workerScopeRefusal" || fn == "ownerActFor" || idx >= len(call.Args) {
		return nil
	}
	lit, ok := call.Args[idx].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return nil // a helper passing its own parameter through
	}
	v, err := strconv.Unquote(lit.Value)
	if err != nil {
		return nil
	}
	if fn == "ownerOnly" {
		v = "bench " + v
	}
	return []string{v}
}

var allSourceCache string

func allSource(t *testing.T) string {
	if allSourceCache != "" {
		return allSourceCache
	}
	files, _ := filepath.Glob("*.go")
	var b strings.Builder
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(src)
	}
	allSourceCache = b.String()
	return allSourceCache
}
