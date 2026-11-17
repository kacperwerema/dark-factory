package main

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/memory"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var memoryCommand = &Command{
	Name:      "memory",
	ShortHelp: "Shared memory between agents: facts with their source, read at start (memory add|search|list|entities|show|edit|forget|pin|unpin|candidates|consolidate)",
	LongHelp: `A memory is one fact an agent learned (a convention, a pitfall, how
something is done, who knows what) with its source, for the agents that
come after. issue-cli start prints the pinned ones and those that match
the issue under == Memories ==.

  memory add "<fact>" [--kind fact|convention|pitfall|howto|who]
             [--scope project|lead|department|agent|machine] [--issue <slug>]
             [--commit <sha>] [--comment N] [--tag a,b] [--system S]
             [--expires YYYY-MM-DD] [--at <status>] [--links id,id]
             [--suggest-pin] [--updates <id> [--yes] | --supersedes <id>[,<id>] | --new]
      Store a fact of at most 500 characters. The update step: the same
      fact again changes nothing; a near-duplicate is refused with the
      similar memories, and you decide: --updates <id> (better wording of
      the same fact: it replaces the whole text, and a text sharing under
      a third of its words is refused with the old text unless --yes),
      --supersedes <id> (it is no longer true; yours replaces it, the old
      one stays in history as invalid) or --new (a different fact). The source is your own issue when you are a worker.
      Scope: a department's agents write to their department by default,
      others to the project; agent is your own notebook; lead is for every
      lead and no worker but the lead flush's, which reads it and only a
      lead or the owner changes it (a standing rule; it stays in the project's log,
      and deal, client or price context stays out); machine reaches
      every project on this machine and is refused from a project whose
      privacy: keeps work or code private. Text that looks like a secret is
      refused and never stored: name where it lives (env:<VAR>).
      --at <status>: the fact belongs to a step, not a subject. transition
      prints it to whoever enters that status, whatever the issue is about
      (at most 3 memories and 600 characters; a pinned one stays in the
      briefing), and start prints it while the issue is there.
  memory search <words...|#topic|@name> [--kind K] [--scope S] [--tag T] [--system S] [--at <status>] [--all] [--limit N]
      The memories you can see, best match first (BM25). A #topic or
      @name in the query is a filter: only memories that carry it (in
      their text or tags); with entities alone, newest first.
  memory list [--kind K] [--scope S] [--tag T] [--at <status>] [--pinned] [--suggested] [--all]
      Every memory you can see, newest first; --all adds invalid ones.
  memory entities [--only topic|agent] [--kind K] [--scope S] [--all]
      Who knows what: every #topic and @name in the memories you can see,
      with how many carry it, of which kinds, and for a topic the @names
      a who memory links to it ("known by").
  memory show <id>
      One memory, its state, links and history.
  memory edit <id> [--text "<fact>"] [--at <status>|none]
  memory forget <id> --reason "<why it is no longer true>"
      Correct or invalidate a memory (it stays in history); --at ties it
      to a status, none unties it.
  memory pin <id> | unpin <id>
      A pinned memory goes into every briefing of its scope. Leads and the
      owner only; a worker adds with --suggest-pin.
  memory candidates <slug> [--all]
      Facts an issue's trail could teach: its progress:, question:,
      answer:, risk: and decision: comments, its decisions and its retro,
      each with the closest memories. Writes nothing and reads no
      transcript; you turn the ones worth keeping into memory add.
  memory consolidate [--scope S]
      The consolidation pack: near-duplicates to merge (add --supersedes
      id,id), stale memories (old and unused), expired ones, and proven
      ones to propose for CLAUDE.md as an issue. Writes nothing. The
      memory-consolidate job (brief: builtin:memory-consolidate) runs it.

Entities: write #topic and @agent (or @person) in the text, or pass them
as tags (--tag playtest,@lead-x), and a who memory links the two:
  issue-cli memory add "@lead-dark-factory knows the #playtest runner" --kind who
The entity index is folded from the memories on each read; nothing else
is stored, and there is no graph database or server.

Ranking decays: a memory's score is weighed by its strength, which halves
every 90 days since it was last added, changed or used, and rises with
uses. Pins keep full strength; decay never deletes or hides a memory.

The project's log is <issues>/.memory/events.jsonl; the machine's is
$XDG_DATA_HOME/ai-workflow-engine/memory/events.jsonl. Plain files and a
word ranking (BM25): no model, no server, nothing leaves the machine. Search
also ranks by meaning when workflow.yaml names a local embedder (memory:
embedder: url: on 127.0.0.1, or command:); it asks mem-guard first and
ranks by words alone at 70% or more (docs/memory.md, Semantic search). The owner
sees, edits, pins and deletes every memory at /p/<project>/memory.

Examples:
  issue-cli memory add "make shot URL= takes one screenshot; no playtest session needed" --kind howto
  issue-cli memory add "the runner finds Chrome for Testing itself" --updates m-3fa2c1
  issue-cli memory add "merge main again right before the ff" --kind howto --at shipping
  issue-cli memory search playtest chrome
  issue-cli memory search @lead-dark-factory
  issue-cli memory search '#playtest' chrome    (quote a #: unquoted, the shell reads a comment)
  issue-cli memory entities --only topic
  issue-cli memory show m-3fa2c1`,
	Run:         runMemory,
	Subcommands: []string{"add", "search", "list", "entities", "show", "edit", "forget", "pin", "unpin", "candidates", "consolidate"},
}

