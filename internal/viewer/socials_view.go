package viewer

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kacperwerema/dark-factory/internal/connectors"
	"github.com/kacperwerema/dark-factory/internal/depttemplate"
	"github.com/kacperwerema/dark-factory/internal/outputs"
	"github.com/kacperwerema/dark-factory/internal/socials"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Connect socials: /p/<project>/socials/<dept>, the owner's page for a
// department's social connector (epic
// factory/open-source-partners-integrate-outside-tools-openmontage-openseo-postiz-instead-of-building-them-in-house#D-7).
// Step 1 writes the `social` connector into projects.yaml (diff first) and
// the tool's key into Keychain; step 2 lists the networks the tool's
// adapter knows, takes each one's developer-app values into Keychain,
// starts the network's own sign-in, reads back the states and disconnects.
// Every call to the tool runs inside a connectors.Hold lease, so the tool
// starts for the action and is stopped after (epic D-5); rendering the page
// never starts it. Every route, GET included, is the owner's
// (ownerOnlyRoute); no value typed here is written anywhere but Keychain,
// or sent back in an answer.

// socialConnector is the slot's connector name (docs/departments.md
// "Swapping a partner": the channel prompts name the slot, never the tool).
const socialConnector = "social"

// socialTemplate is the department template whose examples hold it.
const socialTemplate = "marketing"

// connectHold is how long the tool stays up for a network's sign-in at
// most; a check that sees the new channel, or Done, stops it sooner.
var connectHold = 15 * time.Minute

// socialsHTTP is the client the page's tool calls use; tests replace it.
var socialsHTTP = socials.HTTPClient

// connectSession is the tool held up for one sign-in.
type connectSession struct {
	lease   *connectors.Lease
	network string
	until   time.Time
	before  map[string]bool
	timer   *time.Timer
	// expire ends this hold, and only this one, as its time being up:
	// the timer calls it, and so can a test that has read the page.
	expire func()
}

var connectSessions = struct {
	sync.Mutex
	m map[string]*connectSession
}{m: map[string]*connectSession{}}

// endConnect releases the sign-in hold on addr, if any; why goes to the log.
func endConnect(key, why string) {
	endConnectSession(key, nil, why)
}

// endConnectSession releases the hold on key when it is only (nil: any).
// A timer that fires as a new connect replaces its hold ends nothing.
func endConnectSession(key string, only *connectSession, why string) {
	connectSessions.Lock()
	cs := connectSessions.m[key]
	if cs == nil || (only != nil && cs != only) {
		connectSessions.Unlock()
		return
	}
	delete(connectSessions.m, key)
	connectSessions.Unlock()
	cs.timer.Stop()
	if err := cs.lease.Release(); err != nil {
		log.Printf("socials: %s: %v", key, err)
	}
	log.Printf("socials: %s: sign-in hold for %s ended (%s)", key, cs.network, why)
}

func connectKey(p *tracker.Project, dept string) string {
	return p.Slug + "/" + tracker.DepartmentKey(dept) + "/" + socialConnector
}

// SocialsData is the page.
type SocialsData struct {
	Header           WbHeader
	Title, Dept      string
	DeptKey, BaseURL string
	// Tool is the adapter's name ("Postiz"); empty with no connector.
	Tool string
	// Setup is step 1; Networks step 2 (only once the connector exists).
	Setup    SocialsSetup
	Networks []SocialsNetwork
	// More are networks only the tool's own UI connects.
	More []SocialsNetwork
	// Checked is when the last check ran ("" never), CheckErr why it failed.
	Checked, CheckErr string
	ExpiryKnown       bool
	// Holding: the tool is up for a sign-in until HoldUntil.
	Holding            bool
	HoldNet, HoldUntil string
	UIURL              string
	NoKeychain         bool
	Flash, Error       string
	// Unsupported: the department's social connector has no adapter.
	Unsupported string
}

// SocialsSetup is step 1.
type SocialsSetup struct {
	// Exists: the department has its social connector.
	Exists       bool
	URL, Compose string
	KeyRef       string
	KeySet       bool
	// Diff is the preview's projects.yaml change, Sum its name.
	Diff []outputs.DiffLine
	Sum  string
}

