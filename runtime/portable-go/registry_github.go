package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// GitHubRegistryConfig describes a registry deployed to GitHub Pages and mirrored
// to GitHub Releases. It is intentionally static so the service needs no server,
// database, token, or background process at download time.
type GitHubRegistryConfig struct {
	Repository   string `json:"repository"`
	PagesBase    string `json:"pages_base"`
	ReleaseBase  string `json:"release_base,omitempty"`
	RegistryName string `json:"registry_name"`
	Description  string `json:"description,omitempty"`
}

func normalizeRegistryLocation(location string) string {
	location = strings.TrimSpace(location)
	for _, prefix := range []string{"github:", "github-pages://"} {
		if strings.HasPrefix(location, prefix) {
			repo := strings.TrimPrefix(location, prefix)
			repo = strings.Trim(repo, "/")
			parts := strings.Split(repo, "/")
			if len(parts) == 2 {
				return fmt.Sprintf("https://%s.github.io/%s/index.json", parts[0], parts[1])
			}
		}
	}
	return location
}

func githubPagesIndexURL(repository string) (string, error) {
	parts := strings.Split(strings.Trim(repository, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("GitHub 仓库必须是 OWNER/REPOSITORY")
	}
	return fmt.Sprintf("https://%s.github.io/%s/index.json", parts[0], parts[1]), nil
}

func githubReleaseAssetURL(repository, tag, asset string) (string, error) {
	parts := strings.Split(strings.Trim(repository, "/"), "/")
	if len(parts) != 2 || tag == "" || asset == "" {
		return "", fmt.Errorf("GitHub Release 地址需要 OWNER/REPO、tag 和 asset")
	}
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repository, url.PathEscape(tag), url.PathEscape(asset)), nil
}

