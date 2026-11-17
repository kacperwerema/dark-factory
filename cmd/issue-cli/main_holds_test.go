package main

import (
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// stubMainHolds makes dispatch and start see hold for every issue.
func stubMainHolds(t *testing.T, hold *tracker.MainHold) {
	t.Helper()
	orig := dispatchMainHolds
	dispatchMainHolds = func(string, *tracker.Issue) *tracker.MainHold { return hold }
	t.Cleanup(func() { dispatchMainHolds = orig })
}

func stubHold() *tracker.MainHold {
	return &tracker.MainHold{Main: "main", Since: "2026-11-15", Tests: []tracker.MainHoldTest{{
		Name: "TestStub", File: "v/stub_test.go",
		Commits: []tracker.MainHoldCommit{{Sha: "2c4c60e", Date: "2026-11-15", Subject: "test(v): the stub goes through the helper"}},
	}}}
}

func TestDispatchSaysMainMayAlreadyHoldItAndStillStarts(t *testing.T) {
	proj := makeGateProject(t)
	stubMainHolds(t, stubHold())
	captureStart(t, "dispatched")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runDispatch(ctx, []string{"loner", "--quote", "start loner"}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	want := "  main may already hold it: 2c4c60e test(v): the stub goes through the helper (TestStub); the briefing says so\n"
	if !strings.Contains(out, "✓ Dispatched agent-loner for loner") || !strings.Contains(out, want) {
		t.Errorf("stdout lacks the dispatch or %q:\n%s", want, out)
	}

	captureStart(t, "reattached")
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runDispatch(ctx, []string{"loner", "--quote", "start loner"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), "main may already hold it") {
		t.Errorf("a re-attach repeats the hint:\n%s", stdout)
	}
}

func TestDispatchSaysNothingWhenMainHoldsNothing(t *testing.T) {
	proj := makeGateProject(t)
	stubMainHolds(t, nil)
	captureStart(t, "dispatched")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runDispatch(ctx, []string{"loner", "--quote", "start loner"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), "main may already hold it") {
		t.Errorf("hint without a hold:\n%s", stdout)
	}
}

func TestStartSaysAlreadyOnMainUnlessTheBriefingDid(t *testing.T) {
	proj, _ := makeDispatchPromptProject(t)
	hold := stubHold()
	stubMainHolds(t, hold)
	issue := &tracker.Issue{Slug: "v/stub"}

	ctx, stdout, _ := newTestContext(proj, false)
	printStartMainHolds(ctx, nil, issue)
	out := stdout.String()
	for _, want := range []string{"== Already on main? ==", "- TestStub: 2c4c60e 2026-11-15 test(v)", "go test -count=1 -run '^TestStub$' ./v/"} {
		if !strings.Contains(out, want) {
			t.Errorf("start misses %q:\n%s", want, out)
		}
	}

	briefed := &dispatchBrief{norm: normSpace(tracker.MainHoldsPromptSection(hold))}
	ctx, stdout, _ = newTestContext(proj, false)
	printStartMainHolds(ctx, briefed, issue)
	if stdout.Len() != 0 {
		t.Errorf("start repeats what the briefing holds:\n%s", stdout)
	}

	hold.Tests[0].Commits = append([]tracker.MainHoldCommit{{Sha: "9f9f9f9", Date: "2026-11-17", Subject: "test(v): later"}}, hold.Tests[0].Commits...)
	ctx, stdout, _ = newTestContext(proj, false)
	printStartMainHolds(ctx, briefed, issue)
	if !strings.Contains(stdout.String(), "9f9f9f9") || !strings.Contains(stdout.String(), "2c4c60e") {
		t.Errorf("a commit since the dispatch should print the block again:\n%s", stdout)
	}
}
