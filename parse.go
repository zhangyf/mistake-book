package main

import (
	"regexp"
	"strings"
)

// md 正文是「约定」而不是强 schema：
//
//	## 第 4 题　选择题　✓
//	**题干**：对于 $-3^4$，下列叙述正确的是（　　）
//	**我的作答**：C
//	**批改**：✓ 正确（红笔圈出 + 勾）
//	**错因/要点**：...
//
// 字段可能缺、可能多、题号写法可能带空格，还可能整篇没有字段标签。
// 所以解析策略是**尽力解析 + 原样降级**：解析不出字段就把整块原文吐回前端，
// 前端用 <pre> 展示，绝不白屏。

// knownOrder 是希望展示的字段顺序（前端也按这个顺序渲染）。
//
// 注：2026-10-13 去掉了「批改」——对错由索引的 result 字段表达，md 里不需要
// 再记一遍，老师明确说不要。历史 md 里的 **批改**： 会被解析时直接丢掉。
var knownOrder = []string{"题干", "我的作答", "错因/要点", "错因", "要点", "答案", "正解"}

var (
	headingRe     = regexp.MustCompile(`(?m)^#{1,6}\s+(.*)$`)
	frontMatterRe = regexp.MustCompile(`(?s)\A\s*---\n.*?\n---\s*`)
	fieldRe       = regexp.MustCompile(`^\*\*(.+?)\*\*\s*[:：]?\s*(.*)$`)
	fullSpaceRe   = regexp.MustCompile(`[\s　]+`)
)

// Section 是解析出的一个字段。
type Section struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ParsedMD 是一道题的解析结果。
type ParsedMD struct {
	Parsed   bool      `json:"parsed"`   // false = 没解析出字段，前端展示 Raw
	Heading  string    `json:"heading"`  // 如 "第 4 题　选择题　✓"
	Raw      string    `json:"raw"`      // Parsed=false 时的整块原文
	Sections []Section `json:"sections"` // 解析出的字段，按展示顺序
}

// ParseMarkdown 从一张照片的 md 里取出 qno 对应的那道题。
// qno 形如 "第4题"；匹配不到就取第一块，再没有就整篇。
func ParseMarkdown(md, qno string) ParsedMD {
	if parsed, ok := parseImportedMarkdown(md, qno); ok {
		return parsed
	}
	body := frontMatterRe.ReplaceAllString(md, "")
	body = strings.TrimSpace(body)
	if body == "" {
		return ParsedMD{Parsed: false, Raw: md}
	}

	blocks := splitBlocks(body)
	b := pickBlock(blocks, qno)

	sec := parseFields(b.body)
	if len(sec) == 0 {
		raw := b.body
		if b.heading != "" {
			raw = b.heading + "\n" + b.body
		}
		return ParsedMD{Parsed: false, Heading: b.heading, Raw: raw}
	}
	return ParsedMD{Parsed: true, Heading: b.heading, Sections: sec}
}

// parseImportedMarkdown 读取网页导入生成的无损数据标记。
// 人工编辑的正文可能恰好包含 Markdown 标题/字段标签，不能再按这些内容切题。
// 新格式严格按题号匹配；缺题或标记损坏时不回退到另一道题。旧 Markdown 仍走原解析器。
func parseImportedMarkdown(md, qno string) (ParsedMD, bool) {
	body := strings.TrimSpace(md)
	if !strings.HasPrefix(body, importMarkdownPrefix) {
		return ParsedMD{}, false
	}
	line, _, _ := strings.Cut(body, "\n")
	line = strings.TrimSuffix(line, "\r")
	invalid := ParsedMD{Raw: "导入题目记录损坏，无法读取；请核对原始 Markdown。"}
	if !strings.HasSuffix(line, importMarkdownSuffix) || len(line) > maxImportMetadataBytes*2 {
		return invalid, true
	}
	data := strings.TrimSuffix(strings.TrimPrefix(line, importMarkdownPrefix), importMarkdownSuffix)
	var doc importMarkdownDocument
	if decodeImportJSON([]byte(data), &doc) != nil || doc.Version != 1 || len(doc.Questions) == 0 || len(doc.Questions) > maxImportQuestions {
		return invalid, true
	}
	want := normalizeImportQno(qno)
	seen := map[string]bool{}
	var found *importQuestion
	for i := range doc.Questions {
		q := &doc.Questions[i]
		key := normalizeImportQno(q.Qno)
		if !validImportText(q.Qno, 64, true, true) || !validImportText(q.Stem, 12000, true, false) ||
			!validImportText(q.StudentAnswer, 8000, false, false) || seen[key] {
			return invalid, true
		}
		seen[key] = true
		if key == want || (want == "" && len(doc.Questions) == 1) {
			found = q
		}
	}
	if found == nil {
		return ParsedMD{Raw: "此照片中未找到题号「" + qno + "」，请核对索引与原文。"}, true
	}
	sections := []Section{{Key: "题干", Value: found.Stem}, {Key: "我的作答", Value: found.StudentAnswer}}
	if found.Analysis != "" {
		sections = append(sections, Section{Key: "错因/要点", Value: found.Analysis})
	}
	return ParsedMD{Parsed: true, Heading: found.Qno, Sections: sections}, true
}

