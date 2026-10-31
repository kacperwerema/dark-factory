package tracker

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// MemoryConfig is a workflow file's memory: block: settings for the
// agents' shared memory (docs/memory.md). No block means BM25 ranking
// with no embedder.
type MemoryConfig struct {
	Embedder *EmbedderConfig `yaml:"embedder,omitempty" desc:"Optional local embedder for memory search: url (loopback, Ollama-style /api/embed) or command, model, timeout, min_similarity (see docs/memory.md, Semantic search)"`
}

// EmbedderConfig names a local embedder: an HTTP endpoint on this machine
// or a command, speaking Ollama's /api/embed JSON. Off when absent.
type EmbedderConfig struct {
	// URL is an Ollama-style /api/embed endpoint; it must be loopback.
	URL string `yaml:"url,omitempty" desc:"Embed endpoint on this machine (127.0.0.1, ::1, localhost), e.g. http://127.0.0.1:11434/api/embed"`
	// Command gets the request on stdin and prints the answer.
	Command []string `yaml:"command,omitempty" desc:"Command run from the project root: the request JSON on stdin, the answer JSON on stdout"`
	Model   string   `yaml:"model,omitempty" desc:"Model name sent with each request and keying the vector cache"`
	// Timeout is a Go duration per request; "" means DefaultEmbedTimeout.
	Timeout string `yaml:"timeout,omitempty" desc:"Per request, a Go duration (default 5s)"`
	// MinSimilarity: a cosine below it adds nothing to a memory's score.
	MinSimilarity float64 `yaml:"min_similarity,omitempty" desc:"Cosine below this adds nothing (default 0.5)"`
}

// Defaults for EmbedderConfig.
const (
	DefaultEmbedTimeout  = 5 * time.Second
	DefaultMinSimilarity = 0.5
)

// MemoryEmbedder is wf's embedder config, nil when none is set.
func (wf *WorkflowConfig) MemoryEmbedder() *EmbedderConfig {
	if wf == nil || wf.Memory == nil {
		return nil
	}
	return wf.Memory.Embedder
}

// TimeoutOrDefault parses Timeout; "" or a bad value is the default.
func (e *EmbedderConfig) TimeoutOrDefault() time.Duration {
	if d, err := time.ParseDuration(strings.TrimSpace(e.Timeout)); err == nil && d > 0 {
		return d
	}
	return DefaultEmbedTimeout
}

// MinSimilarityOrDefault is MinSimilarity, or the default when unset.
func (e *EmbedderConfig) MinSimilarityOrDefault() float64 {
	if e.MinSimilarity > 0 {
		return e.MinSimilarity
	}
	return DefaultMinSimilarity
}

// Problems are what makes the embedder unusable or unsafe, in words for
// lint and for search's fallback line. A URL that leaves this machine is
// refused: a hosted embedder sends memory text out, which only the owner
// may turn on (factory/shared-memory-phase-2-local-embeddings-behind-the-embedder-seam#D-2).
func (e *EmbedderConfig) Problems() []string {
	var out []string
	hasURL, hasCmd := strings.TrimSpace(e.URL) != "", len(e.Command) > 0
	switch {
	case hasURL && hasCmd:
		out = append(out, "memory embedder: set url: or command:, not both")
	case !hasURL && !hasCmd:
		out = append(out, "memory embedder: needs url: (a local /api/embed) or command:")
	case hasURL:
		if err := LoopbackURL(e.URL); err != nil {
			out = append(out, "memory embedder: "+err.Error())
		}
	case hasCmd && strings.TrimSpace(e.Command[0]) == "":
		out = append(out, "memory embedder: command: starts with an empty program")
	}
	if strings.TrimSpace(e.Model) == "" {
		out = append(out, "memory embedder: needs model: (it keys the vector cache)")
	}
	if t := strings.TrimSpace(e.Timeout); t != "" {
		if d, err := time.ParseDuration(t); err != nil || d <= 0 {
			out = append(out, fmt.Sprintf("memory embedder: timeout %q is not a positive Go duration (5s, 800ms)", t))
		}
	}
	if e.MinSimilarity < 0 || e.MinSimilarity >= 1 {
		out = append(out, fmt.Sprintf("memory embedder: min_similarity %g is outside 0-1", e.MinSimilarity))
	}
	return out
}

// LoopbackURL is nil when raw is an http(s) URL on this machine:
// 127.0.0.0/8, ::1 or localhost.
func LoopbackURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("url %q is not an http URL", raw)
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("url %q leaves this machine; a hosted embedder sends memory text out and is the owner's gate (owner_only), so it is refused", raw)
}
