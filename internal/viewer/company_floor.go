package viewer

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/connections"
	"github.com/kacperwerema/dark-factory/internal/depttemplate"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The company floor (L1, factory/company-floor-v2-…): the whole company on
// one screen, a fixed plan of rooms that always fits the view (scaled as a
// CSS transform by static/company-floor.js) and an inspector rail holding
// what does not fit a tile. Today's page stays at ?classic=1 (#D-1).
// Components in templates/_workshop_floor.html, styles in static/workshop.css
// ("L1: the company floor").

// The plan's fixed geometry, in plan pixels. Row 1 is HQ and Product; the
// rooms projects.yaml adds go on row 2 and below, never into the plan's
// right floorFreeCol pixels: the roads between rows run there. (The
// minimap sat there too until the floor stopped moving; the plan always fits
// the view now. If a bigger floor stops fitting, moving around comes back.)
const (
	floorPlanW   = 1032
	floorRow1H   = 250
	floorHQW     = 250
	floorGapX    = 22
	floorRowGap  = 40
	floorRowH    = 330
	floorFreeCol = 190
	floorRowMaxW = floorPlanW - floorFreeCol
	floorDeptW   = 240
	floorPlotW   = 180
	floorPlotH   = 150
	floorHeadH   = 44
	floorPad     = 12
	// floorSubChatH is a sub-room's chat link under its head, with its margin.
	floorSubChatH = 30
	floorLeadCol  = 176
	floorSlotW    = 92
	floorSubBigW  = 202
	floorSubBigH  = 180
	floorChipH    = 24
	floorStripW   = 732
	floorStation  = 40
	floorWaitRows = 3
)

// FloorData is the company floor: the classic page's model plus the plan
// and the rail.
type FloorData struct {
	CompanyData
	Plan FloorPlan
	// Pick is the box whose inspector is open when the page loads.
	Pick string
	// Waits are the first floorWaitRows rows of needsYouRows; WaitMore how
	// many more there are.
	Waits    []WbQueueCard
	WaitMore int
	// Inspectors are one per box, in plan order; Rooms the same as links.
	Inspectors []FloorInspector
	Rooms      []FloorLink
	// AfterYOLO is how many issues wait for review after YOLO.
	AfterYOLO int
}

// FloorPlan is the fixed plan: its size, the tiles and the roads.
type FloorPlan struct {
	W, H  int
	Tiles []FloorTile
	// Corridors are the thin dashed roads between neighbouring rooms;
	// Roads the connections between boxes, routed through the gaps.
	Corridors []FloorRoad
	Roads     []FloorRoad
}

// FloorRect is a box on the plan.
type FloorRect struct{ X, Y, W, H int }

// FloorTile is one room on the plan at a size that depends only on how many
// rooms there are.
type FloorTile struct {
	FloorRect
	// Kind is hq, product, dept, group or new.
	Kind    string
	Box     string
	Title   string
	Color   string
	HQ      *WbRoomHQ
	Product *WbRoomFactory
	Dept    *WbRoomDept
	NewDept *WbNewDeptTile
	// Sentence is the one line the tile shows (the CEO's quote, the role or
	// mission); Nums the three numbers; Job the first job that is due.
	Sentence string
	Nums     []FloorNum
	Job      *WbJobLine
	// Stations is Product's (or a department's) line at its column width;
	// More the stations past the strip's last column.
	Stations []WbStation
	StationW int
	More     int
	// Subs are a group's departments as sub-rooms inside its wing; Slot the
	// empty slot that adds one.
	Subs []FloorSub
	Slot *FloorSlot
	// LeadW is a wing's lead column width.
	LeadW int
	// Goals are what the tile shows at the detail level; Badges its foot
	// (at most three).
	Goals  []WbGoal
	Badges []WbFootItem
}

// FloorNum is one of a tile's three numbers.
type FloorNum struct{ Value, Label, Tone, Title string }

