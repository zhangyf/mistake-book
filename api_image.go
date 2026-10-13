package main

import (
	"bytes"
	"net/http"
	"path"
	"strings"
	"time"
)

// GET /api/image?key=exam-bank/.../xxx.jpg
//
// 原图走后端代理而不是预签名 URL：凭证不出后端、链接永不过期、老师在内网也能看。
// 代价是图片流量经过服务器——单老师使用无压力，靠 ETag 协商缓存减少回源。
func (s *server) handleImage(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	// 防越权：只允许读错题本目录下的对象
	if key == "" || !strings.HasPrefix(key, "exam-bank/") {
		failJSON(w, 400, "invalid key")
		return
	}
	data, err := s.st.GetAll(bgctx(), key)
	if err != nil || len(data) == 0 {
		failJSON(w, 404, "图片不存在或读取失败")
		return
	}

	if ct := imageContentType(key); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	if etag := s.headETag(key); etag != "" {
		w.Header().Set("ETag", etag)
		if etagMatch(r.Header.Get("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	http.ServeContent(w, r, path.Base(key), time.Time{}, bytes.NewReader(data))
}

// headETag 取对象的 ETag 做协商缓存；取不到就返回空（不影响出图）。
func (s *server) headETag(key string) string {
	info, err := s.st.store.HeadObject(bgctx(), key)
	if err != nil || info == nil {
		return ""
	}
	return info.ETag
}

func imageContentType(key string) string {
	switch strings.ToLower(path.Ext(key)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".heic":
		return "image/heic"
	}
	return ""
}
