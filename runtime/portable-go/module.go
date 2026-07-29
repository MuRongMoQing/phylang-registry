package main

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type ImportSpec struct {
	Target, Constraint, Alias string
	Only                      map[string]bool
	Raw                       string
}

func parseImportSpec(text string) (ImportSpec, error) {
	var s ImportSpec
	s.Raw = text
	text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "import")), ";"))
	if text == "" {
		return s, errors.New("import 缺少目标")
	}
	// only {a,b}
	if i := strings.Index(text, " only "); i >= 0 {
		tail := strings.TrimSpace(text[i+6:])
		text = strings.TrimSpace(text[:i])
		if !strings.HasPrefix(tail, "{") || !strings.HasSuffix(tail, "}") {
			return s, errors.New("import only 需要 {symbol,...}")
		}
		s.Only = map[string]bool{}
		for _, x := range strings.Split(tail[1:len(tail)-1], ",") {
			x = strings.TrimSpace(x)
			if x != "" {
				s.Only[x] = true
			}
		}
	}
	if i := strings.Index(text, " as "); i >= 0 {
		s.Alias = strings.TrimSpace(text[i+4:])
		text = strings.TrimSpace(text[:i])
		if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(s.Alias) {
			return s, fmt.Errorf("无效 import 别名 %s", s.Alias)
		}
	}
	if strings.HasPrefix(text, "\"") {
		if !strings.HasSuffix(text, "\"") {
			return s, errors.New("import 路径字符串未闭合")
		}
		s.Target = strings.Trim(text, "\"")
		return s, nil
	}
	if i := strings.LastIndex(text, "@"); i > 0 {
		s.Target = strings.TrimSpace(text[:i])
		s.Constraint = strings.TrimSpace(text[i+1:])
	} else {
		s.Target = text
		s.Constraint = "*"
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(s.Target) {
		return s, fmt.Errorf("无效 import 目标 %s", s.Target)
	}
	return s, nil
}
func rootEnv(e *FEnv) *FEnv {
	for e.Parent != nil {
		e = e.Parent
	}
	return e
}
func freshModuleEnv(importer *FEnv, base string) *FEnv {
	r := rootEnv(importer)
	m := &FEnv{Values: map[string]FValue{}, Consts: map[string]bool{}, Functions: map[string]FFunction{}, Laws: map[string]FLaw{}, Exports: map[string]ExportKind{}, Aliases: map[string]string{}, DeclaredQuantities: map[string]Dim{}, DeclaredUnits: map[string]UnitDef{}, BaseDir: base, Packages: importer.Packages, Imports: importer.Imports}
	for _, n := range []string{"pi", "e", "c", "g0"} {
		if v, ok := r.lookupValue(n); ok {
			m.Values[n] = v
			m.Consts[n] = true
		}
	}
	if l, ok := r.lookupLaw("NewtonSecondLaw"); ok {
		m.Laws[l.Name] = l
	}
	return m
}
func parsePackageBlock(t string) (SourcePackageMetadata, error) {
	m := SourcePackageMetadata{Extensions: map[string]string{}}
	re := regexp.MustCompile(`(?s)^package\s+([A-Za-z0-9_.-]+)\s*\{(.*)\}$`)
	x := re.FindStringSubmatch(strings.TrimSpace(t))
	if x == nil {
		return m, errors.New("package 语法错误")
	}
	m.Name = x[1]
	fields, _ := topStatements(x[2])
	for _, f := range fields {
		f = strings.TrimSpace(strings.TrimSuffix(f, ";"))
		parts := strings.Fields(f)
		if len(parts) < 2 {
			continue
		}
		value := strings.Trim(strings.TrimSpace(strings.TrimPrefix(f, parts[0])), "\"")
		switch parts[0] {
		case "version":
			m.Version = value
		case "namespace":
			m.Namespace = value
		case "requires":
			if len(parts) >= 3 && parts[1] == "phylang" {
				m.Requires = strings.Trim(strings.TrimSpace(strings.TrimPrefix(f, "requires phylang")), "\"")
			} else {
				return m, fmt.Errorf("仅支持 requires phylang")
			}
		case "extension":
			if len(parts) < 3 {
				return m, fmt.Errorf("extension 需要名称和值")
			}
			name := parts[1]
			raw := strings.TrimSpace(strings.TrimPrefix(f, "extension "+name))
			m.Extensions[name] = strings.Trim(raw, "\"")
		default:
			return m, fmt.Errorf("未知 package 字段 %s；未来扩展请使用 extension name \"value\"", parts[0])
		}
	}
	if m.Version != "" {
		if _, e := parseSemVersion(m.Version); e != nil {
			return m, e
		}
	}
	if m.Requires != "" && !satisfiesVersion(version, m.Requires) {
		return m, fmt.Errorf("源码包要求 PhyLang %s，当前为 %s", m.Requires, version)
	}
	return m, nil
}
func applyManifestExports(env *FEnv, m *PackageManifest) {
	if m == nil {
		return
	}
	for _, n := range m.Exports.Quantities {
		env.Exports[n] = ExportQuantity
	}
	for _, n := range m.Exports.Units {
		env.Exports[n] = ExportUnit
	}
	for _, n := range m.Exports.Laws {
		env.Exports[n] = ExportLaw
	}
	for _, n := range m.Exports.Functions {
		env.Exports[n] = ExportFunction
	}
	for _, n := range m.Exports.Constants {
		env.Exports[n] = ExportConstant
	}
}
func applyManifestTypeMetadata(env *FEnv, m *PackageManifest) error {
	if m == nil {
		return nil
	}
	for name, meta := range m.Quantities {
		actual, ok := quantityDefs[name]
		if !ok {
			return fmt.Errorf("清单 [quantities.%s] 对应的 quantity 未定义", name)
		}
		want, e := parseDim(meta.Dimension)
		if e != nil {
			return e
		}
		if actual != want {
			return fmt.Errorf("清单 [quantities.%s] 量纲 %s 与源码 %s 不一致", name, want, actual)
		}
	}
	for name, meta := range m.Units {
		actual, ok := unitDefs[name]
		if !ok {
			return fmt.Errorf("清单 [units.%s] 对应的 unit 未定义", name)
		}
		q, ok := quantityDefs[meta.Quantity]
		if !ok {
			if parsed, e := parseDim(meta.Quantity); e == nil {
				q, ok = parsed, true
			}
		}
		if !ok || actual.Dim != q {
			return fmt.Errorf("清单 [units.%s].quantity 与源码单位量纲不一致", name)
		}
		declaredValue, e := evalFrontendExpr(meta.Definition, env)
		if e != nil {
			return fmt.Errorf("[units.%s].definition: %w", name, e)
		}
		if declaredValue.Kind != FQuantity {
			return fmt.Errorf("[units.%s].definition 必须是物理量", name)
		}
		declared := UnitDef{Factor: declaredValue.Number, Dim: declaredValue.Dimension}
		if declared.Dim != actual.Dim || math.Abs(declared.Factor-actual.Factor) > 1e-12*math.Max(1, math.Abs(actual.Factor)) {
			return fmt.Errorf("清单 [units.%s].definition 与源码定义不一致", name)
		}
		for _, alias := range meta.Aliases {
			if old, exists := unitDefs[alias]; exists && old != actual {
				return fmt.Errorf("单位别名冲突: %s", alias)
			}
			unitDefs[alias] = actual
			env.DeclaredUnits[alias] = actual
			if env.Exports[name] == ExportUnit {
				env.Exports[alias] = ExportUnit
			}
		}
	}
	return nil
}

