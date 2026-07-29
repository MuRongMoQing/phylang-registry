package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type RegistryVersion struct {
	Version        string `json:"version"`
	URL            string `json:"url"`
	SHA256         string `json:"sha256"`
	PhyLang        string `json:"phylang"`
	Published      string `json:"published,omitempty"`
	Size           int64  `json:"size,omitempty"`
	Yanked         bool   `json:"yanked,omitempty"`
	ReleaseURL     string `json:"release_url,omitempty"`
	AttestationURL string `json:"attestation_url,omitempty"`
	SBOMURL        string `json:"sbom_url,omitempty"`
	SourceCommit   string `json:"source_commit,omitempty"`
	LocalPath      string `json:"-"`
}
type RegistryPackage struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Repository  string            `json:"repository,omitempty"`
	Keywords    []string          `json:"keywords,omitempty"`
	Versions    []RegistryVersion `json:"versions"`
}
type RegistryIndex struct {
	Schema      string            `json:"schema"`
	Name        string            `json:"name"`
	Updated     string            `json:"updated"`
	Description string            `json:"description,omitempty"`
	Repository  string            `json:"repository,omitempty"`
	BaseURL     string            `json:"base_url,omitempty"`
	GeneratedBy string            `json:"generated_by,omitempty"`
	Packages    []RegistryPackage `json:"packages"`
}
type RegistryConfig struct {
	Registries map[string]string `json:"registries"`
}

func addBundledRegistry(pm *PackageManager, c *RegistryConfig) {
	candidates := []string{filepath.Join(pm.ProjectRoot, "community-registry", "index.json")}
	if exe, e := os.Executable(); e == nil {
		d := filepath.Dir(exe)
		candidates = append(candidates, filepath.Join(d, "community-registry", "index.json"), filepath.Join(d, "..", "share", "phylang", "community-registry", "index.json"))
	}
	for _, candidate := range candidates {
		if _, e := os.Stat(candidate); e == nil {
			if _, exists := c.Registries["bundled"]; !exists {
				c.Registries["bundled"] = candidate
			}
			return
		}
	}
}

func addEnvironmentRegistries(c *RegistryConfig) {
	if raw := strings.TrimSpace(os.Getenv("PHYLANG_REGISTRY_URL")); raw != "" {
		c.Registries["environment"] = normalizeRegistryLocation(raw)
	}
	if raw := strings.TrimSpace(os.Getenv("PHYLANG_GITHUB_REGISTRY")); raw != "" {
		c.Registries["github"] = normalizeRegistryLocation("github:" + raw)
	}
}

func registryConfigPath(pm *PackageManager) string { return filepath.Join(pm.Home, "registries.json") }
func loadRegistryConfig(pm *PackageManager) (RegistryConfig, error) {
	c := RegistryConfig{Registries: map[string]string{}}
	b, e := os.ReadFile(registryConfigPath(pm))
	if os.IsNotExist(e) {
		addBundledRegistry(pm, &c)
		addEnvironmentRegistries(&c)
		return c, nil
	}
	if e != nil {
		return c, e
	}
	e = json.Unmarshal(b, &c)
	if c.Registries == nil {
		c.Registries = map[string]string{}
	}
	addBundledRegistry(pm, &c)
	addEnvironmentRegistries(&c)
	return c, e
}
func saveRegistryConfig(pm *PackageManager, c RegistryConfig) error {
	if e := os.MkdirAll(pm.Home, 0755); e != nil {
		return e
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(registryConfigPath(pm), append(b, '\n'), 0644)
}
func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func isWindowsAbsolutePath(location string) bool {
	if len(location) >= 3 && isASCIILetter(location[0]) && location[1] == ':' && (location[2] == '\\' || location[2] == '/') {
		return true
	}
	return strings.HasPrefix(location, `\\`)
}

func registryLocalPath(location string) (string, bool, error) {
	location = strings.TrimSpace(location)
	if location == "" {
		return "", false, errors.New("仓库地址为空")
	}
	// Check native absolute paths before url.Parse. On Windows, url.Parse treats
	// C:\path\index.json as URL scheme "c", which previously made every local
	// absolute registry path fail with "不支持的仓库协议 c".
	if filepath.IsAbs(location) || isWindowsAbsolutePath(location) {
		return location, true, nil
	}
	u, err := url.Parse(location)
	if err != nil {
		return "", false, err
	}
	if u.Scheme == "" {
		return location, true, nil
	}
	if u.Scheme != "file" {
		return "", false, nil
	}
	pathValue, err := url.PathUnescape(u.Path)
	if err != nil {
		return "", false, err
	}
	if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
		pathValue = `//` + u.Host + pathValue
	}
	pathValue = filepath.FromSlash(pathValue)
	// file:///C:/path is parsed with a leading slash. Remove only that slash
	// when the remainder is an unambiguous Windows drive path.
	if len(pathValue) >= 4 && (pathValue[0] == '/' || pathValue[0] == '\\') && isWindowsAbsolutePath(pathValue[1:]) {
		pathValue = pathValue[1:]
	}
	return pathValue, true, nil
}

