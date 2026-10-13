package main

import (
	"encoding/base64"
	"encoding/json"
	"html"
	"io/fs"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ============================================================================
// POST /api/export —— 按选择的格式和内容导出错题本
//
// 格式：
//   md   —— Markdown，纯文本，方便再贴到别处 / 导入别的工具
//   html —— 单文件 HTML：KaTeX（CSS+JS+字体）全部内联，断网也能打开
//   pdf  —— 不做真·PDF 生成。中文字体要嵌进 PDF 才有字，那是几 MB 的活儿，
//           纯 Go 无依赖做不干净。改成吐一份排版好的 HTML 并自动唤起
//           window.print()，在打印对话框里选「另存为 PDF」——公式是 KaTeX
//           矢量字，比截图清楚，文件还小。
//
// 内容（fields 可多选）：
//   meta 元信息 / stem 题干 / answer 我的作答 / analysis 错因要点 /
//   solution 答案正解 / note 备注 / image 原图 / other md 里的其他字段
//
// 原图：md 用 7 天有效的预签名链接（贴链接，别把几 MB base64 塞进 md）；
// html/pdf 直接内嵌 base64，保证单文件离线可用、打印不缺图。
// ============================================================================

type exportField struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// exportFields 是「导出内容」的全部可选项，顺序即默认勾选顺序。
var exportFields = []exportField{
	{"meta", "元信息（科目/试卷/日期/题号/出处/知识点/对错）"},
	{"stem", "题干"},
	{"answer", "我的作答"},
	{"analysis", "错因要点"},
	{"solution", "答案/正解"},
	{"note", "备注"},
	{"image", "原图"},
	{"other", "md 里的其他字段"},
}

type exportReq struct {
	Subject   string   `json:"subject"`
	IDs       []string `json:"ids"`        // 为空 = 导出该科目全部；非空 = 只导出这些（按给定顺序）
	Format    string   `json:"format"`     // md | html | pdf
	Fields    []string `json:"fields"`     // 为空 = 全选
	WithImage bool     `json:"with_image"` // 兼容：默认跟 fields 里的 image 一致
	Title     string   `json:"title"`      // 可选，默认 "<科目> 错题本"
}

// exportItem 是一道题导出前的规范化结构（md 字段 + 索引字段合并后的结果）。
type exportItem struct {
	ID       string
	Title    string
	Meta     [][2]string
	Tags     []string
	Stem     string
	Answer   string
	Analysis string
	Solution string
	Note     string
	Other    []Section
	ImageSrc string // md: 预签名 URL；html/pdf: data URI
}

// md 字段别名：不同批次录入的字段名可能不一样，认一组别名比认一个名字稳。
var (
	stemKeys     = []string{"题干", "题目"}
	answerKeys   = []string{"我的作答", "作答", "我的答案"}
	analysisKeys = []string{"错因/要点", "错因要点", "错因", "要点", "错因分析", "错误原因"}
	solutionKeys = []string{"答案", "正解", "正确答案", "标准答案", "参考解答"}
)

func (s *server) handleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		failJSON(w, 405, "导出用 POST")
		return
	}
	var req exportReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		failJSON(w, 400, "请求体解析失败: "+err.Error())
		return
	}

	format := strings.ToLower(strings.TrimSpace(req.Format))
	switch format {
	case "md", "markdown":
		format = "md"
	case "html", "pdf":
	default:
		failJSON(w, 400, "format 只能是 md / html / pdf")
		return
	}

	fields := map[string]bool{}
	for _, f := range req.Fields {
		if f != "" {
			fields[f] = true
		}
	}
	if len(fields) == 0 {
		for _, f := range exportFields {
			fields[f.ID] = true
		}
	}

	idx, err := s.st.GetIndex(bgctx())
	if err != nil {
		failJSON(w, 500, "读取索引失败: "+err.Error())
		return
	}

	var chosen []Entry
	if len(req.IDs) > 0 {
		byID := make(map[string]Entry, len(idx.Items))
		for _, it := range idx.Items {
			byID[it.ID] = it
		}
		for _, id := range req.IDs {
			if e, ok := byID[id]; ok {
				chosen = append(chosen, e)
			}
		}
	} else {
		for _, it := range idx.Items {
			if req.Subject == "" || it.Subject == req.Subject {
				chosen = append(chosen, it)
			}
		}
		sort.SliceStable(chosen, func(a, b int) bool {
			if chosen[a].Date != chosen[b].Date {
				return chosen[a].Date < chosen[b].Date
			}
			return chosen[a].ID < chosen[b].ID
		})
	}
	if len(chosen) == 0 {
		failJSON(w, 400, "没有可导出的题目")
		return
	}

	withImage := fields["image"] || req.WithImage
	items := make([]exportItem, 0, len(chosen))
	for _, e := range chosen {
		items = append(items, s.buildExportItem(e, format, withImage))
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "错题本"
		if req.Subject != "" {
			title = req.Subject + " 错题本"
		}
	}
	stamp := time.Now().Format("2006-01-02 15:04")
	dateTag := time.Now().Format("20060102")

	var out []byte
	var ct, ext string
	switch format {
	case "md":
		out = []byte(renderMarkdown(title, stamp, items, fields))
		ct, ext = "text/markdown; charset=utf-8", ".md"
	default:
		out = []byte(renderHTML(title, stamp, items, fields, format == "pdf"))
		ct, ext = "text/html; charset=utf-8", ".html"
	}

	name := "错题本-" + orDefault(req.Subject, "全部") + "-" + dateTag + ext
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+urlPathEscape(name))
	w.Header().Set("X-Export-Count", strconv.Itoa(len(items)))
	w.WriteHeader(http.StatusOK)
	w.Write(out)
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// urlPathEscape 对文件名做 percent-encode（filename* 用的就是 pct-encoded）。
func urlPathEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x80 && (r == '-' || r == '.' || r == '_' || r == '~' ||
			(r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')) {
			b.WriteRune(r)
			continue
		}
		for _, bb := range []byte(string(r)) {
			b.WriteString("%")
			const hexd = "0123456789ABCDEF"
			b.WriteByte(hexd[bb>>4])
			b.WriteByte(hexd[bb&0x0F])
		}
	}
	return b.String()
}