// FloorSub is a department (or a nested group) inside a wing, placed
// relative to the wing.
type FloorSub struct {
	FloorRect
	Box   string
	Room  *WbRoomDept
	Chips []FloorLink
	// More is how many chips did not fit; it links the group's inspector.
	More int
}

// FloorSlot is a wing's empty slot: the New department form with the group
// chosen.
type FloorSlot struct {
	FloorRect
	Label, URL string
}

// FloorLink is a link to a room's inspector.
type FloorLink struct {
	Box, Title, Color, Note string
	Depth                   int
}

// FloorRoad is a road as a polyline in plan pixels.
type FloorRoad struct {
	Points string
	// Pts are the points, for the test that no road crosses a tile.
	Pts         [][2]int
	A, B        string
	Title, URL  string
	Traffic     bool
	Undeclared  bool
	Count       int
	LabelX      int
	LabelY      int
	ShowCounter bool
}

// FloorInspector is one room in the rail.
type FloorInspector struct {
	Box, Title, Color string
	Kind              string
	// Lead is the session (or "N workers"), State its word and tone.
	Lead, State, StateTone string
	Note                   string
	HQ                     *WbRoomHQ
	Product                *WbRoomFactory
	Dept                   *WbRoomDept
	// Children are a group's departments and sub-groups.
	Children []FloorLink
	// CTA is the main button; Spawn makes it the spawn button.
	CTA, CTAURL string
	Spawn       bool
	// ChatURL is the room's chat (the CEO's or the lead's), first in the
	// inspector; Chat its words. Empty for Product.
	ChatURL, Chat string
	// Line is Product's or a department's line, every station.
	Line []WbStation
	// Missing are what the room lacks and the command that fills it.
	Missing []string
	Open    bool
}

func (s *Server) handleCompanyFloor(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	q := r.URL.Query()
	if q.Get("classic") == "1" {
		s.handleCompany(w, r, proj, prefix)
		return
	}
	c := s.buildCompany(proj, prefix, time.Now())
	added := ""
	if c.Added = addedFlash(q); c.Added != "" {
		added = q.Get("added")
		eachRoom(c.Rooms, func(r *WbRoomDept) {
			if strings.EqualFold(r.Head.Title, added) {
				r.Added = true
			}
		})
	}
	if since, dept := q.Get("checking"), q.Get("dept"); since != "" {
		eachRoom(c.Rooms, func(r *WbRoomDept) {
			if r.Connectors != nil && r.Connectors.Dept == dept {
				r.Connectors.Since = since
			}
		})
	}
	d := buildFloor(c, prefix, added, q.Get("dept"))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "company_floor.html", d); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// eachRoom calls fn on every department and group room at every depth.
func eachRoom(rooms []WbRoomDept, fn func(*WbRoomDept)) {
	for i := range rooms {
		fn(&rooms[i])
		eachRoom(rooms[i].Children, fn)
	}
}

// buildFloor lays c out on the plan and builds the rail. added is the room
// the owner just added (?added=), dept the ?dept= of a Check now; either
// picks the open room, else Product.
func buildFloor(c CompanyData, prefix, added, dept string) FloorData {
	d := FloorData{CompanyData: c}
	d.Waits = c.NeedsYou
	if len(d.Waits) > floorWaitRows {
		d.Waits, d.WaitMore = d.Waits[:floorWaitRows], len(c.NeedsYou)-floorWaitRows
	}
	if c.Lot.YOLO != nil {
		d.AfterYOLO = len(c.Lot.YOLO.Queue)
	}
	d.Plan = floorPlan(&d.CompanyData, prefix)
	d.Inspectors = floorInspectors(&d.CompanyData, prefix)
	for _, in := range d.Inspectors {
		depth := 0
		if in.Dept != nil {
			depth = roomDepth(c.Rooms, in.Box, 0)
		}
		d.Rooms = append(d.Rooms, FloorLink{Box: in.Box, Title: in.Title, Color: in.Color, Depth: depth})
	}
	d.Pick = connections.Product
	eachRoom(d.CompanyData.Rooms, func(r *WbRoomDept) {
		if dept != "" && (r.Box == dept || r.Box == tracker.DepartmentKey(dept)) {
			d.Pick = r.Box
		}
	})
	eachRoom(d.CompanyData.Rooms, func(r *WbRoomDept) {
		if added != "" && strings.EqualFold(r.Head.Title, added) {
			d.Pick = r.Box
		}
	})
	for i := range d.Inspectors {
		d.Inspectors[i].Open = d.Inspectors[i].Box == d.Pick
	}
	return d
}