func init() { registerCommand(memoryCommand) }

const memoryUsage = `usage: issue-cli memory add "<fact>" | search <words...|#topic|@name> | list | entities | show <id> | edit <id> --text "..." | forget <id> --reason "..." | pin <id> | unpin <id> | candidates <slug> | consolidate (issue-cli help memory)`

func runMemory(ctx *Context, args []string) error {
	if len(args) == 0 {
		return &usageError{msg: memoryUsage}
	}
	switch args[0] {
	case "add":
		return runMemoryAdd(ctx, args[1:])
	case "search":
		return runMemorySearch(ctx, args[1:])
	case "list":
		return runMemoryList(ctx, args[1:])
	case "entities":
		return runMemoryEntities(ctx, args[1:])
	case "show":
		return runMemoryShow(ctx, args[1:])
	case "edit":
		return runMemoryEdit(ctx, args[1:])
	case "forget":
		return runMemoryForget(ctx, args[1:])
	case "pin", "unpin":
		return runMemoryPin(ctx, args[0], args[1:])
	case "candidates":
		return runMemoryCandidates(ctx, args[1:])
	case "consolidate":
		return runMemoryConsolidate(ctx, args[1:])
	}
	return &usageError{msg: fmt.Sprintf("unknown memory command %q: add, search, list, entities, show, edit, forget, pin, unpin, candidates or consolidate", args[0])}
}

// projectKey is the project's slug, also in bootstrap mode.
func projectKey(p *tracker.Project) string {
	if p.Slug != "" {
		return p.Slug
	}
	return tracker.Slugify(p.Name)
}

// projectIsPrivate: the project keeps its work files or code private
// (privacy: tracker or snapshots), so nothing of it may reach other
// projects.
func projectIsPrivate(p *tracker.Project) bool {
	wf := p.LoadWorkflow()
	return wf != nil && (wf.Privacy.Tracker() || wf.Privacy.Snapshots())
}

// refuseMachineWrite stops a private project's words reaching a machine
// memory by id: --updates, --supersedes and memory edit --text name the
// memory instead of --scope machine, and what they write would reach every
// project's briefing.
func refuseMachineWrite(p *tracker.Project, id string) error {
	if memory.StoreOfID(id) == "machine" && projectIsPrivate(p) {
		return fmt.Errorf("refused: %s keeps its work private (privacy: in workflow.yaml), so its words can't go into machine memory %s; add a project memory instead", projectKey(p), id)
	}
	return nil
}

// memCaller is who runs a memory command.
type memCaller struct {
	Name   string
	Viewer memory.Viewer
	// Department is the caller's department name ("" for Product, HQ, the
	// owner).
	Department string
	Issue      *tracker.Issue
	Lead       bool
	Owner      bool
}

