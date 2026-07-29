package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSemverConstraints(t *testing.T) {
	cases := []struct {
		v, c string
		ok   bool
	}{{"1.2.3", "^1.0.0", true}, {"2.0.0", "^1.0.0", false}, {"0.2.4", "^0.2.0", true}, {"0.3.0", "^0.2.0", false}, {"1.5.0", ">=1.0.0 <2.0.0", true}, {"2.0.0", ">=1.0.0 <2.0.0", false}}
	for _, x := range cases {
		if got := satisfiesVersion(x.v, x.c); got != x.ok {
			t.Fatalf("%s %s got %v", x.v, x.c, got)
		}
	}
}

func TestPackageLifecycleAndRegistry(t *testing.T) {
	root := t.TempDir()
	pkg := filepath.Join(root, "demo")
	if e := InitPackage(pkg, "community.test-demo"); e != nil {
		t.Fatal(e)
	}
	m, e := LoadPackageManifest(pkg)
	if e != nil {
		t.Fatal(e)
	}
	if e = validatePackageSource(m); e != nil {
		t.Fatal(e)
	}
	archive := filepath.Join(root, "demo.phypkg")
	if _, e = PackPackage(pkg, archive); e != nil {
		t.Fatal(e)
	}
	regDir := filepath.Join(root, "registry", "packages")
	if e = os.MkdirAll(regDir, 0755); e != nil {
		t.Fatal(e)
	}
	regPkg := filepath.Join(regDir, "demo.phypkg")
	b, _ := os.ReadFile(archive)
	os.WriteFile(regPkg, b, 0644)
	idx, e := buildRegistryIndex(regDir, "test")
	if e != nil {
		t.Fatal(e)
	}
	idxPath := filepath.Join(root, "registry", "index.json")
	for pi := range idx.Packages {
		for vi := range idx.Packages[pi].Versions {
			idx.Packages[pi].Versions[vi].URL = "packages/demo.phypkg"
		}
	}
	data, _ := jsonMarshalIndent(idx)
	if e = os.WriteFile(idxPath, data, 0644); e != nil {
		t.Fatal(e)
	}
	home := filepath.Join(root, "home")
	old := os.Getenv("PHYLANG_HOME")
	defer os.Setenv("PHYLANG_HOME", old)
	os.Setenv("PHYLANG_HOME", home)
	pm := NewPackageManager(root)
	cfg := RegistryConfig{Registries: map[string]string{"test": idxPath}}
	if e = saveRegistryConfig(pm, cfg); e != nil {
		t.Fatal(e)
	}
	installed, e := downloadRegistryPackage(pm, "community.test-demo", "^0.1.0")
	if e != nil {
		t.Fatal(e)
	}
	if installed.Name != "community.test-demo" {
		t.Fatal(installed.Name)
	}
	found, e := pm.Resolve("community.test-demo", "=0.1.0")
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(found.RootDir, "community.test-demo") {
		t.Fatal(found.RootDir)
	}
}

func jsonMarshalIndent(v any) ([]byte, error) {
	b, e := json.MarshalIndent(v, "", "  ")
	return append(b, '\n'), e
}

