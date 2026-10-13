package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// ============================================================================
// GET /api/subjects —— 集中页：各科错题本卡片
// ============================================================================

type subjectSummary struct {
	Subject  string   `json:"subject"`
	Name     string   `json:"name"` // 老师自己起的名字，没起就用科目名
	Count    int      `json:"count"`
	Exams    []string `json:"exams"`
	LastDate string   `json:"last_date"`
}

func (s *server) handleSubjects(w http.ResponseWriter, r *http.Request) {
	idx, err := s.st.GetIndex(bgctx())
	if err != nil {
		failJSON(w, 500, "读取索引失败: "+err.Error())
		return
	}

	order := []string{}
	agg := map[string]*subjectSummary{}
	for _, it := range idx.Items {
		if it.Subject == "" {
			continue
		}
		sum, ok := agg[it.Subject]
		if !ok {
			sum = &subjectSummary{Subject: it.Subject}
			agg[it.Subject] = sum
			order = append(order, it.Subject)
		}
		sum.Count++
		if it.Exam != "" && !contains(sum.Exams, it.Exam) {
			sum.Exams = append(sum.Exams, it.Exam)
		}
		if it.Date > sum.LastDate {
			sum.LastDate = it.Date
		}
	}

	out := make([]subjectSummary, 0, len(order))
	for _, name := range order {
		sum := agg[name]
		sort.Strings(sum.Exams)
		sum.Name = idx.DisplayName(sum.Subject)
		out = append(out, *sum)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Subject < out[b].Subject })

	writeJSON(w, 200, map[string]interface{}{
		"success":  true,
		"total":    len(idx.Items),
		"subjects": out,
	})
}

// ============================================================================
// PUT /api/subject —— 给某科目的错题本起名（老师 2026-10-13 要求）
//
// body: {"subject":"数学","name":"初一数学"}
// name 为空 = 恢复默认（直接显示科目名）。
// ============================================================================

func (s *server) renameSubject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		failJSON(w, 405, "用 PUT")
		return
	}
	var req struct {
		Subject string `json:"subject"`
		Name    string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		failJSON(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	req.Subject = strings.TrimSpace(req.Subject)
	req.Name = strings.TrimSpace(req.Name)
	if req.Subject == "" {
		failJSON(w, 400, "缺少 subject")
		return
	}
	if len(req.Name) > 40 {
		failJSON(w, 400, "名字太长（40 字以内）")
		return
	}

	var notFound bool
	err := s.mutate(
		func(idx *Index) error {
			exists := false
			for _, it := range idx.Items {
				if it.Subject == req.Subject {
					exists = true
					break
				}
			}
			if !exists {
				notFound = true
				return nil
			}
			if idx.Names == nil {
				idx.Names = map[string]string{}
			}
			if req.Name == "" {
				delete(idx.Names, req.Subject) // 清空 = 恢复默认
			} else {
				idx.Names[req.Subject] = req.Name
			}
			return nil
		},
		func(idx *Index) bool {
			got, ok := idx.Names[req.Subject]
			if req.Name == "" {
				return !ok || got == ""
			}
			return ok && got == req.Name
		},
	)
	if notFound {
		failJSON(w, 404, "索引里没有科目 "+req.Subject)
		return
	}
	if err != nil {
		writeConflict(w, err)
		return
	}
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "subject": req.Subject, "name": req.Name, "dry_run": s.dryRun,
	})
}

// ============================================================================
// GET /api/entries?subject=数学&result=wrong —— 某科目的题目列表 + 分组用的 facets
//
// 后端只吐扁平列表，按日期/知识点/题型分组在前端做（切换维度不重新发请求）。
// ============================================================================

type entryItem struct {
	ID        string   `json:"id"`
	Subject   string   `json:"subject"`
	Exam      string   `json:"exam"` // 章节（字段名沿用 exam，索引/路径都靠它，不改名）
	Date      string   `json:"date"`
	Qno       string   `json:"qno"`
	Qtype     string   `json:"qtype"`
	Result    string   `json:"result"`
	Source    string   `json:"source"`
	Tags      []string `json:"tags"`
	Note      string   `json:"note"`
	Preview   string   `json:"preview"` // 题干预览，列表卡片用
	ImageKey  string   `json:"image_key"`
	MdKey     string   `json:"md_key"`
	UpdatedAt string   `json:"updated_at"`
}

// ============================================================================
// 题干预览
//
// 题干在 md 里，索引里没有。列表页想让老师一眼看到题目，就得把 md 拉下来。
// 按 md_key 缓存（md 一旦录入基本不改，不做失效），并发 8 路拉。
// ============================================================================

var (
	previewMu    sync.Mutex
	previewCache = map[string]string{}
)

