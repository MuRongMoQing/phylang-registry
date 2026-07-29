package main

import (
	"archive/zip"
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type SemVersion struct {
	Major, Minor, Patch int
	Pre                 string
}

func parseSemVersion(s string) (SemVersion, error) {
	s = strings.TrimSpace(strings.TrimPrefix(s, "v"))
	var v SemVersion
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '-'); i >= 0 {
		v.Pre = s[i+1:]
		s = s[:i]
	}
	p := strings.Split(s, ".")
	if len(p) < 1 || len(p) > 3 {
		return v, fmt.Errorf("无效语义版本 %q", s)
	}
	nums := []*int{&v.Major, &v.Minor, &v.Patch}
	for i := range p {
		n, e := strconv.Atoi(p[i])
		if e != nil || n < 0 {
			return v, fmt.Errorf("无效语义版本 %q", s)
		}
		*nums[i] = n
	}
	return v, nil
}
func (v SemVersion) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}
func compareSem(a, b SemVersion) int {
	if a.Major != b.Major {
		if a.Major < b.Major {
			return -1
		}
		return 1
	}
	if a.Minor != b.Minor {
		if a.Minor < b.Minor {
			return -1
		}
		return 1
	}
	if a.Patch != b.Patch {
		if a.Patch < b.Patch {
			return -1
		}
		return 1
	}
	if a.Pre == b.Pre {
		return 0
	}
	if a.Pre == "" {
		return 1
	}
	if b.Pre == "" {
		return -1
	}
	if a.Pre < b.Pre {
		return -1
	}
	return 1
}
func satisfiesVersion(version, constraint string) bool {
	constraint = strings.TrimSpace(constraint)
	if constraint == "" || constraint == "*" {
		return true
	}
	v, e := parseSemVersion(version)
	if e != nil {
		return false
	}
	for _, part := range strings.FieldsFunc(constraint, func(r rune) bool { return r == ',' }) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.Contains(part, " ") {
			for _, x := range strings.Fields(part) {
				if !satisfiesVersion(version, x) {
					return false
				}
			}
			continue
		}
		op := "="
		raw := part
		for _, candidate := range []string{">=", "<=", "!=", "^", "~", ">", "<", "="} {
			if strings.HasPrefix(part, candidate) {
				op = candidate
				raw = strings.TrimSpace(strings.TrimPrefix(part, candidate))
				break
			}
		}
		target, e := parseSemVersion(raw)
		if e != nil {
			return false
		}
		c := compareSem(v, target)
		ok := false
		switch op {
		case "=":
			ok = c == 0
		case "!=":
			ok = c != 0
		case ">":
			ok = c > 0
		case ">=":
			ok = c >= 0
		case "<":
			ok = c < 0
		case "<=":
			ok = c <= 0
		case "^":
			upper := target
			if target.Major > 0 {
				upper.Major++
				upper.Minor = 0
				upper.Patch = 0
			} else if target.Minor > 0 {
				upper.Minor++
				upper.Patch = 0
			} else {
				upper.Patch++
			}
			ok = c >= 0 && compareSem(v, upper) < 0
		case "~":
			upper := target
			upper.Minor++
			upper.Patch = 0
			ok = c >= 0 && compareSem(v, upper) < 0
		}
		if !ok {
			return false
		}
	}
	return true
}

type PackageDependency struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Path     string `json:"path,omitempty"`
	Optional bool   `json:"optional,omitempty"`
}
type PackageExports struct{ Quantities, Units, Laws, Functions, Constants []string }
type PackageQuantityMetadata struct {
	Dimension   string `json:"dimension"`
	Kind        string `json:"kind,omitempty"`
	Description string `json:"description,omitempty"`
	Since       string `json:"since,omitempty"`
	Deprecated  string `json:"deprecated,omitempty"`
}
type PackageUnitMetadata struct {
	Quantity    string   `json:"quantity"`
	Symbol      string   `json:"symbol,omitempty"`
	Definition  string   `json:"definition"`
	Aliases     []string `json:"aliases,omitempty"`
	Description string   `json:"description,omitempty"`
	Since       string   `json:"since,omitempty"`
	Deprecated  string   `json:"deprecated,omitempty"`
}

