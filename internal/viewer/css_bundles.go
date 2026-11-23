package viewer

import (
	"html/template"
	"strings"
)

// cssBundles lists each stylesheet's parts in cascade order. style.css and
// workshop.css were one file each; they are split by view so branches that
// change different pages edit different files (no file under static/ over
// 1000 lines, TestCSSPartsListedAndShort). Every page that loaded the whole
// file loads every part in this order, so the cascade is the one the single
// file had: a rule moves between parts only in step with this list.
var cssBundles = map[string][]string{
	"style": {
		"style.css", // reset, fonts, theme tokens, layout, shared pieces, sidebar
		"style-list.css",
		"style-detail.css",
		"style-board.css",
		"style-docs.css",
		"style-board-filter.css",
		"style-comments.css",
		"style-actions.css",
		"style-modals.css",
		"style-detail-tabs.css",
		"style-workbench.css",
		"style-agents.css",
		"style-canvas.css",
		"style-workboard.css",
		"style-systems.css",
		"style-status.css",
		"style-project-pages.css",
		"style-stats.css",
		"style-updates.css",
	},
	"workshop": {
		"workshop.css", // tokens, motion, header, layout, lots, right panel
		"workshop-company.css",
		"workshop-hall.css",
		"workshop-report.css",
		"workshop-health.css",
		"workshop-station.css",
		"workshop-systems.css",
		"workshop-decisions.css",
		"workshop-docs.css",
		"workshop-updates.css",
		"workshop-retro.css",
		"workshop-release-yolo.css",
		"workshop-ceo.css",
		"workshop-updates-all.css",
		"workshop-shelf.css",
		"workshop-needs-you.css",
		"workshop-hall-parts.css",
		"workshop-crew.css",
		"workshop-system.css",
		"workshop-dept-add.css",
		"workshop-owner-checks.css",
		"workshop-yolo-review.css",
		"workshop-memory.css",
		"workshop-hq-report.css",
		"workshop-reader.css",
		"workshop-floor.css",
		"workshop-phone-taps.css",
		"workshop-crew-all.css",
		"workshop-codemap.css",
		"workshop-phone.css",
		"workshop-menu.css",
		"workshop-phone-screen.css",
	},
}

// stylesheets is the template func {{stylesheets "style"}}: one <link> per
// part of the bundle, in order. An unknown name panics at render, so a typo
// fails the page's test rather than shipping an unstyled page.
func stylesheets(name string) template.HTML {
	parts, ok := cssBundles[name]
	if !ok {
		panic("stylesheets: no CSS bundle " + name)
	}
	var b strings.Builder
	for i, p := range parts {
		if i > 0 {
			b.WriteString("\n    ")
		}
		b.WriteString(`<link rel="stylesheet" href="/static/` + p + `">`)
	}
	return template.HTML(b.String())
}