// SocialsNetwork is one row of step 2.
type SocialsNetwork struct {
	socials.Network
	Redirect string
	// Vars with set / missing; VarsSet when every one is set.
	VarSet  []SocialsVar
	VarsSet bool
	// Base is the page's URL, for the forms' actions.
	Base string
	// Channels are its connected accounts from the last check.
	Channels []SocialsChannel
	// State is the row's chip: connected, expired, disabled, needs a step,
	// not connected, unknown (never checked).
	State, Tone string
	// Diff is what saving its values adds to projects.yaml (first time).
	Diff []outputs.DiffLine
	Sum  string
}

// SocialsChannel is one connected account with the page's URL.
type SocialsChannel struct {
	socials.Channel
	Base string
}

// SocialsVar is one developer-app value: set or missing, never the value.
type SocialsVar struct {
	socials.Var
	Ref string
	Set bool
}

// socialDept is the department with key and its social connector (nil when
// it has none yet); ok is false for no such department.
func socialDept(p *tracker.Project, key string) (*tracker.Department, *tracker.Connector, bool) {
	d := p.DepartmentByKey(key)
	if d == nil {
		return nil, nil, false
	}
	for i := range d.Connectors {
		if d.Connectors[i].Name == socialConnector {
			return d, &d.Connectors[i], true
		}
	}
	return d, nil, true
}

// takesSocials: the department has a social connector, or its workflow has
// the social type (Marketing from the template).
func takesSocials(p *tracker.Project, d *tracker.Department) bool {
	for _, c := range d.Connectors {
		if c.Name == socialConnector {
			return true
		}
	}
	if p.DepartmentWorkflowPath(d.Name) == "" {
		return false
	}
	wf, err := p.WorkflowFor(d.Name)
	return err == nil && slices.Contains(wf.TypeNames(), "social")
}

func (s *Server) buildSocials(p *tracker.Project, prefix string, d *tracker.Department, c *tracker.Connector, now time.Time) SocialsData {
	color := s.projectColor(p)
	lot, rows := s.buildLot(p, color, now)
	key := tracker.DepartmentKey(d.Name)
	data := SocialsData{Title: p.Name + " · Connect socials", Dept: d.Name, DeptKey: key, BaseURL: prefix + "/socials/" + key}
	data.Header = WbHeader{Crumbs: wbCrumbs(1, p.Name, prefix+"/company"), Working: lot.Working, Idle: lot.Idle, AgentWait: lot.AgentWait, OwnerWait: lot.You, DoneWeek: lot.DoneWeek, Waiting: len(rows), WaitingURL: prefix + "/needs-you"}
	data.Header.Crumbs[1].Color, data.Header.Crumbs[1].Current, data.Header.Crumbs[1].URL = color, false, prefix+"/company#dept-"+key
	data.Header.Crumbs[2] = WbCrumb{Label: d.Name + " · Connect socials", Current: true}
	data.Header.YOLO = headerYOLO(lot.YOLO, prefix)
	data.NoKeychain = !keychainHere()
	data.Setup.URL = "http://127.0.0.1:4007"
	if c == nil {
		return data
	}
	a := socials.For(c)
	if a == nil {
		data.Unsupported = "its per_run: tool: names no tool this page knows"
		data.Setup.Exists = true
		return data
	}
	base := a.Base(c)
	data.Tool, data.UIURL = a.Name(), a.UI(base)
	data.Setup = SocialsSetup{Exists: true, URL: base}
	if ref, ok := a.KeyRef(c); ok {
		data.Setup.KeyRef, data.Setup.KeySet = ref.String(), tracker.SecretPresent(ref)
	}
	last, haveLast := socials.Latest(p)[tracker.ConnectorAddress(d.Name, c.Name)]
	if haveLast {
		data.Checked, data.CheckErr, data.ExpiryKnown = shortDuration(now.Sub(last.Date))+" ago", last.Error, last.ExpiryKnown
	}
	connectSessions.Lock()
	if cs := connectSessions.m[connectKey(p, d.Name)]; cs != nil {
		data.Holding, data.HoldNet, data.HoldUntil = true, cs.network, cs.until.Local().Format("15:04")
	}
	connectSessions.Unlock()
	for _, n := range a.Networks() {
		row := SocialsNetwork{Network: n, Redirect: a.RedirectURI(base, n.ID), VarsSet: true, Base: data.BaseURL}
		refs := map[string]string{}
		for _, v := range n.Vars {
			ref := a.EnvRef(v.Name)
			set := tracker.SecretPresent(ref)
			row.VarSet = append(row.VarSet, SocialsVar{Var: v, Ref: ref.String(), Set: set})
			row.VarsSet = row.VarsSet && set
			refs[v.Name] = ref.String()
		}
		if len(refs) > 0 && p.ConfigPath != "" {
			if pl, err := depttemplate.PlanSecrets(p.ConfigPath, p.Slug, d.Name, c.Name, refs); err == nil && pl != nil {
				row.Diff, row.Sum = diffAround(pl.Before, pl.After, 2), pl.Sum()
			}
		}
		row.State, row.Tone = "not connected", "idle"
		if !haveLast || last.Error != "" {
			row.State = "unknown"
		}
		var chs []socials.Channel
		for _, ch := range last.Channels {
			if ch.Provider == n.ID {
				chs = append(chs, ch)
				row.Channels = append(row.Channels, SocialsChannel{Channel: ch, Base: data.BaseURL})
			}
		}
		row.State, row.Tone = networkState(row.State, row.Tone, chs)
		if n.Connect == socials.ConnectElsewhere {
			data.More = append(data.More, row)
			continue
		}
		data.Networks = append(data.Networks, row)
	}
	return data
}