// roomDepth is how deep box sits under the top-level rooms (0 for one).
func roomDepth(rooms []WbRoomDept, box string, depth int) int {
	for _, r := range rooms {
		if r.Box == box {
			return depth
		}
		if n := roomDepth(r.Children, box, depth+1); n >= 0 {
			return n
		}
	}
	return -1
}

// floorPlan places HQ and Product on row 1 and the rooms projects.yaml adds
// on rows 2 and below. Every size comes from counts (rooms, children,
// depth, stations), never from what a room holds.
func floorPlan(c *CompanyData, prefix string) FloorPlan {
	p := FloorPlan{W: floorPlanW}
	hq := FloorTile{FloorRect: FloorRect{0, 0, floorHQW, floorRow1H}, Kind: "hq", Box: connections.HQ, Title: c.HQ.Head.Title, Color: companyHQColor, HQ: &c.HQ}
	hq.Sentence = c.HQ.Desk.CEO.Quote
	hq.Nums = hqNums(c)
	for i, g := range c.HQ.Goals {
		if i < 2 {
			hq.Goals = append(hq.Goals, g.Goal)
		}
	}
	p.Tiles = append(p.Tiles, hq)

	prod := FloorTile{FloorRect: FloorRect{floorHQW + floorGapX, 0, floorPlanW - floorHQW - floorGapX, floorRow1H}, Kind: "product", Box: connections.Product, Title: c.Product.Head.Title, Color: companyProductColor, Product: &c.Product}
	prod.Stations, prod.StationW, prod.More = stationStrip(c.Product.Stations, floorStripW)
	prod.Goals = c.Product.Goals
	p.Tiles = append(p.Tiles, prod)

	// Rows 2+: the top-level rooms in projects.yaml order, then the plot.
	x, y, rowH := 0, floorRow1H+floorRowGap, 0
	place := func(t FloorTile, own bool) FloorTile {
		if x > 0 && (own || x+t.W > floorRowMaxW) {
			y += rowH + floorRowGap
			x, rowH = 0, 0
		}
		t.X, t.Y = x, y
		x += t.W + floorGapX
		rowH = max(rowH, t.H)
		if own {
			x = floorRowMaxW
		}
		return t
	}
	for i := range c.Rooms {
		r := &c.Rooms[i]
		var t FloorTile
		own := false
		if len(r.Children) > 0 {
			t, own = wingTile(r, prefix, c.NewDept)
		} else {
			t = deptTile(r)
		}
		p.Tiles = append(p.Tiles, place(t, own))
	}
	plot := FloorTile{FloorRect: FloorRect{0, 0, floorPlotW, floorPlotH}, Kind: "new", Box: "new", Title: c.NewDept.Tile.Title, NewDept: &c.NewDept}
	p.Tiles = append(p.Tiles, place(plot, false))
	// The gap under the last row carries roads; it is there with or
	// without them, so the plan's height never depends on what moves.
	p.H = y + rowH + floorRowGap
	p.Corridors = floorCorridors(p.Tiles)
	p.Roads = floorRoads(c, p.Tiles, prefix)
	return p
}

// hqNums is HQ's three numbers: open issues, outputs to review, tokens.
func hqNums(c *CompanyData) []FloorNum {
	out := []FloorNum{{Value: fmt.Sprint(c.HQ.Open), Label: "open"}}
	review := 0
	if c.HQ.Shelf != nil {
		review = c.HQ.Shelf.ToReview
	}
	out = append(out, FloorNum{Value: fmt.Sprint(review), Label: "to review", Tone: toneIf(review > 0, "you")})
	out = append(out, spendNum(c.HQ.Spend))
	return out
}

