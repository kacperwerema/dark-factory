package main

// The owner credential on loopback (docs/API/security.md, "Owner sign-in").
//
// On loopback every local client reaches the viewer: the owner's browser,
// issue-cli and every agent on the machine. The owner decisions (the O routes
// of the route inventory: approvals, YOLO, auto-start, send-back, reviews,
// workflow edits, owner boxes) need more than a header any client can send.
//
// The credential is a session cookie (ownerCookie: HttpOnly, SameSite=Strict)
// that a browser gets only from POST /owner/pair with a one-time code. The
// viewer never puts the code in an HTTP answer: it shows it where the owner
// looks, a native dialog on macOS or the viewer's own terminal. Sessions live
// in the viewer's memory only (just their SHA-256), so no file an agent can
// read or write holds or mints one, and a restart signs the owner out.
//
// On a public name the identity gate already verified the owner's Cloudflare
// Access token; that request is the owner without a cookie.

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	ownerCookie = "iv_owner"
	// ownerRefused starts every refusal of the owner gate; tests and the
	// security check look for it.
	ownerRefused = "owner sign-in needed"
	// ownerSessionTTL bounds a session; a viewer restart ends it sooner.
	ownerSessionTTL = 30 * 24 * time.Hour
	// ownerCodeTTL is how long a code may be typed in.
	ownerCodeTTL = 5 * time.Minute
	// ownerCodeEvery is the least time between two codes, so a loop of
	// requests doesn't put a dialog in front of the owner each time.
	ownerCodeEvery = 30 * time.Second
	// ownerCodeTries wrong codes burn the current one.
	ownerCodeTries  = 5
	ownerCodeDigits = 8
)

// ownerAuth holds the owner's sessions and the one pending code.
type ownerAuth struct {
	mu       sync.Mutex
	sessions map[string]time.Time // sha256(token) hex → expiry
	code     string
	codeExp  time.Time
	codeAt   time.Time
	tries    int
	// deliver shows code to the owner outside HTTP and says where
	// ("a dialog", "the viewer's terminal").
	deliver func(code string) (string, error)
	now     func() time.Time
}

func newOwnerAuth() *ownerAuth {
	return &ownerAuth{sessions: map[string]time.Time{}, deliver: deliverOwnerCode("auto"), now: time.Now}
}

// deliverOwnerCode picks how the code reaches the owner: "dialog" (macOS,
// osascript), "terminal" (the viewer's stderr), or "auto": the dialog on
// macOS, the terminal when there is none or it fails.
func deliverOwnerCode(mode string) func(string) (string, error) {
	terminal := func(code string) (string, error) {
		fmt.Fprintf(os.Stderr, "Owner sign-in code: %s-%s (type it on the viewer's /owner page within %s; only you should see it)\n", code[:4], code[4:], ownerCodeTTL)
		return "the viewer's terminal", nil
	}
	dialog := func(code string) (string, error) {
		if runtime.GOOS != "darwin" {
			return "", fmt.Errorf("no dialog on %s", runtime.GOOS)
		}
		msg := fmt.Sprintf("A browser asks to sign in as the owner of the AI Workflow Engine viewer.\n\nCode: %s-%s\n\nType it on the /owner page only if you asked for it. It expires in %s.", code[:4], code[4:], ownerCodeTTL)
		script := fmt.Sprintf(`display dialog %q with title "Owner sign-in" buttons {"OK"} default button "OK" giving up after %d`, msg, int(ownerCodeTTL.Seconds()))
		cmd := exec.Command("osascript", "-e", script)
		if err := cmd.Start(); err != nil {
			return "", err
		}
		go cmd.Wait()
		fmt.Fprintln(os.Stderr, "Owner sign-in: a code was shown in a dialog")
		return "a dialog on this Mac", nil
	}
	switch mode {
	case "terminal":
		return terminal
	case "dialog":
		return dialog
	}
	return func(code string) (string, error) {
		if where, err := dialog(code); err == nil {
			return where, nil
		}
		return terminal(code)
	}
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

// valid reports whether token is a live session.
func (o *ownerAuth) valid(token string) bool {
	if token == "" {
		return false
	}
	h := hashToken(token)
	o.mu.Lock()
	defer o.mu.Unlock()
	exp, ok := o.sessions[h]
	if ok && o.now().After(exp) {
		delete(o.sessions, h)
		return false
	}
	return ok
}

func (o *ownerAuth) newSession() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	o.mu.Lock()
	o.sessions[hashToken(token)] = o.now().Add(ownerSessionTTL)
	o.mu.Unlock()
	return token, nil
}

