package viewer

// Phone notifications (docs/API/security.md "Phone notifications"): Web Push
// to the owner's phone and browsers when something new waits on them.
//
// Events come from needsYouRows (the one waiting-on-you list), plus two
// push-only sources that are not rows: a live worker stuck at a menu, and a
// subscription's usage limit. internal/push keeps the devices, settings and
// the ledger that sends each event to each device once; internal/webpush
// encrypts and signs. Only the viewer that holds viewer.json for the config
// push/ is bound to sends, so a sandbox viewer never pushes to the real
// phone.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/push"
	"github.com/kacperwerema/dark-factory/internal/routing"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/viewerstate"
	"github.com/kacperwerema/dark-factory/internal/webpush"
)

const (
	// pushEvery is the watcher's scan period.
	pushEvery = time.Minute
	// pushTitleMax clips a notification title.
	pushTitleMax = 120
	// stuckCooldown: a worker that stalls again within it pings once.
	stuckCooldown = time.Hour
)

// pushService is the viewer's push sender.
type pushService struct {
	store *push.Store
	// config is this viewer's projects.yaml (absolute); claimed says
	// viewerstate.Claim let it hold viewer.json (the -record-viewer flag
	// doesn't count here).
	config  string
	claimed bool
	// claimWhy is Claim's reason when it said no.
	claimWhy string
	client   *http.Client
	now      func() time.Time

	mu   sync.Mutex
	keys *webpush.Keys
	// warned: the "not sending" line was logged once.
	warned bool
}

func newPushService(dir, config string, claimed bool, why string) *pushService {
	return &pushService{store: push.NewStore(dir), config: config, claimed: claimed, claimWhy: why, client: webpush.Client(), now: time.Now}
}

// Keys loads or makes the VAPID pair.
func (p *pushService) Keys() (*webpush.Keys, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.keys != nil {
		return p.keys, nil
	}
	k, err := webpush.LoadOrCreate(p.store.Dir)
	if err != nil {
		return nil, err
	}
	p.keys = k
	return k, nil
}

// canBind reports whether this viewer may bind the store: never a temp
// folder config writing into a shared (non-temp) state dir.
func (p *pushService) canBind() bool {
	return p.config != "" && !(viewerstate.InSandbox(p.config) && !viewerstate.InSandbox(p.store.Dir))
}

// sameConfig compares two projects.yaml paths.
func sameConfig(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	fa, err1 := os.Stat(a)
	fb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(fa, fb)
}

// active says whether this viewer sends and changes push/, and why not.
func (p *pushService) active(st *push.State) (bool, string) {
	switch {
	case p == nil || p.store.Dir == "":
		return false, "no state directory (HOME unset)"
	case p.config == "":
		return false, "this viewer runs without a projects.yaml"
	case !p.claimed:
		return false, "this viewer is a sandbox (" + scrubHome(p.claimWhy) + ")"
	case st.Settings.Config != "" && !sameConfig(st.Settings.Config, p.config):
		return false, "notifications are sent by the viewer for " + filepath.Base(filepath.Dir(st.Settings.Config)) + "/" + filepath.Base(st.Settings.Config)
	case st.Settings.Config == "" && !p.canBind():
		return false, "this viewer's config is in a temp folder and the state dir is shared"
	}
	return true, ""
}

// scrubHome hides the home folder in a message.
func scrubHome(s string) string {
	if errorPathScrub != nil {
		return errorPathScrub.Replace(s)
	}
	return s
}

// pushKindWords are the notification title's first words per kind.
var pushKindWords = map[string]string{
	"ci":         "CI red",
	"gate":       "Gate ready",
	"playtest":   "Playtest to run",
	"checks":     "Checks to run",
	"question":   "Question",
	"clarify":    "Clarify round",
	"outputs":    "Outputs to review",
	"job":        "Job due",
	"postmortem": "Post-mortem",
	"connectors": "Connector failing",
	"nightly":    "Factory pass rate",
	"stuck":      "Worker stuck",
	"limit":      "Usage limit",
}

