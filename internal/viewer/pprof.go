package viewer

import (
	"net/http"
	"net/http/pprof"
	"strings"
)

// The running viewer's goroutine, heap and CPU profiles, so an audit can read
// them without stopping it. Goroutine stacks and heap samples show the
// process's insides, so this is an O route that also stays on loopback: the
// owner's verified identity, enough elsewhere on a public name, doesn't open
// it (docs/API/security.md).

// pprofPublicRefused answers a public request, the owner's included.
const pprofPublicRefused = "refused: profiles answer only on loopback, from a browser signed in at /owner"

// handlePprof serves /debug/pprof/ and its profiles. Importing net/http/pprof
// also registers them on http.DefaultServeMux, which the viewer never serves.
func (s *Server) handlePprof(w http.ResponseWriter, r *http.Request) {
	if publicRequest(r) {
		http.Error(w, pprofPublicRefused, http.StatusForbidden)
		return
	}
	if !s.fromOwner(r) {
		refuseNonOwner(w, r, r.URL.Path)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	switch strings.TrimPrefix(r.URL.Path, "/debug/pprof/") {
	case "cmdline":
		pprof.Cmdline(w, r)
	case "profile":
		pprof.Profile(w, r)
	case "symbol":
		pprof.Symbol(w, r)
	case "trace":
		pprof.Trace(w, r)
	default:
		// The index, or a named profile (goroutine, heap, …).
		pprof.Index(w, r)
	}
}
