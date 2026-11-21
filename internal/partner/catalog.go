package partner

import (
	"bytes"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kacperwerema/dark-factory/cmd/issue-cli/templates"
)

// The partners catalog: the outside tools a department can use for one of
// its slots, one row per tool, bundled as cmd/issue-cli/templates/
// partners.yaml. `issue-cli department partners` lists it and `department
// add` names the rows for a template's partner_slots:. Listing a row turns
// nothing on. The file is public: public facts only, money in words
// (factory/department-budgets-what-each-department-spends-in-tokens-and-money-shown-in-its-room#D-2).
// See docs/departments.md#the-partners-catalog.

// CatalogFile is the catalog's path inside templates.FS.
const CatalogFile = "partners.yaml"

// Slots are the catalog's slots, in the order they are listed.
var Slots = []string{"analytics", "email", "seo", "social", "video", "support", "cms", "payments"}

// Shapes and runs a row may have.
var (
	Shapes = []string{"tool", "studio", "service"}
	Runs   = []string{"always", "per run"}
)

// CatalogRow is one tool in the catalog.
type CatalogRow struct {
	ID         string `yaml:"id" json:"id"`
	Name       string `yaml:"name" json:"name"`
	Slot       string `yaml:"slot" json:"slot"`
	Shape      string `yaml:"shape" json:"shape"`
	Licence    string `yaml:"licence" json:"licence"`
	LicenceURL string `yaml:"licence_url" json:"licence_url"`
	// LicenceRead is the date the LICENSE file was read (YYYY-MM-DD).
	LicenceRead string `yaml:"licence_read" json:"licence_read"`
	Upstream    string `yaml:"upstream" json:"upstream"`
	// Exception is, on an AGPL row, the decision ref recording the
	// owner's exception for the tool, or "none: <why>".
	Exception string `yaml:"exception" json:"exception,omitempty"`
	Memory    string `yaml:"memory" json:"memory"`
	Runs      string `yaml:"runs" json:"runs"`
	Money     string `yaml:"money" json:"money"`
	Default   bool   `yaml:"default" json:"default,omitempty"`
	Note      string `yaml:"note" json:"note,omitempty"`
	// Setup is the one issue-cli command the owner runs to stand the tool
	// up on this machine ("issue-cli cms up"), when the engine has one;
	// department add prints it under a slot whose default row has one.
	// It turns nothing on: the connector stays the owner's to add.
	Setup string `yaml:"setup" json:"setup,omitempty"`
	// Marks are paths only the tool's own checkout has (a trailing / is a
	// folder): TestTrackedFilesHoldNoPartnerFile fails on a tracked path
	// carrying one, since a partner is never vendored. Required on an AGPL
	// row.
	Marks []string `yaml:"marks" json:"marks,omitempty"`
}

// AGPL reports whether the row's licence is an AGPL one.
func (r CatalogRow) AGPL() bool {
	return strings.Contains(strings.ToUpper(r.Licence), "AGPL")
}

// NoException reports whether an AGPL row says no exception is recorded.
func (r CatalogRow) NoException() bool {
	return strings.HasPrefix(r.Exception, "none")
}

var (
	catalogOnce sync.Once
	catalogRows []CatalogRow
	catalogErr  error
)

// Catalog is the bundled catalog's rows in file order. A row that breaks
// the rules (CheckRows) makes it an error.
func Catalog() ([]CatalogRow, error) {
	catalogOnce.Do(func() {
		raw, err := fs.ReadFile(templates.FS, CatalogFile)
		if err != nil {
			catalogErr = fmt.Errorf("partners catalog: %w", err)
			return
		}
		catalogRows, catalogErr = ParseCatalog(raw)
	})
	return slices.Clone(catalogRows), catalogErr
}

