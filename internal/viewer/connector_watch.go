package viewer

import (
	"log"
	"time"

	"github.com/kacperwerema/dark-factory/internal/connectors"
)

// ConnectorSweepInterval is how often the viewer looks for per_run: tools a
// killed run left up. A tool stopped late costs memory, so a minute.
const ConnectorSweepInterval = time.Minute

// watchConnectors stops what no run holds until stop closes. It only ever
// stops: a tool starts only for a run (connector exec).
func (s *Server) watchConnectors(stop <-chan struct{}) {
	ticker := time.NewTicker(ConnectorSweepInterval)
	defer ticker.Stop()
	lastErr := map[string]bool{}
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
		for i := range s.projects {
			for _, err := range connectors.Sweep(&s.projects[i], "", time.Now) {
				if !lastErr[err.Error()] {
					log.Print(err)
				}
				lastErr[err.Error()] = true
			}
		}
	}
}