func (o *ownerAuth) endSession(token string) {
	o.mu.Lock()
	delete(o.sessions, hashToken(token))
	o.mu.Unlock()
}

// issueCode makes a new code and delivers it; within ownerCodeEvery of the
// last one it does nothing (sent false).
func (o *ownerAuth) issueCode() (where string, sent bool, err error) {
	o.mu.Lock()
	now := o.now()
	if !o.codeAt.IsZero() && now.Sub(o.codeAt) < ownerCodeEvery {
		o.mu.Unlock()
		return "", false, nil
	}
	var sb strings.Builder
	for i := 0; i < ownerCodeDigits; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			o.mu.Unlock()
			return "", false, err
		}
		sb.WriteByte(byte('0' + n.Int64()))
	}
	code := sb.String()
	o.code, o.codeExp, o.codeAt, o.tries = code, now.Add(ownerCodeTTL), now, 0
	deliver := o.deliver
	o.mu.Unlock()
	where, err = deliver(code)
	return where, err == nil, err
}

// redeem uses up the pending code when typed matches it.
func (o *ownerAuth) redeem(typed string) bool {
	typed = strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, typed)
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.code == "" || o.now().After(o.codeExp) {
		o.code = ""
		return false
	}
	if subtle.ConstantTimeCompare([]byte(typed), []byte(o.code)) == 1 {
		o.code = ""
		return true
	}
	o.tries++
	if o.tries >= ownerCodeTries {
		o.code = ""
	}
	return false
}

// fromOwner reports whether r comes from the owner: a verified identity on a
// public name, or a live owner session cookie on loopback. Browser headers
// (Sec-Fetch-Site, Origin) don't count: any client can send them.
func (s *Server) fromOwner(r *http.Request) bool {
	if verifiedIdentity(r) != "" {
		return true
	}
	if s.owner == nil {
		return false
	}
	c, err := r.Cookie(ownerCookie)
	return err == nil && s.owner.valid(c.Value)
}

// ownerOnlyRoute reports whether the request under /p/<project>/ (rest) is
// an O route of docs/API/security.md's inventory. It matches the
// dispatcher's own conditions, or a wider set; TestOwnerRoutesRefuse…
// sends one request per route.
func ownerOnlyRoute(method, rest string) bool {
	if method != http.MethodPost {
		// A file the owner gave the CEO downloads only to the owner.
		return (method == http.MethodGet || method == http.MethodHead) && strings.HasPrefix(rest, "lead/file/")
	}
	issue := strings.HasPrefix(rest, "issue/")
	switch {
	case issue && (strings.HasSuffix(rest, "/approve") || strings.HasSuffix(rest, "/send-back") || strings.HasSuffix(rest, "/yolo-review") || strings.HasSuffix(rest, "/clarify")):
		return true
	case rest == "needs-you/approve", rest == "owner-checks/tick", rest == "departments/add", rest == "lead/file",
		rest == "shelf/review", rest == "shelf/comment", rest == "shelf/comment/resolve":
		return true
	case strings.HasPrefix(rest, "yolo/"), strings.HasPrefix(rest, "autostart/"):
		return true
	case strings.HasPrefix(rest, "decisions/") && strings.HasSuffix(rest, "/confirm"):
		return true
	case strings.HasPrefix(rest, "hall/station/") && strings.Contains(rest, "/edit/"):
		return true
	case strings.HasPrefix(rest, "health/recommendation/"):
		return true
	case strings.HasPrefix(rest, "memory/"):
		return true
	case strings.HasPrefix(rest, "postmortem/"):
		return true
	}
	return false
}

