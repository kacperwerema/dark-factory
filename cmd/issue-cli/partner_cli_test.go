package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// partnerCLIFixture is a project whose Video department (system Video)
// names the stand-in OpenMontage checkout as its partner; the issue is
// partner project project.
func partnerCLIFixture(t *testing.T, project, adapter string) (clip string) {
	t.Helper()
	f := newCallLogFixture(t, "backlog", "", "")
	om, err := filepath.Abs(filepath.Join("..", "..", "internal", "partner", "testdata", "openmontage"))
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(f.root, "issues", "Video"), 0755)
	mustWrite(t, filepath.Join(f.root, "issues", "Video", "clip.md"), "---\ntitle: \"Clip\"\nstatus: \"backlog\"\nsystem: \"Video\"\npartner_project: \""+project+"\"\n---\n\nBody.\n")
	cfg := os.Getenv("ISSUE_VIEWER_CONFIG")
	b, _ := os.ReadFile(cfg)
	mustWrite(t, cfg, string(b)+`    departments:
      - name: Video
        systems: [Video]
        partner:
          name: OpenMontage
          adapter: `+adapter+`
          path: `+om+`
          commit: 195eab2
`)
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	return filepath.Join(f.root, "issues", "Video", "clip.md")
}

func TestPartnerInShowAndDepartmentShow(t *testing.T) {
	partnerCLIFixture(t, "waiting", "openmontage")
	out, err := runCLI(t, "show", "video/clip")
	if err != nil {
		t.Fatal(out)
	}
	assertContains(t, out, "Partner: OpenMontage: pitch · awaiting you (project waiting at ")
	out, _ = runCLI(t, "department", "show", "video")
	assertContains(t, out, "Partner: OpenMontage (adapter openmontage) at ")
	assertContains(t, out, ", pinned 195eab2.")
	if out, _ := runCLI(t, "show", "cli/sample"); strings.Contains(out, "Partner:") {
		t.Errorf("a Product issue shows a partner:\n%s", out)
	}
}

// AC2 in issue-cli: an unknown format is said, never a stage.
func TestPartnerUnknownFormatInShow(t *testing.T) {
	partnerCLIFixture(t, "newer-format", "openmontage")
	out, _ := runCLI(t, "show", "video/clip")
	assertContains(t, out, `Partner: OpenMontage: partner format not understood (checkpoint_brief.json version "2.0")`)
}

func TestPartnerNoProjectYetNamesTheField(t *testing.T) {
	partnerCLIFixture(t, "not-made-yet", "openmontage")
	out, _ := runCLI(t, "show", "video/clip")
	assertContains(t, out, "Partner: OpenMontage: no project not-made-yet yet")
	assertContains(t, out, "issue-cli set-meta video/clip partner_project <id>")
}

func TestPartnerUnknownAdapterInDepartmentShow(t *testing.T) {
	partnerCLIFixture(t, "waiting", "nope")
	out, _ := runCLI(t, "department", "show", "video")
	assertContains(t, out, `Partner: OpenMontage (adapter nope: partner adapter "nope" is unknown (one of openmontage))`)
}

// The gate bridge in show: what the worker does next at the partner.
func TestPartnerAnswerInShow(t *testing.T) {
	clip := partnerCLIFixture(t, "waiting", "openmontage")
	out, _ := runCLI(t, "show", "video/clip")
	assertContains(t, out, "Waits for the owner on Needs you; write the partner's approval only after their D-n.")

	raw, _ := os.ReadFile(clip)
	mustWrite(t, clip, string(raw)+"\n## Decisions\n- **D-1** · 2026-11-02 · owner on Needs you: OpenMontage pitch approved, \"concept B\" (ref partner-approved:pitch@20261101T102000Z)\n")
	out, _ = runCLI(t, "show", "video/clip")
	assertContains(t, out, "OpenMontage: pitch · approved by you")
	assertContains(t, out, "The owner approved pitch (video/clip#D-1). Now re-write checkpoint_pitch.json with human_approved=True")
}

func TestPartnerUnpairedApprovalInShow(t *testing.T) {
	partnerCLIFixture(t, "all-done", "openmontage")
	out, _ := runCLI(t, "show", "video/clip")
	assertContains(t, out, "⚠ OpenMontage: pitch approved at the partner with no owner decision (partner-approved:pitch)")
}
