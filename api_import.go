package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxImportPhotos        = 20
	maxImportPhotoBytes    = 10 << 20
	maxImportBodyBytes     = 12 << 20
	maxImportMetadataBytes = 1 << 20
	maxImportQuestions     = 30
	maxImportPixels        = 40000000
	importMarkdownPrefix   = "<!-- mistake-book-import:v1 "
	importMarkdownSuffix   = " -->"
)

type importFailure struct {
	status  int
	message string
	// retryable 表示这次失败属于模型侧故障，允许自动换备用模型重试一次。
	retryable bool
}

func (e *importFailure) Error() string { return e.message }
func importError(status int, message string) error {
	return &importFailure{status: status, message: message}
}
func retryableImportError(status int, message string) error {
	return &importFailure{status: status, message: message, retryable: true}
}
func writeImportError(w http.ResponseWriter, err error) {
	var failure *importFailure
	if errors.As(err, &failure) {
		failJSON(w, failure.status, failure.message)
		return
	}
	failJSON(w, 500, "导入失败，请保留原照片和导入编号后重试")
}

type importPhoto struct {
	data      []byte
	mime, ext string
}
type importQuestion struct {
	Qno           string   `json:"qno"`
	Qtype         string   `json:"qtype"`
	Stem          string   `json:"stem"`
	StudentAnswer string   `json:"student_answer"`
	Chapter       string   `json:"chapter"`
	Source        string   `json:"source"`
	Analysis      string   `json:"analysis"`
	Note          string   `json:"note"`
	Tags          []string `json:"tags"`
}

type importMetadata struct {
	ImportID  string           `json:"import_id"`
	Confirmed bool             `json:"confirmed,omitempty"`
	Subject   string           `json:"subject"`
	Date      string           `json:"date"`
	Chapter   string           `json:"chapter"`
	Source    string           `json:"source"`
	Questions []importQuestion `json:"questions"`
}

type importMarkdownDocument struct {
	Version     int              `json:"version"`
	PayloadHash string           `json:"payload_sha256"`
	Questions   []importQuestion `json:"questions"`
}

type importResult struct {
	Success         bool     `json:"success"`
	IDs             []string `json:"ids"`
	Imported        int      `json:"imported"`
	AlreadyImported bool     `json:"already_imported"`
	DryRun          bool     `json:"dry_run"`
}

// 只应用于新增导入接口；不改变已有浏览、编辑接口的认证方式。
func importSameOrigin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		site := r.Header.Get("Sec-Fetch-Site")
		if site != "" && site != "same-origin" && site != "none" {
			failJSON(w, 403, "不允许跨站导入请求")
			return
		}
		if values, ok := r.Header["Origin"]; ok {
			if len(values) != 1 {
				failJSON(w, 403, "导入来源无效")
				return
			}
			u, err := url.Parse(values[0])
			// 允许同一 Host 前的 HTTPS 终止代理；不信任 X-Forwarded-Host。
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil ||
				!strings.EqualFold(u.Host, r.Host) || u.Path != "" || u.RawQuery != "" || u.Fragment != "" ||
				(r.TLS != nil && u.Scheme != "https") {
				failJSON(w, 403, "不允许跨站导入请求")
				return
			}
		}
		next(w, r)
	}
}

func importMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	failJSON(w, 405, "请求方法不支持")
	return false
}

func (s *server) handleImportConfig(w http.ResponseWriter, r *http.Request) {
	if !importMethod(w, r, http.MethodGet) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{
		"success": true, "enabled": s.vision.enabled(), "model": s.vision.model(),
		"fallback_model": s.vision.fallbackModel(),
		"max_photos": maxImportPhotos, "max_photo_bytes": maxImportPhotoBytes,
		"max_questions": maxImportQuestions, "dry_run": s.dryRun,
	})
}

func (s *server) handleImportRecognize(w http.ResponseWriter, r *http.Request) {
	if !importMethod(w, r, http.MethodPost) {
		return
	}
	if !s.vision.enabled() {
		failJSON(w, 503, "照片识别未配置")
		return
	}
	photo, text, err := readImportMultipart(w, r, "question_numbers", 2000)
	if err != nil {
		writeImportError(w, err)
		return
	}
	if !validImportText(text, 500, true, false) {
		failJSON(w, 400, "题号不能为空且最多500字符")
		return
	}
	questions, model, err := s.vision.recognize(r.Context(), photo, strings.TrimSpace(text))
	if err != nil {
		writeImportError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{
		"success": true, "questions": questions, "model": model,
		"fallback_used": model != s.vision.model(),
	})
}