// networkState is a row's chip from its channels: the worst state wins.
func networkState(state, tone string, chs []socials.Channel) (string, string) {
	if len(chs) == 0 {
		return state, tone
	}
	rank := map[string]int{socials.StateConnected: 0, socials.StateDisabled: 1, socials.StateStep: 2, socials.StateExpired: 3}
	worst := socials.StateConnected
	for _, ch := range chs {
		if rank[ch.State] > rank[worst] {
			worst = ch.State
		}
	}
	switch worst {
	case socials.StateExpired:
		return "expired", "red"
	case socials.StateStep:
		return "needs a step in the tool", "you"
	case socials.StateDisabled:
		return "disabled", "idle"
	}
	if len(chs) > 1 {
		return fmt.Sprintf("connected (%d)", len(chs)), "green"
	}
	return "connected", "green"
}

// diffAround is the changed lines of before → after with n lines of
// context, "…" rows between far-apart changes.
func diffAround(before, after []byte, n int) []outputs.DiffLine {
	all := outputs.LineDiff(string(before), string(after))
	keep := make([]bool, len(all))
	for i, l := range all {
		if l.Op != ' ' {
			for j := max(0, i-n); j <= min(len(all)-1, i+n); j++ {
				keep[j] = true
			}
		}
	}
	var out []outputs.DiffLine
	gap := false
	for i, l := range all {
		if !keep[i] {
			gap = true
			continue
		}
		if gap && len(out) > 0 {
			out = append(out, outputs.DiffLine{Op: '~', Text: "…"})
		}
		gap = false
		out = append(out, l)
	}
	return out
}

// keychainHere: this machine has a Keychain to write to (macOS).
var keychainHere = func() bool { return runtime.GOOS == "darwin" }

func (s *Server) renderSocials(w http.ResponseWriter, code int, d SocialsData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if err := s.tmpl.ExecuteTemplate(w, "socials.html", d); err != nil {
		http.Error(w, "page error", http.StatusInternalServerError)
	}
}

// socialsFlash is the page's line after an action: fixed words per code,
// so nothing from the URL is shown as typed.
func socialsFlash(q url.Values) string {
	net := q.Get("net")
	switch q.Get("ok") {
	case "setup":
		return "✓ Connector saved to projects.yaml and the key to Keychain. Postiz answered the key."
	case "setup-unchecked":
		return "✓ Connector saved to projects.yaml and the key to Keychain. Press Check now to try the key."
	case "key":
		return "✓ Key saved to Keychain."
	case "fields":
		return "✓ " + net + " values saved to Keychain; the tool reads them at its next start."
	case "connected":
		return "✓ " + net + " connected."
	case "checked":
		return "✓ Checked; the tool is stopped again."
	case "done":
		return "✓ Sign-in finished; the tool is stopped."
	case "disconnected":
		return "✓ Disconnected; the tool is stopped again."
	}
	return ""
}

