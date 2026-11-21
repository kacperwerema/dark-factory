package payments

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Store remembers which provider events the app has already accepted, so a
// repeated webhook is handed to the app once. A key is "<provider>/<event id>".
type Store interface {
	Handled(key string) bool
	// MarkHandled records key; it is called only after the app returned 2xx.
	MarkHandled(key string) error
}

// MemStore is a Store in memory, for tests.
type MemStore struct {
	mu   sync.Mutex
	keys map[string]bool
}

func NewMemStore() *MemStore { return &MemStore{keys: map[string]bool{}} }

func (s *MemStore) Handled(k string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.keys[k]
}

func (s *MemStore) MarkHandled(k string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[k] = true
	return nil
}

// FileStore is a Store backed by an append-only JSONL file, so a restart
// keeps what was handled.
type FileStore struct {
	mu   sync.Mutex
	path string
	keys map[string]bool
}

type handledLine struct {
	Key string    `json:"key"`
	At  time.Time `json:"at"`
}

// OpenFileStore reads path (created on the first MarkHandled). A torn last
// line, from a crash mid-write, is skipped.
func OpenFileStore(path string) (*FileStore, error) {
	s := &FileStore{path: path, keys: map[string]bool{}}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var l handledLine
		if json.Unmarshal(sc.Bytes(), &l) == nil && l.Key != "" {
			s.keys[l.Key] = true
		}
	}
	return s, sc.Err()
}

func (s *FileStore) Handled(k string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.keys[k]
}

func (s *FileStore) MarkHandled(k string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.keys[k] {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	line, _ := json.Marshal(handledLine{Key: k, At: time.Now().UTC()})
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return fmt.Errorf("handled.jsonl: %w", err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	s.keys[k] = true
	return nil
}
