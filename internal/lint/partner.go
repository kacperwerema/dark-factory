package lint

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"github.com/kacperwerema/dark-factory/internal/partner"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// partnerFindings flags a department's partner: block that the pages could
// only show as a problem: no or an unknown adapter, a path that isn't a
// folder, and a commit: pin the checkout's HEAD doesn't match (one git
// call per partner, only here: pages never shell out). Part of the
// departments check (docs/departments.md#partners).
func partnerFindings(p *tracker.Project, d tracker.Department) []Finding {
	cfg := d.Partner
	if cfg == nil {
		return nil
	}
	var out []Finding
	if _, err := partner.For(*cfg); err != nil {
		out = append(out, Finding{Message: err.Error(), Fix: "set partner: adapter: to one of " + strings.Join(partner.Names(), ", ")})
	}
	dir := cfg.Dir(p.Root())
	if dir == "" {
		return append(out, Finding{Message: "partner " + cfg.Label() + " has no path:", Fix: "set partner: path: to the partner's checkout"})
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return append(out, Finding{Message: fmt.Sprintf("partner %s: path %s is not a folder", cfg.Label(), dir), Fix: "install the partner there, or fix partner: path:"})
	}
	if pin := strings.TrimSpace(cfg.Commit); pin != "" {
		head, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
		switch h := strings.TrimSpace(string(head)); {
		case err != nil:
			out = append(out, Finding{Message: fmt.Sprintf("partner %s: commit: %s set but %s is not a git checkout", cfg.Label(), pin, dir)})
		case !strings.HasPrefix(h, pin) && !strings.HasPrefix(pin, h):
			out = append(out, Finding{Message: fmt.Sprintf("partner %s: the checkout is at %s, pinned to %s", cfg.Label(), gitdir.ShortSHA(h), pin), Fix: "git -C " + dir + " checkout " + pin + ", or move the pin on purpose"})
		}
	}
	return out
}

// partnerApprovalFindings flags each of d's issues whose partner project
// has a gated stage approved at the partner with no owner decision on the
// issue (partner-approved:<stage>): the gate bridge's one checkable rule
// (docs/departments.md#the-owners-answer). Silent when the partner block
// is broken (partnerFindings says why) or its adapter can't list
// approvals.
func partnerApprovalFindings(env *Env, d tracker.Department) []Finding {
	p, cfg := env.Project, d.Partner
	if cfg == nil {
		return nil
	}
	if _, err := partner.For(*cfg); err != nil {
		return nil
	}
	if dir := cfg.Dir(p.Root()); dir == "" {
		return nil
	} else if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil
	}
	var out []Finding
	for _, issue := range env.Issues {
		if p.DepartmentOf(issue) != d.Name {
			continue
		}
		approvals, err := partner.Approvals(*cfg, p.Root(), issue)
		if err != nil {
			continue
		}
		for _, a := range partner.Unpaired(approvals, issue.Decisions()) {
			out = append(out, Finding{Where: issue.Slug, Message: partner.UnpairedLine(*cfg, a),
				Fix: "the owner answers the stage on Needs you (a D-n with ref " + partner.ApprovedRefPrefix + a.Stage + "); or the worker re-writes the checkpoint without the approval"})
		}
	}
	return out
}