func memoryCaller(p *tracker.Project, issues []*tracker.Issue) memCaller {
	me := whoAmI(p, issues)
	switch me.Role {
	case tracker.RoleProjectLead:
		return memCaller{Name: me.Name, Viewer: memory.Viewer{All: true, Agent: me.Name}, Lead: true}
	case tracker.RoleDepartmentLead:
		return memCaller{Name: me.Name, Department: me.Department, Lead: true,
			Viewer: memory.Viewer{Department: tracker.DepartmentKey(me.Department), Agent: me.Name, Lead: true}}
	case tracker.RoleWorker:
		c := memCaller{Name: me.Name}
		if me.Slug != "" {
			c.Issue = tracker.FindIssue(issues, me.Slug)
			if c.Name == "" {
				c.Name = tracker.AgentSessionName(me.Slug)
			}
		}
		if c.Issue != nil {
			c.Department = p.DepartmentOf(c.Issue)
		}
		c.Viewer = memory.Viewer{Department: deptKey(c.Department), Agent: c.Name}
		// The lead flush hands the day to the next lead and tidies memory:
		// it reads the lead scope it reports on, and changes none of it
		// (refuseLeadChange; Lead stays false).
		if c.Issue != nil && checkLeadFlushRun(p, c.Issue) == nil {
			c.Viewer.Lead = true
		}
		return c
	}
	return memCaller{Name: "owner", Viewer: memory.Viewer{All: true}, Owner: true}
}

// refuseLeadChange refuses changing a lead-scope memory to a caller who is
// neither a lead nor the owner: a worker may read one (the lead flush does)
// or add a new one, never edit, forget, update or supersede it.
func refuseLeadChange(store memory.Store, who memCaller, ids ...string) error {
	if who.Lead || who.Owner {
		return nil
	}
	set, err := store.Load()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if m := set.Get(id); m != nil && m.Scope == memory.ScopeLead {
			return fmt.Errorf("refused: %s is in the lead scope, which only a lead or the owner changes; name it in a progress: comment and the lead tidies it", id)
		}
	}
	return nil
}

func deptKey(name string) string {
	if name == "" {
		return ""
	}
	return tracker.DepartmentKey(name)
}