func (s *server) previewOf(mdKey, qno string) string {
	if mdKey == "" {
		return ""
	}
	cacheKey := mdKey + "|" + qno

	previewMu.Lock()
	if v, ok := previewCache[cacheKey]; ok {
		previewMu.Unlock()
		return v
	}
	previewMu.Unlock()

	var v string
	if data, err := s.st.GetAll(bgctx(), mdKey); err == nil {
		pm := ParseMarkdown(string(data), qno)
		if pm.Parsed {
			for _, sec := range pm.Sections {
				if matchSectionKey(sec.Key, stemKeys) {
					v = previewText(sec.Value, 90)
					break
				}
			}
		}
		if v == "" {
			v = previewText(pm.Raw, 90)
		}
	}

	previewMu.Lock()
	previewCache[cacheKey] = v
	previewMu.Unlock()
	return v
}

// fillPreviews 并发补齐列表里每道题的题干预览。
func (s *server) fillPreviews(items []entryItem) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i := range items {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			items[i].Preview = s.previewOf(items[i].MdKey, items[i].Qno)
		}(i)
	}
	wg.Wait()
}

func (s *server) handleEntries(w http.ResponseWriter, r *http.Request) {
	subject := r.URL.Query().Get("subject")
	result := r.URL.Query().Get("result")

	idx, err := s.st.GetIndex(bgctx())
	if err != nil {
		failJSON(w, 500, "读取索引失败: "+err.Error())
		return
	}

	items := []entryItem{}
	facets := map[string]map[string]int{
		"tags":   {},
		"qtype":  {},
		"result": {},
		"exam":   {},
		"source": {},
	}
	for _, it := range idx.Items {
		if subject != "" && it.Subject != subject {
			continue
		}
		if result != "" && normResult(it.Result) != result {
			continue
		}
		items = append(items, entryItem{
			ID: it.ID, Subject: it.Subject, Exam: it.Exam, Date: it.Date,
			Qno: it.Qno, Qtype: it.Qtype, Result: normResult(it.Result),
			Source: it.Source, Tags: it.Tags, Note: it.Note,
			ImageKey: it.ImageKey, MdKey: it.MdKey, UpdatedAt: it.UpdatedAt,
		})
		facets["result"][normResult(it.Result)]++
		if it.Qtype != "" {
			facets["qtype"][it.Qtype]++
		}
		if it.Exam != "" {
			facets["exam"][it.Exam]++
		}
		if it.Source != "" {
			facets["source"][it.Source]++
		}
		for _, t := range it.Tags {
			if t != "" {
				facets["tags"][t]++
			}
		}
	}

	s.fillPreviews(items)

	writeJSON(w, 200, map[string]interface{}{
		"success": true,
		"subject": subject,
		"display": idx.DisplayName(subject), // 科目页标题用老师起的名字
		"count":   len(items),
		"items":   items,
		"facets":  facets,
	})
}

// ============================================================================
// GET /api/entry?id=... —— 单题详情：索引字段 + md 正文解析结果
// ============================================================================

func (s *server) handleEntry(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPut:
		s.updateEntry(w, r)
		return
	case http.MethodDelete:
		s.deleteEntry(w, r)
		return
	case http.MethodGet:
	default:
		failJSON(w, 405, "不支持的方法: "+r.Method)
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		failJSON(w, 400, "缺少 id")
		return
	}
	idx, err := s.st.GetIndex(bgctx())
	if err != nil {
		failJSON(w, 500, "读取索引失败: "+err.Error())
		return
	}
	var found *Entry
	for i := range idx.Items {
		if idx.Items[i].ID == id {
			found = &idx.Items[i]
			break
		}
	}
	if found == nil {
		failJSON(w, 404, "索引里没有 id="+id)
		return
	}

	out := map[string]interface{}{
		"success":    true,
		"id":         found.ID,
		"subject":    found.Subject,
		"exam":       found.Exam,
		"date":       found.Date,
		"photo":      found.Photo,
		"qno":        found.Qno,
		"qtype":      found.Qtype,
		"result":     normResult(found.Result),
		"source":     found.Source,
		"analysis":   found.Analysis,
		"tags":       found.Tags,
		"note":       found.Note,
		"updated_at": found.UpdatedAt,
		"md_key":     found.MdKey,
		"image_key":  found.ImageKey,
	}
	if found.ImageKey != "" {
		out["image_url"] = "/api/image?key=" + urlQueryEscape(found.ImageKey)
	}

	if found.MdKey != "" {
		if data, err := s.st.GetAll(bgctx(), found.MdKey); err == nil {
			out["parsed_md"] = ParseMarkdown(string(data), found.Qno)
		} else {
			out["md_error"] = err.Error()
		}
	}

	writeJSON(w, 200, out)
}

func normResult(r string) string {
	switch strings.ToLower(strings.TrimSpace(r)) {
	case "correct", "wrong", "unknown":
		return strings.ToLower(strings.TrimSpace(r))
	}
	return "unknown"
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// urlQueryEscape 只转义 URL query 里必须转义的字符，保持 key 里的中文可读。
func urlQueryEscape(s string) string {
	return strings.NewReplacer(
		" ", "%20",
		"&", "%26",
		"=", "%3D",
		"?", "%3F",
		"#", "%23",
		"+", "%2B",
	).Replace(s)
}
