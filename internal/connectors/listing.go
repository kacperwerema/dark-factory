package connectors

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kacperwerema/dark-factory/internal/jsonfile"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// A url: connector with per_run: keeps a copy of what its server answered to
// initialize and to its lists, the first time the tool is up. A later
// session answers those from the copy while the tool stays down, and starts
// it only for a request the copy can't answer: a tools/call, mostly. The
// copy is machine state beside the connector's lease and lock (listing.json,
// 0600): it describes this machine's server and holds no header.

// listMethods are the requests a copy may answer: lists that change nothing
// on the server.
var listMethods = map[string]bool{
	"tools/list":               true,
	"prompts/list":             true,
	"resources/list":           true,
	"resources/templates/list": true,
}

// AnswerKey is the copy's key for a request: "initialize <protocol version>"
// (a server may answer each version its own way), "<list method> <params>"
// with params compacted and _meta left out (a cursor is its own page), and
// "" for a request no copy keeps.
func AnswerKey(method string, params json.RawMessage) string {
	if method != "initialize" && !listMethods[method] {
		return ""
	}
	var m map[string]json.RawMessage
	_ = json.Unmarshal(params, &m)
	delete(m, "_meta")
	if method == "initialize" {
		var v string
		_ = json.Unmarshal(m["protocolVersion"], &v)
		return "initialize " + v
	}
	if len(m) == 0 {
		return method
	}
	b, _ := json.Marshal(m) // keys sorted
	return method + " " + string(b)
}

// Listing is a server's saved answers, by AnswerKey.
type Listing struct {
	// URL is the sha256 of the url: the answers came from; a copy for
	// another url: is not used.
	URL     string                     `json:"url"`
	Saved   time.Time                  `json:"saved"`
	Answers map[string]json.RawMessage `json:"answers"`
}

// Answer is the saved result for a request, if the copy holds one.
func (l *Listing) Answer(method string, params json.RawMessage) (json.RawMessage, bool) {
	if l == nil {
		return nil, false
	}
	k := AnswerKey(method, params)
	if k == "" {
		return nil, false
	}
	r, ok := l.Answers[k]
	return r, ok
}

func urlSum(url string) string {
	s := sha256.Sum256([]byte(url))
	return hex.EncodeToString(s[:])
}

func listingPath(p *tracker.Project, dept string, c *tracker.Connector) string {
	dir := runDir(p, dept, c)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "listing.json")
}

func readListing(path string) *Listing {
	var l Listing
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &l) != nil {
		return nil
	}
	return &l
}

// LoadListing is c's saved copy, nil when c isn't a url: connector with
// per_run:, or the copy is missing, doesn't parse, was saved from another
// url:, or lacks an initialize or a tools/list answer: a session with no
// copy holds the tool at launch.
func LoadListing(p *tracker.Project, dept string, c *tracker.Connector) *Listing {
	if c.URL == "" || c.PerRun == nil {
		return nil
	}
	path := listingPath(p, dept, c)
	if path == "" {
		return nil
	}
	l := readListing(path)
	if l == nil || l.URL != urlSum(c.URL) {
		return nil
	}
	hasInit, hasList := false, false
	for k := range l.Answers {
		switch {
		case k == "tools/list":
			hasList = true
		case len(k) > len("initialize ") && k[:len("initialize ")] == "initialize ":
			hasInit = true
		}
	}
	if !hasInit || !hasList {
		return nil
	}
	return l
}

// ListingSaved is when c's usable copy was last saved; zero without one.
func ListingSaved(p *tracker.Project, dept string, c *tracker.Connector) time.Time {
	if l := LoadListing(p, dept, c); l != nil {
		return l.Saved
	}
	return time.Time{}
}

var listingMu sync.Mutex

// SaveAnswer puts one live answer into c's copy (read again, merged, written
// aside and renamed), so the copy follows the server whenever its tool is
// up. A copy from another url: is replaced. Two sessions saving at once can
// only lose an answer the next session saves again.
func SaveAnswer(p *tracker.Project, dept string, c *tracker.Connector, key string, result json.RawMessage, now time.Time) error {
	if key == "" || len(result) == 0 {
		return nil
	}
	path := listingPath(p, dept, c)
	if path == "" {
		return nil
	}
	listingMu.Lock()
	defer listingMu.Unlock()
	sum := urlSum(c.URL)
	l := readListing(path)
	if l == nil || l.URL != sum {
		l = &Listing{URL: sum}
	}
	if l.Answers == nil {
		l.Answers = map[string]json.RawMessage{}
	}
	if old, ok := l.Answers[key]; ok && string(old) == string(result) && !l.Saved.IsZero() {
		return nil
	}
	l.Answers[key] = append(json.RawMessage{}, result...)
	l.Saved = now.UTC()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return jsonfile.Write(path, l, 0600)
}
