package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func putEntry(t *testing.T, s *server, body map[string]interface{}) *httptest.ResponseRecorder {
	t.Helper()
	data, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPut, "/api/entry", bytes.NewReader(data))
	r.Header.Set("X-Mistake-Token", "test-auth")
	w := httptest.NewRecorder()
	s.updateEntry(w, r)
	return w
}

func TestUpdateEntryRewritesStemInMarkdown(t *testing.T) {
	f := newFakeImportStore()
	mdKey := "exam-bank/数学/2026-10-未命名/2026-10-13-import-abc.md"
	f.objects[mdKey] = renderImportMarkdown([]importQuestion{
		{Qno: "第4题", Qtype: "解答题", Stem: "旧题干", StudentAnswer: "旧作答"},
	}, "hash")
	f.seedIndex(Index{Items: []Entry{{ID: "数学-2026-10-13-import-abc-第4题", Subject: "数学", Qno: "第4题",
		MdKey: mdKey, Exam: "有理数"}}})
	s := importTestServer(f)

	w := putEntry(t, s, map[string]interface{}{"id": "数学-2026-10-13-import-abc-第4题",
		"stem": "新题干\n```\n────●──▶\n```", "student_answer": "新作答"})
	if w.Code != 200 {
		t.Fatalf("期望 200，实际 %d body=%s", w.Code, w.Body.String())
	}
	if _, ok := parsedMDSection(f.objects[mdKey], "第4题", stemKeys); !ok {
		t.Fatal("改写后的 md 解析失败")
	}
	stem, _ := parsedMDSection(f.objects[mdKey], "第4题", stemKeys)
	ans, _ := parsedMDSection(f.objects[mdKey], "第4题", answerKeys)
	if stem != "新题干\n```\n────●──▶\n```" || ans != "新作答" {
		t.Fatalf("题干/作答未改写: %q / %q", stem, ans)
	}
	// 不带 stem 的普通保存不应重写 md
	before := len(f.writes)
	w2 := putEntry(t, s, map[string]interface{}{"id": "数学-2026-10-13-import-abc-第4题", "note": "只改备注"})
	if w2.Code != 200 {
		t.Fatalf("普通保存失败: %d %s", w2.Code, w2.Body.String())
	}
	for _, key := range f.writes[before:] {
		if key == mdKey {
			t.Fatal("未改题干却重写了 md")
		}
	}
}

func TestUpdateEntryStemRejectsUnknownQuestion(t *testing.T) {
	f := newFakeImportStore()
	mdKey := "exam-bank/数学/2026-10-未命名/2026-10-13-import-xyz.md"
	f.objects[mdKey] = renderImportMarkdown([]importQuestion{{Qno: "第1题", Stem: "旧"}}, "h")
	f.seedIndex(Index{Items: []Entry{{ID: "数学-2026-10-13-import-xyz-第3题", Qno: "第3题", MdKey: mdKey}}})
	s := importTestServer(f)

	writes := len(f.writes)
	w := putEntry(t, s, map[string]interface{}{"id": "数学-2026-10-13-import-xyz-第3题", "stem": "新"})
	if w.Code == 200 {
		t.Fatal("标记里没有该题号时应报错")
	}
	if len(f.writes) != writes {
		t.Fatal("失败时不应写任何对象")
	}
	// 演练模式下不写 md，但校验照旧
	s.dryRun = true
	w2 := putEntry(t, s, map[string]interface{}{"id": "数学-2026-10-13-import-xyz-第3题", "stem": "新"})
	if w2.Code == 200 {
		t.Fatal("dry-run 也应校验题号")
	}
}