type PackageManifest struct {
	Name         string                             `json:"name"`
	Version      string                             `json:"version"`
	Description  string                             `json:"description"`
	License      string                             `json:"license"`
	Authors      []string                           `json:"authors"`
	Repository   string                             `json:"repository"`
	Homepage     string                             `json:"homepage"`
	Entry        string                             `json:"entry"`
	Namespace    string                             `json:"namespace"`
	PhyLang      string                             `json:"phylang"`
	Keywords     []string                           `json:"keywords"`
	Readme       string                             `json:"readme"`
	Dependencies map[string]PackageDependency       `json:"dependencies"`
	Exports      PackageExports                     `json:"exports"`
	TestFiles    []string                           `json:"test_files"`
	Quantities   map[string]PackageQuantityMetadata `json:"quantities,omitempty"`
	Units        map[string]PackageUnitMetadata     `json:"units,omitempty"`
	Metadata     map[string]string                  `json:"metadata,omitempty"`
	Extensions   map[string]map[string]string       `json:"extensions,omitempty"`
	SourcePath   string                             `json:"-"`
	RootDir      string                             `json:"-"`
}

func splitTomlArray(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var a []string
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		return nil, fmt.Errorf("数组格式错误 %s: %w", raw, err)
	}
	return a, nil
}
func unquoteToml(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		var s string
		if e := json.Unmarshal([]byte(raw), &s); e != nil {
			return "", e
		}
		return s, nil
	}
	if raw == "true" || raw == "false" || regexp.MustCompile(`^-?[0-9]+$`).MatchString(raw) {
		return raw, nil
	}
	return "", fmt.Errorf("仅支持 TOML 字符串/布尔/整数，得到 %q", raw)
}
func parseInlineDependency(name, raw string) (PackageDependency, error) {
	d := PackageDependency{Name: name}
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "\"") {
		s, e := unquoteToml(raw)
		d.Version = s
		return d, e
	}
	if !strings.HasPrefix(raw, "{") || !strings.HasSuffix(raw, "}") {
		return d, fmt.Errorf("依赖 %s 格式错误", name)
	}
	body := strings.TrimSpace(raw[1 : len(raw)-1])
	for _, item := range splitCommaAware(body) {
		p := strings.SplitN(item, "=", 2)
		if len(p) != 2 {
			return d, fmt.Errorf("依赖 %s 内联表错误", name)
		}
		k := strings.TrimSpace(p[0])
		v := strings.TrimSpace(p[1])
		switch k {
		case "version":
			d.Version, _ = unquoteToml(v)
		case "path":
			d.Path, _ = unquoteToml(v)
		case "optional":
			d.Optional = v == "true"
		default:
			return d, fmt.Errorf("依赖 %s 未知字段 %s", name, k)
		}
	}
	if d.Version == "" {
		d.Version = "*"
	}
	return d, nil
}
func splitCommaAware(s string) []string {
	var out []string
	start := 0
	quote := false
	depth := 0
	for i, c := range s {
		if c == '"' {
			quote = !quote
		}
		if !quote {
			if c == '{' || c == '[' {
				depth++
			}
			if c == '}' || c == ']' {
				depth--
			}
			if c == ',' && depth == 0 {
				out = append(out, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	out = append(out, strings.TrimSpace(s[start:]))
	return out
}
func LoadPackageManifest(path string) (*PackageManifest, error) {
	if abs, e := filepath.Abs(path); e == nil {
		path = abs
	}
	info, e := os.Stat(path)
	if e != nil {
		return nil, e
	}
	if info.IsDir() {
		for _, n := range []string{"phylang.package.toml", "phys-package.toml"} {
			p := filepath.Join(path, n)
			if _, x := os.Stat(p); x == nil {
				path = p
				break
			}
		}
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	m := &PackageManifest{Entry: "src/package.phy", PhyLang: ">=0.6.0 <0.7.0", Dependencies: map[string]PackageDependency{}, Readme: "README.md", Quantities: map[string]PackageQuantityMetadata{}, Units: map[string]PackageUnitMetadata{}, Metadata: map[string]string{}, Extensions: map[string]map[string]string{}, SourcePath: path, RootDir: filepath.Dir(path)}
	table := ""
	scan := bufio.NewScanner(f)
	line := 0
	for scan.Scan() {
		line++
		text := strings.TrimSpace(stripTomlComment(scan.Text()))
		if text == "" {
			continue
		}
		if strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]") {
			table = strings.TrimSpace(text[1 : len(text)-1])
			continue
		}
		p := strings.Index(text, "=")
		if p < 0 {
			return nil, fmt.Errorf("%s:%d 缺少 =", path, line)
		}
		key := strings.TrimSpace(text[:p])
		raw := strings.TrimSpace(text[p+1:])
		str := func() (string, error) { return unquoteToml(raw) }
		switch table {
		case "package":
			switch key {
			case "name":
				m.Name, e = str()
			case "version":
				m.Version, e = str()
			case "description":
				m.Description, e = str()
			case "license":
				m.License, e = str()
			case "repository":
				m.Repository, e = str()
			case "homepage":
				m.Homepage, e = str()
			case "entry":
				m.Entry, e = str()
			case "namespace":
				m.Namespace, e = str()
			case "phylang":
				m.PhyLang, e = str()
			case "readme":
				m.Readme, e = str()
			case "authors":
				m.Authors, e = splitTomlArray(raw)
			case "keywords":
				m.Keywords, e = splitTomlArray(raw)
			default:
				if strings.HasPrefix(key, "x-") {
					m.Metadata["package."+key] = raw
				} else {
					e = fmt.Errorf("未知 package 字段 %s；实验字段请使用 x- 前缀或 [tool.<名称>]", key)
				}
			}
		case "dependencies":
			var d PackageDependency
			d, e = parseInlineDependency(key, raw)
			if e == nil {
				m.Dependencies[key] = d
			}
		case "exports":
			var a []string
			a, e = splitTomlArray(raw)
			if e == nil {
				switch key {
				case "quantities":
					m.Exports.Quantities = a
				case "units":
					m.Exports.Units = a
				case "laws":
					m.Exports.Laws = a
				case "functions":
					m.Exports.Functions = a
				case "constants":
					m.Exports.Constants = a
				default:
					e = fmt.Errorf("未知 exports 字段 %s", key)
				}
			}
		case "tests":
			if key == "files" {
				m.TestFiles, e = splitTomlArray(raw)
			} else {
				e = fmt.Errorf("未知 tests 字段 %s", key)
			}
		case "metadata":
			// metadata 是稳定的包级扩展区；值按 TOML 原文保存，便于未来工具读取。
			m.Metadata[key] = raw
		case "":
			e = fmt.Errorf("字段 %s 必须位于 TOML 表中", key)
		default:
			switch {
			case strings.HasPrefix(table, "quantities."):
				name := strings.TrimPrefix(table, "quantities.")
				q := m.Quantities[name]
				switch key {
				case "dimension":
					q.Dimension, e = str()
				case "kind":
					q.Kind, e = str()
				case "description":
					q.Description, e = str()
				case "since":
					q.Since, e = str()
				case "deprecated":
					q.Deprecated, e = str()
				default:
					e = fmt.Errorf("未知 quantity 元数据字段 %s", key)
				}
				m.Quantities[name] = q
			case strings.HasPrefix(table, "units."):
				name := strings.TrimPrefix(table, "units.")
				u := m.Units[name]
				switch key {
				case "quantity":
					u.Quantity, e = str()
				case "symbol":
					u.Symbol, e = str()
				case "definition":
					u.Definition, e = str()
				case "aliases":
					u.Aliases, e = splitTomlArray(raw)
				case "description":
					u.Description, e = str()
				case "since":
					u.Since, e = str()
				case "deprecated":
					u.Deprecated, e = str()
				default:
					e = fmt.Errorf("未知 unit 元数据字段 %s", key)
				}
				m.Units[name] = u
			case strings.HasPrefix(table, "tool."):
				if m.Extensions[table] == nil {
					m.Extensions[table] = map[string]string{}
				}
				m.Extensions[table][key] = raw
			default:
				e = fmt.Errorf("未知 TOML 表 [%s]；第三方扩展请使用 [tool.<名称>]", table)
			}
		}
		if e != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, e)
		}
	}
	if e = scan.Err(); e != nil {
		return nil, e
	}
	if e = ValidateManifest(m); e != nil {
		return nil, e
	}
	return m, nil
}
func stripTomlComment(s string) string {
	quote := false
	for i, c := range s {
		if c == '"' && (i == 0 || s[i-1] != '\\') {
			quote = !quote
		}
		if c == '#' && !quote {
			return s[:i]
		}
	}
	return s
}

var packageNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)

