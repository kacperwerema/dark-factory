package wfedit

import (
	"fmt"
	"strings"
)

// diffContext is how many unchanged lines a hunk shows around a change.
const diffContext = 3

// lcs is the index pairs (i in a, j in b) of a longest common subsequence
// of a and b under eq, in order.
func lcs(a, b []string, eq func(x, y string) bool) [][2]int {
	n, m := len(a), len(b)
	t := make([][]int, n+1)
	for i := range t {
		t[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if eq(a[i], b[j]) {
				t[i][j] = t[i+1][j+1] + 1
			} else {
				t[i][j] = max(t[i+1][j], t[i][j+1])
			}
		}
	}
	var out [][2]int
	for i, j := 0, 0; i < n && j < m; {
		switch {
		case eq(a[i], b[j]):
			out = append(out, [2]int{i, j})
			i++
			j++
		case t[i+1][j] >= t[i][j+1]:
			i++
		default:
			j++
		}
	}
	return out
}

// diffLine is one line of an edit script: ' ', '-' or '+'.
type diffLine struct {
	op           byte
	text         string
	aLine, bLine int // 1-based line in old / new for ' ' and the side it has
}

// unifiedDiff is old against new as a unified diff with diffContext lines
// of context, "" when they are equal. Only the lines between the first
// and last change go through the LCS, so a small edit of a big file stays
// cheap.
func unifiedDiff(name, old, new string) string {
	if old == new {
		return ""
	}
	a, b := strings.Split(old, "\n"), strings.Split(new, "\n")
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	var script []diffLine
	for i := 0; i < pre; i++ {
		script = append(script, diffLine{' ', a[i], i + 1, i + 1})
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]
	i, j := 0, 0
	for _, p := range append(lcs(ma, mb, func(x, y string) bool { return x == y }), [2]int{len(ma), len(mb)}) {
		for ; i < p[0]; i++ {
			script = append(script, diffLine{'-', ma[i], pre + i + 1, 0})
		}
		for ; j < p[1]; j++ {
			script = append(script, diffLine{'+', mb[j], 0, pre + j + 1})
		}
		if i < len(ma) && j < len(mb) {
			script = append(script, diffLine{' ', ma[i], pre + i + 1, pre + j + 1})
			i++
			j++
		}
	}
	for k := 0; k < suf; k++ {
		script = append(script, diffLine{' ', a[len(a)-suf+k], len(a) - suf + k + 1, len(b) - suf + k + 1})
	}

	var out strings.Builder
	fmt.Fprintf(&out, "--- a/%s\n+++ b/%s\n", name, name)
	// Changes closer than 2*diffContext unchanged lines share a hunk.
	var changed []int
	for k, l := range script {
		if l.op != ' ' {
			changed = append(changed, k)
		}
	}
	for g := 0; g < len(changed); {
		last := g
		for last+1 < len(changed) && changed[last+1]-changed[last] <= 2*diffContext+1 {
			last++
		}
		start := max(0, changed[g]-diffContext)
		end := min(len(script), changed[last]+diffContext+1)
		writeHunk(&out, script[start:end])
		g = last + 1
	}
	return out.String()
}

func writeHunk(out *strings.Builder, h []diffLine) {
	aStart, bStart, aN, bN := 0, 0, 0, 0
	for _, l := range h {
		if l.op != '+' {
			if aStart == 0 {
				aStart = l.aLine
			}
			aN++
		}
		if l.op != '-' {
			if bStart == 0 {
				bStart = l.bLine
			}
			bN++
		}
	}
	fmt.Fprintf(out, "@@ -%d,%d +%d,%d @@\n", aStart, aN, bStart, bN)
	for _, l := range h {
		fmt.Fprintf(out, "%c%s\n", l.op, l.text)
	}
}
