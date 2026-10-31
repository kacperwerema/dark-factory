package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// The company's header carries the switch; it opens the turn-on dialog with
// the workflow's gates, what still guards, the duration, the valves and the
// owner's words. While YOLO is on, every project page shows the band.
func TestYOLOWorkshopSwitchDialogAndBand(t *testing.T) {
	proj := setupYOLOProject(t)
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	page := pageGet(t, ts.URL+"/p/yp/company?classic=1")
	assertContains(t, page,
		`data-testid="yolo.switch"`, `aria-checked="false"`, `data-yolo="dialog"`,
		`data-testid="yolo.dialog"`, `Turn on YOLO MODE?`,
		`Gates skipped · 1`, `<b>Approve backlog</b><span class="wb-mono">in design → backlog</span>`,
		`<b>Agent tests</b>`, `<b>Permission prompts</b>`,
		`value="08:00" checked`, `value="4h"`, `value="off"`,
		`no local CI here: the CI valve is off.`, `name="no_releases" checked`,
		`data-testid="yolo.dialog.words"`, `required`,
		// Off, one pass waits: the count and the queue, no band.
		`data-testid="yolo.count"`, `1 issue to review after the fact`,
		`data-testid="company.yolo_queue"`, `YOLO · 1 gate`, `href="/p/yp/issue/shop#yolo"`,
		`<script src="/static/workshop-yolo.js" defer></script>`,
	)
	if strings.Contains(page, `data-testid="yolo.band"`) {
		t.Error("band drawn while YOLO is off")
	}
	// The switch is the company's only: the hall shows the count, no switch.
	if hall := pageGet(t, ts.URL+"/p/yp/hall"); strings.Contains(hall, `data-testid="yolo.switch"`) || strings.Contains(hall, `data-testid="yolo.dialog"`) {
		t.Error("the switch or the dialog is drawn outside the company")
	}

	if code, out := postJSON(t, ts.URL+"/p/yp/yolo/on", `{"until":"4h","words":"go yolo","ci_valve":true}`); code != 200 {
		t.Fatalf("on: %d %v", code, out)
	}
	for _, path := range []string{"/company?classic=1", "/hall", "/status", "/health", "/systems", "/decisions", "/retros", "/docs", "/updates", "/issue/shop"} {
		code, html := getBody(t, ts.URL+"/p/yp"+path)
		if code != 200 {
			t.Errorf("%s: status %d", path, code)
			continue
		}
		for _, want := range []string{`data-testid="yolo.band"`, `Gates pass without you · since `, `turns off at `, `data-testid="yolo.band.extend"`, `data-yolo="extend" data-until="4h"`, `data-testid="yolo.band.off"`, `no releases`} {
			if !strings.Contains(html, want) {
				t.Errorf("%s: band lacks %q", path, want)
			}
		}
	}
	// The floor carries the 34px strip instead of the band.
	floor := pageGet(t, ts.URL+"/p/yp/company")
	assertContains(t, floor, `data-testid="floor.yolo"`, `data-testid="floor.yolo.since">since `, `until `, `data-testid="floor.yolo.extend"`, `data-yolo="extend" data-until="4h"`, `data-testid="floor.yolo.off"`, `no releases`, `data-yolo-prefix="/p/yp"`)
	company := pageGet(t, ts.URL+"/p/yp/company?classic=1")
	assertContains(t, company, `wb-yolo-switch wb-yolo-switch--on`, `aria-checked="true"`, `wb-room--yolo`, `gate open — review after the fact`, `data-testid="company.room.product.yolo">YOLO · 1</a>`)
	if strings.Contains(company, `data-testid="yolo.dialog"`) {
		t.Error("the turn-on dialog is still drawn while YOLO is on")
	}
}