// pushTitle is "<kind words>: <thing>", home folder scrubbed, clipped.
func pushTitle(kind, thing string) string {
	w := pushKindWords[kind]
	if w == "" {
		w = "Waiting on you"
	}
	t := scrubHome(w + ": " + strings.Join(strings.Fields(thing), " "))
	if r := []rune(t); len(r) > pushTitleMax {
		t = string(r[:pushTitleMax-1]) + "…"
	}
	return t
}

// pushSeenRows maps a project's Needs you rows to events. Unsent bug
// reports belong to the machine and are not pushed.
func pushSeenRows(p *tracker.Project, rows []WbQueueCard) []push.Seen {
	var out []push.Seen
	for _, r := range rows {
		if r.Kind == "bugs" {
			continue
		}
		thing := r.Title
		if thing == "" {
			thing = r.Slug
		}
		if r.Kind == "gate" && r.Target != "" {
			thing += " → " + r.Target
		}
		out = append(out, push.Seen{
			Key:         p.Slug + "|" + r.Kind + "|" + r.Slug + "|" + r.Target,
			Project:     p.Slug,
			ProjectName: p.Name,
			Kind:        r.Kind,
			Title:       pushTitle(r.Kind, thing),
			URL:         push.SafeURL(r.URL),
			Urgent:      r.Kind == "ci",
		})
	}
	return out
}

// pushRows is the project's Needs you rows (a var so tests can give one
// row of each kind).
var pushRows = func(p *tracker.Project, open []*tracker.Issue, now time.Time) []WbQueueCard {
	return needsYouRows(p, "", "/p/"+p.Slug, open, now)
}

// pushStuck finds live workers stuck at a menu: idle (no issue-cli call
// for agentIdleAfter) while the pane shows a numbered prompt. Questions and
// gates are rows already.
var pushStuck = func(p *tracker.Project, open []*tracker.Issue, now time.Time) []push.Seen {
	sessions := projectSessions(p)
	if len(sessions) == 0 {
		return nil
	}
	live := map[string]bool{}
	for _, s := range sessions {
		live[agentrun.BareName(s.Name)] = true
	}
	wf := p.LoadWorkflow()
	workDir := resolveProjectWorkDir(p)
	var out []push.Seen
	for _, issue := range open {
		name := tracker.AgentSessionName(issue.Slug)
		if !live[name] {
			continue
		}
		wt, _, _ := resolveWorktree(workDir, issue.Slug, wf)
		comments, _ := tracker.LoadComments(issue.FilePath)
		call, known := lastCliCall(p.Slug, agentLogPaths(workDir, wt, name, issue.Assignee)...)
		if state, _ := workerState(wf, issue, comments, call, known, now); state != "idle" {
			continue
		}
		pane, err := agentrun.CapturePane(agentrun.Target(p, name))
		if err != nil {
			continue
		}
		lines := strings.Split(cleanPane(pane), "\n")
		if m, _ := paneMenu(lines, max(0, len(lines)-40)); m == nil {
			continue
		}
		out = append(out, push.Seen{
			Key: p.Slug + "|stuck|" + name + "|", Project: p.Slug, ProjectName: p.Name, Kind: "stuck",
			Title: pushTitle("stuck", issue.Title), URL: push.SafeURL("/p/" + p.Slug + "/agents?agent=" + name),
			Cooldown: stuckCooldown,
		})
	}
	return out
}

// pushLimits are the subscriptions resting after a usage limit: machine
// events (no project, not mutable).
var pushLimits = func(firstProject string, now time.Time) []push.Seen {
	st, err := routing.Load(routing.Dir())
	if err != nil {
		return nil
	}
	var out []push.Seen
	for _, r := range st.Active(now) {
		url := "/needs-you"
		if firstProject != "" {
			url = "/p/" + firstProject + "/agents"
		}
		out = append(out, push.Seen{
			Key: "|limit|" + r.Agent + "|", Kind: "limit",
			Title: pushTitle("limit", r.Agent+" rests until "+r.Until.Local().Format("15:04")),
			URL:   push.SafeURL(url),
		})
	}
	return out
}