// spendNum is this week's tokens as a tile number.
func spendNum(sp *WbSpend) FloorNum {
	if sp == nil || sp.Empty || sp.Tokens == "" {
		return FloorNum{Value: "—", Label: "tokens/wk", Title: "No spend recorded yet"}
	}
	return FloorNum{Value: sp.Tokens, Label: "tokens/wk", Title: sp.Line}
}

func toneIf(ok bool, tone string) string {
	if ok {
		return tone
	}
	return ""
}

// stationStrip fits a line into width: each column width/N, at least
// floorStation; past that the last column is "+K".
func stationStrip(st []WbStation, width int) ([]WbStation, int, int) {
	if len(st) == 0 {
		return nil, 0, 0
	}
	cols := width / floorStation
	if len(st) <= cols {
		return st, width / len(st), 0
	}
	return st[:cols-1], width / cols, len(st) - (cols - 1)
}

// deptTile is a department straight under HQ.
func deptTile(r *WbRoomDept) FloorTile {
	t := FloorTile{FloorRect: FloorRect{0, 0, floorDeptW, floorRowH}, Kind: "dept", Box: r.Box, Title: r.Head.Title, Color: r.Head.Color, Dept: r}
	t.Sentence = roomSentence(r)
	t.Job = firstDueJob(r.Jobs)
	t.Goals = r.Goals
	t.Badges = r.Foot[:min(3, len(r.Foot))]
	if len(r.Stations) > 0 {
		t.Stations, t.StationW, t.More = stationStrip(r.Stations, floorDeptW-2*floorPad)
	}
	return t
}

// roomSentence is a department's one line: its role, else its mission.
func roomSentence(r *WbRoomDept) string {
	if r.Role != "" {
		return r.Role
	}
	return r.Mission
}

// firstDueJob is the first job waiting on the owner, else due, else the
// first job.
func firstDueJob(jobs []WbJobLine) *WbJobLine {
	for _, want := range []string{"waits", "due"} {
		for i := range jobs {
			if jobs[i].State == want {
				return &jobs[i]
			}
		}
	}
	if len(jobs) > 0 {
		return &jobs[0]
	}
	return nil
}

// wingTile is a group as a wing: its lead column, its departments as
// sub-rooms, an empty slot. A small wing (up to 3 departments, no group
// among them) shares a row; a big one takes a row of its own and lays its
// sub-rooms out in rows of 3.
func wingTile(r *WbRoomDept, prefix string, nd WbNewDeptTile) (FloorTile, bool) {
	t := FloorTile{Kind: "group", Box: r.Box, Title: r.Head.Title, Color: "var(--group)", Dept: r, LeadW: floorLeadCol}
	t.Job = firstDueJob(r.Jobs)
	t.Sentence = roomSentence(r)
	t.Goals = r.Goals
	for _, f := range r.Foot {
		if strings.HasPrefix(f.Text, "♥") {
			t.Badges = append(t.Badges, f)
		}
	}
	n := len(r.Children)
	nested := false
	for _, k := range r.Children {
		if len(k.Children) > 0 {
			nested = true
		}
	}
	slot := &FloorSlot{Label: slotLabel(r, nd), URL: slotURL(r, nd)}
	x0 := floorPad + floorLeadCol + floorPad
	if n <= 3 && !nested {
		subW := map[int]int{1: 324, 2: 240, 3: 200}[n]
		t.H = floorRowH
		subH := floorRowH - floorHeadH - 2*floorPad
		x := x0
		for i := range r.Children {
			t.Subs = append(t.Subs, floorSub(&r.Children[i], FloorRect{x, floorPad, subW, subH}))
			x += subW + floorPad
		}
		slot.FloorRect = FloorRect{x, floorPad, floorSlotW, subH}
		t.W = x + floorSlotW + floorPad
		if t.W <= floorRowMaxW {
			t.Slot = slot
			return t, false
		}
		// Three departments don't fit beside a slot: lay out as a big wing.
		t.Subs = nil
	}
	t.W = floorRowMaxW
	cells := n + 1
	rows := (cells + 2) / 3
	t.H = max(floorRowH, floorHeadH+2*floorPad+rows*floorSubBigH+(rows-1)*floorPad)
	for i := 0; i < cells; i++ {
		rect := FloorRect{x0 + (i%3)*(floorSubBigW+floorPad), floorPad + (i/3)*(floorSubBigH+floorPad), floorSubBigW, floorSubBigH}
		if i == n {
			slot.FloorRect = rect
			t.Slot = slot
			break
		}
		t.Subs = append(t.Subs, floorSub(&r.Children[i], rect))
	}
	return t, true
}

