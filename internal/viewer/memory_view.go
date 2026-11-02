package viewer

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The agents' shared memory in the viewer: /p/<project>/memory lists every
// memory of the project (every scope) and the machine's, with filters,
// source, links and history, and the owner edits, pins or deletes any of
// them (POST …/memory/edit|pin|unpin|delete). A delete scrubs the text
// from the log. The writes pass only from the owner's browser
// (fromOwnerBrowser); agents use issue-cli memory. memory.json serves the
// lists. See docs/memory.md.

// MemoryData is the Memory page.
type MemoryData struct {
	Header WbHeader
	Title  string
	Rows   []MemoryRow
	// Filters are the seg links: scope, kind, state, pinned.
	Filters []MemoryFilter
	Recent  []MemoryEventLine
	// Entities is the entity index over the memories the filters (but
	// the entity one) keep: who knows what (phase 4).
	Entities []MemoryEntity
	// Entity is the ?entity= filter, ClearEntityURL the page without it.
	Entity, ClearEntityURL string
	// Grouped: ?group=entity, the rows under each entity in Groups.
	Grouped bool
	Groups  []MemoryGroup
	// Total is how many memories the stores hold (any state).
	Total        int
	Flash, Error string
	LoadError    string
	AddCmd       string
	// ProjectPath and MachinePath are where the logs are.
	ProjectPath, MachinePath string
}

// MemoryFilter is one group of seg links.
type MemoryFilter struct {
	Name, Label string
	Items       []MemoryFilterItem
}

// MemoryFilterItem is one seg link.
type MemoryFilterItem struct {
	Label, Href, Key string
	On               bool
}

// MemoryRow is one memory on the page.
type MemoryRow struct {
	memory.Memory
	State string
	// IssueURL links the source issue when it is this project's.
	IssueURL string
	History  []MemoryEventLine
	// The forms' targets.
	EditURL, DeleteURL, PinURL, UnpinURL string
	// Entities are the memory's #topics and @names, each a filter link.
	Entities []MemoryEntityLink
	// Anchor: this copy carries the row's id (a grouped memory shows
	// under each of its entities, the first one anchors it).
	Anchor bool
}

// MemoryEntityLink is one entity chip: the page filtered to it.
type MemoryEntityLink struct{ Name, Href string }

// MemoryEntity is one entity of the index on the page.
type MemoryEntity struct {
	memory.Entity
	// Key is the entity as a test id: topic-<name> or agent-<name>.
	Key, Href string
	Count     int
	On        bool
}

// MemoryGroup is the rows under one entity (Name "" is the memories with
// no entity).
type MemoryGroup struct {
	MemoryEntity
	Rows []MemoryRow
}

// entityKey is an entity as a test id and anchor: #playtest is
// topic-playtest, @lead-x agent-lead-x.
func entityKey(name string) string {
	switch memory.EntityKind(name) {
	case memory.EntityTopic, memory.EntityAgent:
		return memory.EntityKind(name) + "-" + name[1:]
	}
	return "none"
}

// MemoryEventLine is one event in words.
type MemoryEventLine struct {
	ID, At, Op, By, Detail string
}

func memoryEventLine(e memory.Event) MemoryEventLine {
	at := e.At
	if len(at) >= 16 {
		at = strings.Replace(at[:16], "T", " ", 1)
	}
	return MemoryEventLine{ID: e.ID, At: at, Op: e.Op, By: e.By, Detail: memoryEventDetail(e)}
}

// memoryEventDetail is an event's change in words (the CLI's show says the
// same).
func memoryEventDetail(e memory.Event) string {
	switch e.Op {
	case memory.OpAdd:
		if e.Memory != nil {
			return e.Memory.Text
		}
	case memory.OpUpdate, memory.OpOwnerEdit:
		if e.Text != "" {
			return fmt.Sprintf("%q → %q", e.Old, e.Text)
		}
	case memory.OpSupersede:
		return "superseded by " + e.Target
	case memory.OpForget:
		return e.Reason
	case memory.OpOwnerDelete:
		return "deleted by the owner; its text is gone"
	}
	return ""
}

// memoryQuery is the page's filters from the URL.
type memoryQuery struct{ Scope, Kind, State, Pinned, Entity, Group string }

func (q memoryQuery) keep(m memory.Memory, now time.Time) bool {
	switch q.State {
	case "", "valid":
		if !m.Valid(now) {
			return false
		}
	case "invalid":
		if m.Valid(now) {
			return false
		}
	}
	switch q.Scope {
	case "project", "machine":
		if m.Scope != q.Scope {
			return false
		}
	case "department", "agent":
		if !strings.HasPrefix(m.Scope, q.Scope+":") {
			return false
		}
	}
	if q.Kind != "" && m.Kind != q.Kind {
		return false
	}
	if q.Pinned == "pinned" && !m.Pinned {
		return false
	}
	if q.Pinned == "suggested" && !m.SuggestPin {
		return false
	}
	return true
}

// keepEntity: the ?entity= filter.
func (q memoryQuery) keepEntity(m memory.Memory) bool {
	return q.Entity == "" || m.HasEntities([]string{q.Entity})
}