func (s *server) handleImportConfirm(w http.ResponseWriter, r *http.Request) {
	if !importMethod(w, r, http.MethodPost) {
		return
	}
	photo, text, err := readImportMultipart(w, r, "metadata", maxImportMetadataBytes)
	if err != nil {
		writeImportError(w, err)
		return
	}
	var metadata importMetadata
	if decodeImportJSON([]byte(text), &metadata) != nil {
		failJSON(w, 400, "导入信息 JSON 格式无效")
		return
	}
	if err := normalizeImportMetadata(&metadata); err != nil {
		writeImportError(w, err)
		return
	}
	result, err := s.confirmImport(r.Context(), photo, metadata)
	if err != nil {
		writeImportError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, result)
}

// 整体和单字段双重有界，全部在内存中读取，不使用用户文件名或临时文件。
func readImportMultipart(w http.ResponseWriter, r *http.Request, field string, fieldLimit int64) (importPhoto, string, error) {
	var photo importPhoto
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBodyBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return photo, "", importError(413, "请求体不能超过12MiB")
		}
		return photo, "", importError(400, "读取上传内容失败")
	}
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		return photo, "", importError(400, "请使用 multipart 上传原图")
	}
	reader := multipart.NewReader(bytes.NewReader(data), params["boundary"])
	seen := map[string]bool{}
	var text string
	for {
		part, err := reader.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return photo, "", importError(400, "上传格式无效")
		}
		if part.Header.Get("Content-Transfer-Encoding") != "" {
			part.Close()
			return photo, "", importError(400, "请直接上传未转码的原图")
		}
		name := part.FormName()
		if (name != "photo" && name != field) || seen[name] {
			part.Close()
			return photo, "", importError(400, "只允许一张照片和一份导入信息")
		}
		seen[name] = true
		limit := fieldLimit
		if name == "photo" {
			limit = maxImportPhotoBytes
		}
		content, err := io.ReadAll(io.LimitReader(part, limit+1))
		part.Close()
		if err != nil {
			return photo, "", importError(400, "读取上传字段失败")
		}
		if int64(len(content)) > limit {
			return photo, "", importError(413, "照片或导入信息超过大小限制")
		}
		if name == "photo" {
			photo.data = content
		} else {
			if part.FileName() != "" {
				return photo, "", importError(400, "导入信息必须是文本字段")
			}
			text = string(content)
		}
	}
	if !seen["photo"] || !seen[field] {
		return photo, "", importError(400, "缺少照片或导入信息")
	}
	photo.mime = http.DetectContentType(photo.data)
	switch photo.mime {
	case "image/jpeg":
		photo.ext = ".jpg"
	case "image/png":
		photo.ext = ".png"
	default:
		return photo, "", importError(400, "只支持 JPEG 或 PNG 原图")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(photo.data))
	if err != nil || (format != "jpeg" && format != "png") || cfg.Width <= 0 || cfg.Height <= 0 ||
		int64(cfg.Width)*int64(cfg.Height) > maxImportPixels {
		return photo, "", importError(400, "图片损坏或超过4000万像素")
	}
	return photo, text, nil
}

func decodeImportJSON(data []byte, dst any) error {
	if !utf8.Valid(data) {
		return errors.New("JSON 编码无效")
	}
	if err := checkImportJSONKeys(json.NewDecoder(bytes.NewReader(data))); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("JSON 后有多余内容")
	}
	return nil
}

// 拒绝重复 JSON 键，避免 confirmed 等字段出现相互矛盾的值。
func checkImportJSONKeys(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	keys := map[string]bool{}
	for decoder.More() {
		if delim == '{' {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || keys[name] {
				return errors.New("JSON 字段重复或无效")
			}
			keys[name] = true
		}
		if err := checkImportJSONKeys(decoder); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}

func validImportText(text string, max int, required, singleLine bool) bool {
	if !utf8.ValidString(text) || utf8.RuneCountInString(text) > max || (required && strings.TrimSpace(text) == "") {
		return false
	}
	for _, r := range text {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return false
		}
		if singleLine && (r == '\n' || r == '\r' || r == '\u2028' || r == '\u2029') {
			return false
		}
	}
	return true
}

var importIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var importSimpleQno = regexp.MustCompile(`^(?:第)?([0-9]+)(?:题)?[.、]?$`)
var importSubQno = regexp.MustCompile(`^(?:第)?([0-9]+)(?:题)?(?:第)?\(([0-9]+)\)(?:小问)?$`)

func normalizeImportQno(qno string) string {
	qno = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		if r >= '！' && r <= '～' {
			return r - 0xfee0
		}
		return r
	}, qno)
	if m := importSubQno.FindStringSubmatch(qno); m != nil {
		sub := strings.TrimSuffix(strings.TrimPrefix(normalizeImportQno(m[2]), "第"), "题")
		return normalizeImportQno(m[1]) + "(" + sub + ")"
	}
	if m := importSimpleQno.FindStringSubmatch(qno); m != nil {
		n := strings.TrimLeft(m[1], "0")
		if n == "" {
			n = "0"
		}
		return "第" + n + "题"
	}
	return qno
}

func validImportSource(source string) bool {
	switch source {
	case "", "校内作业", "课外班作业", "校内考试", "自我复习":
		return true
	}
	return false
}

func normalizeImportMetadata(m *importMetadata) error {
	if !m.Confirmed {
		return importError(400, "请核对原图并明确确认后再入库")
	}
	if !importIDPattern.MatchString(m.ImportID) {
		return importError(400, "导入编号必须是32位小写十六进制随机ID")
	}
	if !validImportText(m.Subject, 64, true, true) || !validImportText(m.Chapter, 128, false, true) ||
		!validImportText(m.Source, 16, false, true) {
		return importError(400, "科目、章节或出处长度无效")
	}
	m.Subject, m.Chapter, m.Source = strings.TrimSpace(m.Subject), strings.TrimSpace(m.Chapter), strings.TrimSpace(m.Source)
	if slug(m.Subject) == "." || slug(m.Subject) == ".." {
		return importError(400, "科目不能是路径保留名称")
	}
	if !validImportSource(m.Source) {
		return importError(400, "错题出处不在允许范围内")
	}
	date, err := time.Parse("2006-01-02", m.Date)
	if err != nil || date.Year() < 1 || date.Format("2006-01-02") != m.Date {
		return importError(400, "日期必须是合法的 YYYY-MM-DD")
	}
	if len(m.Questions) == 0 || len(m.Questions) > maxImportQuestions {
		return importError(400, "每张照片须包含1至30题")
	}
	seen, slugs, ids := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i := range m.Questions {
		q := &m.Questions[i]
		if !validImportText(q.Qno, 64, true, true) || !validImportText(q.Qtype, 64, false, true) ||
			!validImportText(q.Stem, 12000, true, false) || !validImportText(q.StudentAnswer, 8000, false, false) ||
			!validImportText(q.Chapter, 128, false, true) || !validImportText(q.Source, 16, false, true) ||
			!validImportText(q.Analysis, 4000, false, false) || !validImportText(q.Note, 2000, false, false) || len(q.Tags) > 20 {
			return importError(400, "题目字段为空、过长或数量超限")
		}
		q.Qno = normalizeImportQno(q.Qno)
		q.Qtype, q.Chapter, q.Source = strings.TrimSpace(q.Qtype), strings.TrimSpace(q.Chapter), strings.TrimSpace(q.Source)
		// 前端已填入批次默认值；逐题显式留空必须保留，不能恢复批次出处。
		if !validImportSource(q.Source) {
			return importError(400, "错题出处不在允许范围内")
		}
		for _, tag := range q.Tags {
			if !validImportText(tag, 32, true, true) {
				return importError(400, "标签不能为空且最多32字符")
			}
		}
		q.Tags = compactTags(q.Tags)
		slices.Sort(q.Tags)
		// 只统一换行；原题中的空格、空行、公式和手写不裁剪。
		for _, p := range []*string{&q.Stem, &q.StudentAnswer, &q.Analysis, &q.Note} {
			*p = strings.ReplaceAll(strings.ReplaceAll(*p, "\r\n", "\n"), "\r", "\n")
		}
		id := entryID(m.Subject, m.Date, "import-"+m.ImportID, q.Qno)
		if seen[q.Qno] || slugs[slug(q.Qno)] || ids[id] {
			return importError(400, "同一照片内题号或题目ID冲突")
		}
		seen[q.Qno], slugs[slug(q.Qno)], ids[id] = true, true, true
	}
	return nil
}