// pushSeen is everything a scan finds waiting, and the projects it covered
// (a project whose issues fail to load is left out, so its events stay).
func (s *Server) pushSeen(now time.Time) ([]push.Seen, []string) {
	var seen []push.Seen
	scanned := []string{""}
	for i := range s.projects {
		p := &s.projects[i]
		issues, err := tracker.LoadIssues(p.IssueDir)
		if err != nil {
			continue
		}
		var open []*tracker.Issue
		for _, is := range issues {
			if is.Status != "done" {
				open = append(open, is)
			}
		}
		scanned = append(scanned, p.Slug)
		seen = append(seen, pushSeenRows(p, pushRows(p, open, now))...)
		seen = append(seen, pushStuck(p, open, now)...)
	}
	first := ""
	if len(s.projects) > 0 {
		first = s.projects[0].Slug
	}
	seen = append(seen, pushLimits(first, now)...)
	return seen, scanned
}

// pushScan is one watcher tick: observe, plan under the lock, send outside
// it, record under it again. It returns how many pushes it sent.
func (s *Server) pushScan(ctx context.Context) int {
	ps := s.push
	if ps == nil {
		return 0
	}
	cur, err := ps.store.Read()
	if err != nil {
		log.Printf("push: %v", err)
		return 0
	}
	if ok, why := ps.active(cur); !ok {
		ps.mu.Lock()
		if !ps.warned && len(cur.Devices) > 0 {
			log.Printf("push: not sending: %s", why)
		}
		ps.warned = true
		ps.mu.Unlock()
		return 0
	}
	if len(cur.Devices) == 0 {
		return 0
	}
	now := ps.now()
	seen, scanned := s.pushSeen(now)
	keys, err := ps.Keys()
	if err != nil {
		log.Printf("push: keys: %v", err)
		return 0
	}
	type out struct {
		job push.Job
		dev push.Device
		msg webpush.Message
	}
	var outs []out
	var subject string
	err = ps.store.Update(func(st *push.State) error {
		if ok, _ := ps.active(st); !ok {
			return nil
		}
		seeding := st.NeedSeed
		st.Observe(seen, scanned, now)
		if seeding {
			log.Printf("push: the ledger was missing; seeded %d devices", len(st.Devices))
		}
		subject = pushSubject(st.Settings, s.allowedHosts)
		for _, j := range st.Plan(now) {
			pay, ok := st.PayloadFor(j)
			dev, okd := st.Device(j.Device)
			if !ok || !okd {
				continue
			}
			body, _ := json.Marshal(pay)
			e := st.Events[j.EventKey]
			outs = append(outs, out{job: j, dev: dev, msg: webpush.Message{Payload: body, Urgent: e != nil && e.Urgent, Topic: webpush.TopicFor(j.EventKey)}})
		}
		return nil
	})
	if err != nil {
		log.Printf("push: %v", err)
		return 0
	}
	if len(outs) == 0 {
		return 0
	}
	results := make([]webpush.Result, len(outs))
	for i, o := range outs {
		if subject == "" {
			results[i] = webpush.Result{Outcome: webpush.Refused, Why: "no contact set on /push"}
			continue
		}
		results[i] = keys.Send(ctx, ps.client, o.dev.Sub(), subject, o.msg, now)
	}
	sent := 0
	err = ps.store.Update(func(st *push.State) error {
		for i, o := range outs {
			st.Record(o.job, results[i], ps.now())
			if results[i].Outcome == webpush.Sent {
				sent++
			} else {
				log.Printf("push: %s to %s: %s %s", o.job.EventKey, o.dev.Label, results[i].Outcome, results[i].Why)
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("push: %v", err)
	}
	return sent
}

// pushSubject is the VAPID contact: the owner's setting, else the first
// public name; "" when there is neither (Apple refuses a made-up one).
func pushSubject(set push.Settings, hosts []string) string {
	if set.Subject != "" {
		return set.Subject
	}
	for _, h := range hosts {
		if h != "" && !isLoopbackHost(h) {
			return "https://" + h
		}
	}
	return ""
}

// watchPush runs pushScan every pushEvery until done.
func (s *Server) watchPush(done <-chan struct{}) {
	t := time.NewTicker(pushEvery)
	defer t.Stop()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { <-done; cancel() }()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			s.pushScan(ctx)
		}
	}
}

// pushStartupLine is the log line at start: devices only, no path.
func (s *Server) pushStartupLine() string {
	if s.push == nil {
		return ""
	}
	st, err := s.push.store.Read()
	if err != nil {
		return "push: settings unreadable"
	}
	ok, why := s.push.active(st)
	switch {
	case len(st.Devices) == 0:
		return ""
	case !ok:
		return fmt.Sprintf("push: %d devices, not sending: %s", len(st.Devices), why)
	}
	return fmt.Sprintf("push: %d devices", len(st.Devices))
}

// pushStatusLine is status.md's Push: line ("" when nothing subscribed).
func (s *Server) pushStatusLine() string {
	if s.push == nil {
		return ""
	}
	st, err := s.push.store.Read()
	if err != nil || (len(st.Devices) == 0 && len(st.Removed) == 0) {
		return ""
	}
	parts := []string{plural(len(st.Devices), "device", "devices")}
	if ok, why := s.push.active(st); !ok {
		parts = append(parts, "not sending: "+why)
	}
	if len(st.Settings.Muted) > 0 {
		parts = append(parts, "muted: "+strings.Join(st.Settings.Muted, ", "))
	}
	if q := st.Settings.Quiet; q.From != "" && q.From != q.To {
		parts = append(parts, "quiet "+q.From+"–"+q.To)
	}
	for _, r := range st.Removed {
		parts = append(parts, "removed: "+r.Label+" on "+r.At.Local().Format("2006-01-02")+" by the push service")
	}
	return "Push: " + strings.Join(parts, " · ")
}

// --- pages and routes ---

// PushPageData is /push.
type PushPageData struct {
	Title        string
	Header       WbHeader
	Owner        bool
	Active       bool
	Why          string
	Key          string
	Subject      string
	NeedsSubject bool
	Devices      []PushDeviceRow
	Removed      []push.Removed
	Projects     []PushProjectRow
	Quiet        push.Quiet
	Saved        string
	Error        string
}

// PushDeviceRow is a device as the page shows it: label, date, id (never
// the endpoint or keys).
type PushDeviceRow struct {
	ID, Label, Added, Refused string
}

// PushProjectRow is a project with its mute.
type PushProjectRow struct {
	Slug, Name string
	Muted      bool
}

func (s *Server) pushPage(r *http.Request) PushPageData {
	d := PushPageData{
		Title:  "Phone notifications",
		Header: WbHeader{Crumbs: []WbCrumb{{Label: "Map", URL: mapURL}, {Label: "Phone notifications", Current: true}}, Quiet: true, HideDone: true, WaitingURL: "/needs-you"},
		Owner:  s.fromOwner(r),
	}
	st, err := s.push.store.Read()
	if err != nil {
		d.Error = scrubHome(err.Error())
		return d
	}
	d.Active, d.Why = s.push.active(st)
	if k, err := s.push.Keys(); err == nil && d.Active {
		d.Key = k.PublicKey()
	}
	d.Subject = st.Settings.Subject
	d.NeedsSubject = pushSubject(st.Settings, s.allowedHosts) == ""
	for _, dev := range st.Devices {
		d.Devices = append(d.Devices, PushDeviceRow{ID: dev.ID, Label: dev.Label, Added: dev.Added.Local().Format("2006-01-02"), Refused: dev.Refused})
	}
	d.Removed = st.Removed
	for _, p := range s.projects {
		d.Projects = append(d.Projects, PushProjectRow{Slug: p.Slug, Name: p.Name, Muted: st.Settings.IsMuted(p.Slug)})
	}
	sort.Slice(d.Projects, func(i, j int) bool { return d.Projects[i].Name < d.Projects[j].Name })
	d.Quiet = st.Settings.Quiet
	switch q := r.URL.Query(); {
	case q.Get("saved") == "1":
		d.Saved = "Saved."
	case q.Get("rebound") == "1":
		d.Saved = "This viewer sends the notifications now."
	case q.Get("bad") != "":
		d.Error = map[string]string{"quiet": "Quiet hours need two times as HH:MM.", "subject": "The contact must be an email address or an https:// address."}[q.Get("bad")]
	}
	return d
}

// pushSubscribeRequest is what static/push.js POSTs.
type pushSubscribeRequest struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
	// OldEndpoint is set by the service worker's pushsubscriptionchange.
	OldEndpoint string `json:"old_endpoint,omitempty"`
}

