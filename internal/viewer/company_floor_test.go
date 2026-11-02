package viewer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The company floor (factory/company-floor-v2-…): a fixed plan, tiles that
// never grow, the inspector rail.

func floorServer(t *testing.T, scenario string) (string, *tracker.Project, func()) {
	t.Helper()
	withMockTmuxSessions(t, nil)
	proj := scenarioProject(t, scenario)
	ts := newTestServer(t, []tracker.Project{proj})
	return ts.URL, &proj, ts.Close
}

// tileRects is every tile's left/top/width/height as the page draws them.
var tileStyle = regexp.MustCompile(`class="wb-fl-(?:tile|plot)[^"]*" style="left:(\d+)px;top:(\d+)px;width:(\d+)px;height:(\d+)px" data-tile="([^"]+)"`)

func tileRects(page string) map[string]string {
	out := map[string]string{}
	for _, m := range tileStyle.FindAllStringSubmatch(page, -1) {
		out[m[5]] = strings.Join(m[1:5], ",")
	}
	return out
}

// stripContent is c without anything a tile could grow from.
func stripContent(c CompanyData) CompanyData {
	c.HQ.Goals, c.HQ.Mission, c.HQ.Shelf, c.HQ.Spend, c.HQ.Handoffs = nil, "", nil, nil, nil
	c.HQ.Desk.CEO.Quote = ""
	c.Product.Goals, c.Product.Mission, c.Product.Shelf, c.Product.Spend = nil, "", nil, nil
	var strip func(rs []WbRoomDept) []WbRoomDept
	strip = func(rs []WbRoomDept) []WbRoomDept {
		out := make([]WbRoomDept, len(rs))
		for i, r := range rs {
			r.Goals, r.Jobs, r.Role, r.Mission, r.Foot = nil, nil, "", "", nil
			r.Shelf, r.Spend, r.Handoffs, r.Connectors, r.Week = nil, nil, nil, nil, nil
			r.Children = strip(r.Children)
			out[i] = r
		}
		return out
	}
	c.Rooms = strip(c.Rooms)
	c.Connections = nil
	return c
}

// TestCompanyFloorTilesKeepPlanSize guards the server half: floorPlan must
// not read what a room holds (it fails the day a size depends on content)
// and the page writes the plan's numbers. The browser half, the laid-out
// size of each kind of tile on this scenario, is session company-floor-long
// (TP12).
func TestCompanyFloorTilesKeepPlanSize(t *testing.T) {
	url, proj, done := floorServer(t, "company-floor-long")
	defer done()
	s := &Server{projects: []tracker.Project{*proj}}
	long := s.buildCompany(proj, "/p/acme", time.Now())
	stripped := stripContent(long)
	a, b := floorPlan(&long, "/p/acme"), floorPlan(&stripped, "/p/acme")
	if len(a.Tiles) != len(b.Tiles) {
		t.Fatalf("%d tiles with content, %d without", len(a.Tiles), len(b.Tiles))
	}
	for i := range a.Tiles {
		if a.Tiles[i].FloorRect != b.Tiles[i].FloorRect {
			t.Errorf("%s: %+v with content, %+v without: a tile grew", a.Tiles[i].Box, a.Tiles[i].FloorRect, b.Tiles[i].FloorRect)
		}
		for j := range a.Tiles[i].Subs {
			if a.Tiles[i].Subs[j].FloorRect != b.Tiles[i].Subs[j].FloorRect {
				t.Errorf("%s in %s grew", a.Tiles[i].Subs[j].Box, a.Tiles[i].Box)
			}
		}
	}
	// The five-department wing takes a row of its own, its rooms in rows of
	// three; every row fits left of the minimap's free column.
	for _, tl := range a.Tiles {
		if tl.Y > 0 && tl.X+tl.W > floorRowMaxW {
			t.Errorf("%s reaches %dpx, into the minimap's column", tl.Box, tl.X+tl.W)
		}
		if tl.Box == "gtm" && (tl.W != floorRowMaxW || len(tl.Subs) != 5 || tl.Slot == nil) {
			t.Errorf("GTM's wing: %dpx, %d sub-rooms, slot %v", tl.W, len(tl.Subs), tl.Slot)
		}
	}

	page := pageGet(t, url+"/p/acme/company")
	rects := tileRects(page)
	for _, tl := range a.Tiles {
		if want := fmt.Sprintf("%d,%d,%d,%d", tl.X, tl.Y, tl.W, tl.H); rects[tl.Box] != want {
			t.Errorf("%s drawn at %q, plan says %q", tl.Box, rects[tl.Box], want)
		}
	}
	// The overflow is in Research's inspector: every goal, job and output.
	insp := section(page, `data-testid="floor.inspector.research"`, `</section>`)
	for _, want := range []string{"User interviews held with paying clients this month", "Insights adopted by Product", "Monthly research digest for the whole company", "Segment review", "Competitor teardown refresh", "Market and user research across every client segment"} {
		if !strings.Contains(insp, want) {
			t.Errorf("Research's inspector lacks %q", want)
		}
	}
	// A long station list keeps the strip's width: "+K" past 18 columns.
	var st []WbStation
	for i := 0; i < 20; i++ {
		st = append(st, WbStation{Label: fmt.Sprint(i)})
	}
	shown, w, more := stationStrip(st, floorStripW)
	if len(shown) != 17 || more != 3 || w*18 > floorStripW {
		t.Errorf("20 stations: %d shown, %dpx columns, +%d", len(shown), w, more)
	}
	if shown, w, more := stationStrip(st[:11], floorStripW); len(shown) != 11 || more != 0 || w != floorStripW/11 {
		t.Errorf("11 stations: %d shown, %dpx, +%d", len(shown), w, more)
	}
}

