package testids

func init() {
	IDs = append(IDs,
		// YOLO mode (templates/_workshop_yolo.html): the switch on L1, the band
		// under the header on every project page, the turn-on dialog.
		"yolo.switch",
		"yolo.count",
		"yolo.band",
		"yolo.band.passed", "yolo.band.never",
		"yolo.band.extend",
		"yolo.band.off",
		"yolo.dialog",
		"yolo.dialog.gates",
		"yolo.dialog.guards",
		"yolo.dialog.ci_valve",
		"yolo.dialog.words",
		"yolo.dialog.go",
	)
}