// ParseCatalog decodes a catalog strictly (a key it doesn't know is an
// error) and checks its rows.
func ParseCatalog(raw []byte) ([]CatalogRow, error) {
	var doc struct {
		Partners []CatalogRow `yaml:"partners"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("partners catalog: %w", err)
	}
	if err := CheckRows(doc.Partners); err != nil {
		return nil, err
	}
	return doc.Partners, nil
}

// exceptionRef is a decision ref: <slug>#D-<n>.
var exceptionRef = regexp.MustCompile(`^[a-z0-9][a-z0-9/-]*#D-[0-9]+$`)

// CheckRows is the catalog's rules: each row has an id (once), a name, a
// known slot, shape and runs, a licence with the URL and date it was read,
// an upstream, what it needs and whether it costs money; an AGPL row names
// its exception's decision ref or says "none: <why>", and no other row has
// one; a slot has at most one default.
func CheckRows(rows []CatalogRow) error {
	seen := map[string]bool{}
	defaults := map[string]string{}
	for i, r := range rows {
		where := fmt.Sprintf("partners catalog: row %d (%s)", i+1, r.ID)
		switch {
		case strings.TrimSpace(r.ID) == "":
			return fmt.Errorf("%s: no id", where)
		case seen[r.ID]:
			return fmt.Errorf("%s: id twice", where)
		case strings.TrimSpace(r.Name) == "":
			return fmt.Errorf("%s: no name", where)
		case !slices.Contains(Slots, r.Slot):
			return fmt.Errorf("%s: slot %q is not one of %s", where, r.Slot, strings.Join(Slots, ", "))
		case !slices.Contains(Shapes, r.Shape):
			return fmt.Errorf("%s: shape %q is not one of %s", where, r.Shape, strings.Join(Shapes, ", "))
		case !slices.Contains(Runs, r.Runs):
			return fmt.Errorf("%s: runs %q is not one of %s", where, r.Runs, strings.Join(Runs, ", "))
		case strings.TrimSpace(r.Licence) == "":
			return fmt.Errorf("%s: no licence", where)
		case !strings.HasPrefix(r.LicenceURL, "https://"):
			return fmt.Errorf("%s: licence_url must be the https URL of the LICENSE file read", where)
		case !strings.HasPrefix(r.Upstream, "https://"):
			return fmt.Errorf("%s: upstream must be an https URL", where)
		case strings.TrimSpace(r.Memory) == "":
			return fmt.Errorf("%s: no memory: say what it needs, or unmeasured", where)
		case strings.TrimSpace(r.Money) == "":
			return fmt.Errorf("%s: no money: say in words whether it costs money", where)
		}
		if _, err := time.Parse("2006-01-02", r.LicenceRead); err != nil {
			return fmt.Errorf("%s: licence_read %q is not a YYYY-MM-DD date", where, r.LicenceRead)
		}
		if r.AGPL() {
			ref := strings.TrimSpace(r.Exception)
			if !exceptionRef.MatchString(ref) && !strings.HasPrefix(ref, "none: ") {
				return fmt.Errorf("%s: an AGPL row names the decision recording its exception (<slug>#D-<n>) or says \"none: <why>\"", where)
			}
			if len(r.Marks) == 0 {
				return fmt.Errorf("%s: an AGPL row lists marks: (paths only its checkout has), so the tracked-file guard can tell a vendored copy", where)
			}
		} else if r.Exception != "" {
			return fmt.Errorf("%s: exception: is for AGPL rows only", where)
		}
		if r.Default {
			if d := defaults[r.Slot]; d != "" {
				return fmt.Errorf("%s: slot %s has a default already (%s)", where, r.Slot, d)
			}
			defaults[r.Slot] = r.ID
		}
		seen[r.ID] = true
	}
	return nil
}

// Match is the row whose id or name equals one of names (case-insensitive,
// blanks skipped), or nil: how lint finds a connector's or partner's row.
func Match(rows []CatalogRow, names ...string) *CatalogRow {
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		for i := range rows {
			if strings.EqualFold(rows[i].ID, n) || strings.EqualFold(rows[i].Name, n) {
				return &rows[i]
			}
		}
	}
	return nil
}

// ForSlot is the rows of one slot, the default first, else in file order.
func ForSlot(rows []CatalogRow, slot string) []CatalogRow {
	var out []CatalogRow
	for _, r := range rows {
		if r.Slot == slot {
			out = append(out, r)
		}
	}
	slices.SortStableFunc(out, func(a, b CatalogRow) int {
		switch {
		case a.Default == b.Default:
			return 0
		case a.Default:
			return -1
		}
		return 1
	})
	return out
}
