package directpost

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Sent is one line of <issues>/.connectors/published.jsonl: what went out,
// where, for which issue. No secret and no caption: the file's hash keeps
// a send from being repeated.
type Sent struct {
	Connector string    `json:"connector"`
	Platform  string    `json:"platform"`
	Issue     string    `json:"issue,omitempty"`
	File      string    `json:"file"`
	SHA256    string    `json:"sha256"`
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	Date      time.Time `json:"date"`
}

// LogPath is the published log under the issues dir.
func LogPath(issueDir string) string {
	return filepath.Join(issueDir, ".connectors", "published.jsonl")
}

// FileHash is the sha256 of file.
func FileHash(file string) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// LoadSent is the log's lines; a bad line is skipped.
func LoadSent(issueDir string) []Sent {
	f, err := os.Open(LogPath(issueDir))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Sent
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var s Sent
		if json.Unmarshal(sc.Bytes(), &s) == nil && s.Connector != "" {
			out = append(out, s)
		}
	}
	return out
}

// AlreadySent is the earlier send of the same file through connector.
func AlreadySent(issueDir, connector, sha string) *Sent {
	for _, s := range LoadSent(issueDir) {
		if s.Connector == connector && s.SHA256 == sha {
			s := s
			return &s
		}
	}
	return nil
}

// AppendSent adds s to the log (0600: it lives in the tracker).
func AppendSent(issueDir string, s Sent) error {
	path := LogPath(issueDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}