var netIDRe = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

func (s *Server) handleSocials(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix, rest string) {
	key, action, _ := strings.Cut(strings.TrimPrefix(rest, "socials/"), "/")
	d, c, ok := socialDept(proj, key)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodGet {
		if action != "" {
			http.NotFound(w, r)
			return
		}
		data := s.buildSocials(proj, prefix, d, c, time.Now())
		q := r.URL.Query()
		if n := q.Get("net"); n != "" && !netIDRe.MatchString(n) {
			q.Del("net")
		}
		data.Flash = socialsFlash(q)
		s.renderSocials(w, http.StatusOK, data)
		return
	}
	if !s.fromOwner(r) {
		http.Error(w, "only the owner connects socials, from this page in the browser signed in at /owner", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	var code int
	var err error
	var ok2 string
	switch action {
	case "setup":
		if r.PostForm.Get("op") != "save" {
			if code, err = s.socialsPreview(w, proj, prefix, d, c, r); err == nil {
				return // the preview was rendered
			}
			break
		}
		code, ok2, err = s.socialsSetup(r, proj, d, c)
	case "key":
		code, ok2, err = socialsKey(r, c)
	case "fields":
		code, ok2, err = s.socialsFields(r, proj, d, c)
	case "connect":
		var to string
		to, code, err = socialsConnect(r, proj, d, c)
		if err == nil {
			http.Redirect(w, r, to, http.StatusSeeOther)
			return
		}
	case "connect-done":
		endConnect(connectKey(proj, d.Name), "the owner pressed Done")
		ok2 = "done"
	case "check":
		code, ok2, err = socialsCheck(r.Context(), proj, d, c)
	case "disconnect":
		code, ok2, err = socialsDisconnect(r, proj, d, c)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		if code == 0 {
			code = http.StatusBadRequest
		}
		fresh := s.findProject(proj.Slug)
		if fresh == nil {
			fresh = proj
		}
		d2, c2, _ := socialDept(fresh, key)
		if d2 == nil {
			d2, c2 = d, c
		}
		data := s.buildSocials(fresh, prefix, d2, c2, time.Now())
		data.Error = err.Error()
		s.renderSocials(w, code, data)
		return
	}
	v := url.Values{"ok": {ok2}}
	if n := r.PostForm.Get("network"); n != "" && netIDRe.MatchString(n) {
		v.Set("net", n)
	}
	http.Redirect(w, r, prefix+"/socials/"+key+"?"+v.Encode(), http.StatusSeeOther)
}

var (
	composeRe = regexp.MustCompile(`^[A-Za-z0-9._/~-]+\.ya?ml$`)
	baseRe    = regexp.MustCompile(`^https?://[A-Za-z0-9.-]+(:[0-9]{1,5})?$`)
)

// socialEntry is the template's social connector with the owner's address
// and compose file, started with up -d and the override, so a changed
// developer-app value recreates the container at its next start.
func socialEntry(base, compose, override string) (string, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if !baseRe.MatchString(base) {
		return "", errors.New("the address is scheme://host[:port], nothing after it (http://127.0.0.1:4007)")
	}
	if err := tracker.CheckConnectorURL(base + "/api/mcp"); err != nil {
		return "", err
	}
	if !composeRe.MatchString(compose) {
		return "", errors.New("the compose file is a path to a .yaml or .yml file, letters, digits and . _ / ~ - only")
	}
	if !composeRe.MatchString(override) {
		return "", fmt.Errorf("the state dir's path %s holds a character a shell line can't take bare", override)
	}
	item, err := depttemplate.ExampleItem(socialTemplate, socialConnector)
	if err != nil {
		return "", err
	}
	for _, rep := range []struct{ old, new string }{
		{"http://127.0.0.1:4007", base},
		{"-f ~/postiz/compose.yaml start'", "-f " + compose + " -f " + override + " up -d'"},
		{"~/postiz/compose.yaml", compose},
	} {
		if !strings.Contains(item, rep.old) {
			return "", fmt.Errorf("the %s template's %s example has no %q any more", socialTemplate, socialConnector, rep.old)
		}
		item = strings.ReplaceAll(item, rep.old, rep.new)
	}
	return item, nil
}

// socialPlan is step 1's projects.yaml change for the form's address and
// compose file.
func socialPlan(r *http.Request, proj *tracker.Project, d *tracker.Department, c *tracker.Connector) (*depttemplate.Planned, int, error) {
	if c != nil {
		return nil, http.StatusConflict, errors.New("the department has its social connector already: replace the key below, or edit projects.yaml by hand")
	}
	if proj.ConfigPath == "" {
		return nil, http.StatusConflict, errors.New("this viewer serves no projects.yaml (-config): add the connector by hand")
	}
	base, compose := strings.TrimSpace(r.PostForm.Get("url")), strings.TrimSpace(r.PostForm.Get("compose"))
	item, err := socialEntry(base, compose, socials.OverridePath(socials.PostizTool))
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	pl, err := depttemplate.PlanConnector(proj.ConfigPath, proj.Slug, d.Name, socialConnector, item)
	if err != nil {
		return nil, http.StatusConflict, err
	}
	return pl, 0, nil
}

// socialsPreview renders step 1 with its diff and the key field.
func (s *Server) socialsPreview(w http.ResponseWriter, proj *tracker.Project, prefix string, d *tracker.Department, c *tracker.Connector, r *http.Request) (int, error) {
	pl, code, err := socialPlan(r, proj, d, c)
	if err != nil {
		return code, err
	}
	data := s.buildSocials(proj, prefix, d, nil, time.Now())
	data.Setup.URL, data.Setup.Compose = strings.TrimRight(strings.TrimSpace(r.PostForm.Get("url")), "/"), strings.TrimSpace(r.PostForm.Get("compose"))
	data.Setup.Diff, data.Setup.Sum = diffAround(pl.Before, pl.After, 2), pl.Sum()
	s.renderSocials(w, http.StatusOK, data)
	return 0, nil
}

// socialsSetup is step 1's save: Keychain, then projects.yaml (the diff
// the owner saw), then the key tried once.
func (s *Server) socialsSetup(r *http.Request, proj *tracker.Project, d *tracker.Department, c *tracker.Connector) (int, string, error) {
	pl, code, err := socialPlan(r, proj, d, c)
	if err != nil {
		return code, "", err
	}
	if r.PostForm.Get("sum") != pl.Sum() {
		return http.StatusConflict, "", depttemplate.ErrChanged
	}
	keyVal := r.PostForm.Get("key")
	if err := tracker.CheckSecretValue(keyVal); err != nil {
		return http.StatusBadRequest, "", fmt.Errorf("the key: %w", err)
	}
	ref := tracker.SecretRef{Scheme: "keychain", Name: socials.PostizKeyService}
	if err := tracker.SecretWrite(ref, keyVal); err != nil {
		return http.StatusInternalServerError, "", err
	}
	if err := socials.WriteOverride(socials.For(&tracker.Connector{PerRun: &tracker.PerRun{Tool: socials.PostizTool}}), socials.OverridePath(socials.PostizTool), nil); err != nil {
		return http.StatusInternalServerError, "", fmt.Errorf("the compose override: %w", err)
	}
	if err := pl.Write(); err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, depttemplate.ErrChanged) {
			code = http.StatusConflict
		}
		return code, "", fmt.Errorf("the key is in Keychain (%s), but projects.yaml: %w", ref, err)
	}
	if err := s.reloadProject(proj.Slug); err != nil {
		return http.StatusInternalServerError, "", fmt.Errorf("saved, but reading projects.yaml back failed: %v; restart the viewer", err)
	}
	forgetConnectors(proj)
	fresh := s.findProject(proj.Slug)
	_, nc, _ := socialDept(fresh, d.Name)
	log.Printf("socials: %s/%s: connector written, key saved to %s", proj.Slug, tracker.DepartmentKey(d.Name), ref)
	if nc == nil {
		return 0, "setup-unchecked", nil
	}
	if code, _, err := socialsCheck(r.Context(), fresh, d, nc); err != nil {
		if errors.Is(err, socials.ErrKey) {
			return code, "", errors.New("saved, but Postiz refused the key (401): replace it below")
		}
		log.Printf("socials: %s/%s: the first check: %v", proj.Slug, tracker.DepartmentKey(d.Name), err)
		return 0, "setup-unchecked", nil
	}
	return 0, "setup", nil
}

