package depttemplate

import (
	"slices"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/partner"
)

// TestTemplatePartnerSlotsKnown: every bundled template's partner_slots:
// is a slot of the partners catalog with at least one row, and a slot the
// catalog doesn't have (or one named twice) keeps a template from loading
// (AC5).
func TestTemplatePartnerSlotsKnown(t *testing.T) {
	rows, err := partner.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	named := 0
	for _, tm := range List() {
		for _, s := range tm.PartnerSlots {
			named++
			if !slices.Contains(partner.Slots, s) {
				t.Errorf("template %s: slot %q is not a catalog slot", tm.Key, s)
			}
			if len(partner.ForSlot(rows, s)) == 0 {
				t.Errorf("template %s: slot %s has no row in the catalog", tm.Key, s)
			}
		}
	}
	if named == 0 {
		t.Fatal("no bundled template names a partner slot")
	}
	sales, err := Load("sales-b2c")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"analytics", "email"} {
		if !slices.Contains(sales.PartnerSlots, s) {
			t.Errorf("sales-b2c's partner_slots %v lacks %s", sales.PartnerSlots, s)
		}
	}
	if err := checkPartnerSlots("x", []string{"crm"}); err == nil || !strings.Contains(err.Error(), "not a slot") {
		t.Errorf("unknown slot: %v", err)
	}
	if err := checkPartnerSlots("x", []string{"email", "email"}); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("slot twice: %v", err)
	}
}
