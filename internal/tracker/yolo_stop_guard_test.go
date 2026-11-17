package tracker

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"gopkg.in/yaml.v3"
)

var stopWordRe = regexp.MustCompile(`(?i)\bstop\b`)

// stopWordOK are prompts whose "stop" YOLO leaves standing, each with why.
var stopWordOK = map[string]string{
	"sales-b2c.yaml/types/support-digest/statuses/research.prompt": "stops the digest's own work (no source, no digest), not a wait for the owner",
	"sales-b2c.yaml/types/launch/statuses/publish.prompt_append":   "publishing outside the machine is an owner_only step YOLO never passes",
	"workflow.yaml/statuses/shipping.prompt":                       "a dirty main checkout is a blocker for the lead, the question: comment case of the YOLO rule",
}

// collectPrompts walks a decoded workflow and calls fn for each prompt: or
// prompt_append: string that sits in a mapping with a status name.
func collectPrompts(n any, path string, fn func(path, prompt string)) {
	switch v := n.(type) {
	case map[string]any:
		if _, named := v["name"]; named {
			for _, k := range []string{"prompt", "prompt_append"} {
				if s, ok := v[k].(string); ok {
					fn(path+"/"+v["name"].(string)+"."+k, s)
				}
			}
		}
		for k, c := range v {
			collectPrompts(c, path+"/"+k, fn)
		}
	case []any:
		for _, c := range v {
			collectPrompts(c, path, fn)
		}
	}
}

// TestNoStatusGuidanceStopsWithoutTheYOLOException: a status prompt that
// still says "stop" once YOLOGuidance has dropped its ask-and-stop sentences
// contradicts YOLO's "don't stop at gates" (the chore prompt did).
func TestNoStatusGuidanceStopsWithoutTheYOLOException(t *testing.T) {
	files, _ := filepath.Glob("../../cmd/issue-cli/templates/workflow/*.yaml")
	files = append(files, "../../workflow.yaml")
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var doc any
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		collectPrompts(doc, filepath.Base(f), func(path, prompt string) {
			kept, _ := YOLOGuidance(prompt)
			if loc := stopWordRe.FindStringIndex(kept); loc != nil && stopWordOK[path] == "" {
				from := max(0, loc[0]-60)
				t.Errorf("%s: says stop under YOLO: ...%s...", path, kept[from:min(len(kept), loc[1]+40)])
			}
		})
	}
}