// section is page from the first from to the next to after it.
func section(page, from, to string) string {
	i := strings.Index(page, from)
	if i < 0 {
		return ""
	}
	j := strings.Index(page[i:], to)
	if j < 0 {
		return page[i:]
	}
	return page[i : i+j]
}

func TestCompanyFloorWaitingCount(t *testing.T) {
	url, _, done := floorServer(t, "company-floor")
	defer done()
	var ny NeedsYouJSON
	if err := json.Unmarshal([]byte(pageGet(t, url+"/p/acme/needs-you.json")), &ny); err != nil {
		t.Fatal(err)
	}
	if ny.Count == 0 {
		t.Fatal("the scenario waits on the owner for nothing")
	}
	page := pageGet(t, url+"/p/acme/company")
	assertContains(t, page, fmt.Sprintf("Waiting on you · %d</h2>", ny.Count), fmt.Sprintf("</span>%d waiting on you", ny.Count))
	if ny.Count > floorWaitRows {
		assertContains(t, page, fmt.Sprintf("%d more: see all and approve", ny.Count-floorWaitRows))
	}
	rail := section(page, `data-testid="floor.waiting"`, `data-testid="floor.waiting.more"`)
	rows := regexp.MustCompile(`<a class="wb-q?row" href="([^"]+)" data-testid="company.queue.(?:row|outputs)">`).FindAllStringSubmatch(rail, -1)
	if want := min(ny.Count, floorWaitRows); len(rows) != want {
		t.Fatalf("the rail lists %d rows, want %d", len(rows), want)
	}
	for i, r := range rows {
		if html.UnescapeString(r[1]) != ny.Rows[i].URL {
			t.Errorf("rail row %d goes to %s, needsYouRows row %d to %s", i, r[1], i, ny.Rows[i].URL)
		}
	}
}

// hooks are the actions a page carries: form actions, spawn, YOLO and send
// hooks.
var hookRe = regexp.MustCompile(`(action|data-spawn-lead|data-yolo|data-send|data-until)="([^"]*)"`)

func hooks(page string) map[string]bool {
	out := map[string]bool{}
	for _, m := range hookRe.FindAllStringSubmatch(page, -1) {
		out[m[1]+"="+m[2]] = true
	}
	return out
}

func TestCompanyFloorKeepsClassicActions(t *testing.T) {
	for _, sc := range []string{"company-floor", "connectors", "nested-groups", "department-group"} {
		t.Run(sc, func(t *testing.T) {
			url, _, done := floorServer(t, sc)
			defer done()
			classic, floor := hooks(pageGet(t, url+"/p/acme/company?classic=1")), hooks(pageGet(t, url+"/p/acme/company"))
			for h := range classic {
				if !floor[h] {
					t.Errorf("the classic page has %s, the floor doesn't", h)
				}
			}
		})
	}
	// With YOLO on: Extend and Turn off.
	url, _, done := floorServer(t, "yolo")
	defer done()
	if code, out := postJSON(t, url+"/p/shop/yolo/on", `{"until":"4h","words":"go"}`); code != 200 {
		t.Fatalf("yolo on: %d %v", code, out)
	}
	classic, floor := hooks(pageGet(t, url+"/p/shop/company?classic=1")), hooks(pageGet(t, url+"/p/shop/company"))
	// data-yolo="band" is the switch's own hook (it points at the band);
	// on the floor the strip stands in for both.
	delete(classic, "data-yolo=band")
	for h := range classic {
		if !floor[h] {
			t.Errorf("YOLO on: the classic page has %s, the floor doesn't", h)
		}
	}
}