// deviceLabel is a short "iPhone · Safari" from the User-Agent.
func deviceLabel(ua string) string {
	dev := "Browser"
	switch {
	case strings.Contains(ua, "iPhone"):
		dev = "iPhone"
	case strings.Contains(ua, "iPad"):
		dev = "iPad"
	case strings.Contains(ua, "Android"):
		dev = "Android"
	case strings.Contains(ua, "Macintosh"):
		dev = "Mac"
	case strings.Contains(ua, "Windows"):
		dev = "Windows"
	case strings.Contains(ua, "Linux"):
		dev = "Linux"
	}
	br := ""
	switch {
	case strings.Contains(ua, "Edg/"):
		br = "Edge"
	case strings.Contains(ua, "Firefox/"):
		br = "Firefox"
	case strings.Contains(ua, "Chrome/"):
		br = "Chrome"
	case strings.Contains(ua, "Safari/"):
		br = "Safari"
	}
	if br == "" {
		return dev
	}
	return dev + " · " + br
}

// handlePush serves /push and its routes. The POSTs are owner-only.
func (s *Server) handlePush(w http.ResponseWriter, r *http.Request) {
	if s.push == nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case r.URL.Path == "/push" && r.Method == http.MethodGet:
		w.Header().Set("Cache-Control", "no-store")
		if err := s.tmpl.ExecuteTemplate(w, "push.html", s.pushPage(r)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	case r.URL.Path == "/push/key" && r.Method == http.MethodGet:
		st, err := s.push.store.Read()
		if err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": scrubHome(err.Error())})
			return
		}
		ok, why := s.push.active(st)
		resp := map[string]any{"ready": ok && pushSubject(st.Settings, s.allowedHosts) != ""}
		if !ok {
			resp["why"] = why
		} else if k, err := s.push.Keys(); err == nil {
			resp["key"] = k.PublicKey()
		}
		if pushSubject(st.Settings, s.allowedHosts) == "" {
			resp["why"] = "set a contact for the push services on /push first"
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSONStatus(w, http.StatusOK, resp)
		return
	case r.Method != http.MethodPost:
		http.NotFound(w, r)
		return
	}
	// Every POST under /push is an O route.
	if ownerOnlyGlobal(r.Method, r.URL.Path) && !s.fromOwner(r) {
		refuseNonOwner(w, r, "push")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/push/")
	now := s.push.now()
	switch rest {
	case "subscribe", "change":
		var req pushSubscribeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "bad subscription JSON"})
			return
		}
		sub := webpush.Subscription{Endpoint: req.Endpoint, P256dh: req.Keys.P256dh, Auth: req.Keys.Auth}
		if err := sub.Validate(); err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if !webpush.AllowedEndpoint(sub.Endpoint) {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "the endpoint is not a browser push service"})
			return
		}
		// What waits now, read before the lock: the new device is seeded
		// with it, so subscribing never floods.
		seen, scanned := s.pushSeen(now)
		dev := push.Device{Endpoint: sub.Endpoint, P256dh: sub.P256dh, Auth: sub.Auth, Origin: requestOrigin(r), Label: deviceLabel(r.UserAgent())}
		var refusal string
		known := true
		err := s.push.store.Update(func(st *push.State) error {
			if ok, why := s.push.active(st); !ok {
				refusal = why
				return errPushInactive
			}
			if st.Settings.Config == "" {
				st.Settings.Config = s.push.config
			}
			st.Observe(seen, scanned, now)
			if rest == "change" && req.OldEndpoint != "" {
				known = st.Change(req.OldEndpoint, dev, now)
				if known {
					return nil
				}
			}
			st.Subscribe(dev, now)
			return nil
		})
		if refusal != "" {
			writeJSONStatus(w, http.StatusConflict, map[string]string{"error": refusal})
			return
		}
		if err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": scrubHome(err.Error())})
			return
		}
		writeJSONStatus(w, http.StatusOK, map[string]any{"id": push.DeviceID(sub.Endpoint), "label": dev.Label})
	case "unsubscribe":
		var req pushSubscribeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Endpoint == "" {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "bad JSON"})
			return
		}
		s.pushMutate(w, func(st *push.State) error {
			st.Unsubscribe(push.DeviceID(req.Endpoint))
			return nil
		}, func() { writeJSONStatus(w, http.StatusOK, map[string]string{"status": "unsubscribed"}) })
	case "forget":
		id := r.FormValue("id")
		s.pushMutate(w, func(st *push.State) error {
			st.Unsubscribe(id)
			return nil
		}, func() { http.Redirect(w, r, "/push?saved=1", http.StatusSeeOther) })
	case "settings":
		q := push.Quiet{From: strings.TrimSpace(r.FormValue("quiet_from")), To: strings.TrimSpace(r.FormValue("quiet_to"))}
		if !push.ValidQuiet(q) {
			http.Redirect(w, r, "/push?bad=quiet", http.StatusSeeOther)
			return
		}
		subject := strings.TrimSpace(r.FormValue("subject"))
		if subject != "" && !validSubject(subject) {
			http.Redirect(w, r, "/push?bad=subject", http.StatusSeeOther)
			return
		}
		var muted []string
		for _, p := range s.projects {
			if r.FormValue("mute_"+p.Slug) != "" {
				muted = append(muted, p.Slug)
			}
		}
		s.pushMutate(w, func(st *push.State) error {
			st.Settings.Quiet = q
			st.Settings.Muted = muted
			if r.Form.Has("subject") {
				if subject != "" && !strings.Contains(subject, ":") {
					subject = "mailto:" + subject
				}
				st.Settings.Subject = subject
			}
			return nil
		}, func() { http.Redirect(w, r, "/push?saved=1", http.StatusSeeOther) })
	case "rebind":
		if !s.push.claimed || !s.push.canBind() {
			http.Error(w, "this viewer can't send notifications: it is a sandbox", http.StatusConflict)
			return
		}
		seen, scanned := s.pushSeen(now)
		err := s.push.store.Update(func(st *push.State) error {
			st.Settings.Config = s.push.config
			st.Observe(seen, scanned, now)
			for _, d := range st.Devices {
				st.Subscribe(d, now)
			}
			return nil
		})
		if err != nil {
			http.Error(w, scrubHome(err.Error()), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/push?rebound=1", http.StatusSeeOther)
	case "test":
		var req pushSubscribeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Endpoint == "" {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "bad JSON"})
			return
		}
		st, err := s.push.store.Read()
		if err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": scrubHome(err.Error())})
			return
		}
		if ok, why := s.push.active(st); !ok {
			writeJSONStatus(w, http.StatusConflict, map[string]string{"error": why})
			return
		}
		dev, ok := st.Device(push.DeviceID(req.Endpoint))
		if !ok {
			writeJSONStatus(w, http.StatusNotFound, map[string]string{"error": "this device is not subscribed"})
			return
		}
		subject := pushSubject(st.Settings, s.allowedHosts)
		if subject == "" {
			writeJSONStatus(w, http.StatusConflict, map[string]string{"error": "set a contact for the push services first"})
			return
		}
		keys, err := s.push.Keys()
		if err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": scrubHome(err.Error())})
			return
		}
		body, _ := json.Marshal(push.Payload{Title: "Test from " + r.Host, Kind: "test", URL: "/needs-you", Tag: "test"})
		res := keys.Send(r.Context(), s.push.client, dev.Sub(), subject, webpush.Message{Payload: body, Topic: "test"}, now)
		writeJSONStatus(w, http.StatusOK, map[string]string{"outcome": string(res.Outcome), "why": res.Why})
	default:
		http.NotFound(w, r)
	}
}

