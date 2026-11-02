package spend

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/bench"
	"gopkg.in/yaml.v3"
)

// PricesPath is the owner's prices file:
// $XDG_CONFIG_HOME/ai-workflow-engine/prices.yaml (else ~/.config/…).
// The engine bundles no prices (D-2): an estimate on a guessed price would
// mislead.
func PricesPath() string {
	base := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "ai-workflow-engine", "prices.yaml")
}

// PricesSnippet is the prices file's shape, for hints.
const PricesSnippet = `prices:                 # USD per million tokens
  <model>: {in: 0, out: 0, cache_read: 0, cache_write: 0}`

// LoadPrices is the prices file's prices, with the owner's benchmark files'
// prices for models it doesn't name. A file that doesn't parse gives none.
func LoadPrices() map[string]bench.Price {
	out := map[string]bench.Price{}
	if data, err := os.ReadFile(PricesPath()); err == nil {
		var f struct {
			Prices map[string]bench.Price `yaml:"prices"`
		}
		if yaml.Unmarshal(data, &f) == nil {
			for k, v := range f.Prices {
				out[k] = v
			}
		}
	}
	for _, name := range bench.List() {
		b, err := bench.Load(name)
		if err != nil {
			continue
		}
		for k, v := range b.Prices {
			if _, ok := out[k]; !ok {
				out[k] = v
			}
		}
	}
	return out
}

// FormatTokens is a token count in short: 950, 12.3k, 4.1M, 2.0B.
func FormatTokens(n int64) string {
	f := float64(n)
	switch {
	case n < 1000:
		return strconv.FormatInt(n, 10)
	case n < 1_000_000:
		return trimZero(f/1e3) + "k"
	case n < 1_000_000_000:
		return trimZero(f/1e6) + "M"
	}
	return trimZero(f/1e9) + "B"
}

func trimZero(f float64) string {
	if f >= 100 {
		return strconv.FormatFloat(math.Round(f), 'f', 0, 64)
	}
	return strings.TrimSuffix(strconv.FormatFloat(f, 'f', 1, 64), ".0")
}

// FormatUSD is a cost: $0.42, $12.40, $1,234.
func FormatUSD(v float64) string {
	if v >= 1000 {
		s := strconv.FormatFloat(math.Round(v), 'f', 0, 64)
		for i := len(s) - 3; i > 0; i -= 3 {
			s = s[:i] + "," + s[i:]
		}
		return "$" + s
	}
	return fmt.Sprintf("$%.2f", v)
}

// CostText is a tally's estimated cost: "$3.40 est.", "≥ $3.40 est." when
// some tokens have no price, "cost unknown" when none has one, "" for
// nothing spent.
func CostText(t Tally) string {
	switch {
	case t.Empty():
		return ""
	case !t.Priced():
		return "cost unknown"
	case !t.CostKnown():
		return "≥ " + FormatUSD(t.USD) + " est."
	}
	return FormatUSD(t.USD) + " est."
}

// Line is a tally in one line: "1.2M tokens · $3.40 est.".
func Line(t Tally) string {
	if t.Empty() {
		return "0 tokens"
	}
	return FormatTokens(t.Tokens()) + " tokens · " + CostText(t)
}