// socialsKey replaces the tool's key in Keychain only.
func socialsKey(r *http.Request, c *tracker.Connector) (int, string, error) {
	a := socials.For(c)
	if a == nil {
		return http.StatusConflict, "", errors.New("no social connector this page knows")
	}
	ref, ok := a.KeyRef(c)
	if !ok || ref.Scheme != "keychain" {
		return http.StatusConflict, "", errors.New("the connector's key is not a keychain: reference: change it where it is kept")
	}
	if err := tracker.CheckSecretValue(r.PostForm.Get("key")); err != nil {
		return http.StatusBadRequest, "", fmt.Errorf("the key: %w", err)
	}
	if err := tracker.SecretWrite(ref, r.PostForm.Get("key")); err != nil {
		return http.StatusInternalServerError, "", err
	}
	return 0, "key", nil
}

// socialsFields saves one network's developer-app values: Keychain first,
// then the secrets: lines (the diff the row showed), then the override.
func (s *Server) socialsFields(r *http.Request, proj *tracker.Project, d *tracker.Department, c *tracker.Connector) (int, string, error) {
	a := socials.For(c)
	if a == nil {
		return http.StatusConflict, "", errors.New("set up the connector first")
	}
	n := socials.Find(a, r.PostForm.Get("network"))
	if n == nil || len(n.Vars) == 0 {
		return http.StatusBadRequest, "", errors.New("no such network with values to save")
	}
	refs := map[string]string{}
	vals := map[string]string{}
	for _, v := range n.Vars {
		val := r.PostForm.Get(v.Name)
		ref := a.EnvRef(v.Name)
		refs[v.Name] = ref.String()
		if val == "" {
			if tracker.SecretPresent(ref) {
				continue // kept as it is
			}
			return http.StatusBadRequest, "", fmt.Errorf("%s: %s is empty", n.Name, v.Label)
		}
		if err := tracker.CheckSecretValue(val); err != nil {
			return http.StatusBadRequest, "", fmt.Errorf("%s: %s: %w", n.Name, v.Label, err)
		}
		vals[v.Name] = val
	}
	pl, err := depttemplate.PlanSecrets(proj.ConfigPath, proj.Slug, d.Name, c.Name, refs)
	if err != nil {
		return http.StatusConflict, "", err
	}
	if pl != nil && r.PostForm.Get("sum") != pl.Sum() {
		return http.StatusConflict, "", depttemplate.ErrChanged
	}
	names := make([]string, 0, len(vals))
	for v := range vals {
		names = append(names, v)
	}
	sort.Strings(names)
	for _, v := range names {
		if err := tracker.SecretWrite(a.EnvRef(v), vals[v]); err != nil {
			return http.StatusInternalServerError, "", err
		}
	}
	if pl != nil {
		if err := pl.Write(); err != nil {
			return http.StatusConflict, "", fmt.Errorf("the values are in Keychain, but projects.yaml: %w", err)
		}
		if err := s.reloadProject(proj.Slug); err != nil {
			return http.StatusInternalServerError, "", fmt.Errorf("saved, but reading projects.yaml back failed: %v; restart the viewer", err)
		}
		forgetConnectors(proj)
	}
	// The override passes every value the connector keeps for the tool.
	vars := map[string]bool{}
	for v := range refs {
		vars[v] = true
	}
	for v, ref := range c.Secrets {
		if pr, err := tracker.ParseSecretRef(ref); err == nil && pr == a.EnvRef(v) {
			vars[v] = true
		}
	}
	list := make([]string, 0, len(vars))
	for v := range vars {
		list = append(list, v)
	}
	if err := socials.WriteOverride(a, socials.OverridePath(c.PerRun.Tool), list); err != nil {
		return http.StatusInternalServerError, "", fmt.Errorf("the compose override: %w", err)
	}
	log.Printf("socials: %s/%s: %s values saved to Keychain (%s)", proj.Slug, tracker.DepartmentKey(d.Name), n.ID, strings.Join(names, ", "))
	return 0, "fields", nil
}