// floorSub is a department in a wing, or a nested group as a sub-room with
// its departments as chips (a third level as chips the same way).
func floorSub(r *WbRoomDept, rect FloorRect) FloorSub {
	s := FloorSub{FloorRect: rect, Box: r.Box, Room: r}
	if len(r.Children) == 0 {
		return s
	}
	fit := max(1, (rect.H-floorHeadH-floorSubChatH-floorPad)/floorChipH)
	var all []FloorLink
	for _, k := range r.Children {
		note := ""
		if len(k.Children) > 0 {
			note = plural(len(k.Children), "department", "departments")
		}
		all = append(all, FloorLink{Box: k.Box, Title: k.Head.Title, Color: k.Head.Color, Note: note})
	}
	if len(all) > fit {
		s.Chips, s.More = all[:fit-1], len(all)-(fit-1)
	} else {
		s.Chips = all
	}
	return s
}

// slotTemplate is the template a wing's empty slot offers: one not yet
// added that the group's own template lists among its children (the GTM
// template lists Marketing), else nil. Only that link names a slot: a
// group the owner made has no template, and its slot says "Department".
func slotTemplate(r *WbRoomDept, nd WbNewDeptTile) *WbDeptChoice {
	gt, err := depttemplate.Load(r.Box)
	if err != nil || !gt.Group {
		return nil
	}
	for _, child := range gt.Children {
		for i := range nd.Templates {
			if nd.Templates[i].Key == child {
				return &nd.Templates[i]
			}
		}
	}
	return nil
}

// slotLabel names a wing's empty slot after slotTemplate, else
// "Department".
func slotLabel(r *WbRoomDept, nd WbNewDeptTile) string {
	if tm := slotTemplate(r, nd); tm != nil {
		return tm.Name
	}
	return "Department"
}

// slotURL is the New department form with the group chosen (and the
// slot's template, when it names one), "" when the project can't add one.
func slotURL(r *WbRoomDept, nd WbNewDeptTile) string {
	if nd.FormURL == "" || len(nd.Templates) == 0 {
		return ""
	}
	u := nd.FormURL + "?parent=" + url.QueryEscape(r.Box)
	if tm := slotTemplate(r, nd); tm != nil {
		u += "&template=" + url.QueryEscape(tm.Key)
	}
	return u
}

// floorCorridors are the thin dashed roads between neighbours: HQ to
// Product, row 1 down to row 2, each row down to the next.
func floorCorridors(tiles []FloorTile) []FloorRoad {
	var out []FloorRoad
	add := func(pts ...[2]int) { out = append(out, FloorRoad{Pts: pts, Points: polyline(pts)}) }
	add([2]int{floorHQW, floorRow1H / 2}, [2]int{floorHQW + floorGapX, floorRow1H / 2})
	rows := map[int][]FloorTile{}
	var ys []int
	for _, t := range tiles {
		if t.Kind == "hq" || t.Kind == "product" {
			continue
		}
		if _, ok := rows[t.Y]; !ok {
			ys = append(ys, t.Y)
		}
		rows[t.Y] = append(rows[t.Y], t)
	}
	above := []FloorTile{tiles[0], tiles[1]}
	for _, y := range ys {
		for _, up := range above {
			// One road per tile above: down to the first tile under its
			// centre (HQ at its centre, Product at the mockup's 560).
			cx := up.X + up.W/2
			if up.Kind == "product" {
				cx = 560
			}
			for _, t := range rows[y] {
				if t.Kind != "new" && cx > t.X+10 && cx < t.X+t.W-10 {
					add([2]int{cx, up.Y + up.H}, [2]int{cx, t.Y})
					break
				}
			}
		}
		above = rows[y]
	}
	return out
}