// The map: the lot in stripes with "YOLO until <time>" and "N after the
// fact" instead of the "!"; after YOLO ends the counter stays until the queue
// is empty.
func TestYOLOWorkshopMapLot(t *testing.T) {
	proj := setupYOLOProject(t)
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	if code, out := postJSON(t, ts.URL+"/p/yp/yolo/on", `{"until":"off","words":"go"}`); code != 200 {
		t.Fatalf("on: %d %v", code, out)
	}
	page := pageGet(t, ts.URL+"/")
	assertContains(t, page, `wb-lot--yolo`, `data-testid="map.lot.yolo">YOLO until turned off</span>`, `data-testid="map.lot.after_fact"`, `<span>1</span><span>after the fact</span>`, `1 issue to review after the fact`)
	// The map is L0: no band, no switch.
	if strings.Contains(page, `data-testid="yolo.band"`) || strings.Contains(page, `data-testid="yolo.switch"`) {
		t.Error("the map draws the band or the switch")
	}

	if code, _ := postJSON(t, ts.URL+"/p/yp/yolo/off", ``); code != 200 {
		t.Fatal("off failed")
	}
	page = pageGet(t, ts.URL+"/")
	if strings.Contains(page, `wb-lot--yolo`) || strings.Contains(page, `data-testid="map.lot.yolo"`) {
		t.Error("lot still in stripes after YOLO ended")
	}
	assertContains(t, page, `data-testid="map.lot.after_fact"`)

	if code, _ := postJSON(t, ts.URL+"/p/yp/issue/shop/yolo-review", `{"verdict":"agree","words":"fine"}`); code != 200 {
		t.Fatal("agree failed")
	}
	if page = pageGet(t, ts.URL+"/"); strings.Contains(page, `data-testid="map.lot.after_fact"`) {
		t.Error("after-the-fact counter stays after the queue is empty")
	}
}

// The hall: gates raised while on; a crate that passed a gate in YOLO keeps
// the yellow marker. The station: "Passed in YOLO" with Agree / Back to.
func TestYOLOWorkshopHallAndStation(t *testing.T) {
	proj := setupYOLOProject(t)
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	hall := pageGet(t, ts.URL+"/p/yp/hall")
	assertContains(t, hall, `data-testid="hall.crate.yolo"`)
	if strings.Contains(hall, `wb-gatecard--raised`) {
		t.Error("gate raised while YOLO is off")
	}
	if code, _ := postJSON(t, ts.URL+"/p/yp/yolo/on", `{"until":"4h","words":"go"}`); code != 200 {
		t.Fatal("on failed")
	}
	hall = pageGet(t, ts.URL+"/p/yp/hall")
	assertContains(t, hall, `wb-gatecard wb-gatecard--raised`, `data-testid="hall.gate.raised">barrier up</span>`, `data-testid="hall.crate.yolo"`)

	station := pageGet(t, ts.URL+"/p/yp/issue/shop")
	assertContains(t, station,
		`data-testid="station.yolo"`, `data-review="/p/yp/issue/shop/yolo-review"`, `Passed in YOLO`,
		`<b>in design</b> → <b>backlog</b> passed without you · agent-shop`,
		`data-testid="station.yolo.agree">Agree</button>`, `data-testid="station.yolo.back">Back to in design</button>`,
	)
	if code, _ := postJSON(t, ts.URL+"/p/yp/issue/shop/yolo-review", `{"status":"backlog","verdict":"agree","words":"ok"}`); code != 200 {
		t.Fatal("agree failed")
	}
	if station = pageGet(t, ts.URL+"/p/yp/issue/shop"); strings.Contains(station, `data-testid="station.yolo"`) {
		t.Error("station still asks for review after Agree")
	}
	// The marker stays to the end of the line, reviewed or not.
	assertContains(t, pageGet(t, ts.URL+"/p/yp/hall"), `data-testid="hall.crate.yolo"`)
}

// A decision recorded in YOLO (how YOLO, no words) carries the tag in the
// log and on the station.
func TestYOLOWorkshopDecisionTag(t *testing.T) {
	proj := setupYOLOProject(t)
	withMockTmuxSessions(t, nil)
	path := filepath.Join(proj.IssueDir, "shop.md")
	if _, err := tracker.AddDecision(path, "shop", tracker.NewDecision{Text: "Group the list by shop aisle", How: "YOLO"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	assertContains(t, pageGet(t, ts.URL+"/p/yp/decisions"),
		`data-testid="decisions.row.shop.d-1.yolo"`, `recorded by: agent in YOLO`, `The agent decided alone in YOLO — no words from you.`)
	assertContains(t, pageGet(t, ts.URL+"/p/yp/issue/shop"), `YOLO · without your words`)
}
