package tracker

import "strings"

// A group is a department other departments name with parent: (GTM over
// Marketing and Sales). It has everything a department has, and its lead is
// the lead of leads: its departments' leads report to it while it is live.
// A group may sit inside another group (GTM → Outbound → Sales), at most
// MaxGroupDepth groups above any department; a longer chain or a loop is a
// lint finding, and the department it reaches is treated as top-level here
// (factory/nested-groups-a-group-inside-a-group-with-goals-rolling-up-and-the-map-drawing-it#D-1).

// MaxGroupDepth is how many groups may sit above one department.
const MaxGroupDepth = 3

// ParentChain walks dept's parent: names upward. It returns the groups
// above dept, nearest first, and what is wrong with the chain: "" when it
// is good, "loop" when it comes back to a department already seen,
// "deep" when it has more than MaxGroupDepth groups, "bad" when a parent:
// names no department (or HQ, Product, dept itself). groups is nil unless
// the chain is good. It never runs more than len(Departments)+1 steps.
func (p *Project) ParentChain(dept string) (groups []*Department, problem string) {
	if p == nil {
		return nil, ""
	}
	d := p.DepartmentByKey(dept)
	if d == nil {
		return nil, ""
	}
	seen := map[*Department]bool{d: true}
	var chain []*Department
	for cur := d; strings.TrimSpace(cur.Parent) != ""; {
		g := p.DepartmentByKey(strings.TrimSpace(cur.Parent))
		switch {
		case g == nil:
			return nil, "bad"
		case seen[g]:
			if g == d && len(chain) == 0 {
				return nil, "bad" // parent: names the department itself
			}
			return nil, "loop"
		}
		seen[g] = true
		chain = append(chain, g)
		if len(chain) > MaxGroupDepth {
			// Keep walking a little to tell a loop from a long chain.
			for c := g; strings.TrimSpace(c.Parent) != ""; {
				n := p.DepartmentByKey(strings.TrimSpace(c.Parent))
				if n == nil {
					break
				}
				if seen[n] {
					return nil, "loop"
				}
				seen[n] = true
				c = n
			}
			return nil, "deep"
		}
		cur = g
	}
	return chain, ""
}

// Ancestors are the groups above dept, nearest first; nil when it is in no
// group or its chain is broken (a loop, too deep, a parent: naming no
// department).
func (p *Project) Ancestors(dept string) []*Department {
	chain, _ := p.ParentChain(dept)
	return chain
}

// GroupOf is the group department dept sits right under, or nil: its
// parent: when the whole chain above it is good.
func (p *Project) GroupOf(dept string) *Department {
	if a := p.Ancestors(dept); len(a) > 0 {
		return a[0]
	}
	return nil
}

// Children are the departments right under group (by name or key), in
// projects.yaml order; nil when it is no group.
func (p *Project) Children(group string) []Department {
	g := p.DepartmentByKey(group)
	if g == nil {
		return nil
	}
	var out []Department
	for i := range p.Departments {
		if p.GroupOf(p.Departments[i].Name) == g {
			out = append(out, p.Departments[i])
		}
	}
	return out
}

// Descendants are every department under group at any depth, each
// followed by its own (depth first, projects.yaml order).
func (p *Project) Descendants(group string) []Department {
	var out []Department
	for _, c := range p.Children(group) {
		out = append(out, c)
		out = append(out, p.Descendants(c.Name)...)
	}
	return out
}

// IsGroup reports whether dept has departments under it.
func (p *Project) IsGroup(dept string) bool { return len(p.Children(dept)) > 0 }

// Groups are the departments that have departments under them, in
// projects.yaml order.
func (p *Project) Groups() []Department {
	if p == nil {
		return nil
	}
	var out []Department
	for _, d := range p.Departments {
		if p.IsGroup(d.Name) {
			out = append(out, d)
		}
	}
	return out
}

// InScope reports whether an issue of department owner ("" is Product) is
// within lead's reach: lead's own department, or one under it at any depth
// when lead is a group.
func (p *Project) InScope(lead, owner string) bool {
	l := p.DepartmentByKey(lead)
	if l == nil || strings.TrimSpace(owner) == "" {
		return false
	}
	if strings.EqualFold(DepartmentKey(owner), DepartmentKey(lead)) {
		return true
	}
	for _, g := range p.Ancestors(owner) {
		if g == l {
			return true
		}
	}
	return false
}
