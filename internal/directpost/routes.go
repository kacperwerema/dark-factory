package directpost

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/socials"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Route kinds.
const (
	RouteDirect = "direct"
	// RouteTool is a social tool's connector (per_run: tool:, e.g. Postiz).
	RouteTool = "tool"
)

// Route is how one channel of a department goes out.
type Route struct {
	Channel string
	// Kind is RouteDirect, RouteTool, or "" when Problem says why not.
	Kind string
	// Address is the connector's <dept>/<name>; Department its owner.
	Address, Department string
	// Platform is a direct route's publish:; Tool a tool route's name.
	Platform, Tool string
	Problem        string
}

// ChannelRe is what a publish: channel may be called.
var ChannelRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Routes are d's publish: channels in name order, each resolved against
// the project's connectors.
func Routes(p *tracker.Project, d *tracker.Department) []Route {
	var names []string
	for ch := range d.Publish {
		names = append(names, ch)
	}
	sort.Strings(names)
	var out []Route
	for _, ch := range names {
		out = append(out, resolve(p, d, ch, strings.TrimSpace(d.Publish[ch])))
	}
	return out
}

func resolve(p *tracker.Project, d *tracker.Department, ch, target string) Route {
	r := Route{Channel: ch}
	if !ChannelRe.MatchString(ch) {
		r.Problem = fmt.Sprintf("channel %q is not a lowercase word (instagram, tiktok)", ch)
		return r
	}
	owner, name := d, target
	if k, n, ok := strings.Cut(target, "/"); ok {
		owner, name = p.DepartmentByKey(k), n
		if owner == nil {
			r.Problem = fmt.Sprintf("%s: no department %q", target, k)
			return r
		}
	}
	if name == "" {
		r.Problem = "names no connector"
		return r
	}
	r.Department = owner.Name
	r.Address = tracker.ConnectorAddress(owner.Name, name)
	var c *tracker.Connector
	for i := range owner.Connectors {
		if owner.Connectors[i].Name == name {
			c = &owner.Connectors[i]
		}
	}
	switch {
	case c == nil:
		r.Problem = fmt.Sprintf("%s has no connector %q", owner.Name, name)
	case c.Publish != "":
		r.Kind, r.Platform = RouteDirect, c.Publish
		if !Known(c.Publish) {
			r.Problem = fmt.Sprintf("%s: publish: %q is not %s", r.Address, c.Publish, strings.Join(Platforms, ", "))
		}
	case socials.For(c) != nil:
		r.Kind, r.Tool = RouteTool, socials.For(c).Name()
	default:
		r.Problem = fmt.Sprintf("%s is neither a direct publisher (publish:) nor a social tool (per_run: tool: postiz)", r.Address)
	}
	return r
}

// Says is the route in words, with the command that uses it.
func (r Route) Says() string {
	switch {
	case r.Problem != "":
		return fmt.Sprintf("%s: no route: %s", r.Channel, r.Problem)
	case r.Kind == RouteDirect:
		return fmt.Sprintf("%s: direct (%s, %s): issue-cli connector exec %s -- publish --file <mp4> --caption-file <txt> [--send]", r.Channel, r.Platform, r.Address, r.Address)
	}
	return fmt.Sprintf("%s: %s (%s): %s's agents post it through that connector (issue-cli connector exec %s)", r.Channel, r.Tool, r.Address, r.Department, r.Address)
}
