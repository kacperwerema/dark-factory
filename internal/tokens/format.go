package tokens

import (
	"math"
	"strconv"
	"strings"
)

// Format is a token count in short: 950, 12.3k, 4.1M, 2B. Every page, report
// and CLI line that shows tokens goes through it (TestOneTokenFormatter).
func Format(n int64) string {
	f := float64(n)
	switch {
	case n < 1000:
		return strconv.FormatInt(n, 10)
	case n < 1_000_000:
		return short(f/1e3) + "k"
	case n < 1_000_000_000:
		return short(f/1e6) + "M"
	}
	return short(f/1e9) + "B"
}

func short(f float64) string {
	if f >= 100 {
		return strconv.FormatFloat(math.Round(f), 'f', 0, 64)
	}
	return strings.TrimSuffix(strconv.FormatFloat(f, 'f', 1, 64), ".0")
}
