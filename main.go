package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// newCtx 返回一个带超时的 context 及其 cancel。
func newCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 2*time.Minute)
}

const usage = `mistake-book - 错题本

把试卷照片摘出的题目 Markdown + 原图上传到腾讯云 COS，并维护全局索引 exam-bank/index.json。

Usage:
  mistake-book <command> [options]

Commands:
  path       计算（不上传）md / 原图的 COS key
  up         上传原图与 md
  index-add  合并条目到索引（同 id 视为更新）
  index-rm   按 id 删除索引条目
  list       列出索引条目（可按科目/试卷/对错过滤）
  search     关键词检索
  get        打印某个 COS 对象的原文（默认索引指向的 md）
  doctor     检查凭证与桶连通性
  serve      启动错题本 Web 服务（浏览 / 改 / 删）

Examples:
  mistake-book path --subject 数学 --exam "第一章有理数测验" --date 2026-09-21 --photo IMG_20260921_222934
  mistake-book up --image photo.jpg --md q4.md --subject 数学 --exam "第一章有理数测验" --date 2026-09-21 --photo IMG_20260921_222934
  mistake-book index-add --input entry.json
  mistake-book index-add --input -          # 从 stdin 读
  mistake-book list --subject 数学 --result wrong
  mistake-book search --kw 乘方
  mistake-book get --key exam-bank/数学/2026-09-第一章有理数测验/2026-09-21-IMG_20260921_222934.md
  mistake-book doctor

  mistake-book serve                                    # 本机 http://127.0.0.1:8081
  mistake-book serve --port 8099 --dry-run              # 演练：改/删不写 COS
  mistake-book serve --addr 0.0.0.0 --auth-token xxxx   # 对外暴露必须带 token
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(1)
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	switch cmd {
	case "path":
		runPath(args)
	case "up":
		runUp(args)
	case "index-add":
		runIndexAdd(args)
	case "index-rm":
		runIndexRm(args)
	case "list":
		runList(args)
	case "search":
		runSearch(args)
	case "get":
		runGet(args)
	case "doctor":
		runDoctor(args)
	case "serve":
		runServe(args)
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "未知命令: %s\n\n", cmd)
		fmt.Print(usage)
		os.Exit(1)
	}
}

// ============================================================================
// 工具
// ============================================================================

func output(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

func fail(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}

var invalidChars = regexp.MustCompile(`[\\/:*?"<>|\s]+`)

// slug 把科目/试卷名/照片名安全化为路径片段。
func slug(s string) string {
	s = invalidChars.ReplaceAllString(strings.TrimSpace(s), "-")
	s = regexp.MustCompile(`-+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "未命名"
	}
	return s
}

func ym(date string) string {
	if len(date) >= 7 {
		return date[:7]
	}
	return date
}

// pickExam 取章节：--exam 和 --chapter 是同义词，谁填了用谁。
func pickExam(exam, chapter string) string {
	if strings.TrimSpace(exam) != "" {
		return strings.TrimSpace(exam)
	}
	return strings.TrimSpace(chapter)
}

// buildKeys 生成 md / 图片 的 COS key。
func buildKeys(subject, exam, date, photo, ext string) (mdKey, imgKey string) {
	folder := fmt.Sprintf("%s/%s/%s-%s", rootPrefix, slug(subject), ym(date), slug(exam))
	base := fmt.Sprintf("%s-%s", date, slug(photo))
	return folder + "/" + base + ".md", folder + "/images/" + base + ext
}

// entryID 生成条目 id：{科目}-{日期}-{照片}-{题号}
func entryID(subject, date, photo, qno string) string {
	return fmt.Sprintf("%s-%s-%s-%s", slug(subject), date, slug(photo), slug(qno))
}

// ============================================================================
// path
// ============================================================================

func runPath(args []string) {
	fs := flag.NewFlagSet("path", flag.ExitOnError)
	subject := fs.String("subject", "", "科目（必填）")
	exam := fs.String("exam", "", "章节（必填，如「第三章 有理数」）")
	chapter := fs.String("chapter", "", "--exam 的同义别名")
	date := fs.String("date", time.Now().Format("2006-01-02"), "日期 YYYY-MM-DD")
	photo := fs.String("photo", "", "照片原始文件名（不含扩展名，必填）")
	ext := fs.String("ext", ".jpg", "原图扩展名")
	fs.Parse(args)

	ex := pickExam(*exam, *chapter)
	if *subject == "" || ex == "" || *photo == "" {
		fail("path 需要 --subject / --exam（或 --chapter） / --photo")
	}
	mdKey, imgKey := buildKeys(*subject, ex, *date, *photo, *ext)
	output(map[string]interface{}{
		"success":    true,
		"md_key":     mdKey,
		"image_key":  imgKey,
		"index_key":  indexKey,
		"suggest_id": entryID(*subject, *date, *photo, "第X题"),
	})
}

// ============================================================================
// up
// ============================================================================

func runUp(args []string) {
	fs := flag.NewFlagSet("up", flag.ExitOnError)
	image := fs.String("image", "", "本地原图路径（必填）")
	md := fs.String("md", "", "本地 md 路径（必填）")
	subject := fs.String("subject", "", "科目（必填）")
	exam := fs.String("exam", "", "章节（必填，如「第三章 有理数」）")
	chapter := fs.String("chapter", "", "--exam 的同义别名")
	date := fs.String("date", time.Now().Format("2006-01-02"), "日期 YYYY-MM-DD")
	photo := fs.String("photo", "", "照片原始文件名（不含扩展名），默认取本地图片文件名")
	expires := fs.Int("expires", 24, "预签名 URL 有效小时数，0 = 不生成")
	fs.Parse(args)

	ex := pickExam(*exam, *chapter)
	if *image == "" || *md == "" || *subject == "" || ex == "" {
		fail("up 需要 --image / --md / --subject / --exam（或 --chapter）")
	}
	stem := *photo
	if stem == "" {
		stem = strings.TrimSuffix(filepath.Base(*image), filepath.Ext(*image))
	}
	ext := strings.ToLower(filepath.Ext(*image))
	if ext == "" {
		ext = ".jpg"
	}
	mdKey, imgKey := buildKeys(*subject, ex, *date, stem, ext)

	st, err := NewStorage()
	if err != nil {
		fail("%v", err)
	}
	ctx, cancel := newCtx()
	defer cancel()
	if err := st.PutFile(ctx, imgKey, *image); err != nil {
		fail("上传原图失败: %v", err)
	}
	if err := st.PutFile(ctx, mdKey, *md); err != nil {
		fail("上传 md 失败: %v", err)
	}

	res := map[string]interface{}{
		"success":   true,
		"image_key": imgKey,
		"md_key":    mdKey,
	}
	if *expires > 0 {
		if url, err := st.Presign(ctx, imgKey, time.Duration(*expires)*time.Hour); err == nil {
			res["image_url"] = url
		}
	}
	output(res)
}

// ============================================================================
// index-add / index-rm
// ============================================================================

func runIndexAdd(args []string) {
	fs := flag.NewFlagSet("index-add", flag.ExitOnError)
	input := fs.String("input", "", "条目 JSON 文件（对象 / 数组 / {\"items\":[]}），- 表示 stdin")
	fs.Parse(args)

	var data []byte
	var err error
	if *input == "-" || *input == "" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(*input)
	}
	if err != nil {
		fail("读取条目失败: %v", err)
	}

	var entries []Entry
	var single Entry
	var arr []Entry
	var wrapped struct {
		Items []Entry `json:"items"`
	}
	trimmed := strings.TrimSpace(string(data))
	switch {
	case strings.HasPrefix(trimmed, "["):
		if err := json.Unmarshal(data, &arr); err != nil {
			fail("解析条目数组失败: %v", err)
		}
		entries = arr
	default:
		if err := json.Unmarshal(data, &wrapped); err == nil && len(wrapped.Items) > 0 {
			entries = wrapped.Items
		} else if err := json.Unmarshal(data, &single); err == nil {
			entries = []Entry{single}
		} else {
			fail("解析条目失败: 需要对象 / 数组 / {\"items\":[]}")
		}
	}

	st, err := NewStorage()
	if err != nil {
		fail("%v", err)
	}
	ctx, cancel := newCtx()
	defer cancel()
	idx, err := st.GetIndex(ctx)
	if err != nil {
		fail("读取索引失败: %v", err)
	}
	for i := range entries {
		e := &entries[i]
		if e.ID == "" {
			e.ID = entryID(e.Subject, e.Date, e.Photo, e.Qno)
		}
		if e.Date == "" {
			e.Date = time.Now().Format("2006-01-02")
		}
	}
	added, updated := idx.MergeEntries(entries)
	if err := st.PutIndex(ctx, idx); err != nil {
		fail("上传索引失败: %v", err)
	}
	output(map[string]interface{}{
		"success":   true,
		"index_key": indexKey,
		"total":     len(idx.Items),
		"added":     added,
		"updated":   updated,
	})
}

func runIndexRm(args []string) {
	fs := flag.NewFlagSet("index-rm", flag.ExitOnError)
	id := fs.String("id", "", "条目 id（必填）")
	fs.Parse(args)
	if *id == "" {
		fail("index-rm 需要 --id")
	}
	st, err := NewStorage()
	if err != nil {
		fail("%v", err)
	}
	ctx, cancel := newCtx()
	defer cancel()
	idx, err := st.GetIndex(ctx)
	if err != nil {
		fail("读取索引失败: %v", err)
	}
	kept := make([]Entry, 0, len(idx.Items))
	removed := false
	for _, it := range idx.Items {
		if it.ID == *id {
			removed = true
			continue
		}
		kept = append(kept, it)
	}
	if !removed {
		fail("索引里没有 id=%s", *id)
	}
	idx.Items = kept
	if err := st.PutIndex(ctx, idx); err != nil {
		fail("上传索引失败: %v", err)
	}
	output(map[string]interface{}{"success": true, "removed": *id, "total": len(idx.Items)})
}

// ============================================================================
// list / search / get
// ============================================================================

func runList(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	subject := fs.String("subject", "", "按科目过滤（精确）")
	exam := fs.String("exam", "", "按章节过滤（子串）")
	chapter := fs.String("chapter", "", "--exam 的同义别名")
	result := fs.String("result", "", "correct / wrong / unknown")
	limit := fs.Int("limit", 0, "只返回最后 N 条")
	fs.Parse(args)

	st, err := NewStorage()
	if err != nil {
		fail("%v", err)
	}
	ctx, cancel := newCtx()
	defer cancel()
	idx, err := st.GetIndex(ctx)
	if err != nil {
		fail("读取索引失败: %v", err)
	}
	items := make([]Entry, 0, len(idx.Items))
	for _, it := range idx.Items {
		if *subject != "" && it.Subject != *subject {
			continue
		}
		if ex := pickExam(*exam, *chapter); ex != "" && !strings.Contains(it.Exam, ex) {
			continue
		}
		if *result != "" && it.Result != *result {
			continue
		}
		items = append(items, it)
	}
	if *limit > 0 && len(items) > *limit {
		items = items[len(items)-*limit:]
	}
	output(map[string]interface{}{"success": true, "count": len(items), "items": items})
}

func runSearch(args []string) {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	kw := fs.String("kw", "", "关键词（必填）")
	fs.Parse(args)
	if *kw == "" {
		fail("search 需要 --kw")
	}
	st, err := NewStorage()
	if err != nil {
		fail("%v", err)
	}
	ctx, cancel := newCtx()
	defer cancel()
	idx, err := st.GetIndex(ctx)
	if err != nil {
		fail("读取索引失败: %v", err)
	}
	lower := strings.ToLower(*kw)
	hits := make([]Entry, 0)
	for _, it := range idx.Items {
		blob, _ := json.Marshal(it)
		if strings.Contains(strings.ToLower(string(blob)), lower) {
			hits = append(hits, it)
		}
	}
	output(map[string]interface{}{"success": true, "count": len(hits), "items": hits})
}

func runGet(args []string) {
	fs := flag.NewFlagSet("get", flag.ExitOnError)
	key := fs.String("key", "", "COS key（必填）")
	out := fs.String("output", "", "写入文件（默认打印到 stdout）")
	fs.Parse(args)
	if *key == "" {
		fail("get 需要 --key")
	}
	st, err := NewStorage()
	if err != nil {
		fail("%v", err)
	}
	ctx, cancel := newCtx()
	defer cancel()
	data, err := st.GetAll(ctx, *key)
	if err != nil {
		fail("下载失败: %v", err)
	}
	if *out != "" {
		if err := os.WriteFile(*out, data, 0o644); err != nil {
			fail("写文件失败: %v", err)
		}
		output(map[string]interface{}{"success": true, "output": *out, "bytes": len(data)})
		return
	}
	os.Stdout.Write(data)
}

// ============================================================================
// doctor
// ============================================================================

func runDoctor(args []string) {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	fs.Parse(args)
	cfg, err := loadCOSConfig()
	if err != nil {
		fail("%v", err)
	}
	st, err := NewStorage()
	if err != nil {
		fail("%v", err)
	}
	ctx, cancel := newCtx()
	defer cancel()
	idx, err := st.GetIndex(ctx)
	res := map[string]interface{}{
		"success":   err == nil,
		"bucket":    cfg.Bucket,
		"region":    cfg.Region,
		"provider":  fmt.Sprintf("%v", cfg.Provider),
		"cred_dir":  credDir(),
		"index_key": indexKey,
		"index_err": "",
	}
	if err != nil {
		res["index_err"] = err.Error()
		res["success"] = false
	} else {
		res["entries"] = len(idx.Items)
	}
	output(res)
	if res["success"] != true {
		os.Exit(1)
	}
}
