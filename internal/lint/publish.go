package lint

import (
	"fmt"
	"slices"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/directpost"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// publishFindings are a publish: connector's mistakes: an unknown
// platform, a secret or account id publishing needs, the owner's hold
// (sends: and needs_approved_output:), and fields a direct publisher
// doesn't use. A missing value on this machine is connector check's; this
// is about projects.yaml.
func publishFindings(label string, c tracker.Connector) []string {
	if strings.TrimSpace(c.Publish) == "" {
		if len(c.Account) > 0 {
			return []string{label + ": account: is only for a publish: connector"}
		}
		return nil
	}
	if !directpost.Known(c.Publish) {
		return []string{fmt.Sprintf("%s: publish: %q is not %s: connector exec refuses it", label, c.Publish, strings.Join(directpost.Platforms, ", "))}
	}
	var out []string
	if c.Kind != tracker.ConnectorTool {
		out = append(out, label+": a publish: connector is kind: tool (issue-cli makes the platform's calls itself)")
	}
	if strings.TrimSpace(c.Command) != "" || c.PerRun != nil || c.Spend != nil || len(c.Headers) > 0 {
		out = append(out, label+": a publish: connector runs no command:, per_run:, spend: or headers: (its keys go under secrets:)")
	}
	need := directpost.Require(c.Publish)
	for _, alts := range need.Secrets {
		if !slices.ContainsFunc(alts, func(k string) bool { _, ok := c.Secrets[k]; return ok }) {
			out = append(out, fmt.Sprintf("%s: publish: %s needs secrets: %s (a keychain: reference)", label, c.Publish, strings.Join(alts, " or ")))
		}
	}
	for _, k := range need.Account {
		if strings.TrimSpace(c.Account[k]) == "" {
			out = append(out, fmt.Sprintf("%s: publish: %s needs account: %s", label, c.Publish, k))
		}
	}
	if c.Publish == directpost.TikTok {
		if _, ok := c.Secrets[directpost.TikTokRefreshToken]; ok {
			for _, k := range []string{directpost.TikTokClientKey, directpost.TikTokClientSecret} {
				if _, ok := c.Secrets[k]; !ok {
					out = append(out, fmt.Sprintf("%s: %s needs %s beside it (the app's, to trade it for a fresh access token)", label, directpost.TikTokRefreshToken, k))
				}
			}
		}
	}
	if !c.Sends || !c.NeedsApprovedOutput {
		out = append(out, label+": a publish: connector posts outside the machine: give it sends: true and needs_approved_output: true (nothing goes out before the owner approves it)")
	}
	return out
}

// publishRouteFindings are a department's publish: routes that name no
// usable connector.
func publishRouteFindings(p *tracker.Project, d tracker.Department) []Finding {
	var out []Finding
	for _, r := range directpost.Routes(p, &d) {
		if r.Problem != "" {
			out = append(out, Finding{
				Message: fmt.Sprintf("publish: %s: %s", r.Channel, r.Problem),
				Fix:     "name a connector with publish: (direct) or per_run: tool: postiz, as <name> or <dept>/<name> (docs/reels.md#direct-publishing)",
			})
		}
	}
	return out
}