func splitList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func runMemoryAdd(ctx *Context, args []string) error {
	fs := newFlagSet("memory add", ctx)
	kind := fs.String("kind", memory.KindFact, "fact, convention, pitfall, howto or who")
	scope := fs.String("scope", "", "project, lead, department, agent or machine (default: your department, else the project)")
	issueFlag := fs.String("issue", "", "the issue it was learned on (a worker's own by default)")
	commit := fs.String("commit", "", "the commit it was learned from")
	comment := fs.Int("comment", 0, "the issue comment (1-based) it was learned from")
	tags := fs.String("tag", "", "tags, comma-separated")
	system := fs.String("system", "", "the system it is about (default: the source issue's)")
	expires := fs.String("expires", "", "the last day it is true (YYYY-MM-DD)")
	at := fs.String("at", "", "the status it matters at: transition prints it on entering that status")
	links := fs.String("links", "", "related memory ids, comma-separated")
	suggest := fs.Bool("suggest-pin", false, "ask a lead or the owner to pin it")
	updates := fs.String("updates", "", "UPDATE: replace this memory's text (the same fact, better worded)")
	supersedes := fs.String("supersedes", "", "SUPERSEDE: these memories (id or id,id) are no longer true or are merged; yours replaces them")
	isNew := fs.Bool("new", false, "ADD even though similar memories exist")
	yes := fs.Bool("yes", false, "with --updates: replace the text even when it shares under a third of its words with yours")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	text := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if text == "" {
		return &usageError{msg: `usage: issue-cli memory add "<fact>" [--kind K] [--scope S] [--issue <slug>] [--commit <sha>] [--updates <id> [--yes] | --supersedes <id> | --new]`}
	}
	opt := memory.AddOptions{}
	decisions := 0
	if *updates != "" {
		opt, decisions = memory.AddOptions{Mode: memory.ModeUpdates, Target: *updates, Yes: *yes}, decisions+1
	}
	if *supersedes != "" {
		ids := splitList(*supersedes)
		if len(ids) == 0 {
			return &usageError{msg: "--supersedes needs a memory id (or id,id to merge several)"}
		}
		opt, decisions = memory.AddOptions{Mode: memory.ModeSupersedes, Target: ids[0], Targets: ids[1:]}, decisions+1
	}
	if *isNew {
		opt, decisions = memory.AddOptions{Mode: memory.ModeNew}, decisions+1
	}
	if decisions > 1 {
		return &usageError{msg: "pick one of --updates, --supersedes and --new"}
	}
	if *yes && *updates == "" {
		return &usageError{msg: "--yes goes with --updates <id>: it replaces a text that shares under a third of its words with yours"}
	}

	p := ctx.Project
	atStatus, err := memoryAtStatus(p, *at)
	if err != nil {
		return err
	}
	issues, _ := tracker.LoadIssues(p.IssueDir)
	who := memoryCaller(p, issues)
	d := memory.Draft{Text: text, Kind: *kind, Project: projectKey(p), Author: who.Name, Tags: splitList(*tags),
		Links: splitList(*links), Expires: strings.TrimSpace(*expires), At: atStatus, SuggestPin: *suggest,
		Source: memory.Source{Commit: strings.TrimSpace(*commit), Comment: *comment}}
	issue := who.Issue
	if slug := strings.TrimSpace(*issueFlag); slug != "" {
		found, _, err := findIssueOrErr(ctx, slug)
		if err != nil {
			return err
		}
		issue = found
	}
	dept := who.Department
	if issue != nil {
		d.Source.Issue, d.System = issue.Slug, issue.System
		dept = p.DepartmentOf(issue)
	}
	if s := strings.TrimSpace(*system); s != "" {
		d.System = s
	}
	d.Department = dept

	store := memory.ProjectStore(p.IssueDir)
	switch strings.TrimSpace(*scope) {
	case "":
		if dept != "" {
			d.Scope = memory.DepartmentScope(deptKey(dept))
		}
	case memory.ScopeProject:
		d.Scope = memory.ScopeProject
	case memory.ScopeLead:
		d.Scope = memory.ScopeLead
	case "department":
		if dept == "" {
			return fmt.Errorf("no department to scope it to: the issue is Product's and you are not a department's agent; use --scope project")
		}
		d.Scope = memory.DepartmentScope(deptKey(dept))
	case "agent":
		d.Scope = memory.AgentScope(who.Name)
	case memory.ScopeMachine:
		if projectIsPrivate(p) {
			return fmt.Errorf("refused: %s keeps its work private (privacy: in workflow.yaml), so its memories stay in the project; add it without --scope machine", projectKey(p))
		}
		store = memory.MachineStore()
	default:
		d.Scope = strings.TrimSpace(*scope)
	}
	if opt.Target != "" && memory.StoreOfID(opt.Target) == "machine" {
		if err := refuseMachineWrite(p, opt.Target); err != nil {
			return err
		}
		store = memory.MachineStore()
	}
	for _, id := range opt.Targets {
		if memory.StoreOfID(id) != memory.StoreOfID(opt.Target) {
			return fmt.Errorf("--supersedes %s: every id must be in the same store (m- the project's, s- the machine's)", *supersedes)
		}
	}

	if opt.Target != "" {
		if err := refuseLeadChange(store, who, append([]string{opt.Target}, opt.Targets...)...); err != nil {
			return err
		}
	}
	if err := checkDurable(ctx, who, issue, issues, text); err != nil {
		return err
	}
	res, err := store.Add(d, opt, ctx.now())
	if err != nil {
		return err
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, map[string]any{"op": res.Op, "memory": res.Memory})
	}
	m := res.Memory
	w := ctx.Stdout
	switch res.Op {
	case memory.OpNoop:
		if atStatus != "" {
			fmt.Fprintf(w, "Already remembered as %s; its text stands, and it matters at %s.\n", m.ID, m.At)
			return nil
		}
		fmt.Fprintf(w, "Already remembered as %s; nothing written.\n", m.ID)
		return nil
	case memory.OpUpdate:
		fmt.Fprintf(w, "✓ Updated %s (history kept: issue-cli memory show %s)\n", m.ID, m.ID)
		return nil
	}
	line := fmt.Sprintf("✓ Added %s (%s, %s)", m.ID, m.Kind, m.Scope)
	if s := m.Source.String(); s != "" {
		line += " · " + s
	}
	if res.Op == memory.OpSupersede {
		line += " · supersedes " + strings.Join(m.Links, ", ")
	}
	fmt.Fprintln(w, line)
	printEntityHint(w, m)
	switch {
	case m.Scope == memory.ScopeLead && !m.Pinned:
		fmt.Fprintf(w, "Every lead's brief lists it, and no worker reads it but the lead flush; a lead pins it for the whole text: issue-cli memory pin %s.", m.ID)
	case m.At != "":
		fmt.Fprintf(w, "Agents see it on entering %s, whatever their issue is about.", m.At)
	default:
		fmt.Fprintf(w, "Agents see it at start when it matches their issue; a fact about a step, not a subject, takes --at <status>.")
	}
	fmt.Fprintf(w, " Fix it: issue-cli memory edit %s --text \"...\"; no longer true: issue-cli memory forget %s --reason \"...\"\n", m.ID, m.ID)
	return nil
}

