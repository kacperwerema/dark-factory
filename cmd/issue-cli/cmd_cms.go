package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/gitfile"
	"github.com/kacperwerema/dark-factory/internal/jsonfile"
	"github.com/kacperwerema/dark-factory/internal/memguard"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var cmsCommand = &Command{
	Name:      "cms",
	ShortHelp: "Stand up the project's CMS (Ghost) on this machine in one command: cms up",
	LongHelp: `Stand up the project's site and CMS, Ghost (MIT, the cms slot's default:
issue-cli department partners --slot cms), on this machine, for Marketing's
cms connector to publish into. The owner runs it once; running it again is
safe.

  cms up      under mem-guard check: writes a compose for Ghost outside any
              repository ($XDG_DATA_HOME/ai-workflow-engine/cms/<project>/,
              default ~/.local/share/…), starts it, sets it up with no
              browser, and puts Ghost's owner password and its Admin API key
              straight into Keychain (ghost-owner, ghost-admin-key; account:
              the project), never printed, never in a file or on a command
              line. It turns Ghost's outbound link tagging off (else every
              link gets ?ref=). It writes no config: you turn the cms
              connector on in projects.yaml yourself.

The compose: ghost:6-alpine, restart: unless-stopped, the port on 127.0.0.1
only, url http://127.0.0.1:<port>, SQLite under the folder's content/, the
update check, Gravatar and RPC ping off, staff device verification off (it
emails a code, and there is no mail), and mail pointed at 127.0.0.1:1025
where nothing listens, so no mail can leave. A compose already there is kept
as it is (you may have edited it for go-live).

Run again: Ghost already set up is not set up again (the password is read
back from Keychain to sign in), an existing "Dark Factory publish"
integration is reused, the admin key is written only when Keychain lacks
it, and link tagging is turned off again.

Refused: for a worker (the owner runs it), off macOS (no Keychain), without
docker, when mem-guard check is blocked, when the folder sits inside a git
work tree, and when Ghost is set up but Keychain has no ghost-owner.

Flags:
  --port <n>        the port on 127.0.0.1 (default 2368, what the cms
                    connector's check reads)
  --email <addr>    the owner's sign-in email for /ghost (default the
                    project's git config user.email)
  --title "<t>"     the site's title (default the project's name)
  --dry-run         print the plan and the compose; changes nothing
  --json            the steps as JSON

Going live (a domain, TLS, MySQL) is the owner's: docs/stand-up-the-cms.md.

Examples:
  issue-cli cms up --dry-run
  issue-cli cms up
  issue-cli cms up --email owner@example.com --title "Orchard"`,
	Run:         runCms,
	Subcommands: []string{"up"},
}

func init() {
	registerCommand(cmsCommand)
}

// Ghost's names as the cms connector and its helper read them.
const (
	cmsDefaultPort      = 2368
	cmsImage            = "ghost:6-alpine"
	cmsIntegration      = "Dark Factory publish"
	cmsOwnerItem        = "ghost-owner"
	cmsAdminKeyItem     = "ghost-admin-key"
	cmsStateFile        = "ghost.json"
	cmsComposeFile      = "compose.yaml"
	cmsUsage            = "usage: issue-cli cms up [--port <n>] [--email <addr>] [--title \"<t>\"] [--dry-run]"
	cmsConnectorExample = "connectors.example.yaml"
)

// Test seams: how long to wait for Ghost, and how often to ask.
var (
	cmsReadyTimeout = 120 * time.Second
	cmsPollEvery    = time.Second
)

// cmsState is ghost.json beside the compose: no secret in it.
type cmsState struct {
	Project string `json:"project"`
	Email   string `json:"email"`
	Title   string `json:"title"`
	Port    int    `json:"port"`
}

// cmsStep is one line of what cms up did.
type cmsStep struct {
	Step  string `json:"step"`
	State string `json:"state"`
	Note  string `json:"note,omitempty"`
}

func runCms(ctx *Context, args []string) error {
	if len(args) == 0 || args[0] != "up" {
		return &usageError{msg: cmsUsage}
	}
	return runCmsUp(ctx, args[1:])
}

