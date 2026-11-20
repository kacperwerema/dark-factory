package main

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/depttemplate"
	"github.com/kacperwerema/dark-factory/internal/partner"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// runDepartmentPartners lists the partners catalog: the outside tools a
// department can use per slot, with licence, what each needs and whether
// it costs money. It reads the bundled file only and changes nothing.
func runDepartmentPartners(ctx *Context, args []string) error {
	fs := newFlagSet("department partners", ctx)
	slot := fs.String("slot", "", "only this slot ("+strings.Join(partner.Slots, ", ")+")")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return &usageError{msg: "usage: issue-cli department partners [--slot <slot>] [--json]"}
	}
	if *slot != "" && !slices.Contains(partner.Slots, *slot) {
		return fmt.Errorf("no slot %q in the partners catalog; slots: %s", *slot, strings.Join(partner.Slots, ", "))
	}
	rows, err := partner.Catalog()
	if err != nil {
		return err
	}
	slots := partner.Slots
	if *slot != "" {
		slots = []string{*slot}
	}
	if ctx.JSONOutput {
		out := []partner.CatalogRow{}
		for _, s := range slots {
			out = append(out, partner.ForSlot(rows, s)...)
		}
		return writeJSON(ctx.Stdout, out)
	}
	w := ctx.Stdout
	for i, s := range slots {
		if i > 0 {
			fmt.Fprintln(w)
		}
		printSlot(w, s, partner.ForSlot(rows, s), slotTemplates(s))
	}
	fmt.Fprintf(w, "\nListing turns nothing on: a tool becomes a connector only when you add it to a department (%s). Read a licence again before relying on its row: licences change.\n",
		tracker.InjectProject(ctx.Project, "issue-cli connectors <department>"))
	return nil
}

// slotTemplates are the bundled templates that fill slot.
func slotTemplates(slot string) []string {
	var out []string
	for _, t := range depttemplate.List() {
		if slices.Contains(t.PartnerSlots, slot) {
			out = append(out, t.Key)
		}
	}
	return out
}

func printSlot(w io.Writer, slot string, rows []partner.CatalogRow, tmpls []string) {
	head := "== " + slot + " =="
	if len(tmpls) > 0 {
		head += " (templates: " + strings.Join(tmpls, ", ") + ")"
	}
	fmt.Fprintln(w, head)
	if len(rows) == 0 {
		fmt.Fprintln(w, "  no options in the catalog")
		return
	}
	if !slices.ContainsFunc(rows, func(r partner.CatalogRow) bool { return r.Default }) {
		fmt.Fprintln(w, "  no default: the choice is yours")
	}
	for _, r := range rows {
		mark := ""
		if r.Default {
			mark = " · default"
		}
		fmt.Fprintf(w, "  %s: %s, %s%s\n", r.ID, r.Name, r.Shape, mark)
		fmt.Fprintf(w, "    licence: %s (read %s from %s)\n", r.Licence, r.LicenceRead, r.LicenceURL)
		if r.AGPL() {
			fmt.Fprintf(w, "    exception: %s\n", r.Exception)
		}
		fmt.Fprintf(w, "    needs: %s · runs %s\n", r.Memory, r.Runs)
		fmt.Fprintf(w, "    money: %s\n", r.Money)
		if r.Note != "" {
			fmt.Fprintf(w, "    %s\n", r.Note)
		}
		if r.Setup != "" {
			fmt.Fprintf(w, "    stand it up: %s\n", r.Setup)
		}
		fmt.Fprintf(w, "    upstream: %s\n", r.Upstream)
	}
}

// partnerOptions are the catalog rows' ids for each of a plan's slots (the
// plan's own and its group's departments'): department add's JSON.
func partnerOptions(plan *depttemplate.Plan) map[string][]string {
	rows, err := partner.Catalog()
	if err != nil {
		return nil
	}
	out := map[string][]string{}
	for _, p := range append([]*depttemplate.Plan{plan}, plan.Children...) {
		for _, s := range p.Template.PartnerSlots {
			if _, ok := out[s]; ok {
				continue
			}
			ids := []string{}
			for _, r := range partner.ForSlot(rows, s) {
				ids = append(ids, r.ID)
			}
			out[s] = ids
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// printPartnerOptions names the catalog's tools for each of the plan's
// departments' slots: listed only, nothing installed or turned on.
func printPartnerOptions(w io.Writer, plan *depttemplate.Plan, proj *tracker.Project) {
	rows, err := partner.Catalog()
	if err != nil {
		fmt.Fprintf(w, "\nPartner options: %v\n", err)
		return
	}
	for _, p := range append([]*depttemplate.Plan{plan}, plan.Children...) {
		slots := p.Template.PartnerSlots
		if len(slots) == 0 {
			continue
		}
		fmt.Fprintf(w, "\nPartner options for %s's slots (listed only: nothing is installed or turned on):\n", p.Name)
		var setups []string
		for _, s := range slots {
			var names []string
			def := false
			for _, r := range partner.ForSlot(rows, s) {
				run := "always up"
				if r.Runs != "always" {
					run = r.Runs
				}
				if r.Default {
					run += ", default"
					def = true
					if r.Setup != "" {
						setups = append(setups, fmt.Sprintf("Stand up %s (%s) once on this machine: %s (it writes no config: you turn the %s connector on in %s)", r.Name, s, tracker.InjectProject(proj, r.Setup), s, valueOr(proj.ConfigPath, "projects.yaml")))
					}
				}
				names = append(names, fmt.Sprintf("%s (%s, %s)", r.Name, r.Licence, run))
			}
			line := strings.Join(names, ", ")
			switch {
			case len(names) == 0:
				line = "no options in the catalog"
			case !def:
				line += "; no default: your choice"
			}
			fmt.Fprintf(w, "  %s: %s\n", s, line)
		}
		fmt.Fprintf(w, "Licence, needs and money for each: %s\n", tracker.InjectProject(proj, "issue-cli department partners --slot <slot>"))
		for _, l := range setups {
			fmt.Fprintln(w, l)
		}
	}
}