// memFilter narrows what search and list show.
type memFilter struct {
	Kind, Scope, Tag, System, At string
	All, Pinned, Suggested       bool
	// Entities (#topic, @name) must all be on the memory.
	Entities []string
}

func (f memFilter) keep(m memory.Memory, ctx *Context) bool {
	if !f.All && !m.Valid(ctx.now()) {
		return false
	}
	if f.Kind != "" && m.Kind != f.Kind {
		return false
	}
	if f.Scope != "" && m.Scope != f.Scope && !strings.HasPrefix(m.Scope, f.Scope+":") {
		return false
	}
	if f.System != "" && !strings.EqualFold(m.System, f.System) {
		return false
	}
	if f.At != "" && !strings.EqualFold(m.At, f.At) {
		return false
	}
	if f.Pinned && !m.Pinned {
		return false
	}
	if f.Suggested && !m.SuggestPin {
		return false
	}
	if !m.HasEntities(f.Entities) {
		return false
	}
	if f.Tag != "" {
		found := false
		for _, t := range m.Tags {
			found = found || strings.EqualFold(t, f.Tag)
		}
		if !found {
			return false
		}
	}
	return true
}

func (f memFilter) check() error {
	if f.Kind != "" {
		if err := memory.CheckKind(f.Kind); err != nil {
			return &usageError{msg: err.Error()}
		}
	}
	switch f.Scope {
	case "", "project", "lead", "department", "agent", "machine":
		return nil
	}
	if err := memory.CheckScope(f.Scope); err != nil {
		return &usageError{msg: err.Error()}
	}
	return nil
}

func filterFlags(fs interface {
	String(string, string, string) *string
	Bool(string, bool, string) *bool
}) (*memFilter, func()) {
	kind := fs.String("kind", "", "only this kind")
	scope := fs.String("scope", "", "only this scope (project, lead, department, agent, machine, or a full scope)")
	tag := fs.String("tag", "", "only memories with this tag")
	system := fs.String("system", "", "only memories about this system")
	at := fs.String("at", "", "only memories that matter at this status")
	all := fs.Bool("all", false, "include superseded, forgotten and expired memories")
	f := &memFilter{}
	return f, func() {
		f.Kind, f.Scope, f.Tag, f.System, f.At, f.All = *kind, *scope, *tag, *system, strings.TrimSpace(*at), *all
	}
}

// memoryAtStatus is raw as the workflows spell it: "" stays "", a status
// no workflow of the project has is refused with the names, so a typo can't
// write a memory nobody is ever shown.
func memoryAtStatus(p *tracker.Project, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	names := p.AllStatuses()
	for _, s := range names {
		if strings.EqualFold(s, raw) {
			return s, nil
		}
	}
	return "", &usageError{msg: fmt.Sprintf("--at %q: no such status (%s)", raw, strings.Join(names, ", "))}
}

// visibleMemories is the project's and the machine's memories the caller
// may read, through the filter.
func visibleMemories(ctx *Context, v memory.Viewer, f memFilter) ([]memory.Memory, error) {
	own, err := memory.ProjectStore(ctx.Project.IssueDir).Load()
	if err != nil {
		return nil, err
	}
	machine, err := memory.MachineStore().Load()
	if err != nil {
		return nil, err
	}
	var out []memory.Memory
	for _, m := range append(own.All(), machine.All()...) {
		if m.VisibleTo(v) && f.keep(m, ctx) {
			out = append(out, m)
		}
	}
	return out, nil
}

