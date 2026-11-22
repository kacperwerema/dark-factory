package outputs

import (
	"regexp"
	"strings"
)

var (
	// A price: a currency sign beside a number ($12, € 5, 9.99 zł) or a
	// number beside a currency code (12 USD, 4,50 EUR).
	priceSign = regexp.MustCompile(`[$€£¥]\s?\d|\d\s?(?:zł|€|£)`)
	priceCode = regexp.MustCompile(`\b\d[\d.,]*\s?(?:USD|EUR|GBP|PLN|CHF|JPY|CAD|AUD)\b|\b(?:USD|EUR|GBP|PLN|CHF|JPY|CAD|AUD)\s?\d`)
	// An API field: a line that names an API (or an endpoint or webhook)
	// and a field (or parameter, header, key) and quotes a name in
	// backticks. Either word alone is too common in a report on our own code.
	apiWord   = regexp.MustCompile(`(?i)\b(?:apis?|endpoints?|webhooks?)\b`)
	fieldWord = regexp.MustCompile(`(?i)\b(?:fields?|param(?:eter)?s?|headers?|keys?|attributes?|propert(?:y|ies)|query string|request body|payload)\b`)
	backticks = regexp.MustCompile("`[^`\n]+`")
	anyURL    = regexp.MustCompile(`https?://[^\s)>\]]+`)
	// The UNVERIFIED list: a heading, or a lead-in from the margin
	// ("UNVERIFIED:", "**Unverified** facts:"), that starts with the word.
	unverifiedHead = regexp.MustCompile(`(?i)^[*_]*unverified\b`)
)

// VendorFacts is what makes a report one that cites vendor facts: the first
// price and the first API field line it holds, outside fenced code. Both are
// empty for a report that cites neither.
func VendorFacts(markdown string) (price, api string) {
	fenced := false
	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced || trimmed == "" {
			continue
		}
		if price == "" && (priceSign.MatchString(trimmed) || priceCode.MatchString(trimmed)) {
			price = trimmed
		}
		if api == "" && apiWord.MatchString(trimmed) && fieldWord.MatchString(trimmed) && backticks.MatchString(trimmed) {
			api = trimmed
		}
		if price != "" && api != "" {
			break
		}
	}
	return price, api
}

// HasSourceURL reports whether the report links any http(s) page.
func HasSourceURL(markdown string) bool {
	return anyURL.MatchString(markdown)
}

// HasUnverifiedList reports whether the report has an UNVERIFIED list: a
// heading that starts with UNVERIFIED (any level, any case), or a line from
// the margin that does ("UNVERIFIED: none."). What stands under it is the
// worker's: "none" is an answer too.
func HasUnverifiedList(markdown string) bool {
	fenced := false
	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if m := headingLine.FindStringSubmatch(line); m != nil {
			if unverifiedHead.MatchString(strings.TrimSpace(m[2])) {
				return true
			}
			continue
		}
		if line != "" && line[0] != ' ' && line[0] != '\t' && unverifiedHead.MatchString(trimmed) {
			return true
		}
	}
	return false
}

// UnsourcedVendorFact is the line of a report that cites a vendor price or
// an API field while the report links no page and lists nothing as
// UNVERIFIED: the facts read from a search summary, with nothing to check
// them by. Empty when the report cites none, links a page or has the list.
func UnsourcedVendorFact(markdown string) (what, line string) {
	price, api := VendorFacts(markdown)
	if price == "" && api == "" {
		return "", ""
	}
	if HasSourceURL(markdown) || HasUnverifiedList(markdown) {
		return "", ""
	}
	if price != "" {
		return "a price", price
	}
	return "an API field", api
}