func importPayloadHash(photo []byte, metadata importMetadata) string {
	metadata.Confirmed = false
	data, _ := json.Marshal(metadata)
	h := sha256.New()
	h.Write([]byte("mistake-book-import:v1\x00"))
	// 长度前缀区分二进制照片与规范化 JSON 的边界。
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(photo)))
	h.Write(size[:])
	h.Write(photo)
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// JSON 标记保留原始正文；json.Marshal 会转义 <、>，用户无法闭合 HTML 注释。
// 普通 Markdown 仍可独立阅读；行首分隔符转义后不再被旧解析器当成结构。
func renderImportMarkdown(questions []importQuestion, payloadHash string) []byte {
	data, _ := json.Marshal(importMarkdownDocument{Version: 1, PayloadHash: payloadHash, Questions: questions})
	var out strings.Builder
	out.WriteString(importMarkdownPrefix)
	out.Write(data)
	out.WriteString(importMarkdownSuffix + "\n\n")
	for _, q := range questions {
		fmt.Fprintf(&out, "## %s\n\n**题干**：\n%s\n\n**我的作答**：\n%s\n\n**错因/要点**：\n%s\n\n",
			q.Qno, escapeImportMarkdown(q.Stem), escapeImportMarkdown(q.StudentAnswer), escapeImportMarkdown(q.Analysis))
	}
	return []byte(out.String())
}

func escapeImportMarkdown(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "**") || strings.HasPrefix(trimmed, "<!--") {
			offset := len(line) - len(trimmed)
			lines[i] = line[:offset] + "\\" + trimmed
		}
	}
	return strings.Join(lines, "\n")
}

// resolvePhotoRefs 把草稿里 figure 块的 @photo 占位换成真实原图 key。
//
// 网页草稿阶段还不知道原图最终存到哪个 key（key 由 科目/章节/日期/导入编号 拼出来），
// 所以框选题图时统一写 @photo。这里在算 payload hash 和渲染 md 之前换掉，
// 保证：重试幂等（同样的输入算出同样的 hash）、落盘的 md 里永远是真实 key。
func resolvePhotoRefs(m *importMetadata, photo importPhoto) {
	name := "import-" + m.ImportID
	_, imageKey := buildKeys(m.Subject, m.Chapter, m.Date, name, photo.ext)
	if imageKey == "" {
		return
	}
	for i := range m.Questions {
		m.Questions[i].Stem = resolvePhotoAlias(m.Questions[i].Stem, imageKey)
	}
}

func importEntries(m importMetadata, photo importPhoto) ([]Entry, []string, string, string) {
	name := "import-" + m.ImportID
	mdKey, imageKey := buildKeys(m.Subject, m.Chapter, m.Date, name, photo.ext)
	entries, ids := make([]Entry, 0, len(m.Questions)), make([]string, 0, len(m.Questions))
	for _, q := range m.Questions {
		id := entryID(m.Subject, m.Date, name, q.Qno)
		entries = append(entries, Entry{ID: id, Subject: m.Subject, Exam: q.Chapter, Date: m.Date,
			Photo: name + photo.ext, Qno: q.Qno, Qtype: q.Qtype, Result: "unknown", Source: q.Source,
			Analysis: q.Analysis, Tags: q.Tags, MdKey: mdKey, ImageKey: imageKey, Note: q.Note,
			UpdatedAt: time.Now().Format("2006-01-02 15:04:05")})
		ids = append(ids, id)
	}
	return entries, ids, mdKey, imageKey
}

func sameImportIdentity(a, b Entry) bool {
	return a.ID == b.ID && a.Subject == b.Subject && a.Date == b.Date && a.Photo == b.Photo &&
		a.Qno == b.Qno && a.MdKey == b.MdKey && a.ImageKey == b.ImageKey
}

