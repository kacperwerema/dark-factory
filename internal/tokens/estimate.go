// Package tokens provides a coarse token-count approximation used by the
// workflow stats view.
//
// Estimate uses len(s)/4, the well-known back-of-envelope proxy for
// Claude/GPT-family BPE tokenization. It is intentionally cheap and tokenizer-
// free; numbers should be read as "approximate tokens", not exact counts for
// any specific model.
package tokens

// Estimate returns an approximate token count for s.
func Estimate(s string) int {
	return EstimateBytes(len(s))
}

// EstimateBytes is Estimate for a text of n bytes.
func EstimateBytes(n int) int {
	return n / 4
}
