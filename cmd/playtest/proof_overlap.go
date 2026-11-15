package main

import (
	"fmt"
	"sort"
	"strings"
)

// ProofOverlapWarnings names each criterion that a judgement step and a
// machine step of the same issue both prove. A linked judgement step makes
// the criterion the owner's (docs/Workflow/proof.md#owner-checks), so the
// machine step's pass never ticks it: an agent meaning the machine step as
// the proof finds the box refused or left open at close. Sessions of one
// issue count together, since proof reads them together.
//
// open, when set, limits it to issues still being worked on: a closed
// issue's session can't be helped any more, and a dozen such lines on every
// validate would hide the one that can. Nil counts every issue.
func ProofOverlapWarnings(all []*Session, open func(issue string) bool) []string {
	type key struct{ issue, criterion string }
	judged := map[key][]string{}
	machine := map[key][]string{}
	for _, s := range all {
		if open != nil && !open(s.Issue) {
			continue
		}
		for _, st := range s.Steps {
			into := machine
			if st.Judgement() {
				into = judged
			}
			for _, id := range st.Proves {
				k := key{s.Issue, id}
				into[k] = append(into[k], s.Name+"/"+st.ID)
			}
		}
	}
	var out []string
	for k, owners := range judged {
		if machines := machine[k]; len(machines) > 0 {
			sort.Strings(owners)
			sort.Strings(machines)
			out = append(out, fmt.Sprintf(
				"%s %s: judgement step %s and machine step %s both prove it; the judgement step makes it the owner's, so the machine step never ticks it: drop proves: from one, or do:/expect: if the step is automated",
				k.issue, k.criterion, strings.Join(owners, ", "), strings.Join(machines, ", ")))
		}
	}
	sort.Strings(out)
	return out
}