func runMemorySearch(ctx *Context, args []string) error {
	fs := newFlagSet("memory search", ctx)
	f, apply := filterFlags(fs)
	limit := fs.Int("limit", 10, "at most this many")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	apply()
	if err := f.check(); err != nil {
		return err
	}
	query := strings.Join(fs.Args(), " ")
	entities, words := memory.SplitEntities(query)
	if len(entities) == 0 && len(memory.Words(words)) == 0 {
		return &usageError{msg: "usage: issue-cli memory search <words...|#topic|@name> (words of 3 letters or more; '#topic' and @name filter by entity: quote a #, or the shell drops it as a comment)"}
	}
	f.Entities = entities
	issues, _ := tracker.LoadIssues(ctx.Project.IssueDir)
	who := memoryCaller(ctx.Project, issues)
	mems, err := visibleMemories(ctx, who.Viewer, *f)
	if err != nil {
		return err
	}
	var hits []memory.Hit
	var hybrid *memory.Hybrid
	if len(memory.Words(words)) == 0 {
		// Entities alone: every memory that carries them, newest first.
		sort.SliceStable(mems, func(i, j int) bool { return mems[i].Changed() > mems[j].Changed() })
		for _, m := range mems {
			if *limit > 0 && len(hits) >= *limit {
				break
			}
			hits = append(hits, memory.Hit{Memory: m, Score: 1})
		}
	} else {
		var ranker memory.Ranker
		ranker, hybrid = memoryRankerScored(ctx, memoryScores(ctx))
		hits = ranker.Rank(memory.Query{Text: words, Department: who.Viewer.Department, Limit: *limit}, mems)
	}
	if ctx.JSONOutput {
		if hits == nil {
			hits = []memory.Hit{}
		}
		return writeJSON(ctx.Stdout, hits)
	}
	if len(hits) == 0 && len(entities) > 0 {
		fmt.Fprintf(ctx.Stdout, "No memory matches %q (%d carry %s; the entities you can see: issue-cli memory entities).\n", query, len(mems), strings.Join(entities, " and "))
	} else if len(hits) == 0 {
		fmt.Fprintf(ctx.Stdout, "No memory matches %q (%d you can see: issue-cli memory list).\n", query, len(mems))
	}
	for _, h := range hits {
		printMemory(ctx.Stdout, h.Memory, ctx)
	}
	if hybrid != nil {
		fmt.Fprintf(ctx.Stdout, "Ranked by %s.\n", hybrid.Note)
	}
	return nil
}

func runMemoryList(ctx *Context, args []string) error {
	fs := newFlagSet("memory list", ctx)
	f, apply := filterFlags(fs)
	pinned := fs.Bool("pinned", false, "only pinned memories")
	suggested := fs.Bool("suggested", false, "only memories a worker suggested pinning")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	apply()
	f.Pinned, f.Suggested = *pinned, *suggested
	if err := f.check(); err != nil {
		return err
	}
	issues, _ := tracker.LoadIssues(ctx.Project.IssueDir)
	mems, err := visibleMemories(ctx, memoryCaller(ctx.Project, issues).Viewer, *f)
	if err != nil {
		return err
	}
	if ctx.JSONOutput {
		if mems == nil {
			mems = []memory.Memory{}
		}
		memoryScores(ctx).Attach(mems)
		return writeJSON(ctx.Stdout, mems)
	}
	if len(mems) == 0 {
		fmt.Fprintln(ctx.Stdout, `No memories yet. An agent adds one with: issue-cli memory add "<fact>"`)
		return nil
	}
	for _, m := range mems {
		printMemory(ctx.Stdout, m, ctx)
	}
	return nil
}

// storeOfID is the store an id is in, or a usage error.
func storeOfID(ctx *Context, id string) (memory.Store, error) {
	switch memory.StoreOfID(id) {
	case "project":
		return memory.ProjectStore(ctx.Project.IssueDir), nil
	case "machine":
		return memory.MachineStore(), nil
	}
	return memory.Store{}, &usageError{msg: fmt.Sprintf("%q is not a memory id: ids look like m-3fa2c1 (the project's) or s-3fa2c1 (the machine's); issue-cli memory list shows them", id)}
}

