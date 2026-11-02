package depttemplate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kacperwerema/dark-factory/cmd/issue-cli/templates"
	"github.com/kacperwerema/dark-factory/internal/gitfile"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

// Updating an installed department to the template's newer text
// (`issue-cli department update`, the Updates page's "Department templates
// with new text"). A file, or a role / mission / goal name in projects.yaml,
// is replaced only when it still holds a text an earlier release bundled,
// byte for byte: the owner never edited it. Anything else is the owner's
// and is kept. The workflow file counts too (template path "workflow"):
// one never edited or upgraded is still a bundled text, so it takes the
// new one whole; one `workflow upgrade` or the owner changed is kept.

// earlier is what earlier releases bundled for one template: each file's
// sha256 by its path in the template folder ("workflow" for the workflow
// file), and the projects.yaml values. Whenever a template's text changes,
// add the old text's hash here and the old file under
// testdata/earlier/<key>/<path>.<n> (TestEarlierHashesAreBacked checks each
// hash has its file and none is today's text).
type earlier struct {
	Files         map[string][]string
	Role, Mission []string
	GoalNames     map[string][]string
}

var earlierTexts = map[string]earlier{
	"marketing": {
		Files: map[string][]string{
			"role.md": {
				"5d566b734815afb8e62f4855cf1d84b7c3a3926e15f42539b3235e48d1231a70",
				"907d58571442e47138c82723d4b6bdb61d3e53397b6b51f071bf9ffb31a391bd",
				"348cd5805be38a4b9a1bc6440a125354d89ad129560803f80f2f19631aa7cc44",
			},
			"jobs/weekly-seo-pass.md": {
				"ceebb064382c0d1ce29bbf644c9e0abd0c384a5f5697119fb949f498dd192524",
				"a7c01c3b318fa469d416e382c43f9f9fa944b7fa059da1c5eb3e2326130ea15c",
				"b69458ba3738b42a9fb48f597ce4254ff1c1c15f6699961f3795381d22e6dd04",
				"c4d8b20e5d9cc91f948cdb95baa95f3de12de39730d39257aba3888f1b04bd0c",
			},
			"workflow": {
				"b52ab5a5500eaf1e272cc4a17b2f5a6eb2756f2463eb23857d9d73e7847e411d",
				// v0.59: features: installed in the catalog's old order.
				"c12bde7ed8e3354dc35b4453904ce4c564deeb20f47c96d04f8976aa4b93982b",
			},
		},
		Role:      []string{"SEO and content for the client sites"},
		Mission:   []string{"Every client site refreshed every week"},
		GoalNames: map[string][]string{"pages": {"Client pages refreshed"}},
	},
	"gtm": {
		Files: map[string][]string{
			"role.md":             {"bb5f53b77a435b61c391c63e41a7dc7b80971cebdc0c659c9c2279719987c5a3"},
			"jobs/weekly-plan.md": {"78566c709d40ee47dfab40337ea00f78507581dc9c5da7dd5de33ecc38f404f1"},
			// v0.59: features: installed in the catalog's old order.
			"workflow": {"cc108ff992c76240154361e3be9af70b3e248bd2aa7866cd4de7cf2391924d53"},
		},
		Role:      []string{"Brings in the next clients: marketing and sales under one lead"},
		Mission:   []string{"A steady pipeline of new clients"},
		GoalNames: map[string][]string{"pipeline": {"New client conversations"}},
	},
}

// Update item states.
const (
	UpdateCurrent = "current" // already the template's text
	UpdateOld     = "old"     // an earlier template's text, untouched: Apply replaces it
	UpdateEdited  = "edited"  // the owner's own text: kept
)

// UpdateItem is one file or projects.yaml value of an installed department.
type UpdateItem struct {
	// What is the file's path (relative to the project root) or the field:
	// "role", "mission", "goal <id> name".
	What  string `json:"what"`
	State string `json:"state"`
	// File is set for a file; Have and Want are a field's values.
	File bool   `json:"file,omitempty"`
	Have string `json:"have,omitempty"`
	Want string `json:"want,omitempty"`
	abs  string
	tmpl string // the file's path in the template folder
	data []byte
	keys []string
}