func copyFileWithHash(src, dst string) (string, int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", 0, err
	}
	defer in.Close()
	if err = os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return "", 0, err
	}
	out, err := os.Create(dst)
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(out, h), in)
	closeErr := out.Close()
	if copyErr != nil {
		return "", n, copyErr
	}
	if closeErr != nil {
		return "", n, closeErr
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func extractManifestJSON(pkgPath string) (map[string]any, error) {
	tmp, err := os.MkdirTemp("", "phylang-manifest-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if err = unzipSafe(pkgPath, tmp); err != nil {
		return nil, err
	}
	root := tmp
	entries, _ := os.ReadDir(tmp)
	if len(entries) == 1 && entries[0].IsDir() {
		root = filepath.Join(tmp, entries[0].Name())
	}
	m, err := LoadPackageManifest(root)
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(m)
	var obj map[string]any
	_ = json.Unmarshal(b, &obj)
	return obj, nil
}

func buildGitHubRegistrySite(packagesDir, outputDir string, cfg GitHubRegistryConfig) (RegistryIndex, error) {
	if cfg.RegistryName == "" {
		cfg.RegistryName = "phylang-community"
	}
	if cfg.PagesBase == "" {
		var err error
		cfg.PagesBase, err = githubPagesIndexURL(cfg.Repository)
		if err != nil {
			return RegistryIndex{}, err
		}
		cfg.PagesBase = strings.TrimSuffix(cfg.PagesBase, "/index.json") + "/"
	}
	cfg.PagesBase = strings.TrimRight(cfg.PagesBase, "/") + "/"
	if err := os.RemoveAll(outputDir); err != nil {
		return RegistryIndex{}, err
	}
	if err := os.MkdirAll(filepath.Join(outputDir, "packages"), 0755); err != nil {
		return RegistryIndex{}, err
	}

	idx, err := buildRegistryIndex(packagesDir, cfg.RegistryName)
	if err != nil {
		return idx, err
	}
	idx.Schema = "phylang.registry/v2"
	idx.BaseURL = cfg.PagesBase
	idx.Repository = "https://github.com/" + strings.Trim(cfg.Repository, "/")
	idx.Description = cfg.Description
	idx.GeneratedBy = "PhyLang Community " + version

	entries, err := os.ReadDir(packagesDir)
	if err != nil {
		return idx, err
	}
	sizes := map[string]int64{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".phypkg") {
			continue
		}
		src := filepath.Join(packagesDir, entry.Name())
		dst := filepath.Join(outputDir, "packages", entry.Name())
		hash, size, err := copyFileWithHash(src, dst)
		if err != nil {
			return idx, err
		}
		sizes[entry.Name()] = size
		// buildRegistryIndex already computed the hash; rechecking while copying
		// catches a race or accidental mutation during publication.
		found := false
		for pi := range idx.Packages {
			for vi := range idx.Packages[pi].Versions {
				v := &idx.Packages[pi].Versions[vi]
				if filepath.Base(v.URL) == entry.Name() {
					if !strings.EqualFold(v.SHA256, hash) {
						return idx, fmt.Errorf("%s 在索引生成期间发生变化", entry.Name())
					}
					v.URL = "packages/" + entry.Name()
					v.Size = size
					if strings.TrimSpace(cfg.Repository) != "" {
						v.ReleaseURL = fmt.Sprintf("https://github.com/%s/releases/latest/download/%s", strings.Trim(cfg.Repository, "/"), entry.Name())
					}
					v.SourceCommit = strings.TrimSpace(os.Getenv("GITHUB_SHA"))
					found = true
					manifest, mErr := extractManifestJSON(src)
					if mErr != nil {
						return idx, mErr
					}
					metaDir := filepath.Join(outputDir, "api", "v1", "packages", idx.Packages[pi].Name, v.Version)
					if err = os.MkdirAll(metaDir, 0755); err != nil {
						return idx, err
					}
					mb, _ := json.MarshalIndent(manifest, "", "  ")
					if err = os.WriteFile(filepath.Join(metaDir, "manifest.json"), append(mb, '\n'), 0644); err != nil {
						return idx, err
					}
					vb, _ := json.MarshalIndent(v, "", "  ")
					if err = os.WriteFile(filepath.Join(metaDir, "version.json"), append(vb, '\n'), 0644); err != nil {
						return idx, err
					}
				}
			}
		}
		if !found {
			return idx, fmt.Errorf("%s 未进入仓库索引", entry.Name())
		}
	}

	sort.Slice(idx.Packages, func(i, j int) bool { return idx.Packages[i].Name < idx.Packages[j].Name })
	b, _ := json.MarshalIndent(idx, "", "  ")
	if err = os.WriteFile(filepath.Join(outputDir, "index.json"), append(b, '\n'), 0644); err != nil {
		return idx, err
	}
	if err = os.MkdirAll(filepath.Join(outputDir, "api", "v1"), 0755); err != nil {
		return idx, err
	}
	if err = os.WriteFile(filepath.Join(outputDir, "api", "v1", "index.json"), append(b, '\n'), 0644); err != nil {
		return idx, err
	}
	health := map[string]any{"ok": true, "schema": idx.Schema, "updated": idx.Updated, "packages": len(idx.Packages), "generated_by": idx.GeneratedBy}
	hb, _ := json.MarshalIndent(health, "", "  ")
	if err = os.WriteFile(filepath.Join(outputDir, "health.json"), append(hb, '\n'), 0644); err != nil {
		return idx, err
	}
	_ = os.WriteFile(filepath.Join(outputDir, ".nojekyll"), []byte{}, 0644)

	var rows strings.Builder
	for _, p := range idx.Packages {
		latest := ""
		if len(p.Versions) > 0 {
			latest = p.Versions[0].Version
		}
		rows.WriteString("<tr><td><code>" + html.EscapeString(p.Name) + "</code></td><td>" + html.EscapeString(latest) + "</td><td>" + html.EscapeString(p.Description) + "</td></tr>\n")
	}
	page := `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>` + html.EscapeString(cfg.RegistryName) + ` · PhyLang 社区包仓库</title><style>body{font-family:system-ui,"Microsoft YaHei",sans-serif;max-width:1100px;margin:40px auto;padding:0 20px;color:#1f2937}code{background:#eef2ff;padding:2px 5px;border-radius:4px}table{border-collapse:collapse;width:100%}th,td{padding:10px;border-bottom:1px solid #ddd;text-align:left}.muted{color:#64748b}</style></head><body><h1>` + html.EscapeString(cfg.RegistryName) + `</h1><p>` + html.EscapeString(cfg.Description) + `</p><p class="muted">更新时间：` + html.EscapeString(idx.Updated) + ` · Schema：` + html.EscapeString(idx.Schema) + `</p><p><code>phylang package registry add github ` + html.EscapeString(cfg.PagesBase+"index.json") + `</code></p><table><thead><tr><th>包</th><th>最新版本</th><th>说明</th></tr></thead><tbody>` + rows.String() + `</tbody></table><p><a href="index.json">index.json</a> · <a href="health.json">health.json</a></p></body></html>`
	if err = os.WriteFile(filepath.Join(outputDir, "index.html"), []byte(page), 0644); err != nil {
		return idx, err
	}
	cfgBytes, _ := json.MarshalIndent(cfg, "", "  ")
	if err = os.WriteFile(filepath.Join(outputDir, "registry-hosting.json"), append(cfgBytes, '\n'), 0644); err != nil {
		return idx, err
	}
	_ = sizes
	return idx, nil
}

func writeGitHubRegistryConfig(path string, cfg GitHubRegistryConfig) error {
	cfgBytes, _ := json.MarshalIndent(cfg, "", "  ")
	return os.WriteFile(path, append(cfgBytes, '\n'), 0644)
}

func loadGitHubRegistryConfig(path string) (GitHubRegistryConfig, error) {
	var cfg GitHubRegistryConfig
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	err = json.Unmarshal(b, &cfg)
	return cfg, err
}

func registryDeploymentTimestamp() string { return time.Now().UTC().Format(time.RFC3339) }