func runCmsUp(ctx *Context, args []string) error {
	fs := newFlagSet("cms up", ctx)
	port := fs.Int("port", cmsDefaultPort, "the port on 127.0.0.1")
	email := fs.String("email", "", "the owner's sign-in email (default: git config user.email)")
	title := fs.String("title", "", "the site's title (default: the project's name)")
	dry := fs.Bool("dry-run", false, "print the plan and the compose, change nothing")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return &usageError{msg: cmsUsage}
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if *port < 1 || *port > 65535 {
		return &usageError{msg: "--port wants 1-65535"}
	}
	proj := ctx.Project
	key := cmsProjectKey(proj)
	dir, err := cmsDir(key)
	if err != nil {
		return err
	}
	st := cmsState{Project: key, Email: strings.TrimSpace(*email), Title: strings.TrimSpace(*title), Port: *port}
	old, hadState := readCmsState(dir)
	if hadState {
		if set["port"] && old.Port != *port {
			return fmt.Errorf("the Ghost in %s runs on port %d: pass --port %d, or remove that folder to start over", dir, old.Port, old.Port)
		}
		st.Port = old.Port
		if st.Email == "" {
			st.Email = old.Email
		}
		if st.Title == "" {
			st.Title = old.Title
		}
	}
	if st.Title == "" {
		st.Title = valueOr(proj.Name, key)
	}
	if st.Email == "" {
		st.Email = gitUserEmail(projectRoot(proj))
	}
	compose := cmsCompose(st.Port)
	if *dry {
		return printCmsPlan(ctx, dir, key, st, compose)
	}

	// The owner runs it: it starts a container and writes Keychain.
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	if me := guardWho(ctx, issues, "cms up"); me.Role == tracker.RoleWorker {
		return fmt.Errorf("workers never stand up the CMS: it starts a container and writes the owner's Keychain, so the owner runs it. Ask on your issue trail:\n  %s\nThe plan: %s",
			tracker.InjectProject(proj, fmt.Sprintf("issue-cli comment %s \"question: …\"", valueOr(me.Slug, "<slug>"))),
			tracker.InjectProject(proj, "issue-cli cms up --dry-run"))
	}
	if tracker.KeychainOS != "darwin" {
		return errors.New("cms up keeps Ghost's password and key in Keychain, which only macOS has: nothing was written. docs/stand-up-the-cms.md says how to set Ghost up by hand elsewhere")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return errors.New("cms up runs Ghost in docker, and docker is not on PATH: install Docker Desktop (or colima with the compose plugin), then run it again. Nothing was written")
	}
	if !strings.Contains(st.Email, "@") {
		return &usageError{msg: "Ghost's owner account needs a sign-in email and git config user.email has none: pass --email <addr>"}
	}
	if found, blocked, msg := memguard.Check(); blocked {
		return fmt.Errorf("mem-guard check is blocked, so nothing was started or written: %s\nRun it again when memory is lower (mem-guard status)", msg)
	} else if !found {
		fmt.Fprintln(ctx.Stderr, "note: mem-guard is not on PATH, so memory was not checked (Ghost takes about 180 MB at idle)")
	}
	if repo := gitWorkTreeAbove(dir); repo != "" {
		return fmt.Errorf("%s is inside the git work tree at %s: the compose and Ghost's content stay outside every repository. Set XDG_DATA_HOME to a folder outside it", dir, repo)
	}

	w := ctx.Stdout
	if ctx.JSONOutput {
		w = ctx.Stderr
	}
	var steps []cmsStep
	step := func(name, state, note string) {
		steps = append(steps, cmsStep{name, state, note})
		line := fmt.Sprintf("✓ %s: %s", name, state)
		if note != "" {
			line += " (" + note + ")"
		}
		fmt.Fprintln(w, line)
	}

	// 1. The compose, outside any repository.
	if err := os.MkdirAll(filepath.Join(dir, "content"), 0o755); err != nil {
		return err
	}
	composePath := filepath.Join(dir, cmsComposeFile)
	switch b, err := os.ReadFile(composePath); {
	case err == nil && string(b) == compose:
		step("compose", "already there", composePath)
	case err == nil:
		step("compose", "kept", composePath+" differs from cms up's: yours stays as it is")
	case os.IsNotExist(err):
		if err := gitfile.WriteAtomic(composePath, []byte(compose), 0o644); err != nil {
			return err
		}
		step("compose", "written", composePath)
	default:
		return err
	}
	if err := writeCmsState(dir, st); err != nil {
		return err
	}

	// 2. Start Ghost and wait for it.
	up := exec.Command("docker", "compose", "-p", cmsComposeProject(key), "-f", composePath, "up", "--detach")
	up.Dir = dir
	up.Stdout, up.Stderr = w, w
	if err := up.Run(); err != nil {
		return fmt.Errorf("docker compose up failed (%v): see its output above. The compose is %s", err, composePath)
	}
	base := cmsBase(st.Port)
	g := newGhostSetup(base)
	if err := g.waitReady(cmsComposeProject(key)); err != nil {
		return err
	}
	step("ghost", "running", base)

	// 3. The owner account: the password goes to Keychain before Ghost has it.
	owner := tracker.SecretRef{Scheme: "keychain", Name: cmsOwnerItem, Account: key}
	adminRef := tracker.SecretRef{Scheme: "keychain", Name: cmsAdminKeyItem, Account: key}
	done, err := g.isSetUp()
	if err != nil {
		return err
	}
	var password string
	if done {
		pw, ok := tracker.SecretValue(owner)
		if !ok {
			return fmt.Errorf("Ghost at %s is set up already, but Keychain has no %s for it, so cms up can't sign in: sign in at %s/ghost yourself and make the integration (docs/stand-up-the-cms.md), or start over by removing %s", base, owner, base, filepath.Join(dir, "content"))
		}
		password = pw
		step("owner account", "already set up", "signing in with the password in Keychain")
	} else {
		pw, err := cmsPassword()
		if err != nil {
			return err
		}
		if err := tracker.SecretWrite(owner, pw); err != nil {
			return fmt.Errorf("the owner password could not go to Keychain, so Ghost was not set up: %v", err)
		}
		if err := g.setup(st, pw); err != nil {
			return err
		}
		password = pw
		step("owner account", "set up", st.Email+", password in Keychain "+owner.String())
	}
	g.secrets = append(g.secrets, password)

	// 4. A session, the integration's admin key, link tagging off.
	if err := g.signIn(st.Email, password); err != nil {
		return err
	}
	if tracker.SecretPresent(adminRef) {
		step("admin key", "already in Keychain", adminRef.String())
	} else {
		k, reused, err := g.adminKey()
		if err != nil {
			return err
		}
		g.secrets = append(g.secrets, k)
		if err := tracker.SecretWrite(adminRef, k); err != nil {
			return fmt.Errorf("the admin key could not go to Keychain: %v. Run cms up again", err)
		}
		state := "integration made"
		if reused {
			state = "integration reused"
		}
		step("admin key", state, "\""+cmsIntegration+"\", key in Keychain "+adminRef.String())
	}
	if err := g.linkTaggingOff(); err != nil {
		return err
	}
	step("link tagging", "off", "links go out as written, no ?ref=")

	next := cmsNext(proj, dir, key, st)
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, struct {
			Dir   string    `json:"dir"`
			URL   string    `json:"url"`
			Email string    `json:"email"`
			Steps []cmsStep `json:"steps"`
			Next  []string  `json:"next"`
		}{dir, base, st.Email, steps, next})
	}
	fmt.Fprintln(w, "\nNext:")
	for _, n := range next {
		fmt.Fprintf(w, "  %s\n", n)
	}
	return nil
}