func fetchBytes(location string, limit int64) ([]byte, error) {
	location = normalizeRegistryLocation(location)
	localPath, isLocal, e := registryLocalPath(location)
	if e != nil {
		return nil, e
	}
	if isLocal {
		return os.ReadFile(localPath)
	}
	u, e := url.Parse(location)
	if e != nil {
		return nil, e
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("不支持的仓库协议 %s", u.Scheme)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	req, e := http.NewRequest(http.MethodGet, location, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", "PhyLang-Community/"+version+" registry-client")
	req.Header.Set("Accept", "application/json, application/octet-stream;q=0.9, */*;q=0.1")
	resp, e := client.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// joinLocalRegistryReference resolves a package path relative to a local
// registry index. Windows drive and UNC paths are handled independently of the
// host GOOS so the same logic can be regression-tested on Linux CI.
func joinLocalRegistryReference(indexPath, reference string) string {
	if isWindowsAbsolutePath(indexPath) {
		base := strings.ReplaceAll(indexPath, `\`, "/")
		ref := strings.ReplaceAll(reference, `\`, "/")
		unc := strings.HasPrefix(base, "//")
		joined := path.Clean(path.Join(path.Dir(base), ref))
		if unc && !strings.HasPrefix(joined, "//") {
			joined = "/" + joined
		}
		return filepath.FromSlash(joined)
	}
	return filepath.Join(filepath.Dir(indexPath), filepath.FromSlash(reference))
}

// resolveRegistryReference resolves one package URL from a registry index.
// Native paths must be classified before url.Parse because url.Parse treats a
// Windows drive letter (for example C:) as a URL scheme.
func resolveRegistryReference(indexLocation, baseLocation, reference string) (string, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return "", errors.New("仓库包地址为空")
	}

	// Absolute native paths and file:// URLs are already complete locations.
	if local, isLocal, err := registryLocalPath(reference); err != nil {
		return "", err
	} else if isLocal && (filepath.IsAbs(local) || isWindowsAbsolutePath(local)) {
		return local, nil
	}

	refURL, err := url.Parse(reference)
	if err != nil {
		return "", err
	}
	if refURL.Scheme != "" {
		if refURL.Scheme != "http" && refURL.Scheme != "https" && refURL.Scheme != "file" {
			return "", fmt.Errorf("不支持的仓库包协议 %s", refURL.Scheme)
		}
		return reference, nil
	}

	base := strings.TrimSpace(baseLocation)
	if base == "" {
		base = indexLocation
	}
	if base == "" {
		return "", errors.New("仓库索引地址为空")
	}

	if localBase, isLocal, err := registryLocalPath(base); err != nil {
		return "", err
	} else if isLocal {
		return joinLocalRegistryReference(localBase, reference), nil
	}

	baseURL, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	if baseURL.Scheme != "http" && baseURL.Scheme != "https" {
		return "", fmt.Errorf("不支持的仓库基础协议 %s", baseURL.Scheme)
	}
	return baseURL.ResolveReference(refURL).String(), nil
}

func loadRegistry(location string) (RegistryIndex, error) {
	location = normalizeRegistryLocation(location)
	var idx RegistryIndex
	b, e := fetchBytes(location, 8<<20)
	if e != nil {
		return idx, e
	}
	if e = json.Unmarshal(b, &idx); e != nil {
		return idx, e
	}
	if idx.Schema != "phylang.registry/v1" && idx.Schema != "phylang.registry/v2" {
		return idx, fmt.Errorf("不支持的仓库 schema %s", idx.Schema)
	}
	for pi := range idx.Packages {
		for vi := range idx.Packages[pi].Versions {
			raw := idx.Packages[pi].Versions[vi].URL
			resolved, resolveErr := resolveRegistryReference(location, idx.BaseURL, raw)
			if resolveErr != nil {
				return idx, fmt.Errorf("解析仓库包地址 %q 失败: %w", raw, resolveErr)
			}
			idx.Packages[pi].Versions[vi].URL = resolved
		}
	}
	for _, p := range idx.Packages {
		if !packageNamePattern.MatchString(p.Name) {
			return idx, fmt.Errorf("仓库包含无效包名 %s", p.Name)
		}
		for _, v := range p.Versions {
			if _, e := parseSemVersion(v.Version); e != nil {
				return idx, e
			}
			if len(v.SHA256) != 64 {
				return idx, fmt.Errorf("%s@%s 缺少 SHA-256", p.Name, v.Version)
			}
		}
	}
	return idx, nil
}
func registrySearch(pm *PackageManager, query string) ([]RegistryPackage, error) {
	cfg, e := loadRegistryConfig(pm)
	if e != nil {
		return nil, e
	}
	var out []RegistryPackage
	seen := map[string]bool{}
	for _, loc := range cfg.Registries {
		idx, e := loadRegistry(loc)
		if e != nil {
			return nil, e
		}
		for _, p := range idx.Packages {
			hay := strings.ToLower(p.Name + " " + p.Description + " " + strings.Join(p.Keywords, " "))
			if query == "" || strings.Contains(hay, strings.ToLower(query)) {
				if !seen[p.Name] {
					out = append(out, p)
					seen[p.Name] = true
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func registryResolve(pm *PackageManager, name, constraint string) (RegistryVersion, error) {
	cfg, e := loadRegistryConfig(pm)
	if e != nil {
		return RegistryVersion{}, e
	}
	var candidates []RegistryVersion
	for _, loc := range cfg.Registries {
		idx, e := loadRegistry(loc)
		if e != nil {
			return RegistryVersion{}, e
		}
		for _, p := range idx.Packages {
			if p.Name != name {
				continue
			}
			for _, v := range p.Versions {
				if v.Yanked {
					continue
				}
				if satisfiesVersion(v.Version, constraint) && satisfiesVersion(version, v.PhyLang) {
					candidates = append(candidates, v)
				}
			}
		}
	}
	if len(candidates) == 0 {
		return RegistryVersion{}, fmt.Errorf("社区仓库中找不到 %s %s", name, constraint)
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, _ := parseSemVersion(candidates[i].Version)
		b, _ := parseSemVersion(candidates[j].Version)
		return compareSem(a, b) > 0
	})
	return candidates[0], nil
}
func downloadRegistryPackage(pm *PackageManager, name, constraint string) (*PackageManifest, error) {
	return downloadRegistryPackageRecursive(pm, name, constraint, map[string]bool{})
}
func downloadRegistryPackageRecursive(pm *PackageManager, name, constraint string, loading map[string]bool) (*PackageManifest, error) {
	v, e := registryResolve(pm, name, constraint)
	if e != nil {
		return nil, e
	}
	key := name + "@" + v.Version
	if loading[key] {
		return nil, fmt.Errorf("社区仓库依赖循环: %s", key)
	}
	if installed, e := pm.Resolve(name, "="+v.Version); e == nil {
		return installed, nil
	}
	loading[key] = true
	defer delete(loading, key)
	b, e := fetchBytes(v.URL, 128<<20)
	if e != nil && v.ReleaseURL != "" {
		b, e = fetchBytes(v.ReleaseURL, 128<<20)
	}
	if e != nil {
		return nil, e
	}
	sum := sha256.Sum256(b)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, v.SHA256) {
		return nil, fmt.Errorf("包校验失败：期望 %s，得到 %s", v.SHA256, got)
	}
	if e = os.MkdirAll(filepath.Join(pm.Home, "cache"), 0755); e != nil {
		return nil, e
	}
	path := filepath.Join(pm.Home, "cache", strings.ReplaceAll(name, ".", "-")+"-"+v.Version+".phypkg")
	if e = os.WriteFile(path, b, 0644); e != nil {
		return nil, e
	}
	m, e := pm.Install(path)
	if e != nil {
		return nil, e
	}
	names := make([]string, 0, len(m.Dependencies))
	for dep := range m.Dependencies {
		names = append(names, dep)
	}
	sort.Strings(names)
	for _, depName := range names {
		d := m.Dependencies[depName]
		if d.Path != "" {
			return nil, fmt.Errorf("仓库包 %s 不允许发布 path 依赖 %s", m.Name, depName)
		}
		if _, depErr := downloadRegistryPackageRecursive(pm, depName, d.Version, loading); depErr != nil && !d.Optional {
			return nil, depErr
		}
	}
	return m, nil
}
func buildRegistryIndex(dir, name string) (RegistryIndex, error) {
	idx := RegistryIndex{Schema: "phylang.registry/v1", Name: name, Updated: time.Now().UTC().Format(time.RFC3339)}
	entries, e := os.ReadDir(dir)
	if e != nil {
		return idx, e
	}
	byName := map[string]*RegistryPackage{}
	for _, x := range entries {
		if x.IsDir() || !strings.HasSuffix(x.Name(), ".phypkg") {
			continue
		}
		path := filepath.Join(dir, x.Name())
		tmp, e := os.MkdirTemp("", "phylang-index-")
		if e != nil {
			return idx, e
		}
		if e = unzipSafe(path, tmp); e != nil {
			os.RemoveAll(tmp)
			return idx, e
		}
		root := tmp
		ls, _ := os.ReadDir(tmp)
		if len(ls) == 1 && ls[0].IsDir() {
			root = filepath.Join(tmp, ls[0].Name())
		}
		m, e := LoadPackageManifest(root)
		if e != nil {
			os.RemoveAll(tmp)
			return idx, e
		}
		data, _ := os.ReadFile(path)
		sum := sha256.Sum256(data)
		p := byName[m.Name]
		if p == nil {
			p = &RegistryPackage{Name: m.Name, Description: m.Description, Repository: m.Repository, Keywords: m.Keywords}
			byName[m.Name] = p
		}
		abs, _ := filepath.Abs(path)
		p.Versions = append(p.Versions, RegistryVersion{Version: m.Version, URL: filepath.Base(path), LocalPath: abs, SHA256: hex.EncodeToString(sum[:]), PhyLang: m.PhyLang, Published: time.Now().UTC().Format(time.RFC3339)})
		os.RemoveAll(tmp)
	}
	for _, p := range byName {
		sort.Slice(p.Versions, func(i, j int) bool {
			a, _ := parseSemVersion(p.Versions[i].Version)
			b, _ := parseSemVersion(p.Versions[j].Version)
			return compareSem(a, b) > 0
		})
		idx.Packages = append(idx.Packages, *p)
	}
	sort.Slice(idx.Packages, func(i, j int) bool { return idx.Packages[i].Name < idx.Packages[j].Name })
	if len(idx.Packages) == 0 {
		return idx, errors.New("目录中没有 .phypkg")
	}
	return idx, nil
}