// openInspector is the box whose inspector renders without hidden.
var openInsp = regexp.MustCompile(`<section class="wb-fl-insp" id="room-([^"]+)" data-room="[^"]+" data-title="[^"]*" data-testid=`)

func openRooms(page string) []string {
	var out []string
	for _, m := range openInsp.FindAllStringSubmatch(page, -1) {
		out = append(out, m[1])
	}
	return out
}

func TestCompanyFloorLandings(t *testing.T) {
	url, _, done := floorServer(t, "connectors")
	defer done()
	if got := openRooms(pageGet(t, url+"/p/acme/company")); len(got) != 1 || got[0] != "product" {
		t.Errorf("default: open %v, want [product]", got)
	}
	// Check now lands on ?checking=…&dept=<key>#dept-<key>.
	page := pageGet(t, url+"/p/acme/company?checking=1700000000&dept=marketing")
	if got := openRooms(page); len(got) != 1 || got[0] != "marketing" {
		t.Errorf("?dept=marketing: open %v", got)
	}
	insp := section(page, `id="room-marketing"`, `</section>`)
	assertContains(t, insp, `id="dept-marketing"`, `data-checking="1700000000"`)
	// New department lands on ?added=<Name>.
	if got := openRooms(pageGet(t, url+"/p/acme/company?added=Marketing")); len(got) != 1 || got[0] != "marketing" {
		t.Errorf("?added=Marketing: open %v", got)
	}
	// Without JS, #room-<box> and #dept-<key> open their inspector, one at
	// a time: TestCompanyFloorWithoutJSOpensOneRoom, in Chrome.
}

func TestCompanyFloorEveryRoomReachable(t *testing.T) {
	for _, sc := range []string{"nested-groups", "company-floor-long", "company-scale"} {
		t.Run(sc, func(t *testing.T) {
			url, proj, done := floorServer(t, sc)
			defer done()
			page := pageGet(t, url+"/p/acme/company")
			plan := section(page, `data-testid="floor.plan"`, `data-testid="floor.nav"`)
			rail := section(page, `data-testid="floor.rail"`, `</aside>`)
			boxes := []string{"hq", "product"}
			for _, d := range proj.Departments {
				boxes = append(boxes, tracker.DepartmentKey(d.Name))
			}
			for _, box := range boxes {
				if strings.Count(page, `id="room-`+box+`"`) != 1 {
					t.Errorf("%s: %d inspectors", box, strings.Count(page, `id="room-`+box+`"`))
				}
				if !strings.Contains(rail, `href="#room-`+box+`"`) {
					t.Errorf("%s: no link to it in the rail", box)
				}
			}
			if strings.Count(page, `id="room-product"`) != 1 {
				t.Error("a second Product room")
			}
			// Each group's inspector links all its direct children.
			for _, d := range proj.Departments {
				kids := proj.Children(d.Name)
				if len(kids) == 0 {
					continue
				}
				insp := section(page, `id="room-`+tracker.DepartmentKey(d.Name)+`"`, `</section>`)
				for _, k := range kids {
					if !strings.Contains(insp, `href="#room-`+tracker.DepartmentKey(k.Name)+`" data-pick="`+tracker.DepartmentKey(k.Name)+`" data-testid="floor.inspector.child"`) {
						t.Errorf("%s's inspector doesn't link %s", d.Name, k.Name)
					}
				}
			}
			// A "+N" chip links its group's inspector.
			for _, m := range regexp.MustCompile(`href="#room-([^"]+)" data-pick="[^"]+" data-testid="floor.chip.more"`).FindAllStringSubmatch(plan, -1) {
				if g := proj.DepartmentByKey(m[1]); g == nil || len(proj.Children(g.Name)) == 0 {
					t.Errorf("+N chip links %s, not a group", m[1])
				}
			}
		})
	}
}

