// Package jsonfile holds the one way a JSON state file is written.
package jsonfile

import (
	"encoding/json"
	"os"

	"github.com/kacperwerema/dark-factory/internal/gitfile"
)

// Write writes v to path as indented JSON with a final newline, aside first
// and renamed into place, so a reader never sees half a file and a failure
// leaves the old file as it was. The folder must exist. Every JSON file that
// is rewritten in place goes through it (TestOneJSONWrite).
func Write(path string, v any, perm os.FileMode) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return gitfile.WriteAtomic(path, append(data, '\n'), perm)
}
