package testids

func init() {
	IDs = append(IDs,
		// The review after YOLO (templates/yolo_review.html,
		// /p/<project>/yolo/review): one entry per issue with evidence.
		"yolo_review.page",
		"yolo_review.summary",
		"yolo_review.issues",
		"yolo_review.group",
		"yolo_review.group.detail",
		"yolo_review.group.evidence",
		"yolo_review.gate",
		"yolo_review.check",
		"yolo_review.check.tick",
		"yolo_review.check.agree",
		"yolo_review.question",
		"yolo_review.decision",
		"yolo_review.words",
		"yolo_review.msg",
		"yolo_review.agree",
		"yolo_review.back",
		"yolo_review.pager",
		"yolo_review.empty",
	)
}
