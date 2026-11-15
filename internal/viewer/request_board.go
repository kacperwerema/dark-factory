package viewer

import (
	"net/http"
	"slices"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// oneBoardLoad runs a read with every part of the page sharing one load of
// each project's board: the handler gets a copy of the server whose projects
// carry the shared load (tracker.Project.ShareBoardLoad), so the helpers
// below it need no request argument. A write keeps the plain server: what it
// reads after writing has to be the file.
func (s *Server) oneBoardLoad(h func(*Server, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.shared == nil || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			h(s, w, r)
			return
		}
		rs := *s
		rs.projects = slices.Clone(s.projects)
		for i := range rs.projects {
			p := &rs.projects[i]
			defer p.ShareBoardLoad()()
			s.shared.Store(p.Slug, p)
			defer s.shared.CompareAndDelete(p.Slug, p)
		}
		h(&rs, w, r)
	}
}

// projectInFlight is findProject for the template funcs, which are bound to
// the plain server: the project of the newest request in flight, whose load
// is no older than the caller's own.
func (s *Server) projectInFlight(slug string) *tracker.Project {
	if s.shared != nil {
		if p, ok := s.shared.Load(slug); ok {
			return p.(*tracker.Project)
		}
	}
	return s.findProject(slug)
}