// refuseNonOwner answers 403 with the way to sign in, and for routes with
// an agent's way around (comment, ask) that way too.
func refuseNonOwner(w http.ResponseWriter, r *http.Request, rest string) {
	msg := ownerRefused + ": only the owner makes this decision, from a browser signed in at /owner (a one-time code the viewer shows you)"
	switch {
	case strings.HasPrefix(rest, "shelf/comment"):
		msg += "; " + shelfCommentRefused
	case rest == "owner-checks/tick":
		msg += "; " + ownerCheckRefused
	case strings.HasPrefix(rest, "issue/") && strings.HasSuffix(rest, "/clarify"):
		msg += "; " + clarifyRefused
	case strings.HasPrefix(rest, "memory/"):
		msg += "; " + memoryRefused
	case strings.HasPrefix(rest, "postmortem/"):
		msg += "; " + postmortemRefused
	default:
		msg += "; an agent asks on its issue trail: issue-cli comment <slug> \"question: …\""
	}
	if strings.Contains(r.Header.Get("Accept"), "application/json") || strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		writeJSONStatus(w, http.StatusForbidden, map[string]string{"error": msg})
		return
	}
	http.Error(w, msg, http.StatusForbidden)
}

type ownerPageData struct {
	Title    string
	Header   WbHeader
	SignedIn bool
	Sent     string // where the last code went
	Error    string
	Next     string
}

// safeNext keeps a redirect target on this viewer: a path, never a scheme,
// host or protocol-relative URL.
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return ""
	}
	if u, err := url.Parse(next); err != nil || u.Host != "" || u.Scheme != "" {
		return ""
	}
	return next
}

// handleOwner serves /owner (the sign-in page) and its posts:
// /owner/code, /owner/pair, /owner/signout.
func (s *Server) handleOwner(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.FormValue("next"))
	back := func(q string) {
		to := "/owner?" + q
		if next != "" {
			to += "&next=" + url.QueryEscape(next)
		}
		http.Redirect(w, r, to, http.StatusSeeOther)
	}
	switch {
	case r.URL.Path == "/owner" && r.Method == http.MethodGet:
		d := ownerPageData{
			Title:    "Owner sign-in",
			Header:   WbHeader{Crumbs: []WbCrumb{{Label: "Map", URL: "/"}, {Label: "Owner sign-in", Current: true}}, Quiet: true, HideDone: true, WaitingURL: "/needs-you"},
			SignedIn: s.fromOwner(r),
			Next:     next,
		}
		switch q := r.URL.Query(); {
		case q.Get("sent") != "":
			d.Sent = q.Get("sent")
		case q.Get("wait") == "1":
			d.Error = "A code was sent less than " + ownerCodeEvery.String() + " ago: use that one."
		case q.Get("wrong") == "1":
			d.Error = "That code is wrong, expired or used. Ask for a new one."
		case q.Get("failed") == "1":
			d.Error = "The viewer could not show a code. Check the viewer's terminal."
		}
		w.Header().Set("Cache-Control", "no-store")
		if err := s.tmpl.ExecuteTemplate(w, "owner.html", d); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	case r.URL.Path == "/owner/code" && r.Method == http.MethodPost:
		where, sent, err := s.owner.issueCode()
		switch {
		case err != nil:
			fmt.Fprintf(os.Stderr, "owner sign-in: could not deliver a code: %v\n", err)
			back("failed=1")
		case !sent:
			back("wait=1")
		default:
			back("sent=" + url.QueryEscape(where))
		}
	case r.URL.Path == "/owner/pair" && r.Method == http.MethodPost:
		if !s.owner.redeem(r.FormValue("code")) {
			if strings.Contains(r.Header.Get("Accept"), "text/html") {
				back("wrong=1")
				return
			}
			http.Error(w, "wrong, expired or used code", http.StatusForbidden)
			return
		}
		token, err := s.owner.newSession()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name: ownerCookie, Value: token, Path: "/",
			MaxAge: int(ownerSessionTTL.Seconds()), HttpOnly: true, SameSite: http.SameSiteStrictMode,
			Secure: r.TLS != nil,
		})
		to := next
		if to == "" {
			to = "/owner"
		}
		http.Redirect(w, r, to, http.StatusSeeOther)
	case r.URL.Path == "/owner/signout" && r.Method == http.MethodPost:
		if c, err := r.Cookie(ownerCookie); err == nil {
			s.owner.endSession(c.Value)
		}
		http.SetCookie(w, &http.Cookie{Name: ownerCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		http.Redirect(w, r, "/owner", http.StatusSeeOther)
	default:
		http.NotFound(w, r)
	}
}