func TestManifestMetadataAndLockTamperDetection(t *testing.T) {
	root := t.TempDir()
	pkg := filepath.Join(root, "metadata")
	if e := InitPackage(pkg, "community.metadata-demo"); e != nil {
		t.Fatal(e)
	}
	manifestPath := filepath.Join(pkg, "phylang.package.toml")
	f, e := os.OpenFile(manifestPath, os.O_APPEND|os.O_WRONLY, 0644)
	if e != nil {
		t.Fatal(e)
	}
	_, _ = f.WriteString(`
[quantities.ExampleRate]
dimension = "Length / Time"
kind = "rate"
description = "测试物理量元数据"
since = "0.1.0"

[units.example_rate]
quantity = "ExampleRate"
symbol = "er"
definition = "1 [m/s]"
aliases = ["er"]
description = "测试单位元数据"

[metadata]
stability = "experimental"

[tool.example]
future_field = "preserved"
`)
	_ = f.Close()
	m, e := LoadPackageManifest(pkg)
	if e != nil {
		t.Fatal(e)
	}
	if m.Quantities["ExampleRate"].Kind != "rate" || m.Units["example_rate"].Aliases[0] != "er" {
		t.Fatalf("metadata not parsed: %#v %#v", m.Quantities, m.Units)
	}
	if e = validatePackageSource(m); e != nil {
		t.Fatal(e)
	}
	pm := NewPackageManager(pkg)
	lock, e := GenerateLock(pm, m)
	if e != nil {
		t.Fatal(e)
	}
	if e = WriteLock(filepath.Join(pkg, "phylang.lock"), lock); e != nil {
		t.Fatal(e)
	}
	// 修改受锁定的源码后，解析必须失败，而不是静默使用被篡改包。
	source := filepath.Join(pkg, "src", "package.phy")
	b, _ := os.ReadFile(source)
	if e = os.WriteFile(source, append(b, []byte("\n// tampered\n")...), 0644); e != nil {
		t.Fatal(e)
	}
	pm = NewPackageManager(pkg)
	if _, e = pm.Resolve("community.metadata-demo", "=0.1.0"); e == nil || !strings.Contains(e.Error(), "锁文件校验失败") {
		t.Fatalf("expected lock checksum failure, got %v", e)
	}
}

func TestGitHubRegistryV2SiteAndDownload(t *testing.T) {
	root := t.TempDir()
	pkg := filepath.Join(root, "package")
	if err := InitPackage(pkg, "community.github-demo"); err != nil {
		t.Fatal(err)
	}
	archiveDir := filepath.Join(root, "packages")
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(archiveDir, "community-github-demo-0.1.0.phypkg")
	if _, err := PackPackage(pkg, archive); err != nil {
		t.Fatal(err)
	}

	site := filepath.Join(root, "site")
	server := httptest.NewServer(http.FileServer(http.Dir(site)))
	defer server.Close()
	cfg := GitHubRegistryConfig{Repository: "owner/repository", PagesBase: server.URL + "/", RegistryName: "test-github"}
	idx, err := buildGitHubRegistrySite(archiveDir, site, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if idx.Schema != "phylang.registry/v2" || idx.BaseURL != server.URL+"/" {
		t.Fatalf("unexpected index: %#v", idx)
	}
	if _, err := os.Stat(filepath.Join(site, "health.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(site, "api", "v1", "packages", "community.github-demo", "0.1.0", "manifest.json")); err != nil {
		t.Fatal(err)
	}

	home := filepath.Join(root, "home")
	oldHome := os.Getenv("PHYLANG_HOME")
	defer os.Setenv("PHYLANG_HOME", oldHome)
	os.Setenv("PHYLANG_HOME", home)
	pm := NewPackageManager(root)
	if err := saveRegistryConfig(pm, RegistryConfig{Registries: map[string]string{"github": server.URL + "/index.json"}}); err != nil {
		t.Fatal(err)
	}
	installed, err := downloadRegistryPackage(pm, "community.github-demo", "^0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if installed.Name != "community.github-demo" {
		t.Fatal(installed.Name)
	}
}

func TestGitHubRegistryLocationShorthand(t *testing.T) {
	got := normalizeRegistryLocation("github:open-physics/phylang-registry")
	want := "https://open-physics.github.io/phylang-registry/index.json"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestRegistryClientSelfTestIsIsolated(t *testing.T) {
	t.Setenv("PHYLANG_HOME", t.TempDir())
	t.Setenv("PHYLANG_REGISTRY_URL", "")
	t.Setenv("PHYLANG_GITHUB_REGISTRY", "")
	workingDir := t.TempDir()
	oldWorkingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(workingDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWorkingDir) })
	if err = registryClientSelfTest(); err != nil {
		t.Fatal(err)
	}
}