// floorRoads are the connections between boxes, routed through the gaps:
// down from A into the gap under its row, along it, and into B; between rows
// that are not neighbours through the free column on the right. A road to
// a sub-room ends at its wing; two rooms in one wing have no road.
func floorRoads(c *CompanyData, tiles []FloorTile, prefix string) []FloorRoad {
	if c.Connections == nil {
		return nil
	}
	owner := map[string]int{}
	for i, t := range tiles {
		owner[t.Box] = i
		if t.Dept != nil {
			eachRoom(t.Dept.Children, func(r *WbRoomDept) { owner[r.Box] = i })
		}
	}
	rowBottom := map[int]int{}
	for _, t := range tiles {
		rowBottom[t.Y] = max(rowBottom[t.Y], t.Y+t.H)
	}
	var out []FloorRoad
	for n, road := range c.Connections.Roads {
		ai, okA := owner[road.A]
		bi, okB := owner[road.B]
		if !okA || !okB || ai == bi {
			continue
		}
		a, b := tiles[ai], tiles[bi]
		if b.Y < a.Y || (b.Y == a.Y && b.X < a.X) {
			a, b = b, a
		}
		off := (n % 5) * 4
		ax, bx := a.X+a.W/2+off, b.X+b.W/2+off
		gA := rowBottom[a.Y] + floorRowGap/2 - 8 + off
		var pts [][2]int
		switch {
		case a.Y == b.Y:
			pts = [][2]int{{ax, a.Y + a.H}, {ax, gA}, {bx, gA}, {bx, b.Y + b.H}}
		case rowBottom[a.Y]+floorRowGap == b.Y:
			pts = [][2]int{{ax, a.Y + a.H}, {ax, gA}, {bx, gA}, {bx, b.Y}}
		default:
			gB := b.Y - floorRowGap/2 - 8 + off
			fx := floorRowMaxW + floorGapX + off
			pts = [][2]int{{ax, a.Y + a.H}, {ax, gA}, {fx, gA}, {fx, gB}, {bx, gB}, {bx, b.Y}}
		}
		fr := FloorRoad{Pts: pts, Points: polyline(pts), A: road.A, B: road.B, URL: road.URL, Title: road.Name + ": " + road.Label, Count: road.Open, Traffic: road.Open > 0, Undeclared: road.Route == "undeclared"}
		// The counter sits on the first horizontal run, in the gap under the
		// first room: never in the free column, where the roads bunch.
		mid := [2]int{(pts[1][0] + pts[2][0]) / 2, pts[1][1]}
		fr.LabelX, fr.LabelY, fr.ShowCounter = mid[0], mid[1], road.Open > 0
		out = append(out, fr)
	}
	return out
}

func polyline(pts [][2]int) string {
	var b strings.Builder
	for i, p := range pts {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%d,%d", p[0], p[1])
	}
	return b.String()
}

