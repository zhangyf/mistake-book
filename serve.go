package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

//go:embed web
var webFS embed.FS

// server 持有 HTTP 层共享的东西。
//
// 模式照抄 jrp：后端只出 JSON API，前端原生 JS 渲染，静态资源 go:embed 打进二进制。
// 后端全部走 COS，没有本地状态，所以这个进程在任何能连 COS 的机器上都能跑。
type server struct {
	st     *Storage
	token  string
	dryRun bool
}

func runServe(args []string) {
	fs_ := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs_.String("addr", "127.0.0.1", "监听地址（0.0.0.0 表示对外暴露，届时必须配 auth-token）")
	port := fs_.Int("port", 8081, "监听端口")
	token := fs_.String("auth-token", os.Getenv("MISTAKE_AUTH_TOKEN"),
		"Bearer token；不设则不做鉴权（仅限 127.0.0.1 本机访问）")
	dryRun := fs_.Bool("dry-run", false, "演练模式：改/删照常计算但不写入 COS")
	fs_.Parse(args)

	// fail-closed：对外暴露却没设 token = 任何人都能改索引，直接拒绝启动。
	if !isLoopback(*addr) && strings.TrimSpace(*token) == "" {
		fmt.Fprintf(os.Stderr,
			"Error: 监听地址 %s 不是本机地址，必须设置 --auth-token 或环境变量 MISTAKE_AUTH_TOKEN\n"+
				"（改/删接口会直接改写 exam-bank/index.json，不设 token 等于把错题本公开）\n", *addr)
		os.Exit(1)
	}

	st, err := NewStorage()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating storage: %v\n", err)
		os.Exit(1)
	}

	srv := &server{st: st, token: strings.TrimSpace(*token), dryRun: *dryRun}

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error mounting web assets: %v\n", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	// 静态资源不鉴权：浏览器第一次打开要能拿到页面，token 在页面里输入后存 localStorage。
	mux.Handle("/", staticHandler(sub))

	srv.registerAPI(mux)

	listen := fmt.Sprintf("%s:%d", *addr, *port)
	authNote := "（无鉴权，仅本机）"
	if srv.token != "" {
		authNote = "（已启用 token 鉴权）"
	}
	fmt.Fprintf(os.Stderr, "mistake-book serve listening on http://%s %s\n", listen, authNote)
	if *dryRun {
		fmt.Fprintf(os.Stderr, "⚠️  演练模式（--dry-run）：改/删不会写入 COS\n")
	}

	httpSrv := &http.Server{
		Addr:         listen,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
	}
	if err := httpSrv.ListenAndServe(); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}

// registerAPI 注册所有 /api 路由，全部走 requireAuth。
func (s *server) registerAPI(mux *http.ServeMux) {
	mux.HandleFunc("/api/subjects", s.requireAuth(s.handleSubjects))
	mux.HandleFunc("/api/entries", s.requireAuth(s.handleEntries))
	mux.HandleFunc("/api/entry", s.requireAuth(s.handleEntry))
	mux.HandleFunc("/api/image", s.requireAuth(s.handleImage))
	mux.HandleFunc("/api/export", s.requireAuth(s.handleExport))
}

// staticHandler 服务内嵌的前端资源，带内容 ETag 协商缓存。
//
// 不能用裸 http.FileServer：embed.FS 的 ModTime 是零值，它既不发 Last-Modified
// 也不发 ETag，浏览器只能启发式缓存 —— 结果就是部署了新二进制，浏览器还拿着旧 JS
// 继续跑，「明明修了怎么还有 bug」的锅全是它。
func staticHandler(sub fs.FS) http.Handler {
	etags := map[string]string{}
	if err := fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(sub, p)
		if err != nil {
			return err
		}
		sum := sha1.Sum(data)
		etags[p] = `"` + hex.EncodeToString(sum[:8]) + `"`
		return nil
	}); err != nil {
		panic(fmt.Errorf("扫描内嵌前端资源失败: %w", err))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		etag, ok := etags[p]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "no-cache")
		if etagMatch(r.Header.Get("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		data, _ := fs.ReadFile(sub, p)
		http.ServeContent(w, r, p, time.Time{}, bytes.NewReader(data))
	})
}

// etagMatch 宽松比较 If-None-Match：忽略 W/ 前缀与引号差异，支持逗号分隔多值。
func etagMatch(header, etag string) bool {
	if header == "" {
		return false
	}
	strip := func(s string) string {
		s = strings.TrimSpace(s)
		s = strings.TrimPrefix(s, "W/")
		return strings.Trim(s, `"`)
	}
	want := strip(etag)
	for _, part := range strings.Split(header, ",") {
		if strip(part) == want {
			return true
		}
	}
	return false
}

// requireAuth 校验 Authorization: Bearer <token>。未设 token 时直接放行（本机场景）。
func (s *server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token == "" {
			next(w, r)
			return
		}
		if subtle.ConstantTimeCompare([]byte(bearerToken(r)), []byte(s.token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
				"success": false, "error": "unauthorized：缺少或错误的 token",
			})
			return
		}
		next(w, r)
	}
}

// bearerToken 取应用层 token。
//
// ⚠️ 首选自定义头 X-Mistake-Token，而不是 Authorization:
// 线上前面还有一层 nginx Basic Auth，浏览器会自动给同源请求带上 Basic 凭据；
// 如果前端 fetch 再设 Authorization，就会把 Basic 覆盖掉，nginx 直接回 401。
// 用独立头两个认证就能共存。（2026-10-13 部署时踩到：首页 200 但 /api 全 401）
func bearerToken(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("X-Mistake-Token")); v != "" {
		return v
	}
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	// 也接受 ?token= ，方便 curl 和直接拼图片链接
	return r.URL.Query().Get("token")
}

// isLoopback 判断监听地址是否只对本机开放。
func isLoopback(addr string) bool {
	if addr == "" {
		return false
	}
	if addr == "localhost" || addr == "::1" || strings.HasPrefix(addr, "127.") {
		return true
	}
	if ip := net.ParseIP(addr); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func failJSON(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]interface{}{"success": false, "error": msg})
}

// bgctx 包级 background context。命名为 bgctx 是为了不和 import 的 "context" 撞名。
func bgctx() context.Context { return context.Background() }