func legacyExports(env *FEnv) {
	if len(env.Exports) > 0 {
		return
	}
	for n := range env.Values {
		if n != "pi" && n != "e" && n != "c" && n != "g0" && !strings.HasPrefix(n, "_") {
			env.Exports[n] = ExportConstant
		}
	}
	for n := range env.Functions {
		if !strings.HasPrefix(n, "_") {
			env.Exports[n] = ExportFunction
		}
	}
	for n := range env.Laws {
		if n != "NewtonSecondLaw" && !strings.HasPrefix(n, "_") {
			env.Exports[n] = ExportLaw
		}
	}
	for n := range env.DeclaredQuantities {
		if !strings.HasPrefix(n, "_") {
			env.Exports[n] = ExportQuantity
		}
	}
	for n := range env.DeclaredUnits {
		if !strings.HasPrefix(n, "_") {
			env.Exports[n] = ExportUnit
		}
	}
}
func mergeModule(importer, module *FEnv, spec ImportSpec) error {
	names := make([]string, 0, len(module.Exports))
	for n := range module.Exports {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		if len(spec.Only) > 0 && !spec.Only[name] {
			continue
		}
		target := name
		if spec.Alias != "" {
			target = spec.Alias + "." + name
		}
		kind := module.Exports[name]
		switch kind {
		case ExportConstant:
			v, ok := module.Values[name]
			if !ok {
				return fmt.Errorf("导出常量 %s 不存在", name)
			}
			if old, exists := importer.lookupValue(target); exists && old != v {
				return fmt.Errorf("导入符号冲突: %s", target)
			}
			importer.Values[target] = v
			importer.Consts[target] = true
		case ExportFunction:
			v, ok := module.Functions[name]
			if !ok {
				return fmt.Errorf("导出函数 %s 不存在", name)
			}
			if _, exists := importer.lookupFunction(target); exists {
				return fmt.Errorf("导入函数冲突: %s", target)
			}
			importer.Functions[target] = v
		case ExportLaw:
			v, ok := module.Laws[name]
			if !ok {
				return fmt.Errorf("导出规律 %s 不存在", name)
			}
			if old, exists := importer.lookupLaw(target); exists {
				if len(old.Params) != len(v.Params) || strings.Join(old.Equations, ";") != strings.Join(v.Equations, ";") {
					return fmt.Errorf("导入规律冲突: %s", target)
				}
				continue
			}
			importer.Laws[target] = v
		case ExportQuantity:
			q, ok := quantityDefs[name]
			if !ok {
				return fmt.Errorf("导出物理量 %s 未注册", name)
			}
			if spec.Alias != "" {
				if old, exists := quantityDefs[target]; exists && old != q {
					return fmt.Errorf("导入物理量冲突: %s", target)
				}
				quantityDefs[target] = q
			}
		case ExportUnit:
			u, ok := unitDefs[name]
			if !ok {
				return fmt.Errorf("导出单位 %s 未注册", name)
			}
			if spec.Alias != "" {
				if old, exists := unitDefs[target]; exists && old != u {
					return fmt.Errorf("导入单位冲突: %s", target)
				}
				unitDefs[target] = u
			}
		}
	}
	if spec.Alias != "" {
		importer.Aliases[spec.Alias] = spec.Target
	}
	return nil
}
func resolveImport(env *FEnv, spec ImportSpec) (entry, key string, manifest *PackageManifest, err error) {
	if spec.Target == "physics.classical" || spec.Target == "physics.si" {
		if m, e := env.Packages.Resolve(spec.Target, spec.Constraint); e == nil {
			entry = filepath.Join(m.RootDir, filepath.FromSlash(m.Entry))
			return entry, "package:" + m.Name + "@" + m.Version, m, nil
		}
		return "", "builtin:" + spec.Target, nil, nil
	}
	pathLike := strings.Contains(spec.Target, "/") || strings.Contains(spec.Target, "\\") || strings.HasSuffix(spec.Target, ".phy")
	if pathLike {
		p := spec.Target
		if !filepath.IsAbs(p) {
			p = filepath.Join(env.BaseDir, p)
		}
		info, e := os.Stat(p)
		if e != nil {
			return "", "", nil, e
		}
		if info.IsDir() {
			manifest, e = LoadPackageManifest(p)
			if e != nil {
				return "", "", nil, e
			}
			entry = filepath.Join(manifest.RootDir, filepath.FromSlash(manifest.Entry))
		} else {
			entry = p
		}
		abs, _ := filepath.Abs(entry)
		return abs, "file:" + abs, manifest, nil
	}
	manifest, err = env.Packages.Resolve(spec.Target, spec.Constraint)
	if err != nil {
		return
	}
	entry = filepath.Join(manifest.RootDir, filepath.FromSlash(manifest.Entry))
	key = "package:" + manifest.Name + "@" + manifest.Version
	return
}
func importDependency(env *FEnv, dep PackageDependency, owner *PackageManifest, out io.Writer) error {
	if dep.Path != "" {
		p := dep.Path
		if !filepath.IsAbs(p) {
			p = filepath.Join(owner.RootDir, p)
		}
		return executeImport("import \""+p+"\";", env, out)
	}
	return executeImport("import "+dep.Name+"@"+dep.Version+";", env, out)
}
func executeImport(stmt string, env *FEnv, out io.Writer) error {
	spec, e := parseImportSpec(stmt)
	if e != nil {
		return e
	}
	entry, key, manifest, e := resolveImport(env, spec)
	if e != nil {
		return e
	}
	if strings.HasPrefix(key, "builtin:") {
		env.Imports.Loaded[key] = true
		return nil
	}
	if env.Imports.Loading[key] {
		return fmt.Errorf("检测到循环导入: %s", key)
	}
	if module, ok := env.Imports.Modules[key]; ok {
		return mergeModule(env, module, spec)
	}
	env.Imports.Loading[key] = true
	defer delete(env.Imports.Loading, key)
	module := freshModuleEnv(env, filepath.Dir(entry))
	module.Manifest = manifest
	if manifest != nil {
		deps := make([]string, 0, len(manifest.Dependencies))
		for n := range manifest.Dependencies {
			deps = append(deps, n)
		}
		sort.Strings(deps)
		for _, n := range deps {
			d := manifest.Dependencies[n]
			if e = importDependency(module, d, manifest, out); e != nil {
				if d.Optional {
					continue
				}
				return fmt.Errorf("依赖 %s: %w", n, e)
			}
		}
	}
	b, e := os.ReadFile(entry)
	if e != nil {
		return e
	}
	if e = runFrontendSourceWithEnv(string(b), entry, module, out); e != nil {
		return e
	}
	if manifest != nil {
		if module.SourceMeta.Name != "" && module.SourceMeta.Name != manifest.Name {
			return fmt.Errorf("源码 package 名称 %s 与清单 %s 不一致", module.SourceMeta.Name, manifest.Name)
		}
		if module.SourceMeta.Version != "" && module.SourceMeta.Version != manifest.Version {
			return fmt.Errorf("源码 package 版本 %s 与清单 %s 不一致", module.SourceMeta.Version, manifest.Version)
		}
		applyManifestExports(module, manifest)
		if e = applyManifestTypeMetadata(module, manifest); e != nil {
			return e
		}
	}
	legacyExports(module)
	env.Imports.Modules[key] = module
	env.Imports.Loaded[key] = true
	return mergeModule(env, module, spec)
}
func exportListStatement(t string, env *FEnv) error {
	body := strings.TrimSpace(strings.TrimPrefix(t, "export"))
	if !strings.HasPrefix(body, "{") || !strings.HasSuffix(body, "}") {
		return errors.New("export 列表应为 export { A, B }")
	}
	for _, n := range strings.Split(body[1:len(body)-1], ",") {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if _, ok := env.Values[n]; ok {
			env.Exports[n] = ExportConstant
			continue
		}
		if _, ok := env.Functions[n]; ok {
			env.Exports[n] = ExportFunction
			continue
		}
		if _, ok := env.Laws[n]; ok {
			env.Exports[n] = ExportLaw
			continue
		}
		if _, ok := env.DeclaredQuantities[n]; ok {
			env.Exports[n] = ExportQuantity
			continue
		}
		if _, ok := env.DeclaredUnits[n]; ok {
			env.Exports[n] = ExportUnit
			continue
		}
		return fmt.Errorf("无法导出未声明符号 %s", n)
	}
	return nil
}