// floorInspectors are the rail's rooms: HQ, Product, then every department
// and group at every depth, in plan order.
func floorInspectors(c *CompanyData, prefix string) []FloorInspector {
	hq := FloorInspector{Box: connections.HQ, Kind: "hq", Title: c.HQ.Head.Title, Color: companyHQColor, HQ: &c.HQ, Lead: c.HQ.Desk.CEO.Session, CTA: "Report →", CTAURL: hqReportURL(prefix)}
	hq.State, hq.StateTone = liveWord(c.HQ.Desk.CEO.Live)
	if q := c.HQ.Desk.CEO.Quote; q != "" {
		hq.Note = "“" + q + "”"
	}
	// The chat even while the CEO is not running: its page starts it.
	hq.ChatURL, hq.Chat = c.HQ.Desk.ChatURL, "Chat with the CEO →"
	if c.HQ.Mission != "" {
		hq.Note = strings.TrimSpace(hq.Note + " " + c.HQ.Mission)
	}
	if c.HQ.Spend != nil && c.HQ.Spend.Empty {
		hq.Missing = append(hq.Missing, "No spend recorded yet: "+c.HQ.Spend.Hint)
	}
	out := []FloorInspector{hq}

	workers := 0
	for _, ch := range c.Product.Head.Chips {
		if strings.HasSuffix(ch.Text, "worker") || strings.HasSuffix(ch.Text, "workers") {
			fmt.Sscan(ch.Text, &workers)
		}
	}
	pr := FloorInspector{Box: connections.Product, Kind: "product", Title: c.Product.Head.Title, Color: companyProductColor, Product: &c.Product, Lead: plural(workers, "worker", "workers"), CTA: "Enter the hall →", CTAURL: c.Product.HallURL, Line: c.Product.Stations, Note: c.Product.Mission}
	pr.State, pr.StateTone = "CI not set up", "idle"
	switch c.Lot.CI {
	case "green":
		pr.State, pr.StateTone = "CI green", "green"
	case "red":
		pr.State, pr.StateTone = "CI red", "red"
	}
	out = append(out, pr)

	var walk func(rooms []WbRoomDept)
	walk = func(rooms []WbRoomDept) {
		for i := range rooms {
			r := &rooms[i]
			in := FloorInspector{Box: r.Box, Kind: "dept", Title: r.Head.Title, Color: r.Head.Color, Dept: r, Lead: r.Lead.Session, Note: strings.TrimSpace(strings.Join(nonBlank(r.Role, r.Mission), " ")), Line: r.Stations}
			if len(r.Children) > 0 {
				in.Kind, in.Color = "group", "var(--group)"
				for _, k := range r.Children {
					note := plural(len(k.Children), "department", "departments")
					if len(k.Children) == 0 {
						note = ""
						if k.Lead.Live {
							note = "lead live"
						}
					}
					in.Children = append(in.Children, FloorLink{Box: k.Box, Title: k.Head.Title, Color: k.Head.Color, Note: note})
				}
				if r.Week != nil && r.Week.URL == "" && r.Week.Command != "" {
					in.Missing = append(in.Missing, "No week report yet: "+r.Week.Command)
				}
			}
			in.State, in.StateTone = liveWord(r.Lead.Live)
			in.ChatURL, in.Chat = r.Lead.ChatURL, "Chat with the "+r.Lead.Who+" →"
			if !r.Lead.Live {
				in.CTA, in.CTAURL, in.Spawn = "▶ Start lead", r.Lead.SpawnURL, true
			}
			if len(r.Goals) == 0 {
				in.Missing = append(in.Missing, "No goals yet: add goals: to "+r.Head.Title+" in projects.yaml")
			}
			if r.Spend != nil && r.Spend.Empty {
				in.Missing = append(in.Missing, "No spend recorded yet: "+r.Spend.Hint)
			}
			out = append(out, in)
			walk(r.Children)
		}
	}
	walk(c.Rooms)
	return out
}

func liveWord(live bool) (string, string) {
	if live {
		return "live", "green"
	}
	return "not running", "idle"
}

func nonBlank(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// stripLabel is a station's name as the floor's strip writes it under the
// box, short like the mockup's ("docs", "ship"); the full name is in the
// station's title.
func stripLabel(label string) string {
	switch label {
	case "documentation":
		return "docs"
	case "shipping":
		return "ship"
	case "in design":
		return "design"
	}
	return label
}
