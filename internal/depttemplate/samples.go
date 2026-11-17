package depttemplate

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kacperwerema/dark-factory/internal/goals"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// A template's mission and goals are samples: the project sets its own
// (department add --mission/--goal, department update --mission/--goal).
// What add writes from the template carries SampleMarker at the end of the
// mission: line and of each goal's "- id:" line, for the owner to see.
// lint's departments check flags a field while its value still reads a
// sample text (the template's, or an earlier release's), unless its line
// says KeptMarker: the owner set it to the sample's value on purpose.

const (
	// SampleMarker ends a line add wrote from the template's sample.
	SampleMarker = "# sample: replace"
	// KeptMarker ends a line the owner set to a sample's value on purpose.
	KeptMarker = "# sample: kept"
)

// markerTail is the start of every sample comment, the one update drops.
const markerTail = "# sample:"

// ParseGoal reads a --goal flag, "<id>=<name>/<target>/<period>". The name
// may hold "/": target and period are the last two fields. A goal with t's
// id keeps that goal's unit, direction, source and rolls_up (t may be nil).
func ParseGoal(s string, t *Template) (tracker.Goal, error) {
	usage := fmt.Errorf("--goal %q: want <id>=<name>/<target>/<period>, e.g. --goal \"signups=Sign-ups/25/week\"", s)
	id, rest, ok := strings.Cut(s, "=")
	id = strings.TrimSpace(id)
	if !ok || id == "" || strings.ContainsAny(id, " /") {
		return tracker.Goal{}, usage
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 3 {
		return tracker.Goal{}, usage
	}
	period := strings.ToLower(strings.TrimSpace(parts[len(parts)-1]))
	target := strings.TrimSpace(parts[len(parts)-2])
	name := strings.TrimSpace(strings.Join(parts[:len(parts)-2], "/"))
	if name == "" || strings.ContainsAny(name, "\n\r") {
		return tracker.Goal{}, fmt.Errorf("--goal %q: the name is empty or more than one line", s)
	}
	if period == "" || !slices.Contains(goals.Periods, period) {
		return tracker.Goal{}, fmt.Errorf("--goal %q: period %q is not week, month, quarter or none", s, period)
	}
	v, err := strconv.ParseFloat(target, 64)
	if err != nil {
		return tracker.Goal{}, fmt.Errorf("--goal %q: target %q is not a number", s, target)
	}
	g := tracker.Goal{ID: id, Name: name, Target: &v, Period: period}
	if t != nil {
		if tg := t.goal(id); tg != nil {
			g.ID, g.Unit, g.Direction, g.Source, g.RollsUp = tg.ID, tg.Unit, tg.Direction, tg.Source, tg.RollsUp
		}
	}
	return g, nil
}

// GoalFlag is g as a --goal flag spells it.
func GoalFlag(g tracker.Goal) string {
	target := ""
	if g.Target != nil {
		target = strconv.FormatFloat(*g.Target, 'f', -1, 64)
	}
	period := g.Period
	if period == "" {
		period = "none"
	}
	return g.ID + "=" + g.Name + "/" + target + "/" + period
}

// sampleMission says mission is t's sample mission, today's or an
// earlier release's.
func (t *Template) sampleMission(mission string) bool {
	m := strings.TrimSpace(mission)
	return m != "" && (m == strings.TrimSpace(t.Mission) || slices.Contains(earlierTexts[t.Key].Mission, m))
}

// sampleGoal says g is one of t's sample goals as it was written: the same
// id, a sample name (today's or an earlier release's), target and period.
func (t *Template) sampleGoal(g tracker.Goal) bool {
	tg := t.goal(g.ID)
	if tg == nil {
		return false
	}
	if g.Name != tg.Name && !slices.Contains(earlierTexts[t.Key].GoalNames[tg.ID], g.Name) {
		return false
	}
	if (g.Target == nil) != (tg.Target == nil) || (g.Target != nil && *g.Target != *tg.Target) {
		return false
	}
	norm := func(p string) string {
		if p = strings.ToLower(strings.TrimSpace(p)); p == "" {
			return "none"
		}
		return p
	}
	return norm(g.Period) == norm(tg.Period)
}

// Samples is what of one department is still its template's sample.
type Samples struct {
	Template *Template `json:"-"`
	Name     string    `json:"name"`
	Key      string    `json:"key"`
	Mission  string    `json:"mission,omitempty"`
	// Goals are the sample goals' ids, in the entry's order.
	Goals []string `json:"goals,omitempty"`
}

// Empty says nothing of the department is a sample any more.
func (s Samples) Empty() bool { return s.Mission == "" && len(s.Goals) == 0 }

// UpdateCommand is the department update command that sets them, with the
// sample values in its flags as the starting point.
func (s Samples) UpdateCommand() string {
	cmd := "issue-cli department update " + s.Key
	if s.Mission != "" {
		cmd += ` --mission "<this project's mission>"`
	}
	for _, id := range s.Goals {
		cmd += ` --goal "` + id + `=<name>/<target>/<period>"`
	}
	return cmd
}

// SampleFields are proj's departments installed from a bundled template
// whose mission or goals still read the template's sample, in the order of
// List. A line ending in KeptMarker is the owner's and not reported.
func SampleFields(proj *tracker.Project) []Samples {
	var kept map[string]bool
	if proj.ConfigPath != "" {
		kept = keptLines(proj.ConfigPath, proj.Slug)
	}
	var out []Samples
	for _, t := range List() {
		d := t.InstalledAs(proj)
		if d == nil {
			continue
		}
		s := Samples{Template: t, Name: d.Name, Key: tracker.DepartmentKey(d.Name)}
		dk := tracker.DepartmentKey(d.Name)
		if t.sampleMission(d.Mission) && !kept[dk+"\x00mission"] {
			s.Mission = d.Mission
		}
		for _, g := range d.Goals {
			if t.sampleGoal(g) && !kept[dk+"\x00goal\x00"+strings.ToLower(g.ID)] {
				s.Goals = append(s.Goals, g.ID)
			}
		}
		if !s.Empty() {
			out = append(out, s)
		}
	}
	return out
}

// keptLines are the fields of project slug's departments whose line ends
// in KeptMarker: "<dept key>\x00mission" and "<dept key>\x00goal\x00<id>".
// An unreadable file has none.
func keptLines(configPath, slug string) map[string]bool {
	_, raw, proj, err := readProject(configPath, slug)
	if err != nil {
		return nil
	}
	lines := splitLines(raw)
	keptOn := func(n *yaml.Node) bool {
		if n == nil || n.Line-1 >= len(lines) {
			return false
		}
		return strings.HasSuffix(strings.TrimRight(lines[n.Line-1], " \r\n"), KeptMarker)
	}
	out := map[string]bool{}
	_, depts := mapValue(proj, "departments")
	if depts == nil || depts.Kind != yaml.SequenceNode {
		return out
	}
	for _, it := range depts.Content {
		_, nv := mapValue(it, "name")
		if nv == nil {
			continue
		}
		dk := tracker.DepartmentKey(nv.Value)
		if _, m := mapValue(it, "mission"); keptOn(m) {
			out[dk+"\x00mission"] = true
		}
		if _, gs := mapValue(it, "goals"); gs != nil && gs.Kind == yaml.SequenceNode {
			for _, g := range gs.Content {
				if _, id := mapValue(g, "id"); keptOn(id) {
					out[dk+"\x00goal\x00"+strings.ToLower(id.Value)] = true
				}
			}
		}
	}
	return out
}

// markSamples ends the rendered entry's mission: line, and each goal's
// "- id:" line when goals are the template's, with SampleMarker. The entry
// is renderEntry's: "- name:" at column 0, its keys at 2, goal items at 4.
func markSamples(item string, mission, goalsToo bool) string {
	lines := strings.SplitAfter(item, "\n")
	inGoals := false
	for i, l := range lines {
		if strings.HasPrefix(l, "  ") && !strings.HasPrefix(l, "   ") {
			inGoals = strings.HasPrefix(l, "  goals:")
		}
		mark := (mission && strings.HasPrefix(l, "  mission: ")) || (goalsToo && inGoals && strings.HasPrefix(l, "    - id: "))
		if mark && strings.HasSuffix(l, "\n") {
			lines[i] = strings.TrimSuffix(l, "\n") + " " + SampleMarker + "\n"
		}
	}
	return strings.Join(lines, "")
}

// Own is what department update --mission/--goal/--drop-goal sets.
type Own struct {
	Mission *string        `json:"mission,omitempty"`
	Goals   []tracker.Goal `json:"goals,omitempty"`
	Drop    []string       `json:"drop_goals,omitempty"`
}

// Empty says no flag was given.
func (o Own) Empty() bool { return o.Mission == nil && len(o.Goals) == 0 && len(o.Drop) == 0 }

// SetOwn sets department name's mission and goals in project slug's entry
// in the config at configPath, as text: only the lines of the values it
// sets change (and lines it adds or drops), every other byte stays. A value
// set to t's sample (t may be nil) ends its line with KeptMarker; any other
// drops the line's sample comment. The result must load and read as asked
// before it replaces the file.
func SetOwn(configPath, slug, name string, t *Template, own Own) error {
	path, raw, _, err := readProject(configPath, slug)
	if err != nil {
		return err
	}
	if own.Mission != nil {
		if strings.ContainsAny(*own.Mission, "\n\r") || strings.TrimSpace(*own.Mission) == "" {
			return fmt.Errorf("--mission: one line of text, not empty")
		}
		kept := t != nil && t.sampleMission(*own.Mission)
		if raw, err = setMission(raw, configPath, slug, name, *own.Mission, kept); err != nil {
			return err
		}
	}
	for _, g := range own.Goals {
		kept := t != nil && t.sampleGoal(g)
		if raw, err = setGoal(raw, configPath, slug, name, g, kept); err != nil {
			return err
		}
	}
	for _, id := range own.Drop {
		if raw, err = dropGoal(raw, configPath, slug, name, id); err != nil {
			return err
		}
	}
	return writeChecked(path, raw, func(p *tracker.Project) error {
		d := p.DepartmentByKey(name)
		if d == nil {
			return fmt.Errorf("department %s is gone from the result", name)
		}
		if own.Mission != nil && d.Mission != *own.Mission {
			return fmt.Errorf("department %s's mission reads %q in the result, not %q", name, d.Mission, *own.Mission)
		}
		for _, want := range own.Goals {
			var got *tracker.Goal
			for i := range d.Goals {
				if strings.EqualFold(d.Goals[i].ID, want.ID) {
					got = &d.Goals[i]
				}
			}
			if got == nil || got.Name != want.Name || got.Target == nil || *got.Target != *want.Target || !strings.EqualFold(got.Period, want.Period) {
				return fmt.Errorf("department %s's goal %s does not read %s in the result", name, want.ID, GoalFlag(want))
			}
		}
		for _, id := range own.Drop {
			for _, g := range d.Goals {
				if strings.EqualFold(g.ID, id) {
					return fmt.Errorf("department %s still has goal %s in the result", name, id)
				}
			}
		}
		return nil
	}, slug)
}

// entryItem parses raw and finds department name's item in project slug.
func entryItem(raw []byte, configPath, slug, name string) ([]string, *yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, nil, fmt.Errorf("parsing %s: %w", configPath, err)
	}
	if len(doc.Content) == 0 {
		return nil, nil, fmt.Errorf("%s is empty", configPath)
	}
	_, projects := mapValue(doc.Content[0], "projects")
	if projects == nil || projects.Kind != yaml.SequenceNode {
		return nil, nil, fmt.Errorf("%s has no projects: list", configPath)
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
		if depts == nil || depts.Kind != yaml.SequenceNode {
			break
		}
		for _, it := range depts.Content {
			if _, nv := mapValue(it, "name"); nv != nil && tracker.DepartmentKey(nv.Value) == tracker.DepartmentKey(name) {
				if it.Style&yaml.FlowStyle != 0 {
					return nil, nil, fmt.Errorf("%s: department %s is written in flow style; edit it by hand", configPath, name)
				}
				return splitLines(raw), it, nil
			}
		}
	}
	return nil, nil, fmt.Errorf("%s: project %s has no department %s", configPath, slug, name)
}