func ValidateManifest(m *PackageManifest) error {
	if !packageNamePattern.MatchString(m.Name) {
		return fmt.Errorf("包名 %q 无效：只能使用小写字母、数字、点、下划线和连字符", m.Name)
	}
	if _, e := parseSemVersion(m.Version); e != nil {
		return fmt.Errorf("包版本: %w", e)
	}
	if !satisfiesVersion(version, m.PhyLang) {
		return fmt.Errorf("包 %s@%s 要求 PhyLang %s，当前为 %s", m.Name, m.Version, m.PhyLang, version)
	}
	entry := filepath.Join(m.RootDir, filepath.FromSlash(m.Entry))
	if info, e := os.Stat(entry); e != nil || info.IsDir() {
		return fmt.Errorf("入口文件不存在: %s", entry)
	}
	for n, d := range m.Dependencies {
		if n != d.Name {
			return fmt.Errorf("依赖键与名称不一致: %s", n)
		}
		if d.Path == "" && d.Version == "" {
			return fmt.Errorf("依赖 %s 缺少版本或路径", n)
		}
	}
	for name, q := range m.Quantities {
		if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*$`).MatchString(name) {
			return fmt.Errorf("物理量元数据名称无效: %s", name)
		}
		if strings.TrimSpace(q.Dimension) == "" {
			return fmt.Errorf("[quantities.%s] 缺少 dimension", name)
		}
		if _, e := parseDim(q.Dimension); e != nil {
			return fmt.Errorf("[quantities.%s].dimension: %w", name, e)
		}
	}
	for name, u := range m.Units {
		if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*$`).MatchString(name) {
			return fmt.Errorf("单位元数据名称无效: %s", name)
		}
		if strings.TrimSpace(u.Quantity) == "" || strings.TrimSpace(u.Definition) == "" {
			return fmt.Errorf("[units.%s] 必须包含 quantity 和 definition", name)
		}
	}
	return nil
}