// previewText 取题干的一段预览，给列表卡片用（列表页不该显示整道题）。
// 折叠空白压成一行；截断时如果 $ 落单（公式被切断），退回到最后一个 $ 之前，
// 免得卡片上出现半个公式渲染不出来。
func previewText(s string, n int) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := string(r[:n])
	if strings.Count(cut, "$")%2 == 1 {
		if i := strings.LastIndex(cut, "$"); i > 0 {
			cut = cut[:i]
		}
	}
	return strings.TrimSpace(cut) + "…"
}

// block 是 md 里的一个题块：标题 + 正文。
type block struct {
	heading string
	body    string
}

// splitBlocks 按 markdown 标题切块；第一个标题之前的内容算作第一块的正文。
func splitBlocks(body string) []block {
	locs := headingRe.FindAllStringSubmatchIndex(body, -1)
	if len(locs) == 0 {
		return []block{{body: body}}
	}
	var blocks []block
	if locs[0][0] > 0 {
		blocks = append(blocks, block{body: strings.TrimSpace(body[:locs[0][0]])})
	}
	for i, loc := range locs {
		heading := strings.TrimSpace(body[loc[2]:loc[3]])
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		blocks = append(blocks, block{
			heading: heading,
			body:    strings.TrimSpace(body[loc[1]:end]),
		})
	}
	return blocks
}

// pickBlock 按题号挑块：先精确匹配，再退化到第一块。
func pickBlock(blocks []block, qno string) block {
	want := fullSpaceRe.ReplaceAllString(strings.TrimSpace(qno), "")
	if want != "" {
		for _, b := range blocks {
			if strings.Contains(fullSpaceRe.ReplaceAllString(b.heading, ""), want) {
				return b
			}
		}
	}
	// 没匹配到：只有一个题块就用它（一张照片一题是常见情况），否则取第一块
	if len(blocks) == 1 {
		return blocks[0]
	}
	return blocks[0]
}

// parseFields 按 **标签**： 切字段。未知标签也保留（内容不能丢），
// 只是排在已知字段之后。
func parseFields(src string) []Section {
	lines := strings.Split(src, "\n")
	var out []Section

	var cur *Section
	flush := func() {
		if cur != nil {
			cur.Value = strings.TrimSpace(cur.Value)
			out = append(out, *cur)
			cur = nil
		}
	}

	for _, line := range lines {
		m := fieldRe.FindStringSubmatch(line)
		if m == nil {
			if cur != nil {
				cur.Value += "\n" + line
			}
			continue
		}
		if strings.TrimSpace(m[1]) == "批改" {
			flush()   // 丢弃该字段：对错以索引的 result 为准
			cur = nil // 后续行也不要并进上一个字段
			continue
		}
		flush()
		cur = &Section{Key: strings.TrimSpace(m[1]), Value: m[2]}
	}
	flush()

	return orderSections(out)
}

// orderSections 已知字段按 knownOrder 排前面，其余保持原顺序在后。
func orderSections(secs []Section) []Section {
	rank := make(map[string]int, len(knownOrder))
	for i, k := range knownOrder {
		rank[k] = i
	}
	var known, extra []Section
	for _, s := range secs {
		if _, ok := rank[s.Key]; ok && s.Key != "" {
			known = append(known, s)
		} else {
			extra = append(extra, s)
		}
	}
	// 稳定排序：已知字段按 knownOrder
	for i := 0; i < len(known); i++ {
		for j := i + 1; j < len(known); j++ {
			if rank[known[j].Key] < rank[known[i].Key] {
				known[i], known[j] = known[j], known[i]
			}
		}
	}
	return append(known, extra...)
}
