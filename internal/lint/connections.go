package lint

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/connections"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// connectionFindings are projects.yaml connections: mistakes (part of the
// departments check): an end or via: that names no box, a route from a box
// to itself or via one of its ends, a via: that forwards nothing (not hq,
// not the asker's group), via: on a feed, a feed from HQ, a duplicate, and
// a department's request straight to Product (engine requests go via the
// CEO: factory/marketing-brand-foundation-brand-analyst-icp-and-tamsamsom-before-channel-work#D-8).
// connections.Declared skips an entry whose ends or via: don't resolve.
func connectionFindings(p *tracker.Project) []Finding {
	if p.Connections == nil {
		return nil
	}
	var out []Finding
	boxes := strings.Join(connections.Boxes(p), ", ")
	check := func(list string, rs []tracker.Route) {
		seen := map[string]bool{}
		for i, r := range rs {
			at := fmt.Sprintf("%s[%d]", list, i)
			add := func(msg, fix string) {
				out = append(out, Finding{Message: at + ": " + msg, Fix: fix})
			}
			from, to := connections.ResolveBox(p, r.From), connections.ResolveBox(p, r.To)
			bad := false
			for _, end := range []struct{ key, raw, box string }{{"from", r.From, from}, {"to", r.To, to}} {
				switch {
				case strings.TrimSpace(end.raw) == "":
					add(end.key+": is empty", "name a box: "+boxes)
					bad = true
				case end.box == "":
					add(fmt.Sprintf("%s: %q names no box", end.key, end.raw), "one of: "+boxes)
					bad = true
				}
			}
			if bad {
				continue
			}
			name := connections.BoxName(p, from) + " → " + connections.BoxName(p, to)
			if from == to {
				add(fmt.Sprintf("%s goes from a box to itself: a link inside one box is no connection", name), "remove it")
				continue
			}
			k := from + ">" + to
			if seen[k] {
				add(name+" is declared twice", "remove one")
			}
			seen[k] = true
			via := strings.TrimSpace(r.Via)
			if list == "feeds" {
				if via != "" {
					add("via: on a feed is ignored: an output goes straight to its user", "remove via:")
				}
				if from == connections.HQ {
					add("a feed from HQ: outputs are made by departments", "remove it, or name the department that makes the output")
				}
				continue
			}
			if via == "" {
				if from != connections.HQ && from != connections.Product && to == connections.Product {
					add(name+" has no via: engine requests go to Product through the CEO (factory/marketing-brand-foundation-brand-analyst-icp-and-tamsamsom-before-channel-work#D-8)", "add via: hq")
				}
				continue
			}
			v := connections.ResolveBox(p, via)
			g := p.GroupOf(from)
			switch {
			case v == "":
				add(fmt.Sprintf("via: %q names no box", via), "via: hq, or the asker's group")
			case v == from || v == to:
				add(fmt.Sprintf("%s via %s: the route goes through one of its own ends", name, connections.BoxName(p, v)), "remove via:, or name hq or the asker's group")
			case from == connections.HQ:
				add("a request from HQ needs no via: the CEO asks anyone directly", "remove via:")
			case v == connections.HQ:
			case !p.IsGroup(v):
				add(fmt.Sprintf("via: %s is no group: only hq or a group's lead forwards a request", connections.BoxName(p, v)), "via: hq, or the asker's group")
			case g == nil || !connections.UnderGroup(p, from, v):
				add(fmt.Sprintf("%s via %s: %s is not in that group, and a group's lead forwards only its own departments' requests", name, connections.BoxName(p, v), connections.BoxName(p, from)), "via: hq, or set the asker's parent:")
			}
		}
	}
	check("requests", p.Connections.Requests)
	check("feeds", p.Connections.Feeds)
	return out
}
