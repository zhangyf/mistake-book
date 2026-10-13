package main

import (
	"net/http"
	"sort"
	"strings"
)

// ============================================================================
// GET /api/subjects —— 集中页：各科错题本卡片
// ============================================================================

type subjectSummary struct {
	Subject  string         `json:"subject"`
	Count    int            `json:"count"`
	Exams    []string       `json:"exams"`
	Results  map[string]int `json:"results"`
	LastDate string         `json:"last_date"`
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
			sum = &subjectSummary{Subject: it.Subject, Results: map[string]int{}}
			agg[it.Subject] = sum
			order = append(order, it.Subject)
		}
		sum.Count++
		if it.Exam != "" && !contains(sum.Exams, it.Exam) {
			sum.Exams = append(sum.Exams, it.Exam)
		}
		res := it.Result
		if res == "" {
			res = "unknown"
		}
		sum.Results[res]++
		if it.Date > sum.LastDate {
			sum.LastDate = it.Date
		}
	}

	out := make([]subjectSummary, 0, len(order))
	for _, name := range order {
		sum := agg[name]
		sort.Strings(sum.Exams)
		if sum.Results["correct"] == 0 && sum.Results["wrong"] == 0 && sum.Results["unknown"] == 0 {
			sum.Results = map[string]int{}
		}
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
// GET /api/entries?subject=数学&result=wrong —— 某科目的题目列表 + 分组用的 facets
//
// 后端只吐扁平列表，按日期/知识点/题型分组在前端做（切换维度不重新发请求）。
// ============================================================================

type entryItem struct {
	ID        string   `json:"id"`
	Subject   string   `json:"subject"`
	Exam      string   `json:"exam"`
	Date      string   `json:"date"`
	Qno       string   `json:"qno"`
	Qtype     string   `json:"qtype"`
	Result    string   `json:"result"`
	Source    string   `json:"source"`
	Tags      []string `json:"tags"`
	Note      string   `json:"note"`
	ImageKey  string   `json:"image_key"`
	UpdatedAt string   `json:"updated_at"`
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
			ImageKey: it.ImageKey, UpdatedAt: it.UpdatedAt,
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

	writeJSON(w, 200, map[string]interface{}{
		"success": true,
		"subject": subject,
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