// toolClient holds the tool up and makes its client with the key read now.
// The caller releases the lease.
func toolClient(ctx context.Context, proj *tracker.Project, d *tracker.Department, c *tracker.Connector) (socials.Adapter, socials.Client, *connectors.Lease, int, error) {
	a := socials.For(c)
	if a == nil || c.PerRun == nil {
		return nil, nil, nil, http.StatusConflict, errors.New("set up the connector first")
	}
	ref, ok := a.KeyRef(c)
	if !ok {
		return nil, nil, nil, http.StatusConflict, errors.New("the connector has no Authorization header naming its key")
	}
	key, ok := tracker.SecretValue(ref)
	if !ok {
		return nil, nil, nil, http.StatusConflict, fmt.Errorf("the key (%s) is not set on this machine: save it in step 1", ref)
	}
	lease, err := connectors.Hold(ctx, proj, d.Name, c, time.Now)
	if err != nil {
		return nil, nil, nil, http.StatusServiceUnavailable, err
	}
	return a, a.NewClient(a.Base(c), key, socialsHTTP()), lease, 0, nil
}

func release(l *connectors.Lease, what string) {
	if err := l.Release(); err != nil {
		log.Printf("socials: %s: %v", what, err)
	}
}

// socialsCheck reads the channel states with the tool held for the call.
func socialsCheck(ctx context.Context, proj *tracker.Project, d *tracker.Department, c *tracker.Connector) (int, string, error) {
	_, cl, lease, code, err := toolClient(ctx, proj, d, c)
	if err != nil {
		return code, "", err
	}
	code, err = recordStatus(ctx, proj, d, c, cl)
	release(lease, "check")
	if err != nil {
		return code, "", err
	}
	return 0, "checked", nil
}