var errPushInactive = fmt.Errorf("push: this viewer doesn't send")

// pushMutate runs fn on the store when this viewer is the sender; 409 with
// the reason when it isn't.
func (s *Server) pushMutate(w http.ResponseWriter, fn func(*push.State) error, done func()) {
	var refusal string
	err := s.push.store.Update(func(st *push.State) error {
		if ok, why := s.push.active(st); !ok {
			refusal = why
			return errPushInactive
		}
		if st.Settings.Config == "" {
			st.Settings.Config = s.push.config
		}
		return fn(st)
	})
	switch {
	case refusal != "":
		http.Error(w, refusal, http.StatusConflict)
	case err != nil:
		http.Error(w, scrubHome(err.Error()), http.StatusInternalServerError)
	default:
		done()
	}
}

// validSubject is an email address or an https URL.
func validSubject(s string) bool {
	switch {
	case strings.HasPrefix(s, "https://"):
		return len(s) > len("https://") && !strings.ContainsAny(s, " \t")
	case strings.HasPrefix(s, "mailto:"):
		s = strings.TrimPrefix(s, "mailto:")
	}
	at := strings.Index(s, "@")
	return at > 0 && strings.Contains(s[at:], ".") && !strings.ContainsAny(s, " \t/:") && !strings.HasSuffix(s, "@localhost")
}