func (s *server) buildExportItem(e Entry, format string, withImage bool) exportItem {
	it := exportItem{ID: e.ID}

	title := e.Qno
	if title == "" {
		title = e.ID
	}
	if e.Qtype != "" {
		it.Title = title + "　" + e.Qtype
	} else {
		it.Title = title
	}

	if e.Date != "" {
		it.Meta = append(it.Meta, [2]string{"日期", e.Date})
	}
	if e.Exam != "" {
		it.Meta = append(it.Meta, [2]string{"试卷", e.Exam})
	}
	if e.Source != "" {
		it.Meta = append(it.Meta, [2]string{"出处", e.Source})
	}
	it.Meta = append(it.Meta, [2]string{"结果", resultLabel(e.Result)})
	if e.Subject != "" {
		it.Meta = append(it.Meta, [2]string{"科目", e.Subject})
	}
	it.Tags = e.Tags
	it.Note = e.Note

	var secs []Section
	if e.MdKey != "" {
		if data, err := s.st.GetAll(bgctx(), e.MdKey); err == nil {
			pm := ParseMarkdown(string(data), e.Qno)
			secs = pm.Sections
			// 解析不出字段也不能丢内容，整块挂到「原文」下
			if !pm.Parsed && strings.TrimSpace(pm.Raw) != "" {
				secs = append(secs, Section{Key: "原文", Value: pm.Raw})
			}
		}
	}

	it.Stem, secs = takeSection(secs, stemKeys)
	it.Answer, secs = takeSection(secs, answerKeys)
	it.Solution, secs = takeSection(secs, solutionKeys)
	mdAnalysis, secs := takeSection(secs, analysisKeys)
	// 索引里手改过的错因要点优先于 md 里解析出来的
	if strings.TrimSpace(e.Analysis) != "" {
		it.Analysis = e.Analysis
	} else {
		it.Analysis = mdAnalysis
	}
	it.Other = secs

	if withImage && e.ImageKey != "" {
		if format == "md" {
			// 预签名链接 7 天有效：md 里塞 base64 会大到没法看
			if u, err := s.st.Presign(bgctx(), e.ImageKey, 7*24*time.Hour); err == nil {
				it.ImageSrc = u
			}
		} else {
			if data, err := s.st.GetAll(bgctx(), e.ImageKey); err == nil {
				ct := imageContentType(e.ImageKey)
				if ct == "" {
					ct = "image/jpeg"
				}
				it.ImageSrc = "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(data)
			}
		}
	}
	return it
}