// cmsNext is what the owner does after cms up: sign in, turn the connector
// on (cms up turns nothing on), see it set, stop, go live.
func cmsNext(proj *tracker.Project, dir, key string, st cmsState) []string {
	base := cmsBase(st.Port)
	config := valueOr(proj.ConfigPath, "projects.yaml")
	turnOn := fmt.Sprintf("Turn the cms connector on (cms up wrote no config): uncomment the cms entry in your Marketing department's connectors: in %s (its commented copy is in the department's %s)", config, cmsConnectorExample)
	if st.Port != cmsDefaultPort {
		turnOn += fmt.Sprintf("; this Ghost is on port %d, so its check: reads %s/ghost/api/admin/site/ and GHOST_URL=%s goes in the helper's environment", st.Port, base, base)
	}
	turnOn += fmt.Sprintf("; its key is keychain:%s (keychain:%s/%s names this project's when several projects have a Ghost)", cmsAdminKeyItem, cmsAdminKeyItem, key)
	return []string{
		fmt.Sprintf("Sign in at %s/ghost as %s; the password is in Keychain: security find-generic-password -s %s -a %s -w (run it yourself: it prints it)", base, st.Email, cmsOwnerItem, key),
		turnOn,
		"See it set: " + tracker.InjectProject(proj, "issue-cli connectors"),
		fmt.Sprintf("Stop Ghost: docker compose -p %s -f %s stop (start it again with cms up)", cmsComposeProject(key), filepath.Join(dir, cmsComposeFile)),
		"Go live on a domain (yours to do: it spends and publishes): docs/stand-up-the-cms.md",
	}
}