// oneID parses a subcommand that takes one id and no flags but fs's.
func oneID(ctx *Context, fs interface {
	NArg() int
	Arg(int) string
}, usage string) (memory.Store, string, error) {
	if fs.NArg() != 1 {
		return memory.Store{}, "", &usageError{msg: usage}
	}
	id := fs.Arg(0)
	s, err := storeOfID(ctx, id)
	return s, id, err
}

func runMemoryShow(ctx *Context, args []string) error {
	fs := newFlagSet("memory show", ctx)
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	store, id, err := oneID(ctx, fs, "usage: issue-cli memory show <id>")
	if err != nil {
		return err
	}
	set, err := store.Load()
	if err != nil {
		return err
	}
	m := set.Get(id)
	if m == nil {
		return memoryNotFound(fmt.Errorf("%w %q", memory.ErrNotFound, id))
	}
	issues, _ := tracker.LoadIssues(ctx.Project.IssueDir)
	if !m.VisibleTo(memoryCaller(ctx.Project, issues).Viewer) {
		return fmt.Errorf("%s is in scope %s, which you can't read", id, m.Scope)
	}
	hist := set.History(id)
	use := memoryScores(ctx).Of(id)
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, map[string]any{"memory": m, "history": hist, "usefulness": use})
	}
	w := ctx.Stdout
	printMemory(w, *m, ctx)
	fmt.Fprintf(w, "  state: %s", m.State(ctx.now()))
	if m.InvalidReason != "" {
		fmt.Fprintf(w, " (%s)", m.InvalidReason)
	}
	fmt.Fprintf(w, " · used %d times", m.Uses)
	fmt.Fprintf(w, " · strength %.2f", memory.Strength(*m, ctx.now()))
	if m.LastUsed != "" && len(m.LastUsed) >= 10 {
		fmt.Fprintf(w, " (last used %s)", m.LastUsed[:10])
	}
	if m.Pinned {
		fmt.Fprintf(w, " · pinned by %s", m.PinnedBy)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %s\n", usefulnessLine(use))
	if !m.Pinned && use.ProposeForget() {
		fmt.Fprintln(w, "  no help over enough stages: the next memory consolidate proposes forgetting it to the owner")
	}
	if len(m.Links) > 0 {
		fmt.Fprintf(w, "  links: %s\n", strings.Join(m.Links, ", "))
	}
	fmt.Fprintln(w, "== History ==")
	for _, e := range hist {
		fmt.Fprintf(w, "%s %s by %s%s\n", e.At, e.Op, nonEmpty(e.By, "?"), eventDetail(e))
	}
	return nil
}

func nonEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// eventDetail is an event's change in words.
func eventDetail(e memory.Event) string {
	switch e.Op {
	case memory.OpAdd:
		if e.Memory != nil && e.Memory.Text != "" {
			return ": " + e.Memory.Text
		}
	case memory.OpUpdate, memory.OpOwnerEdit:
		if e.Text != "" {
			return fmt.Sprintf(": %q → %q", e.Old, e.Text)
		}
	case memory.OpSupersede:
		return ": superseded by " + e.Target
	case memory.OpForget:
		return ": " + e.Reason
	case memory.OpSetAt:
		if e.Status == "" {
			return ": at no status"
		}
		return ": at " + e.Status
	}
	return ""
}

const memoryEditUsage = `usage: issue-cli memory edit <id> [--text "<fact>"] [--at <status>|none]`