func TestCompanyFloorRoadsAvoidTiles(t *testing.T) {
	for _, sc := range []string{"company-scale", "declared-connections", "company-floor"} {
		t.Run(sc, func(t *testing.T) {
			_, proj, done := floorServer(t, sc)
			defer done()
			s := &Server{projects: []tracker.Project{*proj}}
			c := s.buildCompany(proj, "/p/acme", time.Now())
			p := floorPlan(&c, "/p/acme")
			idx := map[string]int{}
			for i, tl := range p.Tiles {
				idx[tl.Box] = i
				if tl.Dept != nil {
					eachRoom(tl.Dept.Children, func(r *WbRoomDept) { idx[r.Box] = i })
				}
			}
			for _, r := range append(append([]FloorRoad(nil), p.Roads...), p.Corridors...) {
				for k := 1; k < len(r.Pts); k++ {
					a, b := r.Pts[k-1], r.Pts[k]
					for i, tl := range p.Tiles {
						if crosses(a, b, tl.FloorRect) {
							t.Errorf("road %s→%s segment %v–%v crosses %s (tile %d)", r.A, r.B, a, b, tl.Box, i)
						}
					}
				}
			}
			if sc == "company-scale" && len(p.Roads) == 0 {
				t.Error("no roads drawn on a floor with connections")
			}
		})
	}
}

// crosses reports whether the axis-aligned segment a–b passes through r's
// inside (touching an edge is fine: roads end at their rooms' edges).
func crosses(a, b [2]int, r FloorRect) bool {
	x0, x1 := min(a[0], b[0]), max(a[0], b[0])
	y0, y1 := min(a[1], b[1]), max(a[1], b[1])
	return x1 > r.X && x0 < r.X+r.W && y1 > r.Y && y0 < r.Y+r.H && !(x0 == x1 && (x0 <= r.X || x0 >= r.X+r.W)) && !(y0 == y1 && (y0 <= r.Y || y0 >= r.Y+r.H))
}

// The mockup is the owner's, built from real data: it, its runtime and its
// text stay out of the repo. Phrases are kept as sha256 of the text after a
// plain first word, so this file doesn't carry them either.
var mockupPhrases = []struct {
	first string
	n     int
	sum   string
}{
	{"Item", 70, "c4c47f04bb94041e00002b576d505fde5436c1643b94e925ab5754648905f504"},
	{"395", 18, "1e69cb2ad0b64eee98804cc2601f28d31b9a572045ba9a726dedcdf1d58d674d"},
	{"6", 40, "75edaba6680e3edbefb1a026e4a31e74406c88740b7e7c79b76191d4733f5dbb"},
	{"wing", 40, "aaa8963d36f8476587ee4b4b8c4494a212f23ae93255d4747d5eab26366f20b5"},
	{"2.6B", 14, "59a7b7599ce78ec15d1c67f938480b528d8861cbf9f28422b12ad191b39c706f"},
	{"✓", 17, "626ce6b309d61196bf239c642961e244a86dfa27ddfb2a5e3d8c6b4734e1b1a2"},
	{"Company", 28, "4fd776d9c2d11e8a11f440c07edf89532f626f485552a0e17ae6742a2883a653"},
	{"273", 23, "5223f9040ddf7874850322c0193c5cafe1f63f8482424df0fdc7fb79e8b62d2e"},
}

