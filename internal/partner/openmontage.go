package partner

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// OpenMontage reads an OpenMontage checkout (a separate AGPL program the
// user installs; nothing of it is in this repo): projects/<id>/project.json,
// the stage order from pipeline_defs/<pipeline_type>.yaml and each
// projects/<id>/checkpoint_<stage>.json, parsed as data only. The current
// stage is the first stage, in the pipeline's order, without a completed
// checkpoint. Formats it knows: project.json and checkpoints at version
// "1.0" (OpenMontageVersions); anything else is ErrFormat.
type OpenMontage struct{}

// OpenMontageVersions are the project.json and checkpoint versions the
// adapter understands.
var OpenMontageVersions = []string{"1.0"}

// openMontageHealth is what Backlot (their local viewer) answers on while
// it runs; only an explicit live_port: links it.
const openMontageHealth = "/api/health"

func init() { Register("openmontage", OpenMontage{}) }

type omProject struct {
	Version      string `json:"version"`
	ProjectID    string `json:"project_id"`
	PipelineType string `json:"pipeline_type"`
}

type omPipeline struct {
	Stages []struct {
		Name  string `yaml:"name"`
		Gated bool   `yaml:"human_approval_default"`
	} `yaml:"stages"`
}

// omStage is one stage of the pipeline, in order; Gated when the pipeline
// asks for the human's approval there by default.
type omStage struct {
	Name  string
	Gated bool
}

type omCheckpoint struct {
	Version   string                     `json:"version"`
	Stage     string                     `json:"stage"`
	Status    string                     `json:"status"`
	Timestamp string                     `json:"timestamp"`
	Artifacts map[string]json.RawMessage `json:"artifacts"`
	Review    json.RawMessage            `json:"review"`
	Cost      *struct {
		Spent     float64 `json:"total_spent_usd"`
		Reserved  float64 `json:"total_reserved_usd"`
		Remaining float64 `json:"budget_remaining_usd"`
	} `json:"cost_snapshot"`
	Error json.RawMessage `json:"error"`
	// ApprovalRequired and Approved are the checkpoint's own gate flags:
	// human_approval_required and the human_approved the agent writes.
	ApprovalRequired bool `json:"human_approval_required"`
	Approved         bool `json:"human_approved"`
}

// omRead reads project id's files: its stages in order and the checkpoints
// there are. found is false (with no error) when the project isn't there.
// Every checkpoint is read and its version checked before anything is
// returned: one file in an unknown format means nothing at all.
func omRead(dir, id string) (stages []omStage, cps map[string]omCheckpoint, found bool, err error) {
	pdir := filepath.Join(dir, "projects", id)
	raw, err := ReadFile(filepath.Join(pdir, "project.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, err
	}
	var proj omProject
	if err := json.Unmarshal(raw, &proj); err != nil {
		return nil, nil, false, fmt.Errorf("%w: project.json: %v", ErrFormat, err)
	}
	if !knownVersion(proj.Version) {
		return nil, nil, false, fmt.Errorf("%w: project.json version %q", ErrFormat, proj.Version)
	}
	stages, err = omStages(dir, proj.PipelineType)
	if err != nil {
		return nil, nil, false, err
	}
	cps = map[string]omCheckpoint{}
	for _, stage := range stages {
		raw, err := ReadFile(filepath.Join(pdir, "checkpoint_"+stage.Name+".json"))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, false, err
		}
		var cp omCheckpoint
		if err := json.Unmarshal(raw, &cp); err != nil {
			return nil, nil, false, fmt.Errorf("%w: checkpoint_%s.json: %v", ErrFormat, stage.Name, err)
		}
		if !knownVersion(cp.Version) {
			return nil, nil, false, fmt.Errorf("%w: checkpoint_%s.json version %q", ErrFormat, stage.Name, cp.Version)
		}
		switch cp.Status {
		case "in_progress", "awaiting_human", "completed", "failed":
		default:
			return nil, nil, false, fmt.Errorf("%w: checkpoint_%s.json status %q", ErrFormat, stage.Name, cp.Status)
		}
		cps[stage.Name] = cp
	}
	return stages, cps, true, nil
}

