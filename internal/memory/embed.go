package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Embedder turns texts into vectors, one per text, in order. Phase 2's
// seam (Design v2 P10): a local endpoint or a command, never a hosted
// service unless the owner opts in.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// embedRequest and embedAnswer are Ollama's /api/embed JSON, the one wire
// format for both kinds of embedder
// (factory/shared-memory-phase-2-local-embeddings-behind-the-embedder-seam#D-1).
type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedAnswer struct {
	Embeddings [][]float32 `json:"embeddings"`
	Error      string      `json:"error,omitempty"`
}

// maxAnswer bounds an embedder's answer: 64 texts of 4,096 floats as JSON
// is well under it.
const maxAnswer = 32 << 20

// HTTPEmbedder posts to an Ollama-style /api/embed URL. The caller checks
// the URL is loopback (tracker.LoopbackURL).
type HTTPEmbedder struct {
	URL     string
	Model   string
	Timeout time.Duration
	Client  *http.Client
}

// Embed implements Embedder.
func (h HTTPEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	body, _ := json.Marshal(embedRequest{Model: h.Model, Input: texts})
	ctx, cancel := withTimeout(ctx, h.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	c := h.Client
	if c == nil {
		// No proxy: a loopback URL must not be sent through one.
		c = &http.Client{Transport: &http.Transport{Proxy: nil}}
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s", h.URL, resp.Status)
	}
	return decodeAnswer(raw, len(texts))
}

// CommandEmbedder runs Argv with the request on stdin and reads the
// answer from stdout, in Dir.
type CommandEmbedder struct {
	Argv    []string
	Dir     string
	Model   string
	Timeout time.Duration
}

// Embed implements Embedder.
func (c CommandEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(c.Argv) == 0 {
		return nil, errors.New("no command")
	}
	body, _ := json.Marshal(embedRequest{Model: c.Model, Input: texts})
	ctx, cancel := withTimeout(ctx, c.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Argv[0], c.Argv[1:]...)
	cmd.Dir = c.Dir
	cmd.Stdin = bytes.NewReader(body)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%s: no answer in time", filepath.Base(c.Argv[0]))
		}
		msg := strings.TrimSpace(errb.String())
		if len(msg) > 200 {
			msg = msg[:200]
		}
		if msg != "" {
			return nil, fmt.Errorf("%s: %v: %s", filepath.Base(c.Argv[0]), err, msg)
		}
		return nil, fmt.Errorf("%s: %v", filepath.Base(c.Argv[0]), err)
	}
	if out.Len() > maxAnswer {
		return nil, errors.New("answer too big")
	}
	return decodeAnswer(out.Bytes(), len(texts))
}

func withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		d = 5 * time.Second
	}
	return context.WithTimeout(ctx, d)
}

// decodeAnswer reads an answer and checks it has want vectors of one
// non-zero length.
func decodeAnswer(raw []byte, want int) ([][]float32, error) {
	var a embedAnswer
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("answer is not embed JSON: %v", err)
	}
	if a.Error != "" {
		return nil, errors.New(a.Error)
	}
	if len(a.Embeddings) != want {
		return nil, fmt.Errorf("answer has %d vectors for %d texts", len(a.Embeddings), want)
	}
	dim := len(a.Embeddings[0])
	for _, v := range a.Embeddings {
		if len(v) == 0 || len(v) != dim {
			return nil, errors.New("answer has empty or uneven vectors")
		}
	}
	return a.Embeddings, nil
}

// GuardLimit is the memory use, in percent, at which no embed request is
// made (the issue brief; mem-guard resumes paused work at 70%).
const GuardLimit = 70

// MemoryUsed asks mem-guard how much of the machine's memory is used. A
// var so tests need no mem-guard. issue-cli never measures memory itself:
// mem-guard owns the numbers (as internal/autostart).
var MemoryUsed = func() (int, error) {
	path, err := exec.LookPath("mem-guard")
	if err != nil {
		return 0, errors.New("mem-guard not found")
	}
	out, err := exec.Command(path, "used").Output()
	if err != nil {
		return 0, fmt.Errorf("mem-guard used: %v", err)
	}
	n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(string(out)), "%"))
	if err != nil {
		return 0, fmt.Errorf("mem-guard used said %q", strings.TrimSpace(string(out)))
	}
	return n, nil
}

// guardOK is nil when an embed request may run now
// (factory/shared-memory-phase-2-local-embeddings-behind-the-embedder-seam#D-3).
func guardOK() error {
	used, err := MemoryUsed()
	if err != nil {
		return err
	}
	if used >= GuardLimit {
		return fmt.Errorf("memory at %d%% (embeds wait below %d%%)", used, GuardLimit)
	}
	return nil
}

// VectorCache keeps one vector per memory text (Hash) and model, as small
// JSON files under Dir/<model>/.
type VectorCache struct {
	Dir   string
	Model string
}

// VectorDir is a project's vector cache: with the work files, so a
// private tracker keeps it private.
func VectorDir(issueDir string) string { return filepath.Join(issueDir, ".memory", "vectors") }

func (c VectorCache) path(hash string) string {
	return filepath.Join(c.Dir, modelSlug(c.Model), hash+".json")
}

// Get is the cached vector of hash, nil when none.
func (c VectorCache) Get(hash string) []float32 {
	if c.Dir == "" || hash == "" {
		return nil
	}
	raw, err := os.ReadFile(c.path(hash))
	if err != nil {
		return nil
	}
	var v []float32
	if json.Unmarshal(raw, &v) != nil || len(v) == 0 {
		return nil
	}
	return v
}

// Put writes hash's vector aside and renames it into place.
func (c VectorCache) Put(hash string, v []float32) error {
	if c.Dir == "" || hash == "" {
		return nil
	}
	p := c.path(hash)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	raw, _ := json.Marshal(v)
	tmp, err := os.CreateTemp(filepath.Dir(p), ".vec-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// modelSlug makes a model name a safe folder name.
func modelSlug(model string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(model) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	s := strings.Trim(b.String(), ".")
	if s == "" {
		return "default"
	}
	return s
}