// allowOwn 只在本次请求确实写过索引后使用，重放保留已入库条目的用户编辑。
func checkImportIndex(idx *Index, m importMetadata, receipt ImportReceipt, entries []Entry, allowOwn bool) (bool, error) {
	if idx == nil {
		return false, importError(500, "读取索引失败，已停止写入")
	}
	if existing, ok := idx.Imports[m.ImportID]; ok {
		if existing.PayloadHash != receipt.PayloadHash || !slices.Equal(existing.IDs, receipt.IDs) {
			return false, importError(409, "导入编号已被其他内容使用，请勿更换原请求内容")
		}
		return true, nil
	}
	wanted := make(map[string]Entry, len(entries))
	keys := map[string]bool{}
	for _, e := range entries {
		wanted[e.ID] = e
		keys[e.MdKey], keys[e.ImageKey] = true, true
	}
	seen := map[string]bool{}
	for _, e := range idx.Items {
		own, exists := wanted[e.ID]
		if exists || keys[e.MdKey] || keys[e.ImageKey] {
			if !allowOwn || !exists || seen[e.ID] || !sameImportIdentity(e, own) {
				return false, importError(409, "题目ID或对象路径已存在，已停止导入以保护原数据")
			}
		}
		seen[e.ID] = true
	}
	for _, other := range idx.Imports {
		for _, id := range other.IDs {
			if _, ok := wanted[id]; ok {
				return false, importError(409, "题目ID已被其他导入占用")
			}
		}
	}
	return false, nil
}

func (s *server) confirmImport(ctx context.Context, photo importPhoto, m importMetadata) (importResult, error) {
	resolvePhotoRefs(&m, photo)
	entries, ids, mdKey, imageKey := importEntries(m, photo)
	hash := importPayloadHash(photo.data, m)
	receipt := ImportReceipt{PayloadHash: hash, IDs: ids}
	result := importResult{Success: true, IDs: ids, Imported: len(ids), DryRun: s.dryRun}
	md := renderImportMarkdown(m.Questions, hash)
	indexMu.Lock()
	defer indexMu.Unlock()

	idx, err := s.st.GetIndex(ctx)
	if err != nil {
		return result, importError(500, "读取索引失败，未写入任何对象")
	}
	already, err := checkImportIndex(idx, m, receipt, entries, false)
	if err != nil {
		return result, err
	}
	if already {
		result.AlreadyImported = true
		return result, nil
	}

	objects := []struct {
		key    string
		data   []byte
		exists bool
	}{{key: mdKey, data: md}, {key: imageKey, data: photo.data}}
	// 所有对象先检查，任意冲突/读取失败均零写入；已有同内容孤立对象不重写。
	for i := range objects {
		old, err := s.st.GetAll(ctx, objects[i].key)
		if err == nil {
			if !bytes.Equal(old, objects[i].data) {
				return result, importError(409, "对象路径已有不同内容，已停止导入")
			}
			objects[i].exists = true
		} else if !objectNotFound(err) {
			return result, importError(500, "读取对象失败，未写入任何对象")
		}
	}
	if s.dryRun {
		return result, nil
	}
	// Markdown 先写，包含 payload 哈希，照片上传失败后仍能核对原样重试。
	for _, object := range objects {
		if object.exists {
			continue
		}
		if err := s.st.PutBytes(ctx, object.key, object.data); err != nil {
			return result, importError(500, "上传失败，可能遗留未索引对象；请用相同导入编号和原内容重试")
		}
	}
	idx, err = s.st.GetIndex(ctx)
	if err != nil {
		return result, importError(500, "对象已上传但索引读取失败；请保留原内容和导入编号重试")
	}
	allowOwn := false
	for attempt := 0; attempt < 3; attempt++ {
		already, err = checkImportIndex(idx, m, receipt, entries, allowOwn)
		if err != nil {
			return result, err
		}
		if already {
			result.AlreadyImported = !allowOwn
			return result, nil
		}
		present := map[string]bool{}
		for _, entry := range idx.Items {
			present[entry.ID] = true
		}
		for _, entry := range entries {
			if !present[entry.ID] {
				idx.Items = append(idx.Items, entry)
			}
		}
		if idx.Imports == nil {
			idx.Imports = map[string]ImportReceipt{}
		}
		idx.Imports[m.ImportID] = receipt
		if err := s.st.PutIndex(ctx, idx); err != nil {
			return result, importError(500, "对象已上传但索引写入未确认；请用相同导入编号和原内容重试")
		}
		allowOwn = true
		idx, err = s.st.GetIndex(ctx)
		if err != nil {
			return result, importError(500, "索引写后校验失败；请用相同导入编号和原内容重试")
		}
		already, err = checkImportIndex(idx, m, receipt, entries, true)
		if err != nil {
			return result, err
		}
		if already {
			return result, nil
		}
	}
	return result, importError(409, "索引并发冲突，可能已上传对象；请用相同导入编号和原内容重试")
}
