package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed studio.html
var studioHTML string

type studioCodeRequest struct {
	Source     string `json:"source"`
	Prefix     string `json:"prefix"`
	Path       string `json:"path"`
	Name       string `json:"name"`
	Constraint string `json:"constraint"`
	Query      string `json:"query"`
	URL        string `json:"url"`
	Repository string `json:"repository"`
}
type studioReply struct {
	OK     bool     `json:"ok"`
	Output string   `json:"output,omitempty"`
	Error  string   `json:"error,omitempty"`
	Items  []string `json:"items,omitempty"`
}

func decodeStudio(r *http.Request, out any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(out)
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func studioMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, studioHTML)
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "version": version, "frontend": true, "backend": true, "autocomplete": true, "packages": true, "registry": true})
	})
	mux.HandleFunc("/backend/api", apiHandler)
	mux.HandleFunc("/api/frontend/run", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, 405, studioReply{Error: "POST required"})
			return
		}
		var q studioCodeRequest
		if e := decodeStudio(r, &q); e != nil {
			writeJSON(w, 400, studioReply{Error: e.Error()})
			return
		}
		var b bytes.Buffer
		_, e := runFrontendSource(q.Source, "<Studio>", &b)
		if e != nil {
			writeJSON(w, 200, studioReply{OK: false, Output: b.String(), Error: e.Error()})
			return
		}
		writeJSON(w, 200, studioReply{OK: true, Output: b.String()})
	})
	mux.HandleFunc("/api/frontend/check", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, 405, studioReply{Error: "POST required"})
			return
		}
		var q studioCodeRequest
		if e := decodeStudio(r, &q); e != nil {
			writeJSON(w, 400, studioReply{Error: e.Error()})
			return
		}
		e := checkFrontendSource(q.Source, "<Studio>")
		if e != nil {
			writeJSON(w, 200, studioReply{OK: false, Error: e.Error()})
			return
		}
		writeJSON(w, 200, studioReply{OK: true, Output: "[PASS] 语法、量纲和规律检查通过。"})
	})
	mux.HandleFunc("/api/packages/list", func(w http.ResponseWriter, r *http.Request) {
		pm := NewPackageManager("")
		items, e := pm.List()
		if e != nil {
			writeJSON(w, 200, studioReply{Error: e.Error()})
			return
		}
		var lines []string
		for _, m := range items {
			lines = append(lines, fmt.Sprintf("%s@%s  %s", m.Name, m.Version, m.Description))
		}
		writeJSON(w, 200, studioReply{OK: true, Output: strings.Join(lines, "\n")})
	})
	mux.HandleFunc("/api/packages/validate", func(w http.ResponseWriter, r *http.Request) {
		var q studioCodeRequest
		if e := decodeStudio(r, &q); e != nil {
			writeJSON(w, 400, studioReply{Error: e.Error()})
			return
		}
		m, e := LoadPackageManifest(q.Path)
		if e == nil {
			e = validatePackageSource(m)
		}
		if e != nil {
			writeJSON(w, 200, studioReply{Error: e.Error()})
			return
		}
		writeJSON(w, 200, studioReply{OK: true, Output: fmt.Sprintf("[PASS] %s@%s 验证通过", m.Name, m.Version)})
	})
	mux.HandleFunc("/api/packages/install", func(w http.ResponseWriter, r *http.Request) {
		var q studioCodeRequest
		if e := decodeStudio(r, &q); e != nil {
			writeJSON(w, 400, studioReply{Error: e.Error()})
			return
		}
		m, e := NewPackageManager("").Install(q.Path)
		if e != nil {
			writeJSON(w, 200, studioReply{Error: e.Error()})
			return
		}
		writeJSON(w, 200, studioReply{OK: true, Output: fmt.Sprintf("[PASS] 已安装 %s@%s", m.Name, m.Version)})
	})
	mux.HandleFunc("/api/packages/registries", func(w http.ResponseWriter, r *http.Request) {
		pm := NewPackageManager("")
		cfg, e := loadRegistryConfig(pm)
		if e != nil {
			writeJSON(w, 200, studioReply{Error: e.Error()})
			return
		}
		names := make([]string, 0, len(cfg.Registries))
		for n := range cfg.Registries {
			names = append(names, n)
		}
		sort.Strings(names)
		var lines []string
		for _, n := range names {
			lines = append(lines, fmt.Sprintf("%s  %s", n, cfg.Registries[n]))
		}
		writeJSON(w, 200, studioReply{OK: true, Output: strings.Join(lines, "\n")})
	})
	mux.HandleFunc("/api/packages/registry-add", func(w http.ResponseWriter, r *http.Request) {
		var q studioCodeRequest
		if e := decodeStudio(r, &q); e != nil {
			writeJSON(w, 400, studioReply{Error: e.Error()})
			return
		}
		if strings.TrimSpace(q.Name) == "" {
			q.Name = "github"
		}
		loc := strings.TrimSpace(q.URL)
		if loc == "" && q.Repository != "" {
			var e error
			loc, e = githubPagesIndexURL(q.Repository)
			if e != nil {
				writeJSON(w, 200, studioReply{Error: e.Error()})
				return
			}
		}
		if loc == "" {
			writeJSON(w, 200, studioReply{Error: "需要 GitHub OWNER/REPO 或 index.json URL"})
			return
		}
		idx, e := loadRegistry(loc)
		if e != nil {
			writeJSON(w, 200, studioReply{Error: e.Error()})
			return
		}
		pm := NewPackageManager("")
		cfg, e := loadRegistryConfig(pm)
		if e != nil {
			writeJSON(w, 200, studioReply{Error: e.Error()})
			return
		}
		cfg.Registries[q.Name] = normalizeRegistryLocation(loc)
		if e = saveRegistryConfig(pm, cfg); e != nil {
			writeJSON(w, 200, studioReply{Error: e.Error()})
			return
		}
		writeJSON(w, 200, studioReply{OK: true, Output: fmt.Sprintf("[PASS] 已添加 %s，schema=%s，包数=%d", q.Name, idx.Schema, len(idx.Packages))})
	})
	mux.HandleFunc("/api/packages/registry-check", func(w http.ResponseWriter, r *http.Request) {
		pm := NewPackageManager("")
		cfg, e := loadRegistryConfig(pm)
		if e != nil {
			writeJSON(w, 200, studioReply{Error: e.Error()})
			return
		}
		var lines []string
		names := make([]string, 0, len(cfg.Registries))
		for n := range cfg.Registries {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			idx, err := loadRegistry(cfg.Registries[n])
			if err != nil {
				writeJSON(w, 200, studioReply{Error: fmt.Sprintf("%s: %v", n, err)})
				return
			}
			lines = append(lines, fmt.Sprintf("[PASS] %s schema=%s packages=%d", n, idx.Schema, len(idx.Packages)))
		}
		writeJSON(w, 200, studioReply{OK: true, Output: strings.Join(lines, "\n")})
	})
	mux.HandleFunc("/api/packages/search", func(w http.ResponseWriter, r *http.Request) {
		var q studioCodeRequest
		if e := decodeStudio(r, &q); e != nil {
			writeJSON(w, 400, studioReply{Error: e.Error()})
			return
		}
		items, e := registrySearch(NewPackageManager(""), q.Query)
		if e != nil {
			writeJSON(w, 200, studioReply{Error: e.Error()})
			return
		}
		var lines []string
		for _, m := range items {
			v := ""
			if len(m.Versions) > 0 {
				v = m.Versions[0].Version
			}
			lines = append(lines, fmt.Sprintf("%s@%s  %s", m.Name, v, m.Description))
		}
		writeJSON(w, 200, studioReply{OK: true, Output: strings.Join(lines, "\n")})
	})
	mux.HandleFunc("/api/packages/fetch", func(w http.ResponseWriter, r *http.Request) {
		var q studioCodeRequest
		if e := decodeStudio(r, &q); e != nil {
			writeJSON(w, 400, studioReply{Error: e.Error()})
			return
		}
		c := q.Constraint
		if c == "" {
			c = "*"
		}
		m, e := downloadRegistryPackage(NewPackageManager(""), q.Name, c)
		if e != nil {
			writeJSON(w, 200, studioReply{Error: e.Error()})
			return
		}
		writeJSON(w, 200, studioReply{OK: true, Output: fmt.Sprintf("[PASS] 已下载并安装 %s@%s", m.Name, m.Version)})
	})
	mux.HandleFunc("/api/completions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, 405, studioReply{Error: "POST required"})
			return
		}
		var q studioCodeRequest
		if e := decodeStudio(r, &q); e != nil {
			writeJSON(w, 400, studioReply{Error: e.Error()})
			return
		}
		writeJSON(w, 200, studioReply{OK: true, Items: completionItems(q.Source, q.Prefix)})
	})
	return mux
}
func openStudioBrowser(url string) error {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		c = exec.Command("open", url)
	default:
		c = exec.Command("xdg-open", url)
	}
	return c.Start()
}
func serveStudio(port int, noOpen bool) error {
	if port < 0 || port > 65535 {
		return fmt.Errorf("端口超出范围")
	}
	ln, e := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if e != nil {
		return e
	}
	actual := ln.Addr().(*net.TCPAddr).Port
	url := fmt.Sprintf("http://127.0.0.1:%d/", actual)
	fmt.Printf("PhyLang Studio Integrated %s: %s\n", version, url)
	if !noOpen {
		go func() { time.Sleep(250 * time.Millisecond); _ = openStudioBrowser(url) }()
	}
	srv := &http.Server{Handler: studioMux(), ReadHeaderTimeout: 5 * time.Second}
	return srv.Serve(ln)
}
func studioSelfTest() error {
	srv := &http.Server{Handler: studioMux(), ReadHeaderTimeout: 2 * time.Second}
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return e
	}
	go srv.Serve(ln)
	defer srv.Shutdown(context.Background())
	base := "http://" + ln.Addr().String()
	payload := `{"source":"let m=2 kg; let a=3 [m/s^2]; print m*a in N;"}`
	r, e := http.Post(base+"/api/frontend/run", "application/json", strings.NewReader(payload))
	if e != nil {
		return e
	}
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if !bytes.Contains(b, []byte("6 N")) {
		return fmt.Errorf("Studio 前端 API 失败: %s", b)
	}
	payload = `{"operation":"run","expression":"x*x+1","assignments":{"x":3}}`
	r, e = http.Post(base+"/backend/api", "application/json", strings.NewReader(payload))
	if e != nil {
		return e
	}
	b, _ = io.ReadAll(r.Body)
	r.Body.Close()
	if !bytes.Contains(b, []byte(`"result":10`)) {
		return fmt.Errorf("Studio 后端 API 失败: %s", b)
	}
	return nil
}
func parsePort(args []string) (int, bool, error) {
	port := 0
	noOpen := false
	for _, a := range args {
		if a == "--no-open" {
			noOpen = true
			continue
		}
		n, e := strconv.Atoi(a)
		if e != nil {
			return 0, false, e
		}
		port = n
	}
	return port, noOpen, nil
}
