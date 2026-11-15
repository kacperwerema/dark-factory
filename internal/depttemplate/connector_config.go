package depttemplate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Planned is a projects.yaml edit worked out but not written: the page
// shows its Diff, and Write applies it only while the file still reads
// Before (the owner saw that diff, not another).
type Planned struct {
	Path, Slug    string
	Before, After []byte
	check         func(*tracker.Project) error
}

// Sum names the edit: the page sends it back with the save.
func (pl *Planned) Sum() string {
	h := sha256.New()
	h.Write(pl.Before)
	h.Write([]byte{0})
	h.Write(pl.After)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// ErrChanged is a save whose file changed after its diff was shown.
var ErrChanged = fmt.Errorf("projects.yaml changed since the diff was shown: look at the new diff and save again")

// Write applies the edit: the file must still read Before, and the result
// must load and pass the edit's check before it replaces the file.
func (pl *Planned) Write() error {
	now, err := os.ReadFile(pl.Path)
	if err != nil {
		return err
	}
	if !bytes.Equal(now, pl.Before) {
		return ErrChanged
	}
	return writeChecked(pl.Path, pl.After, pl.check, pl.Slug)
}

// deptItem finds department name's item in project slug of the config.
func deptItem(configPath, slug, name string) (string, []byte, *yaml.Node, error) {
	path, raw, proj, err := readProject(configPath, slug)
	if err != nil {
		return "", nil, nil, err
	}
	_, depts := mapValue(proj, "departments")
	if depts == nil || depts.Kind != yaml.SequenceNode || depts.Style&yaml.FlowStyle != 0 {
		return "", nil, nil, fmt.Errorf("%s: project %s has no departments: block list", path, slug)
	}
	for _, it := range depts.Content {
		if _, nv := mapValue(it, "name"); nv != nil && tracker.DepartmentKey(nv.Value) == tracker.DepartmentKey(name) {
			if it.Style&yaml.FlowStyle != 0 {
				return "", nil, nil, fmt.Errorf("%s: department %s is written in flow style; edit it by hand", path, name)
			}
			return path, raw, it, nil
		}
	}
	return "", nil, nil, fmt.Errorf("%s: project %s has no department %s", path, slug, name)
}

// PlanConnector adds item (a "- name: <connector>" block at column 0) to
// department dept's connectors:, making the key when it has none.
func PlanConnector(configPath, slug, dept, connector, item string) (*Planned, error) {
	path, raw, d, err := deptItem(configPath, slug, dept)
	if err != nil {
		return nil, err
	}
	lines := splitLines(raw)
	var at int
	var block string
	ck, cs := mapValue(d, "connectors")
	switch {
	case cs != nil && cs.Kind == yaml.SequenceNode && cs.Style&yaml.FlowStyle == 0 && len(cs.Content) > 0:
		for _, c := range cs.Content {
			if _, n := mapValue(c, "name"); n != nil && n.Value == connector {
				return nil, fmt.Errorf("%s: department %s has a connector %s already", path, dept, connector)
			}
		}
		at, block = lastLine(cs), indentBlock(item, dashColumn(lines, cs.Content[0]))
	case ck != nil && (cs == nil || isNull(cs)):
		at, block = ck.Line, indentBlock(item, ck.Column+1)
	case ck != nil:
		return nil, fmt.Errorf("%s: department %s's connectors: is not a block list; add the connector by hand:\n%s", path, dept, item)
	default:
		col := d.Content[0].Column - 1
		at, block = lastLine(d), strings.Repeat(" ", col)+"connectors:\n"+indentBlock(item, col+2)
	}
	return &Planned{Path: path, Slug: slug, Before: raw, After: insertAfter(lines, at, block), check: func(p *tracker.Project) error {
		if findConnector(p, dept, connector) == nil {
			return fmt.Errorf("connector %s/%s is not in the result", tracker.DepartmentKey(dept), connector)
		}
		return nil
	}}, nil
}

var secretVarRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// PlanSecrets adds VAR: <ref> under connector's secrets: and VAR to its
// optional: list for each var it lacks; nil when every var is there.
func PlanSecrets(configPath, slug, dept, connector string, refs map[string]string) (*Planned, error) {
	path, raw, _, err := deptItem(configPath, slug, dept)
	if err != nil {
		return nil, err
	}
	vars := make([]string, 0, len(refs))
	for v, ref := range refs {
		if !secretVarRe.MatchString(v) {
			return nil, fmt.Errorf("env var %q is not a plain NAME", v)
		}
		if _, err := tracker.ParseSecretRef(ref); err != nil {
			return nil, fmt.Errorf("secret %s: %w", v, err)
		}
		vars = append(vars, v)
	}
	slices.Sort(vars)
	out := raw
	for _, v := range vars {
		next, err := addSecret(path, out, slug, dept, connector, v, refs[v])
		if err != nil {
			return nil, err
		}
		out = next
	}
	for _, v := range vars {
		next, err := addOptional(path, out, slug, dept, connector, v)
		if err != nil {
			return nil, err
		}
		out = next
	}
	if bytes.Equal(out, raw) {
		return nil, nil
	}
	return &Planned{Path: path, Slug: slug, Before: raw, After: out, check: func(p *tracker.Project) error {
		c := findConnector(p, dept, connector)
		if c == nil {
			return fmt.Errorf("connector %s/%s is gone from the result", tracker.DepartmentKey(dept), connector)
		}
		for _, v := range vars {
			if c.Secrets[v] != refs[v] || !c.IsOptional(v) {
				return fmt.Errorf("connector %s's secret %s is not %s and optional in the result", connector, v, refs[v])
			}
		}
		return nil
	}}, nil
}

// connectorNode finds connector's item in dept's connectors: of raw.
func connectorNode(path string, raw []byte, slug, dept, connector string) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil || len(doc.Content) == 0 {
		return nil, fmt.Errorf("%s: does not parse", path)
	}
	_, projects := mapValue(doc.Content[0], "projects")
	if projects == nil {
		return nil, fmt.Errorf("%s has no projects: list", path)
	}
	for _, p := range projects.Content {
		s := ""
		if _, n := mapValue(p, "slug"); n != nil {
			s = n.Value
		}
		if s == "" {
			if _, n := mapValue(p, "name"); n != nil {
				s = tracker.Slugify(n.Value)
			}
		}
		if s != slug {
			continue
		}
		_, depts := mapValue(p, "departments")
		if depts == nil {
			break
		}
		for _, d := range depts.Content {
			if _, nv := mapValue(d, "name"); nv == nil || tracker.DepartmentKey(nv.Value) != tracker.DepartmentKey(dept) {
				continue
			}
			_, cs := mapValue(d, "connectors")
			if cs == nil || cs.Kind != yaml.SequenceNode {
				break
			}
			for _, c := range cs.Content {
				if _, n := mapValue(c, "name"); n != nil && n.Value == connector {
					if c.Style&yaml.FlowStyle != 0 {
						return nil, fmt.Errorf("%s: connector %s is written in flow style; edit it by hand", path, connector)
					}
					return c, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("%s: department %s has no connector %s", path, dept, connector)
}

func addSecret(path string, raw []byte, slug, dept, connector, v, ref string) ([]byte, error) {
	c, err := connectorNode(path, raw, slug, dept, connector)
	if err != nil {
		return nil, err
	}
	lines := splitLines(raw)
	sk, ss := mapValue(c, "secrets")
	switch {
	case ss != nil && ss.Kind == yaml.MappingNode && ss.Style&yaml.FlowStyle == 0 && len(ss.Content) > 0:
		if k, val := mapValue(ss, v); k != nil {
			if val.Value == ref {
				return raw, nil
			}
			return nil, fmt.Errorf("%s: connector %s's secret %s is %s already; change it by hand", path, connector, v, val.Value)
		}
		col := ss.Content[0].Column - 1
		return insertAfter(lines, lastLine(ss), strings.Repeat(" ", col)+v+": "+ref+"\n"), nil
	case sk != nil && (ss == nil || isNull(ss)):
		return insertAfter(lines, sk.Line, strings.Repeat(" ", sk.Column+1)+v+": "+ref+"\n"), nil
	case sk != nil:
		return nil, fmt.Errorf("%s: connector %s's secrets: is not a block map; add %s: %s by hand", path, connector, v, ref)
	}
	col := c.Content[0].Column - 1
	pad := strings.Repeat(" ", col)
	return insertAfter(lines, lastLine(c), pad+"secrets:\n"+pad+"  "+v+": "+ref+"\n"), nil
}

func addOptional(path string, raw []byte, slug, dept, connector, v string) ([]byte, error) {
	c, err := connectorNode(path, raw, slug, dept, connector)
	if err != nil {
		return nil, err
	}
	lines := splitLines(raw)
	ok, ol := mapValue(c, "optional")
	switch {
	case ol != nil && ol.Kind == yaml.SequenceNode:
		for _, n := range ol.Content {
			if n.Value == v {
				return raw, nil
			}
		}
		if ol.Style&yaml.FlowStyle != 0 || len(ol.Content) == 0 {
			if ol.Line != ok.Line {
				return nil, fmt.Errorf("%s: connector %s's optional: spans lines; add %s by hand", path, connector, v)
			}
			// optional: [A, B] on one line: write the list again.
			names := []string{}
			for _, n := range ol.Content {
				names = append(names, n.Value)
			}
			names = append(names, v)
			line := strings.Repeat(" ", ok.Column-1) + "optional: [" + strings.Join(names, ", ") + "]\n"
			lines[ok.Line-1] = line
			return []byte(strings.Join(lines, "")), nil
		}
		col := dashColumn(lines, ol.Content[0])
		return insertAfter(lines, lastLine(ol), strings.Repeat(" ", col)+"- "+v+"\n"), nil
	case ok != nil:
		return nil, fmt.Errorf("%s: connector %s's optional: is not a list; add %s by hand", path, connector, v)
	}
	col := c.Content[0].Column - 1
	return insertAfter(lines, lastLine(c), strings.Repeat(" ", col)+"optional: ["+v+"]\n"), nil
}

func findConnector(p *tracker.Project, dept, name string) *tracker.Connector {
	d := p.DepartmentByKey(dept)
	if d == nil {
		return nil
	}
	for i := range d.Connectors {
		if d.Connectors[i].Name == name {
			return &d.Connectors[i]
		}
	}
	return nil
}

// ExampleItem is connector name's item in template key's connector
// examples, as written there ("- name: …" at column 0, comments inside it
// kept, the header comments above it left out).
func ExampleItem(key, name string) (string, error) {
	t, err := Load(key)
	if err != nil {
		return "", err
	}
	if len(t.ExamplesText) == 0 {
		return "", fmt.Errorf("department template %s has no connector examples", key)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(t.ExamplesText, &doc); err != nil || len(doc.Content) == 0 {
		return "", fmt.Errorf("department template %s: connector examples do not parse", key)
	}
	_, cs := mapValue(doc.Content[0], "connectors")
	if cs == nil {
		return "", fmt.Errorf("department template %s: no connectors: in its examples", key)
	}
	lines := splitLines(t.ExamplesText)
	for _, c := range cs.Content {
		if _, n := mapValue(c, "name"); n == nil || n.Value != name {
			continue
		}
		from, to := c.Line, lastLine(c)
		col := dashColumn(lines, c)
		var b strings.Builder
		for i := from; i <= to && i <= len(lines); i++ {
			l := lines[i-1]
			if len(l) >= col && strings.TrimSpace(l[:col]) == "" {
				l = l[col:]
			}
			b.WriteString(l)
		}
		return b.String(), nil
	}
	return "", fmt.Errorf("department template %s has no example connector %s", key, name)
}