func resultLabel(r string) string {
	switch normResult(r) {
	case "correct":
		return "做对了"
	case "wrong":
		return "做错了"
	}
	return "未判定"
}

// takeSection 从 secs 里取走第一个命中别名的字段，返回它的值和剩下的字段。
func takeSection(secs []Section, aliases []string) (string, []Section) {
	for i, s := range secs {
		if matchSectionKey(s.Key, aliases) {
			rest := append(append([]Section{}, secs[:i]...), secs[i+1:]...)
			return s.Value, rest
		}
	}
	return "", secs
}

func matchSectionKey(key string, aliases []string) bool {
	nk := normSectionKey(key)
	if nk == "" {
		return false
	}
	for _, a := range aliases {
		if nk == normSectionKey(a) {
			return true
		}
	}
	return false
}

// normSectionKey 忽略斜杠与空格：「错因/要点」「错因 要点」「错因要点」算同一个。
func normSectionKey(s string) string {
	s = strings.NewReplacer("/", "", "／", "", " ", "", "　", "", "-", "", ":", "", "：", "").Replace(s)
	return strings.TrimSpace(s)
}

// ============================================================================
// Markdown
// ============================================================================

func renderMarkdown(title, stamp string, items []exportItem, fields map[string]bool) string {
	var b strings.Builder
	b.WriteString("# " + title + "\n\n")
	b.WriteString("> 导出时间：" + stamp + "　共 " + strconv.Itoa(len(items)) + " 题\n")
	for _, it := range items {
		b.WriteString("\n---\n\n")
		b.WriteString("## " + it.Title + "\n\n")
		if fields["meta"] {
			var ps []string
			for _, kv := range it.Meta {
				ps = append(ps, kv[0]+"："+kv[1])
			}
			if len(ps) > 0 {
				b.WriteString(strings.Join(ps, "　") + "\n\n")
			}
			if len(it.Tags) > 0 {
				b.WriteString("**知识点**：" + strings.Join(it.Tags, "、") + "\n\n")
			}
		}
		mdField(&b, fields, "stem", "题干", it.Stem)
		mdField(&b, fields, "answer", "我的作答", it.Answer)
		mdField(&b, fields, "analysis", "错因要点", it.Analysis)
		mdField(&b, fields, "solution", "答案/正解", it.Solution)
		mdField(&b, fields, "note", "备注", it.Note)
		if fields["other"] {
			for _, s := range it.Other {
				mdField(&b, fields, "other", s.Key, s.Value)
			}
		}
		if fields["image"] && it.ImageSrc != "" {
			b.WriteString("**原图**：![](" + it.ImageSrc + ")\n\n")
		}
	}
	return b.String()
}

func mdField(b *strings.Builder, fields map[string]bool, id, label, val string) {
	if !fields[id] {
		return
	}
	v := strings.TrimSpace(val)
	if v == "" {
		return
	}
	b.WriteString("**" + label + "**：" + v + "\n\n")
}

// ============================================================================
// HTML（含 PDF —— pdf 就是这份 HTML 加一句 window.print()）
// ============================================================================

