package experiment

import (
	"fmt"
	"os"
	"strings"
)

// PickBenchmark is the experiment's benchmark: flagVal, the block's, or
// the only one in the owner's bench dir.
func PickBenchmark(flagVal string, b *Block, benchDir string) (string, error) {
	if flagVal != "" {
		return flagVal, nil
	}
	if b != nil && b.Benchmark != "" {
		return b.Benchmark, nil
	}
	entries, _ := os.ReadDir(benchDir)
	var names []string
	for _, e := range entries {
		if n, ok := strings.CutSuffix(e.Name(), ".yaml"); ok && !e.IsDir() {
			names = append(names, n)
		}
	}
	if len(names) == 1 {
		return names[0], nil
	}
	if len(names) == 0 {
		return "", fmt.Errorf("no benchmark in %s yet: the owner sets one up (issue-cli bench)", benchDir)
	}
	return "", fmt.Errorf("several benchmarks (%s): pass --benchmark", strings.Join(names, ", "))
}
