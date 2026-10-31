package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strings"
)

// Script CSP (F-12, docs/API/security.md). Every page answer carries
//
//	script-src 'self' 'nonce-<per request>'
//
// so only the viewer's own /static scripts and the inline <script> blocks its
// templates wrote run; an injected <script> or on*= attribute does not.
//
// Templates can't see the request, so they write cspNonce's marker, a random
// value fixed for the process, and nonceWriter swaps it for the request's
// nonce on the way out. Marker and nonce have the same length, so a
// Content-Length set by the handler stays right. Nobody outside the process
// learns the marker: it is replaced in every answer that carries the policy.
//
// Templates use no on*= handlers: static/events.js binds data-click,
// data-change, data-input, data-keydown and data-blur (each a JSON call
// written by the onCall template func), data-stop and data-autosubmit.

// nonceBytes is the size of the marker and of each request's nonce.
const nonceBytes = 16

// nonceMarker is what templates write for the nonce; nonceWriter replaces it.
var nonceMarker = mustRandomHex(nonceBytes)

func mustRandomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("csp: no randomness: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// cspNonce is the template func behind nonce="{{cspNonce}}".
func cspNonce() string { return nonceMarker }

// onCall is the template func behind data-click="{{onCall "fn" arg…}}": the
// JSON call static/events.js makes. Args are literals, or "$this" (the
// element), "$event" or "$value" (the element's value).
func onCall(name string, args ...any) (string, error) {
	b, err := json.Marshal(append([]any{name}, args...))
	return string(b), err
}

// scriptPolicy is the request's full policy: the base one plus the script
// rule with its nonce.
func scriptPolicy(nonce string) string {
	return basePolicy + "; script-src 'self' 'nonce-" + nonce + "'"
}

// errorPathScrub hides the home folder in error bodies (F-17): an error from
// a failed write names the file it wrote. "~" keeps the message useful.
var errorPathScrub = func() *strings.Replacer {
	home, err := os.UserHomeDir()
	if err != nil || len(home) < 2 {
		return nil
	}
	return strings.NewReplacer(home, "~")
}()

// nonceWriter stamps the request's nonce over the marker in an answer that
// still has the page policy, and scrubs the home folder from a plain-text or
// JSON error. An
// answer whose handler set its own policy (raw files, shelf files, images)
// passes through untouched.
type nonceWriter struct {
	http.ResponseWriter
	policy  string
	nonce   []byte
	marker  []byte
	decided bool
	stamp   bool
	errBody *bytes.Buffer
	carry   []byte
}

func newNonceWriter(w http.ResponseWriter) *nonceWriter {
	nonce := mustRandomHex(nonceBytes)
	nw := &nonceWriter{ResponseWriter: w, policy: scriptPolicy(nonce), nonce: []byte(nonce), marker: []byte(nonceMarker)}
	w.Header().Set("Content-Security-Policy", nw.policy)
	return nw
}

func (nw *nonceWriter) decide(code int) {
	if nw.decided {
		return
	}
	nw.decided = true
	h := nw.Header()
	nw.stamp = h.Get("Content-Security-Policy") == nw.policy
	if ct := h.Get("Content-Type"); errorPathScrub != nil && code >= 400 && (strings.HasPrefix(ct, "text/plain") || strings.HasPrefix(ct, "application/json")) {
		nw.errBody = &bytes.Buffer{}
		h.Del("Content-Length")
	}
}

func (nw *nonceWriter) WriteHeader(code int) {
	nw.decide(code)
	nw.ResponseWriter.WriteHeader(code)
}

func (nw *nonceWriter) Write(p []byte) (int, error) {
	nw.decide(http.StatusOK)
	if nw.errBody != nil {
		return nw.errBody.Write(p)
	}
	if !nw.stamp {
		return nw.ResponseWriter.Write(p)
	}
	data := append(nw.carry, p...)
	data = bytes.ReplaceAll(data, nw.marker, nw.nonce)
	// Hold back a tail that could be the start of a marker split across
	// two writes.
	keep := len(nw.marker) - 1
	if keep > len(data) {
		keep = len(data)
	}
	out, tail := data[:len(data)-keep], data[len(data)-keep:]
	nw.carry = append([]byte(nil), tail...)
	if len(out) > 0 {
		if _, err := nw.ResponseWriter.Write(out); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// finish writes what Write held back. securityHeaders calls it after the
// handler returns.
func (nw *nonceWriter) finish() {
	if nw.errBody != nil {
		body := errorPathScrub.Replace(nw.errBody.String())
		if nw.stamp {
			body = strings.ReplaceAll(body, nonceMarker, string(nw.nonce))
		}
		nw.errBody = nil
		nw.ResponseWriter.Write([]byte(body))
		return
	}
	if len(nw.carry) > 0 {
		nw.ResponseWriter.Write(nw.carry)
		nw.carry = nil
	}
}

// Flush sends what is held back, then flushes the connection.
func (nw *nonceWriter) Flush() {
	if nw.errBody == nil && len(nw.carry) > 0 {
		nw.ResponseWriter.Write(nw.carry)
		nw.carry = nil
	}
	if f, ok := nw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the connection.
func (nw *nonceWriter) Unwrap() http.ResponseWriter { return nw.ResponseWriter }