const exportPageCSS = `
:root { --line: #e3e6ea; --muted: #6b7280; --text: #1c1f23; }
* { box-sizing: border-box; }
body { margin: 0; background: #fff; color: var(--text);
  font: 15px/1.7 -apple-system, "PingFang SC", "Microsoft YaHei", "Helvetica Neue", sans-serif; }
.wrap { max-width: 860px; margin: 0 auto; padding: 28px 24px 60px; }
h1 { font-size: 22px; margin: 0 0 4px; }
.sub { color: var(--muted); font-size: 13px; margin: 0 0 8px; }
article { border-top: 1px solid var(--line); padding: 18px 0 6px; break-inside: avoid; }
article h2 { font-size: 17px; margin: 0 0 6px; }
.meta { color: var(--muted); font-size: 13px; margin: 0 0 8px; }
.tag { display: inline-block; background: #eef1f5; color: var(--muted);
  border-radius: 20px; padding: 1px 9px; font-size: 12px; margin: 0 4px 2px 0; }
.f { margin-bottom: 12px; }
.f .k { font-weight: 700; font-size: 13px; color: var(--muted); margin-bottom: 3px; }
.f .v { white-space: pre-wrap; overflow-wrap: anywhere; }
.f .v p { margin: 4px 0; }
.f .v ul { margin: 4px 0; padding-left: 20px; }
.f img { max-width: 100%; border: 1px solid var(--line); border-radius: 6px; }
@media print {
  body { font-size: 12px; }
  .wrap { max-width: none; padding: 0; }
  article { page-break-inside: avoid; }
  h1 { font-size: 18px; }
}
`

func renderHTML(title, stamp string, items []exportItem, fields map[string]bool, autoPrint bool) string {
	css, js, autojs := katexAssets()

	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html lang=\"zh-CN\">\n<head>\n")
	b.WriteString("<meta charset=\"utf-8\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	b.WriteString("<title>" + html.EscapeString(title) + "</title>\n")
	if css != "" {
		b.WriteString("<style>" + css + "</style>\n")
	}
	if js != "" {
		b.WriteString("<script>" + js + "</script>\n")
	}
	if autojs != "" {
		b.WriteString("<script>" + autojs + "</script>\n")
	}
	b.WriteString("<style>" + exportPageCSS + "</style>\n")
	b.WriteString("</head>\n<body>\n<div class=\"wrap\">\n")
	b.WriteString("<h1>" + html.EscapeString(title) + "</h1>\n")
	b.WriteString("<p class=\"sub\">导出时间：" + html.EscapeString(stamp) +
		"　共 " + strconv.Itoa(len(items)) + " 题</p>\n")

	for _, it := range items {
		b.WriteString("<article>\n<h2>" + html.EscapeString(it.Title) + "</h2>\n")
		if fields["meta"] {
			var ps []string
			for _, kv := range it.Meta {
				ps = append(ps, html.EscapeString(kv[0]+"："+kv[1]))
			}
			if len(ps) > 0 {
				b.WriteString("<p class=\"meta\">" + strings.Join(ps, "　") + "</p>\n")
			}
			if len(it.Tags) > 0 {
				b.WriteString("<p class=\"meta\">知识点：")
				for _, t := range it.Tags {
					b.WriteString("<span class=\"tag\">" + html.EscapeString(t) + "</span>")
				}
				b.WriteString("</p>\n")
			}
		}
		htmlField(&b, fields, "stem", "题干", it.Stem)
		htmlField(&b, fields, "answer", "我的作答", it.Answer)
		htmlField(&b, fields, "analysis", "错因要点", it.Analysis)
		htmlField(&b, fields, "solution", "答案/正解", it.Solution)
		htmlField(&b, fields, "note", "备注", it.Note)
		if fields["other"] {
			for _, s := range it.Other {
				htmlField(&b, fields, "other", s.Key, s.Value)
			}
		}
		if fields["image"] && it.ImageSrc != "" {
			b.WriteString("<div class=\"f\"><div class=\"k\">原图</div>" +
				"<img src=\"" + it.ImageSrc + "\" alt=\"原图\"></div>\n")
		}
		b.WriteString("</article>\n")
	}
	b.WriteString("</div>\n")

	b.WriteString(`<script>
document.addEventListener('DOMContentLoaded', function () {
  if (window.renderMathInElement) {
    try {
      renderMathInElement(document.body, {
        delimiters: [
          { left: '$$', right: '$$', display: true },
          { left: '$', right: '$', display: false },
          { left: '\\[', right: '\\]', display: true },
          { left: '\\(', right: '\\)', display: false }
        ],
        throwOnError: false
      });
    } catch (e) {}
  }
`)
	if autoPrint {
		// 等 KaTeX 渲染完再唤起打印，否则公式可能还是源码
		b.WriteString("  setTimeout(function () { window.print(); }, 400);\n")
	}
	b.WriteString("});\n</script>\n</body>\n</html>\n")
	return b.String()
}

