package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistryLocalPathClassification(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantLocal bool
		wantPath  string
	}{
		{name: "windows backslash drive", input: `C:\\Users\\Tester\\AppData\\Local\\Temp\\index.json`, wantLocal: true, wantPath: `C:\\Users\\Tester\\AppData\\Local\\Temp\\index.json`},
		{name: "windows slash drive", input: `D:/PhyLang/index.json`, wantLocal: true, wantPath: `D:/PhyLang/index.json`},
		{name: "windows UNC", input: `\\\\server\\share\\index.json`, wantLocal: true, wantPath: `\\\\server\\share\\index.json`},
		{name: "relative path", input: `community-registry/index.json`, wantLocal: true, wantPath: `community-registry/index.json`},
		{name: "https URL", input: `https://example.invalid/index.json`, wantLocal: false},
		{name: "http URL", input: `http://127.0.0.1/index.json`, wantLocal: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotPath, gotLocal, err := registryLocalPath(tc.input)
			if err != nil {
				t.Fatalf("registryLocalPath(%q): %v", tc.input, err)
			}
			if gotLocal != tc.wantLocal {
				t.Fatalf("registryLocalPath(%q) local=%v, want %v", tc.input, gotLocal, tc.wantLocal)
			}
			if tc.wantPath != "" && gotPath != tc.wantPath {
				t.Fatalf("registryLocalPath(%q) path=%q, want %q", tc.input, gotPath, tc.wantPath)
			}
		})
	}
}

func TestLoadRegistryFromNativeAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.json")
	data := []byte(`{"schema":"phylang.registry/v2","name":"native-path-test","updated":"2026-07-29T00:00:00Z","packages":[]}` + "\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	idx, err := loadRegistry(path)
	if err != nil {
		t.Fatalf("loadRegistry(%q): %v", path, err)
	}
	if idx.Schema != "phylang.registry/v2" || idx.Name != "native-path-test" {
		t.Fatalf("unexpected registry: %#v", idx)
	}
}

func normalizeTestPath(value string) string {
	return strings.ReplaceAll(value, `\`, "/")
}

func TestResolveRegistryReferenceWindowsDrivePath(t *testing.T) {
	got, err := resolveRegistryReference(
		`C:\Users\Tester\AppData\Local\Temp\registry\index.json`,
		"",
		"packages/demo.phypkg",
	)
	if err != nil {
		t.Fatal(err)
	}
	want := "C:/Users/Tester/AppData/Local/Temp/registry/packages/demo.phypkg"
	if normalizeTestPath(got) != want {
		t.Fatalf("got %q want %q", normalizeTestPath(got), want)
	}
}

func TestResolveRegistryReferenceWindowsSlashDrivePath(t *testing.T) {
	got, err := resolveRegistryReference(
		"D:/PhyLang/registry/index.json",
		"",
		"packages/demo.phypkg",
	)
	if err != nil {
		t.Fatal(err)
	}
	want := "D:/PhyLang/registry/packages/demo.phypkg"
	if normalizeTestPath(got) != want {
		t.Fatalf("got %q want %q", normalizeTestPath(got), want)
	}
}

func TestResolveRegistryReferenceWindowsUNCPath(t *testing.T) {
	got, err := resolveRegistryReference(
		`\\server\share\registry\index.json`,
		"",
		"packages/demo.phypkg",
	)
	if err != nil {
		t.Fatal(err)
	}
	want := "//server/share/registry/packages/demo.phypkg"
	if normalizeTestPath(got) != want {
		t.Fatalf("got %q want %q", normalizeTestPath(got), want)
	}
}

func TestResolveRegistryReferenceFileURLWindowsPath(t *testing.T) {
	got, err := resolveRegistryReference(
		"file:///C:/PhyLang/registry/index.json",
		"",
		"packages/demo.phypkg",
	)
	if err != nil {
		t.Fatal(err)
	}
	want := "C:/PhyLang/registry/packages/demo.phypkg"
	if normalizeTestPath(got) != want {
		t.Fatalf("got %q want %q", normalizeTestPath(got), want)
	}
}

func TestResolveRegistryReferenceHTTP(t *testing.T) {
	got, err := resolveRegistryReference(
		"https://example.invalid/registry/index.json",
		"",
		"packages/demo.phypkg",
	)
	if err != nil {
		t.Fatal(err)
	}
	want := "https://example.invalid/registry/packages/demo.phypkg"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
