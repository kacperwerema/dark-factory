package spend

import (
	"os"
	"sort"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/bench"
	"github.com/kacperwerema/dark-factory/internal/tokens"
	"gopkg.in/yaml.v3"
)

// PriceBook is the prices spend --kinds and --turns use, where each came
// from, and the plan's weekly limit, read from one prices file.
type PriceBook struct {
	// Path is the prices file read (the owner's prices.yaml or --prices).
	Path string
	// Prices are by model; Source says where each came from: the file's
	// path or "benchmark <name>".
	Prices map[string]bench.Price
	Source map[string]string
	// Limit is the file's weekly_limit:, nil without one.
	Limit *WeeklyLimit
	// Err is why the file didn't parse ("" when it did or is missing).
	Err string
}

// WeeklyLimit is what a full week of the plan allows: tokens or USD
// (prices.yaml weekly_limit: {usd: N} or {tokens: N}).
type WeeklyLimit struct {
	USD    float64 `yaml:"usd,omitempty" json:"usd,omitempty"`
	Tokens int64   `yaml:"tokens,omitempty" json:"tokens,omitempty"`
}

// LimitSnippet is the weekly_limit line's shape, for hints.
const LimitSnippet = "weekly_limit: {usd: N}   # or {tokens: N}: what a full week of the plan allows"

// LoadPriceBook reads path (PricesPath when ""), with the benchmark files'
// prices for models it doesn't name, like LoadPrices.
func LoadPriceBook(path string) PriceBook {
	if path == "" {
		path = PricesPath()
	}
	pb := PriceBook{Path: path, Prices: map[string]bench.Price{}, Source: map[string]string{}}
	if data, err := os.ReadFile(path); err == nil {
		var f struct {
			Prices map[string]bench.Price `yaml:"prices"`
			Limit  *WeeklyLimit           `yaml:"weekly_limit"`
		}
		if err := yaml.Unmarshal(data, &f); err != nil {
			pb.Err = err.Error()
		} else {
			for k, v := range f.Prices {
				pb.Prices[k], pb.Source[k] = v, path
			}
			if f.Limit != nil && (f.Limit.USD > 0 || f.Limit.Tokens > 0) {
				pb.Limit = f.Limit
			}
		}
	}
	for _, name := range bench.List() {
		b, err := bench.Load(name)
		if err != nil {
			continue
		}
		for k, v := range b.Prices {
			if _, ok := pb.Prices[k]; !ok {
				pb.Prices[k], pb.Source[k] = v, "benchmark "+name
			}
		}
	}
	return pb
}

// PriceState is one model's price as the output shows it.
type PriceState struct {
	Model string `json:"model"`
	// Key is the prices entry that priced it ("" when none did).
	Key string `json:"key,omitempty"`
	bench.Price
	// Status is "confirmed" (the entry says confirmed: true), "assumed"
	// (any other price) or "none" (no price: its cost is unknown).
	Status string `json:"status"`
	Source string `json:"source,omitempty"`
}

// States are the price of each model in models, sorted.
func (pb PriceBook) States(models map[string]bool) []PriceState {
	var out []PriceState
	for m := range models {
		st := PriceState{Model: m, Status: "none"}
		if key, ok := priceKey(m, pb.Prices); ok {
			st.Key, st.Price, st.Source, st.Status = key, pb.Prices[key], pb.Source[key], "assumed"
			if st.Price.Confirmed {
				st.Status = "confirmed"
			}
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Model < out[j].Model })
	return out
}

// priceKey is the prices key bench.PriceFor prices model with: its own,
// else the longest key that is a prefix of it.
func priceKey(model string, prices map[string]bench.Price) (string, bool) {
	if _, ok := prices[model]; ok {
		return model, true
	}
	best := ""
	for k := range prices {
		if k != "" && strings.HasPrefix(model, k) && len(k) > len(best) {
			best = k
		}
	}
	return best, best != ""
}

// Share is t's share of the weekly limit over days (the limit is
// pro rata: a 3-day window is held to 3/7 of it); ok is false without a
// limit, or for a USD limit when some tokens have no price.
func (l *WeeklyLimit) Share(t Tally, days float64) (share float64, ok bool) {
	if l == nil || days <= 0 {
		return 0, false
	}
	scale := days / 7
	if l.Tokens > 0 {
		return float64(t.Tokens()) / (float64(l.Tokens) * scale), true
	}
	if l.USD > 0 && t.Priced() {
		return t.USD / (l.USD * scale), true
	}
	return 0, false
}

// Text is the limit as a line: "$1,000 a week" or "5.0B tokens a week".
func (l *WeeklyLimit) Text() string {
	if l.Tokens > 0 {
		return tokens.Format(l.Tokens) + " tokens a week"
	}
	return FormatUSD(l.USD) + " a week"
}
