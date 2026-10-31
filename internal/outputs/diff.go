package outputs

import "strings"

// DiffLine is one line of LineDiff: Op is ' ' (in both), '-' (only in the
// old text) or '+' (only in the new).
type DiffLine struct {
	Op   byte
	Text string
}

// maxDiffCells caps the LCS table: past it, the changed middle shows as all
// removed then all added.
const maxDiffCells = 4_000_000

// LineDiff is old against new line by line. Only the lines between the
// first and last change go through the LCS.
func LineDiff(old, new string) []DiffLine {
	a, b := splitLines(old), splitLines(new)
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	var out []DiffLine
	for _, l := range a[:pre] {
		out = append(out, DiffLine{' ', l})
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]
	var pairs [][2]int
	if len(ma)*len(mb) <= maxDiffCells {
		pairs = lcsLines(ma, mb)
	}
	i, j := 0, 0
	for _, pr := range append(pairs, [2]int{len(ma), len(mb)}) {
		for ; i < pr[0]; i++ {
			out = append(out, DiffLine{'-', ma[i]})
		}
		for ; j < pr[1]; j++ {
			out = append(out, DiffLine{'+', mb[j]})
		}
		if pr[0] < len(ma) {
			out = append(out, DiffLine{' ', ma[i]})
			i++
			j++
		}
	}
	for _, l := range a[len(a)-suf:] {
		out = append(out, DiffLine{' ', l})
	}
	return out
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// lcsLines is the index pairs of a longest common subsequence of a and b.
func lcsLines(a, b []string) [][2]int {
	n, m := len(a), len(b)
	t := make([][]int32, n+1)
	for i := range t {
		t[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				t[i][j] = t[i+1][j+1] + 1
			} else {
				t[i][j] = max(t[i+1][j], t[i][j+1])
			}
		}
	}
	var out [][2]int
	for i, j := 0, 0; i < n && j < m; {
		switch {
		case a[i] == b[j]:
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
