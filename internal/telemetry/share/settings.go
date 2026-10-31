package share

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/telemetry"
)

// URLEnv sets the collector URL for one shell, over the settings file.
const URLEnv = "ISSUE_CLI_TELEMETRY_URL"

// DefaultURL is the collector a release sends to when the user set none.
// Empty: the engine ships no collector, so nothing is sent until the owner
// deploys one and sets it here in a release (or a user sets their own).
const DefaultURL = ""

// SettingsFile is the per-install share setting under the config dir.
const SettingsFile = "telemetry-share.json"

// Settings is the user's choice for this install. Sharing is on by default
// (opt-out, factory/usage-telemetry…#D-7): no file means on.
type Settings struct {
	Share   bool   `json:"share"`
	Install string `json:"install_id,omitempty"`
	Decided string `json:"decided,omitempty"`
	URL     string `json:"url,omitempty"`
	// Notice is the day the one-time "usage stats are on" line was shown.
	Notice string `json:"notice_shown,omitempty"`
	// Default is true when no settings file exists yet (not stored).
	Default bool `json:"-"`
}

// ConfigDir is ${XDG_CONFIG_HOME:-~/.config}/ai-workflow-engine.
func ConfigDir() string {
	base := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "ai-workflow-engine")
}

// SettingsPath is where the share setting lives ("" without a home dir).
func SettingsPath() string {
	d := ConfigDir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, SettingsFile)
}

// StateDir is ${XDG_STATE_HOME:-~/.local/state}/issue-cli, beside the
// global telemetry file.
func StateDir() string {
	p := telemetry.GlobalPath()
	if p == "" {
		return ""
	}
	return filepath.Dir(p)
}

// SentLogPath is the log of every batch sent (or tried).
func SentLogPath() string {
	d := StateDir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "telemetry-sent.jsonl")
}

// StampPath is touched when an automatic send starts, at most once a day.
func StampPath() string {
	d := StateDir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "telemetry-send.stamp")
}

// Load reads the settings; a missing file is the default: share on, with
// no install id yet (EnsureInstall makes one).
func Load() (Settings, error) {
	p := SettingsPath()
	if p == "" {
		return Settings{Share: true, Default: true}, nil
	}
	raw, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return Settings{Share: true, Default: true}, nil
	}
	if err != nil {
		return Settings{}, err
	}
	var s Settings
	if err := json.Unmarshal(raw, &s); err != nil {
		return Settings{}, fmt.Errorf("%s: %w", p, err)
	}
	if s.Share && !installRe.MatchString(s.Install) {
		return Settings{}, fmt.Errorf("%s: install_id is not 32 hex characters; run issue-cli telemetry share on again", p)
	}
	return s, nil
}

// Save writes the settings atomically.
func Save(s Settings) error {
	p := SettingsPath()
	if p == "" {
		return fmt.Errorf("cannot determine the config directory (no $XDG_CONFIG_HOME or home directory)")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// EnsureInstall writes the settings file on the first run: share on (the
// default), a fresh random install id, and the day the notice was shown.
// created is false when a file already exists (nothing is changed).
func EnsureInstall(now time.Time) (s Settings, created bool, err error) {
	s, err = Load()
	if err != nil || !s.Default {
		return s, false, err
	}
	id, err := NewInstallID()
	if err != nil {
		return s, false, err
	}
	s = Settings{Share: true, Install: id, Notice: now.UTC().Format("2006-01-02")}
	return s, true, Save(s)
}

// TurnOn turns sharing back on, with a fresh random install id when none.
func TurnOn(now time.Time) (Settings, error) {
	s, err := Load()
	if err != nil {
		s = Settings{}
	}
	s.Default = false
	if !installRe.MatchString(s.Install) {
		id, err := NewInstallID()
		if err != nil {
			return s, err
		}
		s.Install = id
	}
	s.Share = true
	s.Decided = now.UTC().Format("2006-01-02")
	return s, Save(s)
}

// TurnOff opts out and forgets the install id, so a later opt-in is a new,
// unlinkable install.
func TurnOff(now time.Time) (Settings, error) {
	s, _ := Load()
	s = Settings{Share: false, Decided: now.UTC().Format("2006-01-02"), URL: s.URL, Notice: s.Notice}
	return s, Save(s)
}

// NewInstallID is 16 random bytes as hex.
func NewInstallID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// EffectiveURL is the collector URL in effect: the env, then the settings, then the
// built-in default (empty).
func (s Settings) EffectiveURL() string {
	if u := strings.TrimSpace(os.Getenv(URLEnv)); u != "" {
		return u
	}
	if u := strings.TrimSpace(s.URL); u != "" {
		return u
	}
	return DefaultURL
}

// State is why sharing does or doesn't send, for messages and tests.
type State struct {
	Settings Settings
	URL      string
	// Sending is true only when sharing is on (the default), telemetry
	// isn't off, and a URL is set.
	Sending bool
	// Why is a one-line reason when not sending.
	Why string
}

// Current reads the settings and env into a State.
func Current() State {
	s, err := Load()
	st := State{Settings: s, URL: s.EffectiveURL()}
	switch {
	case err != nil:
		st.Why = "the share setting can't be read (" + err.Error() + ")"
	case telemetry.EnvDisabled():
		st.Why = telemetry.DisableEnv + " is off"
	case !s.Share:
		st.Why = "sharing is turned off (issue-cli telemetry share on turns it back on)"
	case st.URL == "":
		st.Why = "no collector URL is set, so nothing is sent"
	default:
		st.Sending = true
	}
	return st
}

// SentRecord is one line of the sent log.
type SentRecord struct {
	At     string `json:"at"`
	Day    string `json:"day"`
	URL    string `json:"url"`
	Status int    `json:"status"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	Batch  Batch  `json:"batch"`
}

// AppendSent adds a record to the sent log.
func AppendSent(r SentRecord) error {
	p := SentLogPath()
	if p == "" {
		return fmt.Errorf("cannot determine the state directory")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	unlock, err := telemetry.LockFile(p+".lock", 2*time.Second)
	if err != nil {
		return err
	}
	defer unlock()
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// ReadSent returns the sent log, oldest first. A missing log is empty.
func ReadSent() ([]SentRecord, error) {
	p := SentLogPath()
	if p == "" {
		return nil, nil
	}
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []SentRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), MaxBodyBytes*2)
	for sc.Scan() {
		var r SentRecord
		if json.Unmarshal(sc.Bytes(), &r) == nil {
			out = append(out, r)
		}
	}
	return out, sc.Err()
}

// SentDays is the set of days already sent successfully to url.
func SentDays(records []SentRecord, url string) map[string]bool {
	m := map[string]bool{}
	for _, r := range records {
		if r.OK && r.URL == url {
			m[r.Day] = true
		}
	}
	return m
}

// PendingDays are the full UTC days of the last PendingWindow days not yet
// sent, oldest first. Today is never pending.
func PendingDays(now time.Time, sent map[string]bool) []string {
	var out []string
	today := now.UTC().Truncate(24 * time.Hour)
	for i := PendingWindow; i >= 1; i-- {
		d := today.AddDate(0, 0, -i).Format("2006-01-02")
		if !sent[d] {
			out = append(out, d)
		}
	}
	return out
}

// PendingWindow is how many past days a send covers.
const PendingWindow = 7
