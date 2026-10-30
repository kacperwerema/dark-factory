package testids

func init() {
	IDs = append(IDs,
		// Agents canvas (/p/<project>/agents). Nodes are keyed by tmux session
		// name (lead-<project>, agent-<slug>).
		"agents.toolbar",
		"agents.layout.tree",
		"agents.layout.radial",
		"agents.opt.issues",
		"agents.opt.stopped",
		"agents.live",
		"agents.spawn_lead",
		"agents.canvas",
		"agents.tree",
		"agents.radial",
		"agents.you",
		"agents.node.*",
		"agents.node.*.state",
		"agents.node.*.issue",
		"agents.dept.*",
		"agents.unfinished",
		"agents.panel",
		"agents.panel.title",
		"agents.panel.issue",
		"agents.panel.last_call",
		"agents.panel.approve",
		"agents.panel.spawn",
		"agents.panel.open",
		"agents.panel.open_status",
		"agents.panel.copy",
		"agents.panel.activity",
		"agents.panel.branch",
		"agents.panel.diff",
		"agents.spawn_modal",
		"agents.spawn_modal.steps",
		"agents.spawn_modal.close",
	)
}
