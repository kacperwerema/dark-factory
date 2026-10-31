package main

import (
	"net/http"
	"strings"
)

// Request body caps (docs/API/security.md): every handler that reads a
// body gets at most maxBodyBytes, image uploads maxUploadBodyBytes. The
// upload handlers keep their own smaller limits on top.
const (
	maxBodyBytes       = 4 << 20
	maxUploadBodyBytes = 12 << 20
)

// basePolicy is the Content-Security-Policy every response starts with: it
// stops framing by other sites (clickjacking of Approve or Send), <base>
// hijacking, plugins and forms posting elsewhere. securityHeaders adds the
// script rule with the request's nonce (csp.go, scriptPolicy). Routes that
// serve untrusted files set their own, stricter policy.
const basePolicy = "frame-ancestors 'self'; base-uri 'self'; object-src 'none'; form-action 'self'"

// securityHeaders sets the headers every response carries and caps the
// request body.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		if r.Body != nil && r.Method != http.MethodGet && r.Method != http.MethodHead {
			limit := int64(maxBodyBytes)
			if strings.HasSuffix(r.URL.Path, "/upload") || strings.HasSuffix(r.URL.Path, "/lead/image") {
				limit = maxUploadBodyBytes
			} else if strings.HasSuffix(r.URL.Path, "/lead/file") {
				limit = ceoFileMax + 64<<10
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		nw := newNonceWriter(w)
		next.ServeHTTP(nw, r)
		nw.finish()
	})
}