// Status implements Adapter.
func (OpenMontage) Status(cfg tracker.Partner, dir, id string) (Status, error) {
	st := Status{Project: id, State: StateNone}
	omStagesList, cps, found, err := omRead(dir, id)
	if err != nil || !found {
		return st, err
	}
	stages := make([]string, len(omStagesList))
	for i, s := range omStagesList {
		stages[i] = s.Name
	}
	if cfg.LivePort > 0 {
		base := "http://127.0.0.1:" + strconv.Itoa(cfg.LivePort)
		st.LiveURL, st.HealthURL = base+"/p/"+id, base+openMontageHealth
	}

	// The newest cost snapshot, whichever stage wrote it.
	var newest time.Time
	for _, stage := range stages {
		cp, ok := cps[stage]
		if !ok || cp.Cost == nil {
			continue
		}
		if ts := parseTime(cp.Timestamp); st.Cost == nil || !ts.Before(newest) {
			st.Cost = &Cost{Spent: cp.Cost.Spent, Reserved: cp.Cost.Reserved, Remaining: cp.Cost.Remaining}
			newest = ts
		}
	}

	st.State = StateDone
	for _, stage := range stages {
		cp, ok := cps[stage]
		if ok && cp.Status == "completed" {
			continue
		}
		st.Stage, st.State = stage, StateWorking
		if !ok {
			break
		}
		st.Since = parseTime(cp.Timestamp)
		st.Summary = reviewSummary(cp.Review)
		for name := range cp.Artifacts {
			st.Artifacts = append(st.Artifacts, name)
		}
		sort.Strings(st.Artifacts)
		switch cp.Status {
		case "awaiting_human":
			st.State = StateAwaiting
		case "failed":
			st.State, st.Error = StateFailed, errorText(cp.Error)
		}
		break
	}
	return st, nil
}

// omStages is the stage order of pipeline from the checkout's own
// pipeline_defs (never hard-coded: it changes upstream).
func omStages(dir, pipeline string) ([]omStage, error) {
	if pipeline == "" || strings.ContainsAny(pipeline, `/\`) || strings.HasPrefix(pipeline, ".") {
		return nil, fmt.Errorf("%w: project.json pipeline_type %q", ErrFormat, pipeline)
	}
	raw, err := ReadFile(filepath.Join(dir, "pipeline_defs", pipeline+".yaml"))
	if err != nil {
		return nil, fmt.Errorf("pipeline %s: %w", pipeline, err)
	}
	var def omPipeline
	if err := yaml.Unmarshal(raw, &def); err != nil {
		return nil, fmt.Errorf("%w: pipeline_defs/%s.yaml: %v", ErrFormat, pipeline, err)
	}
	var out []omStage
	for _, s := range def.Stages {
		name := strings.TrimSpace(s.Name)
		if name == "" || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
			return nil, fmt.Errorf("%w: pipeline_defs/%s.yaml stage %q", ErrFormat, pipeline, s.Name)
		}
		out = append(out, omStage{Name: name, Gated: s.Gated})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: pipeline_defs/%s.yaml has no stages", ErrFormat, pipeline)
	}
	return out, nil
}

func knownVersion(v string) bool {
	for _, k := range OpenMontageVersions {
		if v == k {
			return true
		}
	}
	return false
}

func parseTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999"} {
		if t, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
			return t
		}
	}
	return time.Time{}
}

// reviewSummary is a checkpoint review's summary line, when the review is
// an object with a summary string (the schema leaves review free-form).
func reviewSummary(raw json.RawMessage) string {
	var r struct {
		Summary string `json:"summary"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &r) != nil {
		return ""
	}
	return strings.TrimSpace(r.Summary)
}

// errorText is a failed checkpoint's error: a string, or an object's
// message.
func errorText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var o struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &o) == nil && o.Message != "" {
		return o.Message
	}
	return string(raw)
}

// Approvals implements Approver: each stage with a checkpoint, gated when
// the pipeline's human_approval_default or the checkpoint's
// human_approval_required says so, approved when human_approved is set.
func (OpenMontage) Approvals(_ tracker.Partner, dir, id string) ([]Approval, error) {
	stages, cps, found, err := omRead(dir, id)
	if err != nil || !found {
		return nil, err
	}
	var out []Approval
	for _, s := range stages {
		cp, ok := cps[s.Name]
		if !ok {
			continue
		}
		out = append(out, Approval{Stage: s.Name, Gated: s.Gated || cp.ApprovalRequired, Approved: cp.Approved, At: parseTime(cp.Timestamp)})
	}
	return out, nil
}

// ApprovalHow implements Approver: OpenMontage's own approval is the
// checkpoint's human_approved flag, with a decision_log entry saying why.
func (OpenMontage) ApprovalHow(stage, ref string) string {
	return fmt.Sprintf("re-write checkpoint_%s.json with human_approved=True and append a decision_log entry for stage %s quoting %s", stage, stage, ref)
}
