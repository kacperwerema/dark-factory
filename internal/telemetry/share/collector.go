package share

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/kacperwerema/dark-factory/internal/telemetry"
)

// DataFile is the collector's store inside its data dir.
const DataFile = "batches.jsonl"

// Stored is one line of the collector's store: the batch and when it came.
type Stored struct {
	Received string `json:"received"`
	Batch    Batch  `json:"batch"`
}

// Store appends accepted batches to <Dir>/batches.jsonl.
type Store struct {
	Dir string
	Now func() time.Time
}

// Path is the store's file.
func (s Store) Path() string { return filepath.Join(s.Dir, DataFile) }

// Append writes one accepted batch under a lock.
func (s Store) Append(b Batch) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	line, err := json.Marshal(Stored{Received: now().UTC().Format(time.RFC3339), Batch: b})
	if err != nil {
		return err
	}
	unlock, err := telemetry.LockFile(s.Path()+".lock", 2*time.Second)
	if err != nil {
		return err
	}
	defer unlock()
	f, err := os.OpenFile(s.Path(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// DecodeBatch reads one batch strictly: unknown fields, trailing data and
// anything ValidateShape refuses are errors.
func DecodeBatch(r io.Reader) (Batch, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var b Batch
	if err := dec.Decode(&b); err != nil {
		return b, fmt.Errorf("not a batch: %w", err)
	}
	if dec.More() {
		return b, fmt.Errorf("not a batch: trailing data")
	}
	if err := ValidateShape(b); err != nil {
		return b, err
	}
	return b, nil
}

// Handler is the collector's HTTP API: POST /v1/batch and GET /healthz.
func Handler(st Store) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(BatchPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "POST a batch", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes+1))
		if err != nil {
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}
		if len(body) > MaxBodyBytes {
			http.Error(w, "batch too large", http.StatusRequestEntityTooLarge)
			return
		}
		b, err := DecodeBatch(bytes.NewReader(body))
		if err != nil {
			http.Error(w, "refused: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := st.Append(b); err != nil {
			http.Error(w, "store error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok\n")
	})
	return mux
}

// ReadStore reads a collector's store: path is batches.jsonl or the data
// dir holding it. A batch re-sent for the same install and day replaces the
// earlier one. Lines that don't pass ValidateShape are skipped and counted.
func ReadStore(path string) (batches []Batch, skipped int, err error) {
	if fi, statErr := os.Stat(path); statErr == nil && fi.IsDir() {
		path = filepath.Join(path, DataFile)
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	idx := map[string]int{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), MaxBodyBytes*2)
	for sc.Scan() {
		var s Stored
		if json.Unmarshal(sc.Bytes(), &s) != nil || ValidateShape(s.Batch) != nil {
			skipped++
			continue
		}
		k := s.Batch.Install + "|" + s.Batch.Day
		if i, ok := idx[k]; ok {
			batches[i] = s.Batch
			continue
		}
		idx[k] = len(batches)
		batches = append(batches, s.Batch)
	}
	return batches, skipped, sc.Err()
}
