# Claude Code mods

A Claude Code plugin marketplace with one plugin, `issue-approve`. It puts a
dispatched agent's issue approvals (and ▶ Playtest, ▶ Run checks when they
apply) above the prompt, so you can approve from the terminal you are
watching. The buttons call the same board endpoints as the issue page.

Install it once, at user scope:

```
claude plugin marketplace add <your clone>/claude-mods
claude plugin install issue-approve@ai-workflow-engine --scope user
```

The full description is in [docs/agent-dispatch.md](../docs/agent-dispatch.md)
(the `issue-approve` section). The folder stays here because installed mods
point at this path.