// recordStatus reads the states through cl (the caller holds the tool),
// records them, and ends a sign-in hold once its new channel shows.
func recordStatus(ctx context.Context, proj *tracker.Project, d *tracker.Department, c *tracker.Connector, cl socials.Client) (int, error) {
	st, err := cl.Status(ctx)
	rec := socials.Check{Date: time.Now().UTC(), Connector: tracker.ConnectorAddress(d.Name, c.Name), Channels: st.Channels, ExpiryKnown: st.ExpiryKnown}
	if err != nil {
		rec.Error = err.Error()
		_ = socials.Append(proj, rec)
		if errors.Is(err, socials.ErrKey) {
			return http.StatusBadGateway, socials.ErrKey
		}
		return http.StatusBadGateway, err
	}
	if err := socials.Append(proj, rec); err != nil {
		return http.StatusInternalServerError, err
	}
	ck := connectKey(proj, d.Name)
	connectSessions.Lock()
	cs := connectSessions.m[ck]
	connectSessions.Unlock()
	if cs != nil {
		for _, ch := range st.Channels {
			if !cs.before[ch.ID] {
				// The hold is a second lease: ending it leaves the
				// caller's lease to stop the tool.
				endConnect(ck, "a check saw the new channel")
				break
			}
		}
	}
	return 0, nil
}

