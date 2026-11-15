package socials

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Check is one line of <issues>/.connectors/socials.jsonl: what a check
// found for a connector. It sits with the work files (Settled rule
// "Business data sits with the work files") and holds no name, handle,
// token or key: channel ids, providers and states only.
type Check struct {
	Date time.Time `json:"date"`
	// Connector is the address, <dept>/<name>.
	Connector   string    `json:"connector"`
	Channels    []Channel `json:"channels"`
	ExpiryKnown bool      `json:"expiry_known,omitempty"`
	// Error is why the check found nothing ("" when it read the tool).
	Error string `json:"error,omitempty"`
}

// LogPath is the project's socials check log.
func LogPath(p *tracker.Project) string {
	if p == nil || p.IssueDir == "" {
		return ""
	}
	return filepath.Join(p.IssueDir, ".connectors", "socials.jsonl")
}

// Append adds one check to the log.
func Append(p *tracker.Project, c Check) error {
	path := LogPath(p)
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

// Latest is the newest check per connector address.
func Latest(p *tracker.Project) map[string]Check {
	out := map[string]Check{}
	path := LogPath(p)
	if path == "" {
		return out
	}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		var c Check
		if json.Unmarshal(sc.Bytes(), &c) != nil || c.Connector == "" {
			continue
		}
		if old, ok := out[c.Connector]; !ok || !c.Date.Before(old.Date) {
			out[c.Connector] = c
		}
	}
	return out
}

// Expired are the providers of c's channels whose sign-in expired.
func (c Check) Expired() []string {
	var out []string
	for _, ch := range c.Channels {
		if ch.State == StateExpired {
			out = append(out, ch.Provider)
		}
	}
	return out
}