func (q memoryQuery) href(prefix, name, value string) string {
	vals := map[string]string{"scope": q.Scope, "kind": q.Kind, "state": q.State, "pinned": q.Pinned, "entity": q.Entity, "group": q.Group}
	vals[name] = value
	v := url.Values{}
	for _, k := range []string{"scope", "kind", "state", "pinned", "entity", "group"} {
		if vals[k] != "" {
			v.Set(k, vals[k])
		}
	}
	if len(v) == 0 {
		return prefix + "/memory"
	}
	return prefix + "/memory?" + v.Encode()
}

func (q memoryQuery) filters(prefix string) []MemoryFilter {
	group := func(name, label, cur string, opts ...[2]string) MemoryFilter {
		f := MemoryFilter{Name: name, Label: label}
		for _, o := range opts {
			f.Items = append(f.Items, MemoryFilterItem{Label: o[1], Key: nonEmptyKey(o[0]), Href: q.href(prefix, name, o[0]), On: cur == o[0]})
		}
		return f
	}
	kinds := [][2]string{{"", "all"}}
	for _, k := range memory.Kinds {
		kinds = append(kinds, [2]string{k, k})
	}
	return []MemoryFilter{
		group("scope", "Scope", q.Scope, [2]string{"", "all"}, [2]string{"project", "project"}, [2]string{"department", "department"}, [2]string{"agent", "agent"}, [2]string{"machine", "machine"}),
		group("kind", "Kind", q.Kind, kinds...),
		group("state", "State", q.State, [2]string{"", "valid"}, [2]string{"invalid", "superseded or forgotten"}, [2]string{"all", "all"}),
		group("pinned", "Pins", q.Pinned, [2]string{"", "any"}, [2]string{"pinned", "pinned"}, [2]string{"suggested", "suggested"}),
		group("group", "Group", q.Group, [2]string{"", "none"}, [2]string{"entity", "by entity"}),
	}
}

func nonEmptyKey(k string) string {
	if k == "" {
		return "any"
	}
	return k
}

func (s *Server) buildMemory(p *tracker.Project, prefix string, q memoryQuery, now time.Time) MemoryData {
	color := s.projectColor(p)
	lot, rows := s.buildLot(p, color, now)
	d := MemoryData{Title: p.Name + " · Memory", AddCmd: `issue-cli memory add "<fact>" --kind fact|convention|pitfall|howto|who`,
		ProjectPath: memory.ProjectPath(p.IssueDir), MachinePath: memory.MachinePath()}
	d.Header = WbHeader{Crumbs: wbCrumbs(1, p.Name, prefix+"/company"), Working: lot.Working, Idle: lot.Idle, AgentWait: lot.AgentWait, DoneWeek: lot.DoneWeek, Waiting: len(rows), WaitingURL: prefix + "/needs-you"}
	d.Header.Crumbs[1].Color, d.Header.Crumbs[1].Current, d.Header.Crumbs[1].URL = color, false, prefix+"/company"
	d.Header.Crumbs[2] = WbCrumb{Label: "Memory", Current: true}
	d.Header.YOLO = headerYOLO(lot.YOLO, prefix)
	d.Filters = q.filters(prefix)

	issues, _ := tracker.LoadIssues(p.IssueDir)
	var sets []*memory.Set
	for _, st := range []memory.Store{memory.ProjectStore(p.IssueDir), memory.MachineStore()} {
		set, err := st.Load()
		if err != nil {
			if d.LoadError == "" {
				d.LoadError = err.Error()
			}
			continue
		}
		sets = append(sets, set)
	}
	var kept []memory.Memory
	for _, set := range sets {
		for _, m := range set.All() {
			d.Total++
			if !q.keep(m, now) {
				continue
			}
			kept = append(kept, m)
			if !q.keepEntity(m) {
				continue
			}
			r := MemoryRow{Memory: m, State: m.State(now), EditURL: prefix + "/memory/edit", DeleteURL: prefix + "/memory/delete", PinURL: prefix + "/memory/pin", UnpinURL: prefix + "/memory/unpin", Anchor: true}
			for _, e := range m.Entities() {
				r.Entities = append(r.Entities, MemoryEntityLink{Name: e, Href: q.href(prefix, "entity", e)})
			}
			if m.Source.Issue != "" && tracker.FindIssue(issues, m.Source.Issue) != nil {
				r.IssueURL = prefix + "/issue/" + m.Source.Issue
			}
			for _, e := range set.History(m.ID) {
				r.History = append(r.History, memoryEventLine(e))
			}
			d.Rows = append(d.Rows, r)
		}
		for _, e := range set.Recent(now, 7) {
			if len(d.Recent) < 12 {
				d.Recent = append(d.Recent, memoryEventLine(e))
			}
		}
	}
	d.Entity, d.ClearEntityURL = memory.NormEntity(q.Entity), q.href(prefix, "entity", "")
	d.Entities = memoryEntities(memory.Index(kept), q, prefix)
	if q.Group == "entity" {
		d.Grouped, d.Groups = true, groupByEntity(d.Rows, d.Entities)
	}
	return d
}