func printCmsPlan(ctx *Context, dir, key string, st cmsState, compose string) error {
	plan := []string{
		"mem-guard check (blocked: nothing is written or started)",
		"write " + filepath.Join(dir, cmsComposeFile) + " if it isn't there (an existing one is kept)",
		"docker compose -p " + cmsComposeProject(key) + " up --detach, then wait for " + cmsBase(st.Port) + "/ghost/api/admin/site/",
		"set Ghost up as " + valueOr(st.Email, "<--email>") + ", site \"" + st.Title + "\", with a random owner password written to Keychain " + cmsOwnerItem + " (account " + key + ") first",
		"sign in, make or reuse the \"" + cmsIntegration + "\" integration, its Admin API key to Keychain " + cmsAdminKeyItem + " (account " + key + ")",
		"turn outbound link tagging off",
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, map[string]any{"dry_run": true, "dir": dir, "url": cmsBase(st.Port), "plan": plan, "compose": compose})
	}
	w := ctx.Stdout
	fmt.Fprintln(w, "Dry run: cms up (nothing changed)")
	for i, p := range plan {
		fmt.Fprintf(w, "  %d. %s\n", i+1, p)
	}
	fmt.Fprintf(w, "\nThe compose (%s):\n%s", filepath.Join(dir, cmsComposeFile), indentLines(compose, "  "))
	fmt.Fprintln(w, "Nothing goes outside the machine: the port is on 127.0.0.1, the pings are off and mail goes nowhere. Apply: "+tracker.InjectProject(ctx.Project, "issue-cli cms up"))
	return nil
}

// cmsCompose is the compose cms up writes: Ghost on loopback, nothing
// leaving the machine.
func cmsCompose(port int) string {
	p := strconv.Itoa(port)
	return `# Ghost for this project's site, written by issue-cli cms up
# (docs/stand-up-the-cms.md). Loopback only; nothing leaves the machine:
# no update check, Gravatar or RPC ping, and mail goes to 127.0.0.1:1025,
# where nothing listens. SQLite is Ghost's development database: going live
# uses upstream's compose with MySQL 8 (the docs page, owner's steps).
services:
  ghost:
    image: ` + cmsImage + `
    restart: unless-stopped
    ports: ["127.0.0.1:` + p + `:2368"]
    environment:
      url: http://127.0.0.1:` + p + `
      database__client: sqlite3
      database__connection__filename: /var/lib/ghost/content/data/ghost.db
      privacy__useUpdateCheck: "false"
      privacy__useGravatar: "false"
      privacy__useRpcPing: "false"
      # Ghost emails a code to a new device at sign-in; with no mail that
      # would lock you out of /ghost. Turn it back on with real mail.
      security__staffDeviceVerification: "false"
      mail__transport: SMTP
      mail__options__host: 127.0.0.1
      mail__options__port: "1025"
    volumes: ["./content:/var/lib/ghost/content"]
`
}

func cmsBase(port int) string { return "http://127.0.0.1:" + strconv.Itoa(port) }

func cmsComposeProject(key string) string { return "ghost-" + key }

