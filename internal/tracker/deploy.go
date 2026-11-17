package tracker

import (
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// The preconditions a deploy step may name in needs: (docs/workflow.md,
// Deploy).
const (
	// NeedMainGreen: local CI is green on main's head.
	NeedMainGreen = "main-green"
	// NeedNoLiveWorkers: no worker session of the project is live.
	NeedNoLiveWorkers = "no-live-workers"
	// NeedOwnerOK: the owner's words are passed with --quote.
	NeedOwnerOK = "owner-ok"
)

// DeployNeedWords are the needs: words in the order the docs list them.
var DeployNeedWords = []string{NeedMainGreen, NeedNoLiveWorkers, NeedOwnerOK}

// DeployStepTimeout is a step's limit when it names no timeout:.
const DeployStepTimeout = 30 * time.Minute

// DeployStep is one entry of deploy:, the project's own deploy walked in
// order by issue-cli deploy.
type DeployStep struct {
	Name    string      `yaml:"name" desc:"The step's name, unique in deploy: (issue-cli deploy --step <name> runs only it)"`
	Run     string      `yaml:"run" desc:"Command run with sh -c from the project's main checkout; a non-zero exit makes the step red and stops the deploy"`
	Needs   DeployNeeds `yaml:"needs,omitempty" desc:"Preconditions checked before the step runs, a word or a list: main-green (local CI green on main's head), no-live-workers, owner-ok (the owner's words with --quote)"`
	Probe   string      `yaml:"probe,omitempty" desc:"Command run with sh -c after run succeeds; it must exit 0 or the step is red (e.g. curl -fsS https://example.com/health)"`
	Timeout string      `yaml:"timeout,omitempty" desc:"Limit for run and probe together, a Go duration (default 30m)"`
}

// DeployNeeds is a step's needs:, written as one word or a list.
type DeployNeeds []string

// UnmarshalYAML accepts a plain word as a one-item list.
func (n *DeployNeeds) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		if v := strings.TrimSpace(node.Value); v != "" {
			*n = DeployNeeds{v}
		}
		return nil
	}
	var list []string
	if err := node.Decode(&list); err != nil {
		return err
	}
	*n = list
	return nil
}

// Has reports whether the step names need.
func (n DeployNeeds) Has(need string) bool {
	for _, v := range n {
		if strings.TrimSpace(v) == need {
			return true
		}
	}
	return false
}

// Limit is the step's timeout:, DeployStepTimeout when unset.
func (s DeployStep) Limit() time.Duration {
	if d, err := time.ParseDuration(strings.TrimSpace(s.Timeout)); err == nil && d > 0 {
		return d
	}
	return DeployStepTimeout
}

// CheckDeploy names the first problem in a deploy: list: a step with no
// name or run, a name used twice, an unknown needs: word or a bad timeout:.
func CheckDeploy(steps []DeployStep) error {
	seen := map[string]bool{}
	for i, s := range steps {
		name := strings.TrimSpace(s.Name)
		if name == "" {
			return fmt.Errorf("deploy: step %d has no name", i+1)
		}
		if seen[name] {
			return fmt.Errorf("deploy: two steps are called %q; names must be unique", name)
		}
		seen[name] = true
		if strings.TrimSpace(s.Run) == "" {
			return fmt.Errorf("deploy: step %q has no run: command", name)
		}
		for _, need := range s.Needs {
			if !knownNeed(strings.TrimSpace(need)) {
				return fmt.Errorf("deploy: step %q needs %q; known needs: %s", name, need, strings.Join(DeployNeedWords, ", "))
			}
		}
		if t := strings.TrimSpace(s.Timeout); t != "" {
			if d, err := time.ParseDuration(t); err != nil || d <= 0 {
				return fmt.Errorf("deploy: step %q has timeout %q; want a Go duration such as 10m", name, s.Timeout)
			}
		}
	}
	return nil
}

func knownNeed(need string) bool {
	for _, w := range DeployNeedWords {
		if need == w {
			return true
		}
	}
	return false
}

func cloneDeploy(steps []DeployStep) []DeployStep {
	if steps == nil {
		return nil
	}
	out := make([]DeployStep, len(steps))
	for i, s := range steps {
		s.Needs = append(DeployNeeds(nil), s.Needs...)
		out[i] = s
	}
	return out
}