// Update is what bringing department Name up to its template's text does.
type Update struct {
	Template *Template    `json:"template"`
	Name     string       `json:"name"`
	Key      string       `json:"key"`
	Config   string       `json:"config"`
	Items    []UpdateItem `json:"items"`
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// file is the template's current text of rel (role.md, jobs/<id>.md, or
// "workflow": its bundled workflow template).
func (t *Template) file(rel string) ([]byte, error) {
	if rel == "workflow" {
		if t.Workflow == "" {
			return nil, fmt.Errorf("template %s has no workflow", t.Key)
		}
		return workflowupgrade.ReadTemplate(t.Workflow)
	}
	return fs.ReadFile(templates.FS, path.Join(templatesDir, t.Key, rel))
}

// goal is the template's goal id, nil when it has none.
func (t *Template) goal(id string) *tracker.Goal {
	for i := range t.Goals {
		if strings.EqualFold(t.Goals[i].ID, id) {
			return &t.Goals[i]
		}
	}
	return nil
}

// NewUpdate compares proj's department installed from t with t's text.
func NewUpdate(proj *tracker.Project, t *Template) (*Update, error) {
	d := t.InstalledAs(proj)
	if d == nil {
		return nil, fmt.Errorf("%s has no department from the %s template (issue-cli department add %s)", proj.Name, t.Key, t.Key)
	}
	e := earlierTexts[t.Key]
	u := &Update{Template: t, Name: d.Name, Key: tracker.DepartmentKey(d.Name), Config: proj.ConfigPath}
	addFile := func(rel, tmplRel string) {
		if rel == "" || strings.HasPrefix(rel, "builtin:") {
			return
		}
		abs := rel
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(proj.Root(), filepath.FromSlash(rel))
		}
		have, err := os.ReadFile(abs)
		if err != nil {
			return
		}
		want, err := t.file(tmplRel)
		if err != nil {
			return
		}
		it := UpdateItem{What: rel, File: true, abs: abs, tmpl: tmplRel, data: want, State: UpdateEdited}
		switch {
		case string(have) == string(want):
			it.State = UpdateCurrent
		case slices.Contains(e.Files[tmplRel], sha(have)):
			it.State = UpdateOld
		}
		u.Items = append(u.Items, it)
	}
	addField := func(what, have, want string, old []string, keys ...string) {
		if strings.TrimSpace(have) == "" || want == "" {
			return
		}
		it := UpdateItem{What: what, Have: have, Want: want, keys: keys, State: UpdateEdited}
		switch {
		case have == want:
			it.State = UpdateCurrent
		case slices.Contains(old, have):
			it.State = UpdateOld
		}
		u.Items = append(u.Items, it)
	}
	addFile(d.Prompt, "role.md")
	if t.Workflow != "" {
		addFile(d.Workflow, "workflow")
	}
	for _, j := range t.Jobs {
		for _, dj := range d.Jobs {
			if strings.EqualFold(dj.ID, j.ID) {
				addFile(dj.Brief, j.Brief)
			}
		}
	}
	addField("role", d.Role, t.Role, e.Role, "role")
	addField("mission", d.Mission, t.Mission, e.Mission, "mission")
	for _, g := range t.Goals {
		for _, dg := range d.Goals {
			if strings.EqualFold(dg.ID, g.ID) {
				addField("goal "+g.ID+" name", dg.Name, g.Name, e.GoalNames[g.ID], "goals", dg.ID, "name")
			}
		}
	}
	return u, nil
}

// Pending counts the items Apply replaces.
func (u *Update) Pending() int { return u.count(UpdateOld) }

// Edited counts the owner's own texts Apply keeps.
func (u *Update) Edited() int { return u.count(UpdateEdited) }

func (u *Update) count(state string) int {
	n := 0
	for _, it := range u.Items {
		if it.State == state {
			n++
		}
	}
	return n
}

// Apply replaces every old item with the template's text: files first
// (each read again: one edited since the plan is kept), then the
// projects.yaml values, each spliced in as text so comments stay.
func (u *Update) Apply(proj *tracker.Project) error {
	e := earlierTexts[u.Template.Key]
	for _, it := range u.Items {
		if it.State != UpdateOld || !it.File {
			continue
		}
		have, err := os.ReadFile(it.abs)
		if err != nil {
			return err
		}
		if !slices.Contains(e.Files[it.tmpl], sha(have)) {
			return fmt.Errorf("%s changed since it was read: left as it is (files replaced before it stay replaced); run the update again to finish", it.What)
		}
		perm := os.FileMode(0o644)
		if info, err := os.Stat(it.abs); err == nil {
			perm = info.Mode().Perm()
		}
		if err := gitfile.WriteAtomic(it.abs, it.data, perm); err != nil {
			return fmt.Errorf("writing %s: %w (files replaced before it stay replaced; run the update again to finish)", it.What, err)
		}
	}
	for _, it := range u.Items {
		if it.State != UpdateOld || it.File {
			continue
		}
		if err := SetField(u.Config, proj.Slug, u.Name, it.keys, it.Have, it.Want); err != nil {
			return fmt.Errorf("%s: %w (what was replaced before it stays replaced; run the update again to finish)", it.What, err)
		}
	}
	return nil
}

// SideFile is a file SideFiles wrote, or kept because one with other text
// was already there.
type SideFile struct {
	Path string `json:"path"`
	Kept bool   `json:"kept,omitempty"`
}

// SidePath is the comparison copy SideFiles writes for p (role.md →
// role.template.md): lint's untracked check leaves exactly those out.
func SidePath(p string) string {
	ext := path.Ext(p)
	return strings.TrimSuffix(p, ext) + ".template" + ext
}

// SideFiles writes the template's text next to each file the owner edited
// (role.md → role.template.md), to compare by hand. A side file already
// there with other text is never overwritten: it is reported as kept.
// Paths are relative to the project root.
func (u *Update) SideFiles() ([]SideFile, error) {
	var out []SideFile
	for _, it := range u.Items {
		if it.State != UpdateEdited || !it.File {
			continue
		}
		side := SidePath(it.abs)
		sf := SideFile{Path: SidePath(it.What)}
		if have, err := os.ReadFile(side); err == nil {
			sf.Kept = string(have) != string(it.data)
			out = append(out, sf)
			continue
		}
		if err := gitfile.WriteAtomic(side, it.data, 0o644); err != nil {
			return out, err
		}
		out = append(out, sf)
	}
	return out, nil
}

// Updates are proj's installed departments whose template has newer text
// for a file or value the owner never edited.
func Updates(proj *tracker.Project) []*Update {
	var out []*Update
	for _, t := range List() {
		if !t.Installed(proj) {
			continue
		}
		if u, err := NewUpdate(proj, t); err == nil && u.Pending() > 0 {
			out = append(out, u)
		}
	}
	return out
}
