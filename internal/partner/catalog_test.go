package partner

import (
	"regexp"
	"strings"
	"testing"
)

// TestPartnersCatalogRowsComplete: every row of the bundled catalog has a
// slot, a licence with the URL and date it was read, what it needs and
// whether it costs money; an AGPL row names its exception's decision ref
// or says none; a slot has at most one default, and social's is Postiz,
// the owner's pick (open-source partners epic D-5).
func TestPartnersCatalogRowsComplete(t *testing.T) {
	rows, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("the bundled partners catalog has no rows")
	}
	slots := map[string]int{}
	agpl := 0
	for _, r := range rows {
		if r.Slot == "" || r.Licence == "" || r.LicenceRead == "" || r.LicenceURL == "" {
			t.Errorf("%s: slot %q, licence %q, read %q, url %q: each is needed", r.ID, r.Slot, r.Licence, r.LicenceRead, r.LicenceURL)
		}
		if r.AGPL() {
			agpl++
			if r.Exception == "" {
				t.Errorf("%s is AGPL and names no exception (a decision ref or none: <why>)", r.ID)
			}
		}
		if r.Default {
			slots[r.Slot]++
		}
	}
	if agpl == 0 {
		t.Error("no AGPL row: the exception rule is not exercised")
	}
	if social := ForSlot(rows, "social"); len(social) < 2 || social[0].ID != "postiz" || !social[0].Default {
		t.Errorf("the social slot: want Postiz the default, listed first, and its swap candidate after it: %+v", social)
	}
	for s, n := range slots {
		if n > 1 {
			t.Errorf("slot %s has %d defaults", s, n)
		}
	}

	// The rules refuse each incomplete row.
	good := rows[0]
	for name, mut := range map[string]func(*CatalogRow){
		"no licence":         func(r *CatalogRow) { r.Licence = "" },
		"no read date":       func(r *CatalogRow) { r.LicenceRead = "" },
		"bad read date":      func(r *CatalogRow) { r.LicenceRead = "Nov 2026" },
		"no slot":            func(r *CatalogRow) { r.Slot = "" },
		"unknown slot":       func(r *CatalogRow) { r.Slot = "crm" },
		"agpl, no exception": func(r *CatalogRow) { r.Licence = "AGPL-3.0"; r.Exception = "" },
		"agpl, bare none":    func(r *CatalogRow) { r.Licence = "AGPL-3.0"; r.Exception = "none" },
		"exception on MIT":   func(r *CatalogRow) { r.Licence = "MIT"; r.Exception = "x/y#D-1" },
		"no money":           func(r *CatalogRow) { r.Money = "" },
		"unknown runs":       func(r *CatalogRow) { r.Runs = "sometimes" },
	} {
		r := good
		mut(&r)
		if err := CheckRows([]CatalogRow{r}); err == nil {
			t.Errorf("%s: CheckRows accepted it", name)
		}
	}
	for _, ex := range []string{"factory/some-epic#D-4", "none: a fallback only"} {
		r := good
		r.Licence, r.Exception = "AGPL-3.0", ex
		if err := CheckRows([]CatalogRow{r}); err != nil {
			t.Errorf("AGPL with exception %q refused: %v", ex, err)
		}
	}
	if err := CheckRows([]CatalogRow{good, good}); err == nil {
		t.Error("an id twice was accepted")
	}
	if _, err := ParseCatalog([]byte("partners:\n  - id: x\n    price: 3\n")); err == nil || !strings.Contains(err.Error(), "price") {
		t.Errorf("an unknown key was accepted: %v", err)
	}
}

// TestPartnersCatalogHoldsNoPrice: money is in words, never a price; the
// file is public (factory/department-budgets-what-each-department-spends-in-tokens-and-money-shown-in-its-room#D-2) (AC4).
func TestPartnersCatalogHoldsNoPrice(t *testing.T) {
	rows, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	price := regexp.MustCompile(`[$€£¥]|\b(?:USD|EUR|GBP|PLN|zł)\b|\d+(?:[.,]\d+)?\s*(?:dollars?|euros?|cents?)\b|\bper (?:month|year)\b.*\d|\d.*\bper (?:month|year)\b`)
	for _, r := range rows {
		for field, v := range map[string]string{"money": r.Money, "memory": r.Memory, "note": r.Note} {
			if m := price.FindString(v); m != "" {
				t.Errorf("%s %s holds a price (%q): %q", r.ID, field, m, v)
			}
		}
	}
	for _, s := range []string{"$2 a month", "€0.25 per 1,000", "10 dollars"} {
		if !price.MatchString(s) {
			t.Errorf("the price pattern misses %q", s)
		}
	}
}