// requestOrigin is the scheme and host the device subscribed on.
func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// --- the app shell ---

// handleManifest serves /manifest.webmanifest.
func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	m := map[string]any{
		"name":             "Dark Factory",
		"short_name":       "Factory",
		"description":      "What waits on you across your factories",
		"id":               "/needs-you",
		"start_url":        "/needs-you",
		"scope":            "/",
		"display":          "standalone",
		"background_color": "#14171f",
		"theme_color":      "#1B1F2A",
		"icons": []map[string]string{
			{"src": "/static/icons/icon-192.png", "sizes": "192x192", "type": "image/png", "purpose": "any"},
			{"src": "/static/icons/icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any"},
			{"src": "/static/icons/icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "maskable"},
		},
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "no-cache")
	json.NewEncoder(w).Encode(m)
}

// handleServiceWorker serves /sw.js from the root, so its scope is the
// whole viewer.
func (s *Server) handleServiceWorker(w http.ResponseWriter, r *http.Request) {
	data, err := staticFS.ReadFile("static/sw.js")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(data)
}

// handleTouchIcon serves /apple-touch-icon.png (iOS asks for it at the root).
func (s *Server) handleTouchIcon(w http.ResponseWriter, r *http.Request) {
	data, err := staticFS.ReadFile("static/icons/apple-touch-icon.png")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "max-age=86400")
	w.Write(data)
}
