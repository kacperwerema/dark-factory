package tracker

import (
	"os"
	"path/filepath"
	"strings"
)

// Partner is an outside program a department hands its work to: a studio
// with its own stages and approval gates (OpenMontage for Video). The
// engine only reads the partner's files, through the adapter named here
// (internal/partner); swapping the partner is a change to this block, never
// to the department's workflow file
// (factory/video-department-openmontage-as-a-video-producer-department-template-workflow-connector-videos-on-the-shelf#D-4).
// See docs/departments.md#partners.
type Partner struct {
	// Name is what the pages call it ("OpenMontage"); the adapter's name
	// when empty.
	Name string `yaml:"name"`
	// Adapter is the reader that understands the partner's files.
	Adapter string `yaml:"adapter"`
	// Path is the user's own checkout of the partner, outside this repo;
	// ~ is the home folder, a relative path is from the project root.
	Path string `yaml:"path"`
	// Commit is the SHA the department is pinned to; lint compares it with
	// the checkout's HEAD.
	Commit string `yaml:"commit"`
	// LivePort is the partner's own local viewer (127.0.0.1:<port>),
	// linked only while it answers; 0 is none.
	LivePort int `yaml:"live_port"`
	// Exception is the decision ref (<slug>#D-<n>) recording the owner's
	// licence exception for a partner the catalog knows as AGPL: lint check
	// departments warns without it (docs/departments.md#the-licence-rule).
	Exception string `yaml:"exception"`
}

// Label is the partner's name on the pages.
func (pt Partner) Label() string {
	if s := strings.TrimSpace(pt.Name); s != "" {
		return s
	}
	return strings.TrimSpace(pt.Adapter)
}

// Dir is the partner's checkout as an absolute path ("" when unset): ~
// expanded, relative paths from root.
func (pt Partner) Dir(root string) string {
	path := strings.TrimSpace(pt.Path)
	switch {
	case path == "":
		return ""
	case path == "~" || strings.HasPrefix(path, "~/"):
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~"))
	case !filepath.IsAbs(path):
		path = filepath.Join(root, path)
	}
	return filepath.Clean(path)
}

// PartnerOf is the partner of the department issue belongs to, and that
// department's name; nil when the department names none (or the issue is
// Product's).
func (p *Project) PartnerOf(issue *Issue) (*Partner, string) {
	name := p.DepartmentOf(issue)
	if name == "" {
		return nil, ""
	}
	for i := range p.Departments {
		if p.Departments[i].Name == name {
			return p.Departments[i].Partner, name
		}
	}
	return nil, name
}

// PartnerApprovalHow is how a worker writes the partner's own approval of a
// stage, quoting the owner's decision ref, in the adapter's words.
// internal/partner sets it in init (it imports this package, not the other
// way round); unset, the briefing says it generically.
var PartnerApprovalHow func(pt Partner, stage, ref string) string

// PartnerBrief is the worker briefing's partner bullet: the gate bridge's
// one rule (docs/departments.md#the-owners-answer). "" when the issue's
// department names no partner.
func PartnerBrief(p *Project, issue *Issue, root string) string {
	pt, _ := p.PartnerOf(issue)
	if pt == nil {
		return ""
	}
	how := "write the partner's approval of <stage> the way its guide says, quoting <D-n ref>"
	if PartnerApprovalHow != nil {
		how = PartnerApprovalHow(*pt, "<stage>", "<D-n ref>")
	}
	return "- Partner: " + pt.Label() + " (adapter " + pt.Adapter + ", checkout " + pt.Dir(root) + "). " +
		"At a gated stage write its checkpoint as awaiting the owner and stop: the owner answers on Needs you or the Station, which records a D-n on this issue (ref partner-approved:<stage> or partner-sent-back:<stage>) and tells you. " +
		"Only then " + how + "; never without that D-n (health lint flags an approval with none). " +
		"A send-back: revise the stage and ask again. issue-cli show " + issue.Slug + " prints the partner's stage and the owner's answer.\n"
}