// scalarText is value as one line of YAML.
func scalarText(value string) string {
	b, err := yaml.Marshal(value)
	if err != nil {
		return strconv.Quote(value)
	}
	return strings.TrimRight(string(b), "\n")
}

// numberText is v as YAML writes a number.
func numberText(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// replaceValue puts text in place of scalar n's value on its line. The
// line's comment stays, but a sample comment goes, and kept writes
// KeptMarker instead.
func replaceValue(lines []string, n *yaml.Node, text string, kept bool, configPath, what string) error {
	if n.Kind != yaml.ScalarNode || n.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 || n.Line-1 >= len(lines) {
		return fmt.Errorf("%s: %s is written in a way this can't change: edit it by hand to %s", configPath, what, text)
	}
	line := []rune(lines[n.Line-1])
	if n.Column-1 > len(line) {
		return fmt.Errorf("%s: %s is not where the parser says", configPath, what)
	}
	head, rest := string(line[:n.Column-1]), string(line[n.Column-1:])
	nl := ""
	if strings.HasSuffix(rest, "\n") {
		nl = "\n"
		if strings.HasSuffix(rest, "\r\n") {
			nl = "\r\n"
		}
	}
	body, tail := strings.TrimRight(rest, "\r\n"), ""
	if i := strings.Index(body, " #"); i >= 0 {
		body, tail = body[:i], body[i:]
		// The spaces before the comment are the comment's: they stay.
		trimmed := strings.TrimRight(body, " ")
		body, tail = trimmed, body[len(trimmed):]+tail
	}
	var check string
	if err := yaml.Unmarshal([]byte(body), &check); err != nil || check != n.Value {
		return fmt.Errorf("%s: %s is written in a way this can't change: edit it by hand to %s", configPath, what, text)
	}
	if strings.HasPrefix(strings.TrimSpace(tail), markerTail) {
		tail = ""
	}
	if kept {
		tail = " " + KeptMarker
	}
	lines[n.Line-1] = head + text + strings.TrimRight(tail, " ") + nl
	return nil
}

// keyColumn is the 0-based column of mapping m's keys.
func keyColumn(m *yaml.Node) int {
	if len(m.Content) > 0 {
		return m.Content[0].Column - 1
	}
	return m.Column - 1
}

// setMission sets the item's mission:, adding the line under role: (or
// name:) when it has none.
func setMission(raw []byte, configPath, slug, name, mission string, kept bool) ([]byte, error) {
	lines, it, err := entryItem(raw, configPath, slug, name)
	if err != nil {
		return nil, err
	}
	if _, m := mapValue(it, "mission"); m != nil && !isNull(m) {
		if err := replaceValue(lines, m, scalarText(mission), kept, configPath, "department "+name+"'s mission"); err != nil {
			return nil, err
		}
		return []byte(strings.Join(lines, "")), nil
	}
	if k, _ := mapValue(it, "mission"); k != nil {
		return nil, fmt.Errorf("%s: department %s's mission: has no value: write it by hand", configPath, name)
	}
	after := 0
	for _, key := range []string{"name", "parent", "color", "role"} {
		if _, v := mapValue(it, key); v != nil {
			after = max(after, lastLine(v))
		}
	}
	line := strings.Repeat(" ", keyColumn(it)) + "mission: " + scalarText(mission)
	if kept {
		line += " " + KeptMarker
	}
	return insertAfter(lines, after, line+"\n"), nil
}

// setGoal sets goal g's name, target and period in the item's goals:,
// adding the goal (or the goals: key) when the item has none by that id.
func setGoal(raw []byte, configPath, slug, name string, g tracker.Goal, kept bool) ([]byte, error) {
	lines, it, err := entryItem(raw, configPath, slug, name)
	if err != nil {
		return nil, err
	}
	gk, gs := mapValue(it, "goals")
	if gs != nil && !isNull(gs) && (gs.Kind != yaml.SequenceNode || gs.Style&yaml.FlowStyle != 0) {
		return nil, fmt.Errorf("%s: department %s's goals: is not a block list; edit it by hand", configPath, name)
	}
	var item *yaml.Node
	if gs != nil && gs.Kind == yaml.SequenceNode {
		for _, gi := range gs.Content {
			if _, id := mapValue(gi, "id"); id != nil && strings.EqualFold(id.Value, g.ID) {
				item = gi
			}
		}
	}
	if item == nil {
		return addGoal(lines, it, gk, gs, g, kept), nil
	}
	if item.Kind != yaml.MappingNode || item.Style&yaml.FlowStyle != 0 {
		return nil, fmt.Errorf("%s: department %s's goal %s is written in flow style; edit it by hand", configPath, name, g.ID)
	}
	what := "department " + name + "'s goal " + g.ID
	vals := []struct{ key, text string }{{"name", scalarText(g.Name)}, {"target", numberText(*g.Target)}, {"period", g.Period}}
	var missing []string
	for _, v := range vals {
		_, n := mapValue(item, v.key)
		if n == nil || isNull(n) {
			if k, _ := mapValue(item, v.key); k != nil {
				return nil, fmt.Errorf("%s: %s has %s: with no value: write it by hand", configPath, what, v.key)
			}
			missing = append(missing, strings.Repeat(" ", keyColumn(item))+v.key+": "+v.text+"\n")
			continue
		}
		if err := replaceValue(lines, n, v.text, false, configPath, what+" "+v.key); err != nil {
			return nil, err
		}
	}
	_, id := mapValue(item, "id")
	if err := replaceValue(lines, id, lines2text(lines, id), kept, configPath, what+" id"); err != nil {
		return nil, err
	}
	if len(missing) > 0 {
		return insertAfter(lines, lastLine(item), strings.Join(missing, "")), nil
	}
	return []byte(strings.Join(lines, "")), nil
}

// lines2text is scalar n's value as written on its line (its own text,
// quoting kept), so replaceValue only changes the comment.
func lines2text(lines []string, n *yaml.Node) string {
	if n.Line-1 >= len(lines) {
		return n.Value
	}
	line := []rune(lines[n.Line-1])
	if n.Column-1 > len(line) {
		return n.Value
	}
	body := strings.TrimRight(string(line[n.Column-1:]), "\r\n")
	if i := strings.Index(body, " #"); i >= 0 {
		body = body[:i]
	}
	return strings.TrimRight(body, " ")
}

// addGoal appends goal g after the item's last goal, or a goals: key at the
// item's end when it has none.
func addGoal(lines []string, it, gk, gs *yaml.Node, g tracker.Goal, kept bool) []byte {
	col := keyColumn(it)
	dash, keys := col+2, col+4
	if gs != nil && gs.Kind == yaml.SequenceNode && len(gs.Content) > 0 {
		dash = dashColumn(lines, gs.Content[0])
		keys = keyColumn(gs.Content[0])
	}
	idLine := strings.Repeat(" ", dash) + "-" + strings.Repeat(" ", max(1, keys-dash-1)) + "id: " + scalarText(g.ID)
	if kept {
		idLine += " " + KeptMarker
	}
	block := idLine + "\n" +
		strings.Repeat(" ", keys) + "name: " + scalarText(g.Name) + "\n" +
		strings.Repeat(" ", keys) + "target: " + numberText(*g.Target) + "\n"
	for _, kv := range [][2]string{{"unit", g.Unit}, {"direction", g.Direction}, {"source", g.Source}, {"rolls_up", g.RollsUp}} {
		if kv[1] != "" {
			block += strings.Repeat(" ", keys) + kv[0] + ": " + scalarText(kv[1]) + "\n"
		}
	}
	block += strings.Repeat(" ", keys) + "period: " + g.Period + "\n"
	switch {
	case gs != nil && gs.Kind == yaml.SequenceNode && len(gs.Content) > 0:
		return insertAfter(lines, lastLine(gs), block)
	case gk != nil:
		// goals: with no value: the item goes under the key.
		return insertAfter(lines, gk.Line, block)
	default:
		return insertAfter(lines, lastLine(it), strings.Repeat(" ", col)+"goals:\n"+block)
	}
}

// dropGoal deletes goal id's lines from the item's goals:, and the goals:
// key with its last goal.
func dropGoal(raw []byte, configPath, slug, name, id string) ([]byte, error) {
	lines, it, err := entryItem(raw, configPath, slug, name)
	if err != nil {
		return nil, err
	}
	gk, gs := mapValue(it, "goals")
	if gs == nil || gs.Kind != yaml.SequenceNode || gs.Style&yaml.FlowStyle != 0 {
		return nil, fmt.Errorf("%s: department %s has no goal %s", configPath, name, id)
	}
	for _, gi := range gs.Content {
		if _, n := mapValue(gi, "id"); n != nil && strings.EqualFold(n.Value, id) {
			from, to := gi.Line, lastLine(gi)
			if len(gs.Content) == 1 {
				from = gk.Line
			}
			return deleteLines(lines, from, to), nil
		}
	}
	var ids []string
	for _, gi := range gs.Content {
		if _, n := mapValue(gi, "id"); n != nil {
			ids = append(ids, n.Value)
		}
	}
	return nil, fmt.Errorf("%s: department %s has no goal %s (goals: %s)", configPath, name, id, strings.Join(ids, ", "))
}
