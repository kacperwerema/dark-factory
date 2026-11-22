package outputs

import "testing"

// A report that cites a vendor price or an API field needs a source URL or
// an UNVERIFIED list; one that cites neither needs nothing.
func TestUnsourcedVendorFact(t *testing.T) {
	cases := []struct {
		name, text, want string
	}{
		{"price, nothing else", "# R\n\nThe Pro plan costs $29/mo.\n", "a price"},
		{"price as a code", "# R\n\nHosting is 12 EUR a month.\n", "a price"},
		{"price in zloty", "# R\n\nAbonament: 49 zł.\n", "a price"},
		{"api field", "# R\n\nThe API takes the `max_tokens` and `top_k` parameters.\n", "an API field"},
		{"parameter", "# R\n\nSet the API's `cursor` parameter to page.\n", "an API field"},
		{"webhook header", "# R\n\nThe webhook signs with the `X-Sig` header.\n", "an API field"},
		{"price with a URL", "# R\n\nThe Pro plan costs $29/mo (https://example.com/pricing).\n", ""},
		{"api with an UNVERIFIED heading", "# R\n\nThe API takes `max_tokens`.\n\n## UNVERIFIED\n\n- `max_tokens`: the docs page was unreachable\n", ""},
		{"UNVERIFIED lead-in saying none", "# R\n\nThe Pro plan costs $29/mo.\n\nUNVERIFIED: none.\n", ""},
		{"Unverified heading with a number", "# R\n\nCosts $5.\n\n### Unverified facts\n\n- the price\n", ""},
		{"field word alone", "# R\n\nThe `status` field shows each issue.\n", ""},
		{"api word alone", "# R\n\nMove guidance to `prompt_append:` (API, Factory).\n", ""},
		{"no vendor facts", "# R\n\nThe board shows the `status` of each issue.\n", ""},
		{"price only in a code block", "# R\n\n```\necho $5\n```\n", ""},
		{"unverified mid-sentence is no list", "# R\n\nCosts $5, which stays unverified.\n", "a price"},
		{"indented unverified line is no list", "# R\n\nCosts $5.\n\n  UNVERIFIED: none\n", "a price"},
	}
	for _, c := range cases {
		got, _ := UnsourcedVendorFact(c.text)
		if got != c.want {
			t.Errorf("%s: UnsourcedVendorFact = %q, want %q", c.name, got, c.want)
		}
	}
}