// cmsProjectKey names the project's folder, compose project and Keychain
// account: its slug, else its name slugified.
func cmsProjectKey(proj *tracker.Project) string {
	if k := tracker.DepartmentKey(proj.Slug); k != "" {
		return k
	}
	if k := tracker.DepartmentKey(proj.Name); k != "" {
		return k
	}
	return "project"
}

// cmsDir is $XDG_DATA_HOME/ai-workflow-engine/cms/<key>, else under
// ~/.local/share: outside every repository.
func cmsDir(key string) (string, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", errors.New("no home folder: set XDG_DATA_HOME to where Ghost's folder may go")
		}
		base = filepath.Join(home, ".local", "share")
	}
	abs, err := filepath.Abs(filepath.Join(base, "ai-workflow-engine", "cms", key))
	if err != nil {
		return "", err
	}
	return abs, nil
}

// gitWorkTreeAbove is the nearest folder at or above dir (or the nearest
// one that exists) holding .git, "" when none does.
func gitWorkTreeAbove(dir string) string {
	for d := dir; ; {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		up := filepath.Dir(d)
		if up == d {
			return ""
		}
		d = up
	}
}

func gitUserEmail(root string) string {
	out, err := exec.Command("git", "-C", root, "config", "user.email").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func readCmsState(dir string) (cmsState, bool) {
	b, err := os.ReadFile(filepath.Join(dir, cmsStateFile))
	if err != nil {
		return cmsState{}, false
	}
	var st cmsState
	if json.Unmarshal(b, &st) != nil || st.Port == 0 {
		return cmsState{}, false
	}
	return st, true
}

func writeCmsState(dir string, st cmsState) error {
	return jsonfile.Write(filepath.Join(dir, cmsStateFile), st, 0o644)
}

// cmsPassword is 24 random bytes, URL-safe: 32 characters.
func cmsPassword() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ghostSetup talks to Ghost's Admin API with a session, as its own admin
// screens do: an Origin header and a cookie jar held in memory only.
type ghostSetup struct {
	base   string
	client *http.Client
	// secrets are scrubbed out of every error this returns.
	secrets []string
}

func newGhostSetup(base string) *ghostSetup {
	jar, _ := cookiejar.New(nil)
	return &ghostSetup{base: base, client: &http.Client{Jar: jar, Timeout: 30 * time.Second}}
}

func (g *ghostSetup) url(path string) string { return g.base + "/ghost/api/admin" + path }

// call sends body (JSON, nil for none) and decodes the answer into out.
func (g *ghostSetup) call(method, path string, body, out any, want ...int) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, g.url(path), rd)
	if err != nil {
		return err
	}
	req.Header.Set("Origin", g.base)
	req.Header.Set("Accept-Version", "v6.0")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return g.scrub(fmt.Errorf("Ghost %s %s: %v", method, path, err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	ok := false
	for _, c := range want {
		ok = ok || resp.StatusCode == c
	}
	if !ok {
		var e struct {
			Errors []struct{ Message, Context string } `json:"errors"`
		}
		msg := resp.Status
		if json.Unmarshal(raw, &e) == nil && len(e.Errors) > 0 {
			msg += ": " + e.Errors[0].Message
			if e.Errors[0].Context != "" {
				msg += " (" + e.Errors[0].Context + ")"
			}
		}
		return g.scrub(fmt.Errorf("Ghost %s %s answered %s", method, path, msg))
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("Ghost %s %s: the answer is not the JSON expected", method, path)
		}
	}
	return nil
}

// scrub takes every secret out of err's text.
func (g *ghostSetup) scrub(err error) error {
	msg := err.Error()
	for _, s := range g.secrets {
		if s != "" {
			msg = strings.ReplaceAll(msg, s, "<secret>")
		}
	}
	return errors.New(msg)
}

// waitReady asks /site/ (no key needed) until it answers 200.
func (g *ghostSetup) waitReady(composeProject string) error {
	deadline := time.Now().Add(cmsReadyTimeout)
	var last error
	for {
		if last = g.call("GET", "/site/", nil, nil, http.StatusOK); last == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("Ghost did not answer at %s within %s (%v): docker compose -p %s logs ghost shows why; run cms up again once it runs", g.base, cmsReadyTimeout, last, composeProject)
		}
		time.Sleep(cmsPollEvery)
	}
}

