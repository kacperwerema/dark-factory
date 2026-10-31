package yolo

import (
	"time"

	"github.com/michal-franc/issue-viewer/internal/ci"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

func init() {
	// The dispatch prompt asks whether YOLO is on, to brief the worker with
	// the YOLO rule instead of "stop at every gate".
	tracker.YOLOOn = func(workDir, project string) bool {
		if workDir == "" || project == "" {
			return false
		}
		_, on, err := Active(ci.RepoRoot(workDir), project, time.Now())
		return err == nil && on
	}
}
