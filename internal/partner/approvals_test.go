package partner

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func approvalsOf(t *testing.T, project string) []Approval {
	t.Helper()
	wd, _ := os.Getwd()
	got, err := Approvals(omCfg(), wd, issueWith("video/"+project, ""))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestOpenMontageApprovals(t *testing.T) {
	got := approvalsOf(t, "all-done")
	var words []string
	for _, a := range got {
		words = append(words, a.Stage+map[bool]string{true: "+gated", false: ""}[a.Gated]+map[bool]string{true: "+approved", false: ""}[a.Approved])
	}
	if want := "brief+approved,pitch+gated+approved,storyboard+gated+approved,render+approved"; strings.Join(words, ",") != want {
		t.Fatalf("approvals %v, want %s", words, want)
	}
	if w := approvalsOf(t, "waiting"); len(w) != 2 || w[1].Stage != "pitch" || !w[1].Gated || w[1].Approved {
		t.Fatalf("waiting: %+v, want pitch gated and not approved", w)
	}
	if n := approvalsOf(t, "not-started-yet"); n != nil {
		t.Fatalf("a missing project lists %v", n)
	}
}

func decisionsFrom(t *testing.T, lines ...string) []tracker.Decision {
	t.Helper()
	body := "## Decisions\n" + strings.Join(lines, "\n") + "\n"
	return tracker.ParseDecisions("video/x", body)
}

// AC2: a gated stage approved at the partner with no owner decision for it
// is flagged; one with the owner's partner-approved D-n is not.
func TestUnpairedFlagsApprovalWithoutOwnerDecision(t *testing.T) {
	approvals := approvalsOf(t, "all-done")
	decisions := decisionsFrom(t,
		`- **D-1** · 2026-11-02 · owner on Needs you: OpenMontage pitch approved, "concept B" (ref partner-approved:pitch@20261101T102000Z)`,
		`- **D-2** · 2026-11-02 · owner on Needs you: OpenMontage storyboard sent back, "too dark" (ref partner-sent-back:storyboard)`,
		`- **D-3** · 2026-11-02 · deferred · owner: OpenMontage storyboard approved (ref partner-approved:storyboard)`,
	)
	un := Unpaired(approvals, decisions)
	if len(un) != 1 || un[0].Stage != "storyboard" {
		t.Fatalf("unpaired %+v, want storyboard only (a send-back or a deferred decision is no approval; ungated stages aren't checked)", un)
	}
	if line := UnpairedLine(omCfg(), un[0]); line != "OpenMontage: storyboard approved at the partner with no owner decision (partner-approved:storyboard)" {
		t.Errorf("line %q", line)
	}
	if un := Unpaired(approvals, nil); len(un) != 2 {
		t.Errorf("with no decisions %d unpaired, want pitch and storyboard", len(un))
	}
}

func TestAnswerToTheAskItAnswers(t *testing.T) {
	asked := time.Date(2026, 11, 1, 10, 20, 0, 0, time.UTC)
	ans := Answers(decisionsFrom(t,
		`- **D-1** · 2026-11-02 · owner on Needs you: OpenMontage pitch sent back, "more contrast" (ref partner-sent-back:pitch@20261101T090000Z)`,
		`- **D-4** · 2026-11-02 · owner on Needs you: OpenMontage pitch approved, "good now" (ref partner-approved:pitch@20261101T102000Z)`,
	))
	a, ok := AnswerTo(ans, "pitch", asked.Add(300*time.Millisecond))
	if !ok || !a.Approved || a.Ref != "video/x#D-4" || a.Quote != "good now" {
		t.Fatalf("answer %+v %v, want D-4 approved with its words", a, ok)
	}
	if _, ok := AnswerTo(ans, "pitch", asked.Add(time.Hour)); ok {
		t.Fatal("a newer ask of the same stage counts as answered")
	}
	if _, ok := AnswerTo(ans, "storyboard", asked); ok {
		t.Fatal("another stage counts as answered")
	}
}

func TestAnswerRefs(t *testing.T) {
	asked := time.Date(2026, 11, 1, 11, 20, 5, 0, time.FixedZone("CET", 3600))
	if got := ApprovedRef(" pitch ", asked); got != "partner-approved:pitch@20261101T102005Z" {
		t.Fatalf("ApprovedRef %q", got)
	}
	if SentBackRef("pitch", time.Time{}) != "partner-sent-back:pitch" {
		t.Fatal("SentBackRef with no ask time")
	}
	if !IsAnswerRef("partner-approved:pitch") || !IsAnswerRef("partner-sent-back:x") || IsAnswerRef("N-26") {
		t.Fatal("IsAnswerRef")
	}
	if !strings.Contains(OpenMontage{}.ApprovalHow("pitch", "video/x#D-4"), "checkpoint_pitch.json with human_approved=True") {
		t.Fatal("ApprovalHow")
	}
}

type noApprovals struct{}

func (noApprovals) Status(tracker.Partner, string, string) (Status, error) { return Status{}, nil }

func TestApprovalsNeedAnApprover(t *testing.T) {
	defer Register("plain", noApprovals{})()
	wd, _ := os.Getwd()
	cfg := tracker.Partner{Adapter: "plain", Path: fixture}
	if _, err := Approvals(cfg, wd, issueWith("video/all-done", "")); err != ErrNoApprovals {
		t.Fatalf("err %v, want ErrNoApprovals", err)
	}
	if !strings.Contains(ApprovalHow(cfg, "pitch", "video/x#D-1"), "quoting video/x#D-1") {
		t.Fatal("generic ApprovalHow")
	}
}

// The video worker's briefing names OpenMontage's own approval: the
// checkpoint's human_approved plus a decision_log entry quoting the D-n.
func TestBriefingSaysHowOpenMontageIsApproved(t *testing.T) {
	cfg := omCfg()
	proj := &tracker.Project{Departments: []tracker.Department{{Name: "Video", Systems: []string{"Video"}, Partner: &cfg}}}
	got := tracker.PartnerBrief(proj, &tracker.Issue{Slug: "video/x", System: "Video"}, t.TempDir())
	if !strings.Contains(got, "Only then re-write checkpoint_<stage>.json with human_approved=True and append a decision_log entry for stage <stage> quoting <D-n ref>; never without that D-n") {
		t.Fatalf("briefing:\n%s", got)
	}
}