func TestCompanyFloorNoMockupCommitted(t *testing.T) {
	out, err := exec.Command("git", "ls-files").Output()
	if err != nil {
		t.Skip("not a git checkout:", err)
	}
	files := strings.Fields(string(out))
	for _, f := range files {
		low := strings.ToLower(f)
		if strings.Contains(low, "firmafit") || strings.HasSuffix(low, "/support.js") || strings.Contains(low, "vendor/react") {
			t.Errorf("%s: the mockup's files are not committed", f)
		}
	}
	for _, f := range files {
		// Every text file the page could carry a phrase in: templates,
		// styles and scripts, fixtures and sessions, docs, and all Go source.
		if !(strings.HasPrefix(f, "templates/") || strings.HasPrefix(f, "static/") || strings.HasPrefix(f, "playtest/") || strings.HasPrefix(f, "docs/") || strings.HasSuffix(f, ".go")) || f == "company_floor_test.go" {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		src := string(b)
		if strings.Contains(src, "<x-dc>") {
			t.Errorf("%s carries the mockup's <x-dc> markup", f)
		}
		for _, p := range mockupPhrases {
			for i := strings.Index(src, p.first+" "); i >= 0; {
				if i+p.n <= len(src) {
					h := sha256.Sum256([]byte(src[i : i+p.n]))
					if hex.EncodeToString(h[:]) == p.sum {
						t.Errorf("%s carries a phrase from the mockup", f)
					}
				}
				j := strings.Index(src[i+1:], p.first+" ")
				if j < 0 {
					break
				}
				i += 1 + j
			}
		}
	}
}

// A wing's empty slot names a template only when the group's own template
// lists it as a child, and its link carries that template; a group the
// owner made (or a word that only appears in a description) names none.
func TestCompanyFloorSlotFollowsTheGroupTemplate(t *testing.T) {
	nd := WbNewDeptTile{FormURL: "/p/acme/departments/new", Templates: []WbDeptChoice{
		{Key: "marketing", Name: "Marketing", Description: "SEO and content"},
		{Key: "ops-desk", Name: "Ops desk", Description: "Runs DevOps workshops"},
	}}
	gtm := &WbRoomDept{Box: "gtm", Head: WbRoomHead{Title: "GTM"}}
	if got := slotLabel(gtm, nd); got != "Marketing" {
		t.Errorf("GTM's slot = %q, want Marketing (the GTM template's child)", got)
	}
	if got := slotURL(gtm, nd); got != "/p/acme/departments/new?parent=gtm&template=marketing" {
		t.Errorf("GTM's slot links %q", got)
	}
	ops := &WbRoomDept{Box: "ops", Head: WbRoomHead{Title: "Ops"}}
	if got := slotLabel(ops, nd); got != "Department" {
		t.Errorf("a group without a template: slot %q, want Department", got)
	}
	if got := slotURL(ops, nd); got != "/p/acme/departments/new?parent=ops" {
		t.Errorf("a group without a template links %q", got)
	}
	// Marketing added: GTM's template has no other child left to offer.
	nd.Templates = nd.Templates[1:]
	if got := slotLabel(gtm, nd); got != "Department" {
		t.Errorf("Marketing added: GTM's slot = %q, want Department", got)
	}
}

// TestCompanyFloorChatIsOneClick: HQ's CEO robot links the CEO chat, and
// every department tile, wing and sub-room links its lead's chat, live or
// not (the chat page starts a lead that is not running). The same link is
// first in the inspector. The floor has no pan, zoom or minimap.
func TestCompanyFloorChatIsOneClick(t *testing.T) {
	url, _, done := floorServer(t, "company-floor")
	defer done()
	page := pageGet(t, url+"/p/acme/company")
	assertContains(t, page,
		`href="/p/acme/lead" draggable="false" title="Chat with the CEO" data-testid="floor.hq.chat"`,
		`Chat with the CEO →`,
		`href="/p/acme/lead?dept=gtm" draggable="false" data-testid="floor.group.chat"`,
		`href="/p/acme/lead?dept=marketing" draggable="false" data-testid="floor.sub.chat"`,
	)
	hq := section(page, `id="room-hq"`, `</section>`)
	assertContains(t, hq, `href="/p/acme/lead" data-testid="floor.chat">Chat with the CEO →</a>`)
	mk := section(page, `id="room-marketing"`, `</section>`)
	assertContains(t, mk, `href="/p/acme/lead?dept=marketing" data-testid="floor.chat">Chat with the`)
	if strings.Contains(section(page, `id="room-product"`, `</section>`), `floor.chat`) {
		t.Error("Product has no lead to chat with, but its inspector links a chat")
	}
	for _, gone := range []string{`floor.minimap`, `floor.zoom_in`, `floor.zoom_out`, `floor.fit`, `floor.nav`, `<kbd class="wb-fl-key"`} {
		if strings.Contains(page, gone) {
			t.Errorf("the floor still has %s", gone)
		}
	}

	url2, _, done2 := floorServer(t, "company-floor-long")
	defer done2()
	long := pageGet(t, url2+"/p/acme/company")
	assertContains(t, long, `href="/p/acme/lead?dept=research" draggable="false" data-testid="floor.dept.chat"`)
}