func htmlField(b *strings.Builder, fields map[string]bool, id, label, val string) {
	if !fields[id] {
		return
	}
	v := strings.TrimSpace(val)
	if v == "" {
		return
	}
	b.WriteString("<div class=\"f\"><div class=\"k\">" + html.EscapeString(label) + "</div><div class=\"v\">")
	inUL := false
	for _, ln := range strings.Split(v, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") {
			if !inUL {
				b.WriteString("<ul>")
				inUL = true
			}
			b.WriteString("<li>" + html.EscapeString(strings.TrimSpace(t[2:])) + "</li>")
			continue
		}
		if inUL {
			b.WriteString("</ul>")
			inUL = false
		}
		b.WriteString("<p>" + html.EscapeString(ln) + "</p>")
	}
	if inUL {
		b.WriteString("</ul>")
	}
	b.WriteString("</div></div>\n")
}

// ============================================================================
// 内联 KaTeX：让导出的 HTML 断网也能看公式、打印不缺字
//
// katex.min.css 里每个 @font-face 都写了 woff2/woff/ttf 三个 url(fonts/...)，
// 但我们只带了 woff2，所以先把 woff/ttf 两条 src 去掉，再把 woff2 换成 data URI。
// ============================================================================

var (
	fontURLRe  = regexp.MustCompile(`url\(fonts/([A-Za-z0-9_-]+)\.woff2\)`)
	fontDropRe = regexp.MustCompile(`,?url\(fonts/[A-Za-z0-9_-]+\.(?:woff|ttf)\)\s*format\("(?:woff|truetype)"\)`)
)

var (
	katexOnce    sync.Once
	katexCSSText string
	katexJSText  string
	autoRenderJS string
)

func katexAssets() (css, js, auto string) {
	katexOnce.Do(func() {
		if raw, err := fs.ReadFile(webFS, "web/katex/katex.min.css"); err == nil {
			text := fontDropRe.ReplaceAllString(string(raw), "")
			cache := map[string]string{}
			katexCSSText = fontURLRe.ReplaceAllStringFunc(text, func(m string) string {
				name := fontURLRe.FindStringSubmatch(m)[1]
				if hit, ok := cache[name]; ok {
					return hit
				}
				f, err := fs.ReadFile(webFS, "web/katex/fonts/"+name+".woff2")
				if err != nil {
					return m
				}
				d := "url(data:font/woff2;base64," + base64.StdEncoding.EncodeToString(f) + ")"
				cache[name] = d
				return d
			})
		}
		if j, err := fs.ReadFile(webFS, "web/katex/katex.min.js"); err == nil {
			katexJSText = string(j)
		}
		if j, err := fs.ReadFile(webFS, "web/katex/auto-render.min.js"); err == nil {
			autoRenderJS = string(j)
		}
	})
	return katexCSSText, katexJSText, autoRenderJS
}