func runMemoryEdit(ctx *Context, args []string) error {
	fs := newFlagSet("memory edit", ctx)
	text := fs.String("text", "", "the new text")
	at := fs.String("at", "", "the status it matters at; none unties it")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	store, id, err := oneID(ctx, fs, memoryEditUsage)
	if err != nil {
		return err
	}
	newText, newAt := strings.TrimSpace(*text), strings.TrimSpace(*at)
	if newText == "" && newAt == "" {
		return &usageError{msg: memoryEditUsage}
	}
	status := ""
	if !strings.EqualFold(newAt, "none") {
		if status, err = memoryAtStatus(ctx.Project, newAt); err != nil {
			return err
		}
	}
	issues, _ := tracker.LoadIssues(ctx.Project.IssueDir)
	who := memoryCaller(ctx.Project, issues)
	if err := refuseLeadChange(store, who, id); err != nil {
		return err
	}
	if newText != "" {
		if err := refuseMachineWrite(ctx.Project, id); err != nil {
			return err
		}
		if _, err := store.Edit(id, newText, who.Name, who.Owner, ctx.now()); err != nil {
			return memoryNotFound(err)
		}
	}
	if newAt != "" {
		if err := store.SetAt(id, status, who.Name, ctx.now()); err != nil {
			return memoryNotFound(err)
		}
	}
	if ctx.JSONOutput {
		set, err := store.Load()
		if err != nil {
			return err
		}
		return writeJSON(ctx.Stdout, set.Get(id))
	}
	line := fmt.Sprintf("✓ Edited %s (history kept)", id)
	switch {
	case newAt != "" && status == "":
		line += ": it matters at no status now"
	case newAt != "":
		line += ": transition prints it on entering " + status
	}
	fmt.Fprintln(ctx.Stdout, line)
	return nil
}

func runMemoryForget(ctx *Context, args []string) error {
	fs := newFlagSet("memory forget", ctx)
	reason := fs.String("reason", "", "why it is no longer true")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	store, id, err := oneID(ctx, fs, `usage: issue-cli memory forget <id> --reason "<why>"`)
	if err != nil {
		return err
	}
	issues, _ := tracker.LoadIssues(ctx.Project.IssueDir)
	who := memoryCaller(ctx.Project, issues)
	if err := refuseLeadChange(store, who, id); err != nil {
		return err
	}
	if err := store.Forget(id, *reason, who.Name, ctx.now()); err != nil {
		return memoryNotFound(err)
	}
	fmt.Fprintf(ctx.Stdout, "✓ Forgot %s: agents no longer see it; it stays in history (issue-cli memory show %s)\n", id, id)
	return nil
}

func runMemoryPin(ctx *Context, verb string, args []string) error {
	fs := newFlagSet("memory "+verb, ctx)
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	store, id, err := oneID(ctx, fs, "usage: issue-cli memory "+verb+" <id>")
	if err != nil {
		return err
	}
	issues, _ := tracker.LoadIssues(ctx.Project.IssueDir)
	who := memoryCaller(ctx.Project, issues)
	if !who.Lead && !who.Owner {
		return fmt.Errorf("refused: only leads and the owner %s a memory; suggest it with issue-cli memory add ... --suggest-pin, and your lead sees it in issue-cli memory list --suggested", verb)
	}
	if err := store.Pin(id, who.Name, verb == "pin", ctx.now()); err != nil {
		return memoryNotFound(err)
	}
	if verb == "pin" {
		fmt.Fprintf(ctx.Stdout, "✓ Pinned %s: it goes into every briefing of its scope\n", id)
	} else {
		fmt.Fprintf(ctx.Stdout, "✓ Unpinned %s\n", id)
	}
	return nil
}

func memoryNotFound(err error) error {
	if errors.Is(err, memory.ErrNotFound) {
		return fmt.Errorf("%w (issue-cli memory list --all shows the ids)", err)
	}
	return err
}

// printMemory is one memory: id, kind, scope, date, author, then its text
// and source.
func printMemory(w io.Writer, m memory.Memory, ctx *Context) {
	head := fmt.Sprintf("%s · %s · %s · %s", m.ID, m.Kind, m.Scope, m.Day())
	if m.Scope == memory.ScopeMachine && m.Project != "" {
		head += " · from " + m.Project
	}
	if m.Author != "" {
		head += " · " + m.Author
	}
	if m.Pinned {
		head += " · pinned"
	}
	if m.At != "" {
		head += " · at " + m.At
	}
	if st := m.State(ctx.now()); st != "valid" {
		head += " · " + st
	}
	fmt.Fprintf(w, "%s\n  %s\n", head, m.Text)
	if s := m.Source.String(); s != "" {
		fmt.Fprintf(w, "  (%s)\n", s)
	}
}