type PackageLockEntry struct {
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Source       string            `json:"source"`
	Checksum     string            `json:"sha256"`
	Dependencies map[string]string `json:"dependencies"`
}
type PackageLock struct {
	Format    int                `json:"format"`
	Generated string             `json:"generated"`
	Packages  []PackageLockEntry `json:"packages"`
}
type PackageManager struct {
	Home        string
	ProjectRoot string
	Locked      map[string]PackageLockEntry
}

func findProjectRoot(project string) string {
	abs, _ := filepath.Abs(project)
	for p := abs; ; p = filepath.Dir(p) {
		if info, e := os.Stat(filepath.Join(p, "community-packages")); e == nil && info.IsDir() {
			if _, e = os.Stat(filepath.Join(p, "runtime", "portable-go")); e == nil {
				return p
			}
		}
		for _, n := range []string{"phylang.package.toml", "phys-package.toml"} {
			if _, e := os.Stat(filepath.Join(p, n)); e == nil {
				return p
			}
		}
		next := filepath.Dir(p)
		if next == p {
			break
		}
	}
	return abs
}
func NewPackageManager(project string) *PackageManager {
	project = findProjectRoot(project)
	home := os.Getenv("PHYLANG_HOME")
	if home == "" {
		if h, e := os.UserHomeDir(); e == nil {
			home = filepath.Join(h, ".phylang")
		} else {
			home = filepath.Join(project, ".phylang")
		}
	}
	pm := &PackageManager{Home: home, ProjectRoot: project, Locked: map[string]PackageLockEntry{}}
	if b, e := os.ReadFile(filepath.Join(project, "phylang.lock")); e == nil {
		var lock PackageLock
		if json.Unmarshal(b, &lock) == nil {
			for _, x := range lock.Packages {
				pm.Locked[x.Name] = x
			}
		}
	}
	return pm
}
func (pm *PackageManager) storeRoot() string { return filepath.Join(pm.Home, "packages") }
func (pm *PackageManager) packageRoots() []string {
	var roots []string
	roots = append(roots, filepath.Join(pm.ProjectRoot, "vendor"), filepath.Join(pm.ProjectRoot, "phylang-packages"), filepath.Join(pm.ProjectRoot, "packages"), filepath.Join(pm.ProjectRoot, "community-packages"), filepath.Join(pm.ProjectRoot, "stdlib", "packages"), pm.storeRoot())
	if exe, e := os.Executable(); e == nil {
		d := filepath.Dir(exe)
		roots = append(roots, filepath.Join(d, "packages"), filepath.Join(d, "community-packages"), filepath.Join(d, "stdlib", "packages"), filepath.Join(d, "..", "share", "phylang", "packages"))
	}
	if extra := os.Getenv("PHYLANG_PACKAGE_PATH"); extra != "" {
		for _, r := range strings.Split(extra, string(os.PathListSeparator)) {
			if strings.TrimSpace(r) != "" {
				roots = append(roots, r)
			}
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, r := range roots {
		r = filepath.Clean(r)
		if !seen[r] {
			out = append(out, r)
			seen[r] = true
		}
	}
	return out
}
func (pm *PackageManager) packageCandidates(name string) []string {
	var out []string
	for _, root := range pm.packageRoots() {
		out = append(out, filepath.Join(root, filepath.FromSlash(name)))
	}
	return out
}

func (pm *PackageManager) Resolve(name, constraint string) (*PackageManifest, error) {
	pinned, hasPin := pm.Locked[name]
	if hasPin {
		if !satisfiesVersion(pinned.Version, constraint) {
			return nil, fmt.Errorf("锁文件固定 %s@%s，但不满足 %s", name, pinned.Version, constraint)
		}
		constraint = "=" + pinned.Version
	}
	var candidates []*PackageManifest
	if m, e := LoadPackageManifest(pm.ProjectRoot); e == nil && m.Name == name && satisfiesVersion(m.Version, constraint) {
		candidates = append(candidates, m)
	}
	for _, base := range pm.packageCandidates(name) {
		info, e := os.Stat(base)
		if e != nil {
			continue
		}
		if !info.IsDir() {
			continue
		}
		if m, e := LoadPackageManifest(base); e == nil && m.Name == name && satisfiesVersion(m.Version, constraint) {
			candidates = append(candidates, m)
		}
		entries, _ := os.ReadDir(base)
		for _, x := range entries {
			if !x.IsDir() {
				continue
			}
			m, e := LoadPackageManifest(filepath.Join(base, x.Name()))
			if e == nil && m.Name == name && satisfiesVersion(m.Version, constraint) {
				candidates = append(candidates, m)
			}
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("找不到包 %s（版本约束 %s）。可运行 phylang package list 查看已安装包", name, constraint)
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, _ := parseSemVersion(candidates[i].Version)
		b, _ := parseSemVersion(candidates[j].Version)
		return compareSem(a, b) > 0
	})
	chosen := candidates[0]
	if hasPin && pinned.Checksum != "" {
		got, e := hashPackageDirectory(chosen.RootDir)
		if e != nil {
			return nil, e
		}
		if !strings.EqualFold(got, pinned.Checksum) {
			return nil, fmt.Errorf("锁文件校验失败：%s@%s 期望 %s，实际 %s；请检查包是否被修改或重新运行 package lock", name, chosen.Version, pinned.Checksum, got)
		}
	}
	return chosen, nil
}
func copyFile(src, dst string) error {
	in, e := os.Open(src)
	if e != nil {
		return e
	}
	defer in.Close()
	if e = os.MkdirAll(filepath.Dir(dst), 0755); e != nil {
		return e
	}
	out, e := os.Create(dst)
	if e != nil {
		return e
	}
	_, e = io.Copy(out, in)
	ce := out.Close()
	if e == nil {
		e = ce
	}
	return e
}
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		return copyFile(path, target)
	})
}
func (pm *PackageManager) Install(source string) (*PackageManifest, error) {
	temp := ""
	root := source
	if strings.HasSuffix(strings.ToLower(source), ".phypkg") || strings.HasSuffix(strings.ToLower(source), ".zip") {
		d, e := os.MkdirTemp("", "phylang-package-")
		if e != nil {
			return nil, e
		}
		temp = d
		defer os.RemoveAll(temp)
		if e = unzipSafe(source, d); e != nil {
			return nil, e
		}
		root = d
		entries, _ := os.ReadDir(d)
		if len(entries) == 1 && entries[0].IsDir() {
			root = filepath.Join(d, entries[0].Name())
		}
	}
	m, e := LoadPackageManifest(root)
	if e != nil {
		return nil, e
	}
	target := filepath.Join(pm.storeRoot(), filepath.FromSlash(m.Name), m.Version)
	if _, e = os.Stat(target); e == nil {
		return nil, fmt.Errorf("包已安装: %s@%s", m.Name, m.Version)
	}
	if e = os.MkdirAll(filepath.Dir(target), 0755); e != nil {
		return nil, e
	}
	if e = copyTree(m.RootDir, target); e != nil {
		return nil, e
	}
	return LoadPackageManifest(target)
}
func unzipSafe(path, dst string) error {
	r, e := zip.OpenReader(path)
	if e != nil {
		return e
	}
	defer r.Close()
	for _, f := range r.File {
		clean := filepath.Clean(f.Name)
		if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
			return errors.New("压缩包包含不安全路径")
		}
		target := filepath.Join(dst, clean)
		if f.FileInfo().IsDir() {
			if e = os.MkdirAll(target, 0755); e != nil {
				return e
			}
			continue
		}
		if e = os.MkdirAll(filepath.Dir(target), 0755); e != nil {
			return e
		}
		in, e := f.Open()
		if e != nil {
			return e
		}
		out, e := os.Create(target)
		if e != nil {
			in.Close()
			return e
		}
		_, e = io.Copy(out, in)
		in.Close()
		out.Close()
		if e != nil {
			return e
		}
	}
	return nil
}
func PackPackage(dir, out string) (string, error) {
	m, e := LoadPackageManifest(dir)
	if e != nil {
		return "", e
	}
	if out == "" {
		out = filepath.Join(dir, fmt.Sprintf("%s-%s.phypkg", strings.ReplaceAll(m.Name, ".", "-"), m.Version))
	}
	f, e := os.Create(out)
	if e != nil {
		return "", e
	}
	zw := zip.NewWriter(f)
	e = filepath.Walk(m.RootDir, func(path string, info os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(m.RootDir, path)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "build/") || strings.HasPrefix(rel, ".git/") || strings.HasSuffix(rel, ".phypkg") {
			return nil
		}
		h, e := zip.FileInfoHeader(info)
		if e != nil {
			return e
		}
		h.Name = filepath.ToSlash(filepath.Join(m.Name+"-"+m.Version, rel))
		h.Method = zip.Deflate
		w, e := zw.CreateHeader(h)
		if e != nil {
			return e
		}
		in, e := os.Open(path)
		if e != nil {
			return e
		}
		defer in.Close()
		_, e = io.Copy(w, in)
		return e
	})
	ce := zw.Close()
	fe := f.Close()
	if e == nil {
		e = ce
	}
	if e == nil {
		e = fe
	}
	return out, e
}
func (pm *PackageManager) List() ([]*PackageManifest, error) {
	var out []*PackageManifest
	seen := map[string]bool{}
	for _, root := range pm.packageRoots() {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, e error) error {
			if e != nil || info == nil || info.IsDir() {
				return nil
			}
			if info.Name() == "phylang.package.toml" || info.Name() == "phys-package.toml" {
				if m, x := LoadPackageManifest(path); x == nil {
					key := m.Name + "@" + m.Version
					if !seen[key] {
						out = append(out, m)
						seen[key] = true
					}
				}
			}
			return nil
		})
	}
	if m, e := LoadPackageManifest(pm.ProjectRoot); e == nil {
		key := m.Name + "@" + m.Version
		if !seen[key] {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			a, _ := parseSemVersion(out[i].Version)
			b, _ := parseSemVersion(out[j].Version)
			return compareSem(a, b) > 0
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}
func InitPackage(dir, name string) error {
	if name == "" {
		name = filepath.Base(dir)
		name = strings.ToLower(strings.ReplaceAll(name, " ", "-"))
	}
	if !packageNamePattern.MatchString(name) {
		return fmt.Errorf("无效包名 %s", name)
	}
	for _, d := range []string{"src", "tests", "examples", "docs"} {
		if e := os.MkdirAll(filepath.Join(dir, d), 0755); e != nil {
			return e
		}
	}
	manifest := fmt.Sprintf(`[package]
name = %q
version = "0.1.0"
description = "请填写包说明"
license = "MIT"
authors = ["Your Name"]
repository = ""
entry = "src/package.phy"
namespace = %q
phylang = ">=0.6.0 <0.7.0"
keywords = ["physics"]
readme = "README.md"

[dependencies]

[exports]
quantities = ["ExampleRate"]
units = ["example_rate"]
laws = ["ExampleLaw"]
functions = ["scale_example"]
constants = []

[tests]
files = ["tests/basic.phy"]
`, name, strings.ReplaceAll(name, "-", "_"))
	if e := os.WriteFile(filepath.Join(dir, "phylang.package.toml"), []byte(manifest), 0644); e != nil {
		return e
	}
	source := fmt.Sprintf(`package %s {
    version "0.1.0";
    requires phylang ">=0.6.0 <0.7.0";
}

export quantity ExampleRate : Length / Time;
export unit example_rate : ExampleRate = 1 [m/s];

export fn scale_example(value, factor) {
    let result = value * factor;
    return result;
}

export law ExampleLaw {
    parameters {
        distance: Length;
        duration: Time;
        rate: ExampleRate;
    }
    assumptions { duration > 0 s; }
    equation { distance == rate * duration; }
}
`, name)
	if e := os.WriteFile(filepath.Join(dir, "src", "package.phy"), []byte(source), 0644); e != nil {
		return e
	}
	test := fmt.Sprintf(`import %s;
print 2 example_rate in [m/s];
verify ExampleLaw with { distance=10 m; duration=5 s; rate=2 [m/s]; };
`, name)
	if e := os.WriteFile(filepath.Join(dir, "tests", "basic.phy"), []byte(test), 0644); e != nil {
		return e
	}
	readme := fmt.Sprintf("# %s\n\nPhyLang 社区扩展包。\n", name)
	return os.WriteFile(filepath.Join(dir, "README.md"), []byte(readme), 0644)
}
func hashPackageDirectory(root string) (string, error) {
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if rel == "phylang.lock" || strings.HasPrefix(rel, "build/") || strings.HasPrefix(rel, ".git/") || strings.HasSuffix(rel, ".phypkg") {
			return nil
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, rel := range files {
		h.Write([]byte(rel))
		h.Write([]byte{0})
		b, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if e != nil {
			return "", e
		}
		h.Write(b)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func GenerateLock(pm *PackageManager, m *PackageManifest) (*PackageLock, error) {
	lock := &PackageLock{Format: 1, Generated: time.Now().UTC().Format(time.RFC3339)}
	seen := map[string]bool{}
	var visit func(*PackageManifest) error
	visit = func(x *PackageManifest) error {
		key := x.Name + "@" + x.Version
		if seen[key] {
			return nil
		}
		seen[key] = true
		checksum, e := hashPackageDirectory(x.RootDir)
		if e != nil {
			return e
		}
		source := "package:" + x.Name + "@" + x.Version
		if rel, relErr := filepath.Rel(pm.ProjectRoot, x.RootDir); relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			source = filepath.ToSlash(rel)
		}
		entry := PackageLockEntry{Name: x.Name, Version: x.Version, Source: source, Checksum: checksum, Dependencies: map[string]string{}}
		for n, d := range x.Dependencies {
			var dep *PackageManifest
			var e error
			if d.Path != "" {
				p := d.Path
				if !filepath.IsAbs(p) {
					p = filepath.Join(x.RootDir, p)
				}
				dep, e = LoadPackageManifest(p)
			} else {
				dep, e = pm.Resolve(n, d.Version)
			}
			if e != nil {
				if d.Optional {
					continue
				}
				return e
			}
			entry.Dependencies[n] = dep.Version
			if e = visit(dep); e != nil {
				return e
			}
		}
		lock.Packages = append(lock.Packages, entry)
		return nil
	}
	if e := visit(m); e != nil {
		return nil, e
	}
	sort.Slice(lock.Packages, func(i, j int) bool { return lock.Packages[i].Name < lock.Packages[j].Name })
	return lock, nil
}
func WriteLock(path string, l *PackageLock) error {
	// Preserve the original generation time when the semantic lock content did
	// not change. This makes `package lock` reproducible and lets CI detect real
	// dependency drift instead of timestamp-only differences.
	if previousBytes, readErr := os.ReadFile(path); readErr == nil {
		var previous PackageLock
		if json.Unmarshal(previousBytes, &previous) == nil &&
			previous.Format == l.Format &&
			reflect.DeepEqual(previous.Packages, l.Packages) &&
			previous.Generated != "" {
			l.Generated = previous.Generated
		}
	}
	b, e := json.MarshalIndent(l, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile(path, append(b, '\n'), 0644)
}
