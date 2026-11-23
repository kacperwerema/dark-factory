package testshard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kacperwerema/dark-factory/internal/jsonfile"
)

// ListingEnv names the file holding the run's listing: tools/test-shards
// lists each sharded package (go test -list) before it starts a shard, writes
// what it found there and sets ListingEnv in every go test it starts, so
// TestShardedPackagesRunEveryTestOnce reads that listing instead of building
// each package's test binary a second time to list it again.
const ListingEnv = "DF_TEST_SHARD_LISTING"

// Listing is each sharded package's listed tests, by import path.
type Listing map[string][]string

// WriteListing writes l to path, and leaves the file alone when it already
// holds the same: a test that reads it is rerun by go test's cache when the
// file changes, so an unchanged listing keeps that test cached.
func WriteListing(path string, l Listing) error {
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, append(data, '\n')) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return jsonfile.Write(path, l, 0o644)
}

// ReadListing reads a file WriteListing wrote.
func ReadListing(path string) (Listing, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var l Listing
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	return l, nil
}