// memoryEntities is the page's entity index, each a filter link.
func memoryEntities(idx []memory.Entity, q memoryQuery, prefix string) []MemoryEntity {
	cur := memory.NormEntity(q.Entity)
	out := make([]MemoryEntity, 0, len(idx))
	for _, e := range idx {
		out = append(out, MemoryEntity{Entity: e, Key: entityKey(e.Name), Href: q.href(prefix, "entity", e.Name), Count: len(e.IDs), On: e.Name == cur})
	}
	return out
}

// groupByEntity puts rows under each entity of the index (most memories
// first), a row under every entity it carries; rows with none go last.
// Only the first copy of a row anchors its id.
func groupByEntity(rows []MemoryRow, idx []MemoryEntity) []MemoryGroup {
	byName := map[string][]MemoryRow{}
	var none []MemoryRow
	for _, r := range rows {
		if len(r.Entities) == 0 {
			none = append(none, r)
			continue
		}
		for _, e := range r.Entities {
			byName[e.Name] = append(byName[e.Name], r)
		}
	}
	anchored := map[string]bool{}
	var out []MemoryGroup
	add := func(g MemoryGroup, rs []MemoryRow) {
		for _, r := range rs {
			r.Anchor = !anchored[r.ID]
			anchored[r.ID] = true
			g.Rows = append(g.Rows, r)
		}
		g.Count = len(g.Rows)
		out = append(out, g)
	}
	for _, e := range idx {
		if rs := byName[e.Name]; len(rs) > 0 {
			add(MemoryGroup{MemoryEntity: e}, rs)
		}
	}
	if len(none) > 0 {
		add(MemoryGroup{MemoryEntity: MemoryEntity{Key: "none"}}, none)
	}
	return out
}

func (s *Server) handleMemory(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	v := r.URL.Query()
	q := memoryQuery{Scope: v.Get("scope"), Kind: v.Get("kind"), State: v.Get("state"), Pinned: v.Get("pinned"), Entity: memory.NormEntity(v.Get("entity")), Group: v.Get("group")}
	if q.Group != "entity" {
		q.Group = ""
	}
	d := s.buildMemory(proj, prefix, q, time.Now())
	d.Flash, d.Error = v.Get("done"), v.Get("error")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "memory_wb.html", d); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleMemoryJSON(w http.ResponseWriter, r *http.Request, proj *tracker.Project) {
	own, err1 := memory.ProjectStore(proj.IssueDir).Load()
	machine, err2 := memory.MachineStore().Load()
	if err := errors.Join(err1, err2); err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	list := func(set *memory.Set) []memory.Memory {
		if all := set.All(); all != nil {
			return all
		}
		return []memory.Memory{}
	}
	// entities: the index over every valid memory of both stores (the
	// owner's view: every scope).
	now := time.Now()
	entities := memory.Index(append(own.Active(now), machine.Active(now)...))
	writeJSONStatus(w, http.StatusOK, map[string]any{"project": list(own), "machine": list(machine), "entities": entities})
}

// memoryRefused is what an agent or a script gets for a write.
const memoryRefused = "only the owner edits, pins or deletes memories here, from the Memory page in the browser; an agent uses issue-cli memory edit|forget (a lead: pin)"

// handleMemoryWrite is the owner's edit, pin, unpin or delete of one
// memory; it redirects back to the page with what happened.
func (s *Server) handleMemoryWrite(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix, action string) {
	if !s.fromOwner(r) {
		http.Error(w, memoryRefused, http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id := strings.TrimSpace(r.PostForm.Get("id"))
	var store memory.Store
	switch memory.StoreOfID(id) {
	case "project":
		store = memory.ProjectStore(proj.IssueDir)
	case "machine":
		store = memory.MachineStore()
	default:
		http.Error(w, "not a memory id: "+id, http.StatusBadRequest)
		return
	}
	back := func(key, msg string) {
		http.Redirect(w, r, prefix+"/memory?"+url.Values{key: {msg}}.Encode()+"#"+id, http.StatusSeeOther)
	}
	now := time.Now()
	var err error
	done := id + " saved"
	switch action {
	case "edit":
		_, err = store.Edit(id, r.PostForm.Get("text"), "owner", true, now)
	case "pin", "unpin":
		err = store.Pin(id, "owner", action == "pin", now)
		done = id + " " + action + "ned"
	case "delete":
		err = store.OwnerDelete(id, now)
		done = id + " deleted; its text is gone from the log"
	}
	if err != nil {
		back("error", id+": "+err.Error())
		return
	}
	log.Printf("memory: %s %s by the owner", id, action)
	back("done", done)
}

// hqMemoryFoot is the HQ room's foot link to the Memory page.
func hqMemoryFoot(p *tracker.Project, prefix string) WbFootItem {
	text := "🧠 Memory →"
	if set, err := memory.ProjectStore(p.IssueDir).Load(); err == nil {
		if n := len(set.Active(time.Now())); n > 0 {
			text = "🧠 " + plural(n, "memory", "memories") + " →"
		}
	}
	return WbFootItem{Text: text, URL: prefix + "/memory"}
}