func (g *ghostSetup) isSetUp() (bool, error) {
	var out struct {
		Setup []struct {
			Status bool `json:"status"`
		} `json:"setup"`
	}
	if err := g.call("GET", "/authentication/setup/", nil, &out, http.StatusOK); err != nil {
		return false, err
	}
	return len(out.Setup) > 0 && out.Setup[0].Status, nil
}

func (g *ghostSetup) setup(st cmsState, password string) error {
	g.secrets = append(g.secrets, password)
	body := map[string]any{"setup": []map[string]string{{
		"name": "Owner", "email": st.Email, "password": password, "blogTitle": st.Title,
	}}}
	return g.call("POST", "/authentication/setup/", body, nil, http.StatusOK, http.StatusCreated)
}

func (g *ghostSetup) signIn(email, password string) error {
	g.secrets = append(g.secrets, password)
	body := map[string]string{"username": email, "password": password}
	if err := g.call("POST", "/session/", body, nil, http.StatusOK, http.StatusCreated); err != nil {
		return fmt.Errorf("%v: if you changed the owner password in Ghost, put the new one in Keychain %s first", err, cmsOwnerItem)
	}
	return nil
}

type ghostIntegration struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	APIKeys []struct {
		ID     string `json:"id"`
		Type   string `json:"type"`
		Secret string `json:"secret"`
	} `json:"api_keys"`
}

// adminKey is the "<id>:<hex>" Admin API key of the Dark Factory
// integration, made when Ghost has none by that name.
func (g *ghostSetup) adminKey() (key string, reused bool, err error) {
	var list struct {
		Integrations []ghostIntegration `json:"integrations"`
	}
	if err := g.call("GET", "/integrations/?include=api_keys", nil, &list, http.StatusOK); err != nil {
		return "", false, err
	}
	for _, it := range list.Integrations {
		if it.Name == cmsIntegration {
			if k := integrationAdminKey(it); k != "" {
				return k, true, nil
			}
		}
	}
	var made struct {
		Integrations []ghostIntegration `json:"integrations"`
	}
	body := map[string]any{"integrations": []map[string]string{{"name": cmsIntegration}}}
	if err := g.call("POST", "/integrations/?include=api_keys", body, &made, http.StatusOK, http.StatusCreated); err != nil {
		return "", false, err
	}
	if len(made.Integrations) == 0 {
		return "", false, errors.New("Ghost made the integration but sent no admin key back: run cms up again")
	}
	k := integrationAdminKey(made.Integrations[0])
	if k == "" {
		return "", false, errors.New("Ghost's new integration has no admin key: run cms up again")
	}
	return k, false, nil
}

// integrationAdminKey is the admin key as "<id>:<hex>": Ghost's secret
// field carries both, or only the hex, which is then joined with the id.
func integrationAdminKey(it ghostIntegration) string {
	for _, k := range it.APIKeys {
		if k.Type != "admin" || k.Secret == "" {
			continue
		}
		if strings.Contains(k.Secret, ":") {
			return k.Secret
		}
		if k.ID != "" {
			return k.ID + ":" + k.Secret
		}
	}
	return ""
}

// linkTaggingOff turns outbound_link_tagging off and reads it back.
func (g *ghostSetup) linkTaggingOff() error {
	body := map[string]any{"settings": []map[string]any{{"key": "outbound_link_tagging", "value": false}}}
	var out struct {
		Settings []struct {
			Key   string `json:"key"`
			Value any    `json:"value"`
		} `json:"settings"`
	}
	if err := g.call("PUT", "/settings/", body, &out, http.StatusOK); err != nil {
		return err
	}
	for _, s := range out.Settings {
		if s.Key == "outbound_link_tagging" {
			if s.Value == false || s.Value == "false" {
				return nil
			}
			return fmt.Errorf("Ghost kept outbound_link_tagging at %v: turn it off in Settings → Analytics at %s/ghost", s.Value, g.base)
		}
	}
	return errors.New("Ghost's settings answer did not name outbound_link_tagging: check it is off at " + g.base + "/ghost")
}
