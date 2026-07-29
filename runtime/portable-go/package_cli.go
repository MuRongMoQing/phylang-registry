package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func packageUsage() string {
	return `PhyLang 社区包管理器

  phylang package init [dir] [--name package.name]
  phylang package validate [dir]
  phylang package audit [dir]
  phylang package lock [dir]
  phylang package test [dir]
  phylang package pack [dir] [--out file.phypkg]
  phylang package install <dir|file.phypkg>
  phylang package uninstall <name> [version]
  phylang package list
  phylang package info <name> [constraint]
  phylang package graph [dir]
  phylang package contribution [dir] [--out contribution.json]
  phylang package registry add <name> <index-url-or-file>
  phylang package registry add-github <name> <OWNER/REPO> [--pages-url URL]
  phylang package registry list
  phylang package registry check [name]
  phylang package search <query>
  phylang package fetch <name> [constraint]
  phylang package registry-build <phypkg-dir> [--out index.json]
  phylang package github-site <phypkg-dir> --out site --repo OWNER/REPO [--pages-url URL]
`
}
func packageDirArg(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "--") {
			return a
		}
	}
	d, _ := os.Getwd()
	return d
}
func flagValue(args []string, name string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == name {
			return args[i+1]
		}
	}
	return ""
}
func validatePackageSource(m *PackageManifest) error {
	frontendMu.Lock()
	defer frontendMu.Unlock()
	b, e := os.ReadFile(filepath.Join(m.RootDir, filepath.FromSlash(m.Entry)))
	if e != nil {
		return e
	}
	env := newRootFEnv(m.RootDir)
	env.Manifest = m
	if e = runFrontendSourceWithEnv(string(b), m.Entry, env, io.Discard); e != nil {
		return e
	}
	applyManifestExports(env, m)
	if e = applyManifestTypeMetadata(env, m); e != nil {
		return e
	}
	for n, k := range env.Exports {
		switch k {
		case ExportQuantity:
			if _, ok := quantityDefs[n]; !ok {
				return fmt.Errorf("清单导出的物理量 %s 未定义", n)
			}
		case ExportUnit:
			if _, ok := unitDefs[n]; !ok {
				return fmt.Errorf("清单导出的单位 %s 未定义", n)
			}
		case ExportLaw:
			if _, ok := env.Laws[n]; !ok {
				return fmt.Errorf("清单导出的规律 %s 未定义", n)
			}
		case ExportFunction:
			if _, ok := env.Functions[n]; !ok {
				return fmt.Errorf("清单导出的函数 %s 未定义", n)
			}
		case ExportConstant:
			if _, ok := env.Values[n]; !ok {
				return fmt.Errorf("清单导出的常量 %s 未定义", n)
			}
		}
	}
	if env.SourceMeta.Name != "" && env.SourceMeta.Name != m.Name {
		return fmt.Errorf("源码 package 名称 %s 与清单 %s 不一致", env.SourceMeta.Name, m.Name)
	}
	return nil
}
func auditPackage(m *PackageManifest) []string {
	var issues []string
	if strings.TrimSpace(m.Description) == "" || strings.Contains(m.Description, "请填写") {
		issues = append(issues, "package.description 不能为空")
	}
	if strings.TrimSpace(m.License) == "" {
		issues = append(issues, "package.license 不能为空")
	}
	if len(m.Authors) == 0 {
		issues = append(issues, "package.authors 至少包含一位作者")
	}
	if strings.TrimSpace(m.Repository) == "" {
		issues = append(issues, "建议填写 package.repository")
	}
	if len(m.Keywords) == 0 {
		issues = append(issues, "建议填写 package.keywords")
	}
	if len(m.TestFiles) == 0 {
		issues = append(issues, "tests.files 至少应包含一个测试")
	}
	if len(m.Exports.Quantities)+len(m.Exports.Units)+len(m.Exports.Laws)+len(m.Exports.Functions)+len(m.Exports.Constants) == 0 {
		issues = append(issues, "社区包必须在 [exports] 中显式声明公开 API")
	}
	if _, e := os.Stat(filepath.Join(m.RootDir, m.Readme)); e != nil {
		issues = append(issues, "README 文件不存在")
	}
	if m.Repository != "" && !strings.HasPrefix(m.Repository, "https://") {
		issues = append(issues, "repository 应使用 https://")
	}
	return issues
}
func testPackage(m *PackageManifest) error {
	if e := validatePackageSource(m); e != nil {
		return e
	}
	if len(m.TestFiles) == 0 {
		return errors.New("清单未声明 tests.files")
	}
	for _, rel := range m.TestFiles {
		p := filepath.Join(m.RootDir, filepath.FromSlash(rel))
		var out strings.Builder
		if e := runFrontendFile(p, &out); e != nil {
			return fmt.Errorf("测试 %s 失败: %w", rel, e)
		}
		fmt.Printf("[PASS] %s\n", rel)
		if out.Len() > 0 {
			fmt.Print(out.String())
		}
	}
	return nil
}
func uninstallPackage(pm *PackageManager, name, ver string) error {
	base := filepath.Join(pm.storeRoot(), filepath.FromSlash(name))
	if ver != "" {
		base = filepath.Join(base, ver)
	}
	if _, e := os.Stat(base); e != nil {
		return e
	}
	return os.RemoveAll(base)
}
func dependencyGraph(pm *PackageManager, m *PackageManifest) ([]string, error) {
	var lines []string
	seen := map[string]bool{}
	var walk func(*PackageManifest, string) error
	walk = func(x *PackageManifest, indent string) error {
		key := x.Name + "@" + x.Version
		lines = append(lines, indent+key)
		if seen[key] {
			lines[len(lines)-1] += " (已列出)"
			return nil
		}
		seen[key] = true
		names := make([]string, 0, len(x.Dependencies))
		for n := range x.Dependencies {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			d := x.Dependencies[n]
			var child *PackageManifest
			var e error
			if d.Path != "" {
				p := d.Path
				if !filepath.IsAbs(p) {
					p = filepath.Join(x.RootDir, p)
				}
				child, e = LoadPackageManifest(p)
			} else {
				child, e = pm.Resolve(n, d.Version)
			}
			if e != nil {
				if d.Optional {
					lines = append(lines, indent+"  "+n+" (可选，未解析)")
					continue
				}
				return e
			}
			if e = walk(child, indent+"  "); e != nil {
				return e
			}
		}
		return nil
	}
	return lines, walk(m, "")
}
func contributionRecord(m *PackageManifest) map[string]any {
	return map[string]any{"schema": "phylang.community/v1", "name": m.Name, "version": m.Version, "description": m.Description, "license": m.License, "authors": m.Authors, "repository": m.Repository, "homepage": m.Homepage, "phylang": m.PhyLang, "keywords": m.Keywords, "exports": map[string]any{"quantities": m.Exports.Quantities, "units": m.Exports.Units, "laws": m.Exports.Laws, "functions": m.Exports.Functions, "constants": m.Exports.Constants}, "submitted_with": "PhyLang " + version}
}
func packageCommand(args []string) error {
	if len(args) == 0 || args[0] == "help" {
		fmt.Print(packageUsage())
		return nil
	}
	sub := args[0]
	rest := args[1:]
	cwd, _ := os.Getwd()
	pm := NewPackageManager(cwd)
	switch sub {
	case "init":
		dir := packageDirArg(rest)
		name := flagValue(rest, "--name")
		if e := InitPackage(dir, name); e != nil {
			return e
		}
		fmt.Println("[PASS] 已创建包:", dir)
		return nil
	case "validate":
		dir := packageDirArg(rest)
		m, e := LoadPackageManifest(dir)
		if e != nil {
			return e
		}
		if e = validatePackageSource(m); e != nil {
			return e
		}
		fmt.Printf("[PASS] %s@%s 清单、入口、导出和兼容性验证通过。\n", m.Name, m.Version)
		return nil
	case "audit":
		dir := packageDirArg(rest)
		m, e := LoadPackageManifest(dir)
		if e != nil {
			return e
		}
		if e = validatePackageSource(m); e != nil {
			return e
		}
		issues := auditPackage(m)
		if len(issues) > 0 {
			for _, x := range issues {
				fmt.Println("[WARN]", x)
			}
			return fmt.Errorf("社区发布审核发现 %d 项问题", len(issues))
		}
		fmt.Println("[PASS] 社区发布审核通过")
		return nil
	case "lock":
		dir := packageDirArg(rest)
		m, e := LoadPackageManifest(dir)
		if e != nil {
			return e
		}
		pm = NewPackageManager(m.RootDir)
		// 重新生成锁文件时忽略旧锁，否则依赖内容更新后无法自我修复。
		pm.Locked = map[string]PackageLockEntry{}
		lock, e := GenerateLock(pm, m)
		if e != nil {
			return e
		}
		path := filepath.Join(m.RootDir, "phylang.lock")
		if e = WriteLock(path, lock); e != nil {
			return e
		}
		fmt.Println("[PASS] 已写入", path)
		return nil
	case "test":
		dir := packageDirArg(rest)
		m, e := LoadPackageManifest(dir)
		if e != nil {
			return e
		}
		return testPackage(m)
	case "pack", "build":
		dir := packageDirArg(rest)
		m, e := LoadPackageManifest(dir)
		if e != nil {
			return e
		}
		if e = validatePackageSource(m); e != nil {
			return e
		}
		out := flagValue(rest, "--out")
		path, e := PackPackage(m.RootDir, out)
		if e != nil {
			return e
		}
		fmt.Println("[PASS] 已生成", path)
		return nil
	case "install":
		if len(rest) < 1 {
			return errors.New("package install 需要目录或 .phypkg")
		}
		m, e := pm.Install(rest[0])
		if e != nil {
			return e
		}
		fmt.Printf("[PASS] 已安装 %s@%s 到 %s\n", m.Name, m.Version, m.RootDir)
		return nil
	case "uninstall":
		if len(rest) < 1 {
			return errors.New("package uninstall 需要包名")
		}
		ver := ""
		if len(rest) > 1 {
			ver = rest[1]
		}
		if e := uninstallPackage(pm, rest[0], ver); e != nil {
			return e
		}
		fmt.Println("[PASS] 已卸载", rest[0], ver)
		return nil
	case "list":
		items, e := pm.List()
		if e != nil {
			return e
		}
		if len(items) == 0 {
			fmt.Println("未安装社区包")
			return nil
		}
		for _, m := range items {
			fmt.Printf("%-36s %-12s %s\n", m.Name, m.Version, m.Description)
		}
		return nil
	case "info":
		if len(rest) < 1 {
			return errors.New("package info 需要包名")
		}
		constraint := "*"
		if len(rest) > 1 {
			constraint = rest[1]
		}
		m, e := pm.Resolve(rest[0], constraint)
		if e != nil {
			return e
		}
		b, _ := json.MarshalIndent(m, "", "  ")
		fmt.Println(string(b))
		return nil
	case "graph":
		dir := packageDirArg(rest)
		m, e := LoadPackageManifest(dir)
		if e != nil {
			return e
		}
		lines, e := dependencyGraph(NewPackageManager(m.RootDir), m)
		if e != nil {
			return e
		}
		fmt.Println(strings.Join(lines, "\n"))
		return nil
	case "registry":
		if len(rest) == 0 {
			return errors.New("package registry 需要 add/add-github/list/remove/check")
		}
		cfg, e := loadRegistryConfig(pm)
		if e != nil {
			return e
		}
		switch rest[0] {
		case "list":
			if len(cfg.Registries) == 0 {
				fmt.Println("未配置社区仓库")
				return nil
			}
			names := make([]string, 0, len(cfg.Registries))
			for n := range cfg.Registries {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				fmt.Printf("%-20s %s\n", n, cfg.Registries[n])
			}
			return nil
		case "add":
			if len(rest) < 3 {
				return errors.New("registry add 需要名称和地址")
			}
			if _, e = loadRegistry(rest[2]); e != nil {
				return fmt.Errorf("仓库索引验证失败: %w", e)
			}
			cfg.Registries[rest[1]] = rest[2]
			if e = saveRegistryConfig(pm, cfg); e != nil {
				return e
			}
			fmt.Println("[PASS] 已添加社区仓库", rest[1])
			return nil
		case "add-github":
			if len(rest) < 3 {
				return errors.New("registry add-github 需要名称和 OWNER/REPO")
			}
			loc := flagValue(rest[3:], "--pages-url")
			if loc == "" {
				loc, e = githubPagesIndexURL(rest[2])
				if e != nil {
					return e
				}
			} else if !strings.HasSuffix(loc, ".json") {
				loc = strings.TrimRight(loc, "/") + "/index.json"
			}
			if _, e = loadRegistry(loc); e != nil {
				return fmt.Errorf("无法读取 GitHub Pages 仓库：%w", e)
			}
			cfg.Registries[rest[1]] = loc
			if e = saveRegistryConfig(pm, cfg); e != nil {
				return e
			}
			fmt.Printf("[PASS] 已添加 GitHub 仓库 %s -> %s\n", rest[1], loc)
			return nil
		case "check":
			names := make([]string, 0, len(cfg.Registries))
			if len(rest) > 1 {
				if _, ok := cfg.Registries[rest[1]]; !ok {
					return fmt.Errorf("未配置仓库 %s", rest[1])
				}
				names = append(names, rest[1])
			} else {
				for n := range cfg.Registries {
					names = append(names, n)
				}
				sort.Strings(names)
			}
			for _, n := range names {
				idx, err := loadRegistry(cfg.Registries[n])
				if err != nil {
					return fmt.Errorf("仓库 %s 检查失败: %w", n, err)
				}
				fmt.Printf("[PASS] %-16s schema=%s packages=%d updated=%s\n", n, idx.Schema, len(idx.Packages), idx.Updated)
			}
			return nil
		case "remove":
			if len(rest) < 2 {
				return errors.New("registry remove 需要名称")
			}
			delete(cfg.Registries, rest[1])
			return saveRegistryConfig(pm, cfg)
		default:
			return fmt.Errorf("未知 registry 子命令 %s", rest[0])
		}
	case "search":
		query := ""
		if len(rest) > 0 {
			query = rest[0]
		}
		items, e := registrySearch(pm, query)
		if e != nil {
			return e
		}
		for _, x := range items {
			latest := ""
			if len(x.Versions) > 0 {
				latest = x.Versions[0].Version
			}
			fmt.Printf("%-36s %-12s %s\n", x.Name, latest, x.Description)
		}
		if len(items) == 0 {
			fmt.Println("没有匹配包")
		}
		return nil
	case "fetch":
		if len(rest) < 1 {
			return errors.New("package fetch 需要包名")
		}
		constraint := "*"
		if len(rest) > 1 {
			constraint = rest[1]
		}
		m, e := downloadRegistryPackage(pm, rest[0], constraint)
		if e != nil {
			return e
		}
		fmt.Printf("[PASS] 已下载并安装 %s@%s\n", m.Name, m.Version)
		return nil
	case "registry-build":
		dir := packageDirArg(rest)
		idx, e := buildRegistryIndex(dir, "local-community")
		if e != nil {
			return e
		}
		out := flagValue(rest, "--out")
		if out == "" {
			out = filepath.Join(dir, "index.json")
		}
		absOut, _ := filepath.Abs(out)
		for pi := range idx.Packages {
			for vi := range idx.Packages[pi].Versions {
				lp := idx.Packages[pi].Versions[vi].LocalPath
				if lp != "" {
					rel, _ := filepath.Rel(filepath.Dir(absOut), lp)
					idx.Packages[pi].Versions[vi].URL = filepath.ToSlash(rel)
				}
			}
		}
		b, _ := json.MarshalIndent(idx, "", "  ")
		if e = os.WriteFile(out, append(b, '\n'), 0644); e != nil {
			return e
		}
		fmt.Println("[PASS] 已生成仓库索引", out)
		return nil
	case "github-site":
		dir := packageDirArg(rest)
		out := flagValue(rest, "--out")
		if out == "" {
			return errors.New("github-site 需要 --out 输出目录")
		}
		repo := flagValue(rest, "--repo")
		if repo == "" {
			return errors.New("github-site 需要 --repo OWNER/REPO")
		}
		pages := flagValue(rest, "--pages-url")
		cfg := GitHubRegistryConfig{Repository: repo, PagesBase: pages, RegistryName: "phylang-community", Description: "PhyLang GitHub 社区包仓库"}
		idx, e := buildGitHubRegistrySite(dir, out, cfg)
		if e != nil {
			return e
		}
		fmt.Printf("[PASS] 已生成 GitHub Pages 站点 %s（%d 个包）\n", out, len(idx.Packages))
		return nil
	case "contribution":
		dir := packageDirArg(rest)
		m, e := LoadPackageManifest(dir)
		if e != nil {
			return e
		}
		if issues := auditPackage(m); len(issues) > 0 {
			return fmt.Errorf("贡献记录生成前必须通过 audit: %s", strings.Join(issues, "; "))
		}
		b, _ := json.MarshalIndent(contributionRecord(m), "", "  ")
		out := flagValue(rest, "--out")
		if out == "" {
			out = filepath.Join(m.RootDir, "phylang-contribution.json")
		}
		if e = os.WriteFile(out, append(b, '\n'), 0644); e != nil {
			return e
		}
		fmt.Println("[PASS] 已生成社区贡献记录", out)
		return nil
	default:
		return fmt.Errorf("未知 package 子命令 %s\n%s", sub, packageUsage())
	}
}
