package main

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/michal-franc/issue-viewer/internal/memory"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

var memoryCommand = &Command{
	Name:      "memory",
	ShortHelp: "Shared memory between agents: facts with their source, read at start (memory add|search|list|entities|show|edit|forget|pin|unpin|candidates|consolidate)",
	LongHelp: `A memory is one fact an agent learned (a convention, a pitfall, how
something is done, who knows what) with its source, for the agents that
come after. issue-cli start prints the pinned ones and those that match
the issue under == Memories ==.

  memory add "<fact>" [--kind fact|convention|pitfall|howto|who]
             [--scope project|department|agent|machine] [--issue <slug>]
             [--commit <sha>] [--comment N] [--tag a,b] [--system S]
             [--expires YYYY-MM-DD] [--links id,id] [--suggest-pin]
             [--updates <id> | --supersedes <id>[,<id>] | --new]
      Store a fact of at most 500 characters. The update step: the same
      fact again changes nothing; a near-duplicate is refused with the
      similar memories, and you decide: --updates <id> (better wording of
      the same fact), --supersedes <id> (it is no longer true; yours
      replaces it, the old one stays in history as invalid) or --new (a
      different fact). The source is your own issue when you are a worker.
      Scope: a department's agents write to their department by default,
      others to the project; agent is your own notebook; machine reaches
      every project on this machine and is refused from a project whose
      privacy: keeps work or code private. Text that looks like a secret is
      refused and never stored: name where it lives (env:<VAR>).
  memory search <words...|#topic|@name> [--kind K] [--scope S] [--tag T] [--system S] [--all] [--limit N]
      The memories you can see, best match first (BM25). A #topic or
      @name in the query is a filter: only memories that carry it (in
      their text or tags); with entities alone, newest first.
  memory list [--kind K] [--scope S] [--tag T] [--pinned] [--suggested] [--all]
      Every memory you can see, newest first; --all adds invalid ones.
  memory entities [--only topic|agent] [--kind K] [--scope S] [--all]
      Who knows what: every #topic and @name in the memories you can see,
      with how many carry it, of which kinds, and for a topic the @names
      a who memory links to it ("known by").
  memory show <id>
      One memory, its state, links and history.
  memory edit <id> --text "<fact>"
  memory forget <id> --reason "<why it is no longer true>"
      Correct or invalidate a memory (it stays in history).
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
			Viewer: memory.Viewer{Department: tracker.DepartmentKey(me.Department), Agent: me.Name}}
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
		return c
	}
	return memCaller{Name: "owner", Viewer: memory.Viewer{All: true}, Owner: true}
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
	scope := fs.String("scope", "", "project, department, agent or machine (default: your department, else the project)")
	issueFlag := fs.String("issue", "", "the issue it was learned on (a worker's own by default)")
	commit := fs.String("commit", "", "the commit it was learned from")
	comment := fs.Int("comment", 0, "the issue comment (1-based) it was learned from")
	tags := fs.String("tag", "", "tags, comma-separated")
	system := fs.String("system", "", "the system it is about (default: the source issue's)")
	expires := fs.String("expires", "", "the last day it is true (YYYY-MM-DD)")
	links := fs.String("links", "", "related memory ids, comma-separated")
	suggest := fs.Bool("suggest-pin", false, "ask a lead or the owner to pin it")
	updates := fs.String("updates", "", "UPDATE: replace this memory's text (the same fact, better worded)")
	supersedes := fs.String("supersedes", "", "SUPERSEDE: these memories (id or id,id) are no longer true or are merged; yours replaces them")
	isNew := fs.Bool("new", false, "ADD even though similar memories exist")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	text := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if text == "" {
		return &usageError{msg: `usage: issue-cli memory add "<fact>" [--kind K] [--scope S] [--issue <slug>] [--commit <sha>] [--updates <id> | --supersedes <id> | --new]`}
	}
	opt := memory.AddOptions{}
	decisions := 0
	if *updates != "" {
		opt, decisions = memory.AddOptions{Mode: memory.ModeUpdates, Target: *updates}, decisions+1
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

	p := ctx.Project
	issues, _ := tracker.LoadIssues(p.IssueDir)
	who := memoryCaller(p, issues)
	d := memory.Draft{Text: text, Kind: *kind, Project: projectKey(p), Author: who.Name, Tags: splitList(*tags),
		Links: splitList(*links), Expires: strings.TrimSpace(*expires), SuggestPin: *suggest,
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
		store = memory.MachineStore()
	}
	for _, id := range opt.Targets {
		if memory.StoreOfID(id) != memory.StoreOfID(opt.Target) {
			return fmt.Errorf("--supersedes %s: every id must be in the same store (m- the project's, s- the machine's)", *supersedes)
		}
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
	fmt.Fprintf(w, "Agents see it at start when it matches their issue. Fix it: issue-cli memory edit %s --text \"...\"; no longer true: issue-cli memory forget %s --reason \"...\"\n", m.ID, m.ID)
	return nil
}

// memFilter narrows what search and list show.
type memFilter struct {
	Kind, Scope, Tag, System string
	All, Pinned, Suggested   bool
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
	case "", "project", "department", "agent", "machine":
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
	scope := fs.String("scope", "", "only this scope (project, department, agent, machine, or a full scope)")
	tag := fs.String("tag", "", "only memories with this tag")
	system := fs.String("system", "", "only memories about this system")
	all := fs.Bool("all", false, "include superseded, forgotten and expired memories")
	f := &memFilter{}
	return f, func() { f.Kind, f.Scope, f.Tag, f.System, f.All = *kind, *scope, *tag, *system, *all }
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
		ranker, hybrid = memoryRanker(ctx)
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
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, map[string]any{"memory": m, "history": hist})
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
	}
	return ""
}

func runMemoryEdit(ctx *Context, args []string) error {
	fs := newFlagSet("memory edit", ctx)
	text := fs.String("text", "", "the new text")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	store, id, err := oneID(ctx, fs, `usage: issue-cli memory edit <id> --text "<fact>"`)
	if err != nil {
		return err
	}
	if strings.TrimSpace(*text) == "" {
		return &usageError{msg: `usage: issue-cli memory edit <id> --text "<fact>"`}
	}
	issues, _ := tracker.LoadIssues(ctx.Project.IssueDir)
	who := memoryCaller(ctx.Project, issues)
	m, err := store.Edit(id, *text, who.Name, who.Owner, ctx.now())
	if err != nil {
		return memoryNotFound(err)
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, m)
	}
	fmt.Fprintf(ctx.Stdout, "✓ Edited %s (history kept)\n", m.ID)
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
	if err := store.Forget(id, *reason, memoryCaller(ctx.Project, issues).Name, ctx.now()); err != nil {
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
	if st := m.State(ctx.now()); st != "valid" {
		head += " · " + st
	}
	fmt.Fprintf(w, "%s\n  %s\n", head, m.Text)
	if s := m.Source.String(); s != "" {
		fmt.Fprintf(w, "  (%s)\n", s)
	}
}

// Start's injection limits: pins are Letta-style core memory (a few, short).
const (
	startMemoryLimit = 5
	pinLimit         = 10
	pinChars         = 1500
)

// issueViewer is what the worker on issue may read.
func issueViewer(p *tracker.Project, issue *tracker.Issue) memory.Viewer {
	return memory.Viewer{Department: deptKey(p.DepartmentOf(issue)), Agent: agentNameForSlug(issue.Slug)}
}

// issueQuery is the ranker's query for issue.
func issueQuery(p *tracker.Project, issue *tracker.Issue, limit int) memory.Query {
	return memory.Query{
		Text:   issue.Title + " " + issue.System + " " + strings.Join(issue.Labels, " ") + " " + issue.Slug,
		System: issue.System, Department: deptKey(p.DepartmentOf(issue)), Limit: limit,
	}
}

// printStartMemories is start's == Memories == block: the pinned memories
// of the issue's scopes, then those matching the issue, recorded as used.
func printStartMemories(w io.Writer, ctx *Context, issue *tracker.Issue) {
	p := ctx.Project
	fmt.Fprintln(w, "\n== Memories ==")
	mems, err := visibleMemories(ctx, issueViewer(p, issue), memFilter{})
	if err != nil {
		fmt.Fprintf(w, "Could not read the memories: %v\n", err)
		return
	}
	pins, rest := memory.SplitPins(mems, pinLimit, pinChars)
	ranker, _ := memoryRanker(ctx)
	hits := ranker.Rank(issueQuery(p, issue, startMemoryLimit), rest)
	if len(mems) == 0 {
		fmt.Fprintln(w, "None yet on this project.")
	}
	if len(pins.Shown) > 0 {
		fmt.Fprintln(w, "Pinned:")
		for _, m := range pins.Shown {
			fmt.Fprintf(w, "  %s (%s) %s\n", m.ID, m.Kind, m.Text)
		}
		if pins.More > 0 {
			fmt.Fprintf(w, "  +%d pinned: issue-cli memory list --pinned\n", pins.More)
		}
	}
	if len(hits) > 0 {
		fmt.Fprintln(w, "Matching this issue:")
		for _, h := range hits {
			printMemory(w, h.Memory, ctx)
		}
	} else if len(mems) > 0 {
		fmt.Fprintln(w, "None matches this issue.")
	}
	if len(mems) > len(pins.Shown)+len(hits) {
		fmt.Fprintf(w, "(%d you can see: issue-cli memory search <words>)\n", len(mems))
	}
	var used []string
	for _, m := range pins.Shown {
		used = append(used, m.ID)
	}
	for _, h := range hits {
		used = append(used, h.ID)
	}
	recordUse(ctx, used, agentNameForSlug(issue.Slug))
	fmt.Fprintf(w, "Learned something the next agent should know? issue-cli memory add \"<fact>\" --kind fact|convention|pitfall|howto|who (a similar one: --updates <id>)\n")
}

// recordUse writes use events to each id's store; errors only cost the
// count.
func recordUse(ctx *Context, ids []string, by string) {
	var own, machine []string
	for _, id := range ids {
		if memory.StoreOfID(id) == "machine" {
			machine = append(machine, id)
		} else {
			own = append(own, id)
		}
	}
	_ = memory.ProjectStore(ctx.Project.IssueDir).RecordUse(own, by, ctx.now())
	_ = memory.MachineStore().RecordUse(machine, by, ctx.now())
}

// printMemoryReflection is the hint at done and retrospective
// (LangMem's reflection, done by the agent): what to add, and the similar
// memories to update instead.
func printMemoryReflection(w io.Writer, ctx *Context, issue *tracker.Issue) {
	fmt.Fprintln(w, "\n== Memory ==")
	fmt.Fprintf(w, "Anything the next agent should know? issue-cli memory add \"<fact>\" --issue %s --kind fact|convention|pitfall|howto|who\n", issue.Slug)
	fmt.Fprintf(w, "What this issue's trail could teach: issue-cli memory candidates %s\n", issue.Slug)
	mems, err := visibleMemories(ctx, issueViewer(ctx.Project, issue), memFilter{})
	if err != nil {
		return
	}
	ranker, _ := memoryRanker(ctx)
	hits := ranker.Rank(issueQuery(ctx.Project, issue, 3), mems)
	if len(hits) == 0 {
		return
	}
	fmt.Fprintln(w, "Already remembered about this (update one instead with --updates <id>, or --supersedes <id> if it is no longer true):")
	for _, h := range hits {
		fmt.Fprintf(w, "  %s (%s) %s\n", h.ID, h.Kind, h.Text)
	}
}
