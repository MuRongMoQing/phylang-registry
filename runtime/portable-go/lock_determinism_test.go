package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteLockPreservesTimestampWhenContentIsUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "phylang.lock")
	first := &PackageLock{
		Format:    1,
		Generated: "2026-01-01T00:00:00Z",
		Packages: []PackageLockEntry{{
			Name:         "example.package",
			Version:      "1.0.0",
			Source:       "package:example.package@1.0.0",
			Checksum:     "abc",
			Dependencies: map[string]string{},
		}},
	}
	if err := WriteLock(path, first); err != nil {
		t.Fatal(err)
	}
	second := &PackageLock{
		Format:    1,
		Generated: "2026-02-02T00:00:00Z",
		Packages:  first.Packages,
	}
	if err := WriteLock(path, second); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got PackageLock
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Generated != first.Generated {
		t.Fatalf("generated timestamp changed for identical lock content: got %q want %q", got.Generated, first.Generated)
	}
}