// socialsConnect: an OAuth network's sign-in URL with the tool held up for
// the sign-in (connectHold); an app-password network connected now.
func socialsConnect(r *http.Request, proj *tracker.Project, d *tracker.Department, c *tracker.Connector) (string, int, error) {
	a := socials.For(c)
	if a == nil {
		return "", http.StatusConflict, errors.New("set up the connector first")
	}
	n := socials.Find(a, r.PostForm.Get("network"))
	if n == nil || n.Connect == socials.ConnectElsewhere {
		return "", http.StatusBadRequest, errors.New("that network connects only in the tool's own UI")
	}
	for _, v := range n.Vars {
		if !tracker.SecretPresent(a.EnvRef(v.Name)) {
			return "", http.StatusConflict, fmt.Errorf("%s needs its %s first", n.Name, v.Label)
		}
	}
	ctx := r.Context()
	ck := connectKey(proj, d.Name)
	endConnect(ck, "a new connect")
	_, cl, lease, code, err := toolClient(ctx, proj, d, c)
	if err != nil {
		return "", code, err
	}
	prefix := strings.TrimSuffix(r.URL.Path, "/connect")
	if n.Connect == socials.ConnectPassword {
		fields := map[string]string{}
		for _, f := range n.Fields {
			v := strings.TrimSpace(r.PostForm.Get("f_" + f.Key))
			if v == "" {
				v = f.Default
			}
			if err := tracker.CheckSecretValue(v); err != nil {
				release(lease, "connect")
				return "", http.StatusBadRequest, fmt.Errorf("%s: %s: %w", n.Name, f.Label, err)
			}
			fields[f.Key] = v
		}
		if err := cl.ConnectFields(ctx, n.ID, fields); err != nil {
			release(lease, "connect")
			return "", http.StatusBadGateway, fmt.Errorf("%s: %w", n.Name, err)
		}
		code, err := recordStatus(ctx, proj, d, c, cl)
		release(lease, "connect")
		if err != nil {
			return "", code, err
		}
		return prefix + "?" + url.Values{"ok": {"connected"}, "net": {n.ID}}.Encode(), 0, nil
	}
	st, err := cl.Status(ctx)
	if err != nil {
		release(lease, "connect")
		return "", http.StatusBadGateway, err
	}
	to, err := cl.AuthURL(ctx, n.ID)
	if err != nil {
		release(lease, "connect")
		return "", http.StatusBadGateway, fmt.Errorf("%s: %w", n.Name, err)
	}
	cs := &connectSession{lease: lease, network: n.Name, until: time.Now().Add(connectHold), before: map[string]bool{}}
	for _, ch := range st.Channels {
		cs.before[ch.ID] = true
	}
	cs.expire = func() { endConnectSession(ck, cs, "time is up") }
	// Stored and timed under one lock: a timer firing at once finds it.
	connectSessions.Lock()
	connectSessions.m[ck] = cs
	cs.timer = time.AfterFunc(connectHold, cs.expire)
	connectSessions.Unlock()
	log.Printf("socials: %s: tool up for %s's sign-in until %s", ck, n.ID, cs.until.Format("15:04"))
	return to, 0, nil
}

// socialsDisconnect removes one channel from the tool, then checks again.
func socialsDisconnect(r *http.Request, proj *tracker.Project, d *tracker.Department, c *tracker.Connector) (int, string, error) {
	id := r.PostForm.Get("channel")
	if id == "" || len(id) > 100 || strings.ContainsAny(id, "/?#") {
		return http.StatusBadRequest, "", errors.New("no channel to disconnect")
	}
	last := socials.Latest(proj)[tracker.ConnectorAddress(d.Name, c.Name)]
	known := false
	for _, ch := range last.Channels {
		known = known || ch.ID == id
	}
	if !known {
		return http.StatusConflict, "", errors.New("that channel is not in the last check: Check now, then disconnect")
	}
	if r.PostForm.Get("confirm") != "yes" {
		return http.StatusBadRequest, "", errors.New("disconnect needs the confirm box ticked")
	}
	_, cl, lease, code, err := toolClient(r.Context(), proj, d, c)
	if err != nil {
		return code, "", err
	}
	if err := cl.Disconnect(r.Context(), id); err != nil {
		release(lease, "disconnect")
		return http.StatusBadGateway, "", err
	}
	code, err = recordStatus(r.Context(), proj, d, c, cl)
	release(lease, "disconnect")
	if err != nil {
		return code, "", err
	}
	return 0, "disconnected", nil
}

// socialsLink is a room's Connect socials link, "" when the department
// takes no socials.
func socialsLink(p *tracker.Project, prefix string, d *tracker.Department) string {
	if d == nil || !takesSocials(p, d) {
		return ""
	}
	return prefix + "/socials/" + tracker.DepartmentKey(d.Name)
}

// socialsExpired is "x, linkedin" for department dept's networks whose
// sign-in expired at the last check, "" for none.
func socialsExpired(p *tracker.Project, dept string) string {
	last, ok := socials.Latest(p)[tracker.ConnectorAddress(dept, socialConnector)]
	if !ok {
		return ""
	}
	return strings.Join(last.Expired(), ", ")
}
