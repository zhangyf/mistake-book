package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
)

func testImportPhoto(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func testImportMetadata() importMetadata {
	return importMetadata{ImportID: strings.Repeat("a", 32), Confirmed: true, Subject: "数学", Date: "2026-10-13", Chapter: "公共目录", Source: "校内作业",
		Questions: []importQuestion{{Qno: "第1题", Qtype: "计算题", Stem: "公共题头\n计算 $1+2$", StudentAnswer: "3\n原手写第二行", Chapter: "第一章", Source: "自我复习", Analysis: "用户手填", Note: "索引备注", Tags: []string{"运算"}}}}
}
func importTestServer(f *fakeImportStore) *server {
	return &server{st: &Storage{store: f}, token: "test-auth"}
}
func importTestMux(s *server) *http.ServeMux {
	mux := http.NewServeMux()
	s.registerAPI(mux)
	return mux
}
func multipartImportRequest(t *testing.T, route, field, text string, photo []byte) *http.Request {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	part, err := mw.CreateFormFile("photo", "../../never-write-this.png")
	if err != nil {
		t.Fatal(err)
	}
	part.Write(photo)
	mw.WriteField(field, text)
	mw.Close()
	r := httptest.NewRequest(http.MethodPost, route, &b)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.Header.Set("X-Mistake-Token", "test-auth")
	return r
}
func requestImportConfirm(t *testing.T, s *server, m importMetadata, photo []byte) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	r := multipartImportRequest(t, "/api/import/confirm", "metadata", string(data), photo)
	w := httptest.NewRecorder()
	importTestMux(s).ServeHTTP(w, r)
	return w
}
func requireImportStatus(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("状态=%d, 期望=%d, 正文=%s", w.Code, status, w.Body.String())
	}
}
func decodeImportResult(t *testing.T, w *httptest.ResponseRecorder) importResult {
	t.Helper()
	requireImportStatus(t, w, 200)
	var result importResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestImportAuthAndMethods(t *testing.T) {
	for _, route := range []string{"config", "recognize", "confirm"} {
		t.Run(route, func(t *testing.T) {
			f := newFakeImportStore()
			s := importTestServer(f)
			mux := importTestMux(s)
			r := httptest.NewRequest("POST", "/api/import/"+route, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			requireImportStatus(t, w, 401)
			r = httptest.NewRequest("DELETE", "/api/import/"+route, nil)
			r.Header.Set("Authorization", "Bearer test-auth")
			w = httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			requireImportStatus(t, w, 405)
			if len(f.writes) != 0 {
				t.Fatal("鉴权或方法错误不能写入")
			}
		})
	}
}

func TestImportOrigin(t *testing.T) {
	cases := []struct {
		name, origin, site string
		allowed            bool
	}{
		{"同源", "http://example.com", "same-origin", true},
		{"TLS终止代理", "https://example.com", "same-origin", true},
		{"命令行", "", "", true},
		{"跨域", "https://evil.invalid", "", false},
		{"同站不同源", "http://example.com", "same-site", false},
		{"跨站", "", "cross-site", false},
		{"null来源", "null", "", false},
		{"带路径", "http://example.com/path", "", false},
		{"不同端口", "http://example.com:8080", "", false},
		{"伪主机", "http://example.com.evil.invalid", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			r := httptest.NewRequest("POST", "http://example.com/api/import/confirm", nil)
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.site != "" {
				r.Header.Set("Sec-Fetch-Site", tc.site)
			}
			w := httptest.NewRecorder()
			importSameOrigin(func(http.ResponseWriter, *http.Request) { called = true })(w, r)
			if called != tc.allowed {
				t.Fatalf("来源判断错误: %s", w.Body.String())
			}
		})
	}
	for _, route := range []string{"recognize", "confirm"} {
		f := newFakeImportStore()
		s := importTestServer(f)
		r := httptest.NewRequest("POST", "/api/import/"+route, nil)
		r.Header.Set("X-Mistake-Token", "test-auth")
		r.Header.Set("Origin", "https://evil.invalid")
		w := httptest.NewRecorder()
		importTestMux(s).ServeHTTP(w, r)
		requireImportStatus(t, w, 403)
		if len(f.writes) != 0 {
			t.Fatal("跨站请求不能写入")
		}
	}
}

func TestImportConfigDisabledAndDryRun(t *testing.T) {
	f := newFakeImportStore()
	s := importTestServer(f)
	s.dryRun = true
	r := httptest.NewRequest("GET", "/api/import/config", nil)
	r.Header.Set("X-Mistake-Token", "test-auth")
	w := httptest.NewRecorder()
	importTestMux(s).ServeHTTP(w, r)
	requireImportStatus(t, w, 200)
	var got map[string]any
	json.Unmarshal(w.Body.Bytes(), &got)
	if len(got) != 7 || got["enabled"] != false || got["model"] != defaultVisionModel || got["max_photos"] != float64(20) ||
		got["max_photo_bytes"] != float64(10485760) || got["max_questions"] != float64(30) || got["dry_run"] != true {
		t.Fatalf("配置契约错误: %v", got)
	}
	r = httptest.NewRequest("POST", "/api/import/recognize", nil)
	r.Header.Set("X-Mistake-Token", "test-auth")
	w = httptest.NewRecorder()
	importTestMux(s).ServeHTTP(w, r)
	requireImportStatus(t, w, 503)
	result := decodeImportResult(t, requestImportConfirm(t, s, testImportMetadata(), testImportPhoto(t)))
	if !result.DryRun || result.Imported != 1 || len(f.writes) != 0 {
		t.Fatal("dry-run 写入了存储")
	}
}

func TestImportMetadataValidationZeroWrites(t *testing.T) {
	cases := []struct {
		name   string
		change func(*importMetadata)
	}{
		{"未确认", func(m *importMetadata) { m.Confirmed = false }},
		{"编号大写", func(m *importMetadata) { m.ImportID = strings.Repeat("A", 32) }},
		{"编号短", func(m *importMetadata) { m.ImportID = "abc" }},
		{"科目空", func(m *importMetadata) { m.Subject = " " }},
		{"科目长", func(m *importMetadata) { m.Subject = strings.Repeat("数", 65) }},
		{"章节长", func(m *importMetadata) { m.Chapter = strings.Repeat("章", 129) }},
		{"日期不存在", func(m *importMetadata) { m.Date = "2026-02-30" }},
		{"日期格式", func(m *importMetadata) { m.Date = "2026-1-01" }},
		{"无题", func(m *importMetadata) { m.Questions = nil }},
		{"超过30题", func(m *importMetadata) { m.Questions = make([]importQuestion, 31) }},
		{"非法出处", func(m *importMetadata) { m.Source = "其他" }},
		{"非法题出处", func(m *importMetadata) { m.Questions[0].Source = "其他" }},
		{"题干空", func(m *importMetadata) { m.Questions[0].Stem = " \n" }},
		{"题干长", func(m *importMetadata) { m.Questions[0].Stem = strings.Repeat("字", 12001) }},
		{"作答长", func(m *importMetadata) { m.Questions[0].StudentAnswer = strings.Repeat("字", 8001) }},
		{"分析长", func(m *importMetadata) { m.Questions[0].Analysis = strings.Repeat("字", 4001) }},
		{"备注长", func(m *importMetadata) { m.Questions[0].Note = strings.Repeat("字", 2001) }},
		{"题型长", func(m *importMetadata) { m.Questions[0].Qtype = strings.Repeat("字", 65) }},
		{"题章节长", func(m *importMetadata) { m.Questions[0].Chapter = strings.Repeat("字", 129) }},
		{"题号空", func(m *importMetadata) { m.Questions[0].Qno = " " }},
		{"题号换行", func(m *importMetadata) { m.Questions[0].Qno = "1\n## 2" }},
		{"题号长", func(m *importMetadata) { m.Questions[0].Qno = strings.Repeat("1", 65) }},
		{"控制字符", func(m *importMetadata) { m.Questions[0].Stem = "a\x00b" }},
		{"标签多", func(m *importMetadata) { m.Questions[0].Tags = make([]string, 21) }},
		{"标签长", func(m *importMetadata) { m.Questions[0].Tags = []string{strings.Repeat("字", 33)} }},
		{"标签空", func(m *importMetadata) { m.Questions[0].Tags = []string{" "} }},
		{"规范题号冲突", func(m *importMetadata) {
			q := m.Questions[0]
			q.Qno = "第 ０１ 题"
			m.Questions = append(m.Questions, q)
		}},
		{"简写题号冲突", func(m *importMetadata) { q := m.Questions[0]; q.Qno = "1"; m.Questions = append(m.Questions, q) }},
		{"slug冲突", func(m *importMetadata) {
			m.Questions[0].Qno = "a/b"
			q := m.Questions[0]
			q.Qno = "a:b"
			m.Questions = append(m.Questions, q)
		}},
	}
	photo := testImportPhoto(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeImportStore()
			m := testImportMetadata()
			tc.change(&m)
			w := requestImportConfirm(t, importTestServer(f), m, photo)
			requireImportStatus(t, w, 400)
			if len(f.writes) != 0 || len(f.reads) != 0 {
				t.Fatal("验证前访问了存储")
			}
		})
	}
}

func TestImportMultipartValidation(t *testing.T) {
	valid := testImportPhoto(t)
	hugePixels := bytes.Clone(valid)
	binary.BigEndian.PutUint32(hugePixels[16:20], 10000)
	binary.BigEndian.PutUint32(hugePixels[20:24], 10000)
	binary.BigEndian.PutUint32(hugePixels[29:33], crc32.ChecksumIEEE(hugePixels[12:29]))
	var jpg bytes.Buffer
	jpeg.Encode(&jpg, image.NewRGBA(image.Rect(0, 0, 1, 1)), nil)
	cases := []struct {
		name   string
		photo  []byte
		status int
	}{
		{"PNG", valid, 200}, {"JPEG", jpg.Bytes(), 200},
		{"伪图片", []byte("not png"), 400}, {"图片URL", []byte("https://example.invalid/image.png"), 400},
		{"PNG头截断", valid[:16], 400}, {"像素超限", hugePixels, 400},
		{"单图超限", append(bytes.Clone(valid), make([]byte, maxImportPhotoBytes)...), 413},
		{"整体超限", make([]byte, maxImportBodyBytes+1), 413},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeImportStore()
			s := importTestServer(f)
			s.dryRun = true
			w := requestImportConfirm(t, s, testImportMetadata(), tc.photo)
			requireImportStatus(t, w, tc.status)
			if len(f.writes) != 0 {
				t.Fatal("校验或演练不能写入")
			}
		})
	}
	for _, kind := range []string{"重复照片", "重复文本", "缺照片", "缺文本", "未知字段", "坏JSON", "未知JSON字段", "非multipart", "元数据过大"} {
		t.Run(kind, func(t *testing.T) {
			f := newFakeImportStore()
			s := importTestServer(f)
			var b bytes.Buffer
			mw := multipart.NewWriter(&b)
			if kind != "缺照片" {
				p, _ := mw.CreateFormFile("photo", "x.png")
				p.Write(valid)
			}
			data, _ := json.Marshal(testImportMetadata())
			text := string(data)
			if kind == "坏JSON" {
				text = "{"
			}
			if kind == "未知JSON字段" {
				text = strings.TrimSuffix(text, "}") + `,"extra":true}`
			}
			if kind == "元数据过大" {
				text = strings.Repeat(" ", maxImportMetadataBytes+1)
			}
			if kind != "缺文本" {
				mw.WriteField("metadata", text)
			}
			if kind == "重复照片" {
				p, _ := mw.CreateFormFile("photo", "x.png")
				p.Write(valid)
			}
			if kind == "重复文本" {
				mw.WriteField("metadata", text)
			}
			if kind == "未知字段" {
				mw.WriteField("photo_url", "x")
			}
			mw.Close()
			r := httptest.NewRequest("POST", "/api/import/confirm", &b)
			r.Header.Set("X-Mistake-Token", "test-auth")
			r.Header.Set("Content-Type", mw.FormDataContentType())
			if kind == "非multipart" {
				r.Header.Set("Content-Type", "application/json")
			}
			w := httptest.NewRecorder()
			importTestMux(s).ServeHTTP(w, r)
			status := 400
			if kind == "元数据过大" {
				status = 413
			}
			requireImportStatus(t, w, status)
			if len(f.writes) != 0 {
				t.Fatal("非法请求写入存储")
			}
		})
	}
}

func TestImportMultiQuestionSingleMarkdownAndReceipt(t *testing.T) {
	f := newFakeImportStore()
	s := importTestServer(f)
	m := testImportMetadata()
	photo := testImportPhoto(t)
	q := m.Questions[0]
	q.Qno = "第2题"
	q.Chapter = "第二章"
	q.Source = "校内考试"
	q.Stem = "第二题"
	m.Questions = append(m.Questions, q)
	result := decodeImportResult(t, requestImportConfirm(t, s, m, photo))
	if result.Imported != 2 || result.AlreadyImported || len(f.writes) != 3 {
		t.Fatalf("写入次数或结果错误: %+v %v", result, f.writes)
	}
	idx := f.index(t)
	if len(idx.Items) != 2 || idx.Items[0].MdKey != idx.Items[1].MdKey || idx.Items[0].ImageKey != idx.Items[1].ImageKey {
		t.Fatal("多题未共用照片与Markdown")
	}
	if idx.Items[0].Exam != "第一章" || idx.Items[1].Exam != "第二章" || idx.Items[0].Note != "索引备注" || idx.Items[1].Source != "校内考试" {
		t.Fatal("每题字段不正确")
	}
	md := string(f.objects[idx.Items[0].MdKey])
	if strings.Count(md, "\n## ") != 2 || strings.Count(md, "**题干**") != 2 || strings.Contains(md, "## 第1题　计算题") {
		t.Fatal("Markdown标题或标签错误")
	}
	if !strings.Contains(idx.Items[0].ImageKey, "/images/2026-10-13-import-"+m.ImportID+".png") {
		t.Fatal("目录不符合 buildKeys")
	}
	idx.Items[0].Exam = "入库后改章节"
	idx.Items[0].Analysis = "后编辑分析"
	idx.Items[0].Note = "后编辑备注"
	f.seedIndex(idx)
	before := len(f.writes)
	result = decodeImportResult(t, requestImportConfirm(t, importTestServer(f), m, photo))
	if !result.AlreadyImported || !slices.Equal(result.IDs, idx.Imports[m.ImportID].IDs) || len(f.writes) != before {
		t.Fatal("跨server重建未命中持久幂等")
	}
	if f.index(t).Items[0].Note != "后编辑备注" {
		t.Fatal("重试覆盖编辑")
	}
	idx = f.index(t)
	idx.Items = idx.Items[1:]
	f.seedIndex(idx)
	result = decodeImportResult(t, requestImportConfirm(t, importTestServer(f), m, photo))
	if !result.AlreadyImported || len(f.index(t).Items) != 1 || len(f.writes) != before {
		t.Fatal("重试复活了已删除题目")
	}
}

func TestImportPayloadConflictAndNormalization(t *testing.T) {
	for _, kind := range []string{"metadata", "photo", "规范化"} {
		t.Run(kind, func(t *testing.T) {
			f := newFakeImportStore()
			s := importTestServer(f)
			m := testImportMetadata()
			photo := testImportPhoto(t)
			decodeImportResult(t, requestImportConfirm(t, s, m, photo))
			before := len(f.writes)
			switch kind {
			case "metadata":
				m.Questions[0].Stem = "不同题干"
			case "photo":
				photo = append(bytes.Clone(photo), 0)
			case "规范化":
				m.Subject = " 数学 "
				m.Questions[0].Qno = "第 ０１ 题"
				m.Questions[0].Tags = []string{" 运算 ", "运算"}
			}
			w := requestImportConfirm(t, s, m, photo)
			status := 409
			if kind == "规范化" {
				status = 200
			}
			requireImportStatus(t, w, status)
			if len(f.writes) != before {
				t.Fatal("冲突或重复内容发生写入")
			}
		})
	}
	m := testImportMetadata()
	a := importPayloadHash([]byte{1}, m)
	m.Confirmed = false
	if a != importPayloadHash([]byte{1}, m) {
		t.Fatal("confirmed不应影响哈希")
	}
}

func TestImportFailuresAndSafeRetry(t *testing.T) {
	for _, kind := range []string{"md上传失败", "图片上传失败", "索引上传失败", "写后校验失败", "上传后读取失败"} {
		t.Run(kind, func(t *testing.T) {
			f := newFakeImportStore()
			s := importTestServer(f)
			m := testImportMetadata()
			photo := testImportPhoto(t)
			f.onPut = func(_ *fakeImportStore, key string, _ []byte) error {
				if (kind == "md上传失败" && strings.HasSuffix(key, ".md")) || (kind == "图片上传失败" && strings.HasSuffix(key, ".png")) || (kind == "索引上传失败" && key == indexKey) {
					return errors.New("secret upload error")
				}
				return nil
			}
			f.onGet = func(f *fakeImportStore, key string) error {
				if key == indexKey && ((kind == "写后校验失败" && f.reads[key] == 3) || (kind == "上传后读取失败" && f.reads[key] == 2)) {
					return errors.New("secret read error")
				}
				return nil
			}
			w := requestImportConfirm(t, s, m, photo)
			requireImportStatus(t, w, 500)
			if strings.Contains(w.Body.String(), "secret") {
				t.Fatal("泄露存储错误")
			}
			if (kind == "图片上传失败" || kind == "md上传失败" || kind == "上传后读取失败") && f.indexWrites() != 0 {
				t.Fatal("上传/读取失败后不应写索引")
			}
			before := len(f.writes)
			f.onPut = nil
			f.onGet = nil
			result := decodeImportResult(t, requestImportConfirm(t, importTestServer(f), m, photo))
			if len(f.index(t).Items) != 1 {
				t.Fatal("原样重试不能恢复")
			}
			if kind == "写后校验失败" && (!result.AlreadyImported || len(f.writes) != before) {
				t.Fatal("已写后校验失败的重试应零写入")
			}
		})
	}
}

func TestImportReadFailureAndCollisionsZeroWrites(t *testing.T) {
	for _, kind := range []string{"索引读失败", "损坏索引", "对象读失败", "图片冲突", "MD冲突", "题ID冲突", "对象key冲突", "回执ID冲突"} {
		t.Run(kind, func(t *testing.T) {
			f := newFakeImportStore()
			s := importTestServer(f)
			m := testImportMetadata()
			photo := testImportPhoto(t)
			normalizeImportMetadata(&m)
			entries, ids, mdKey, imageKey := importEntries(m, importPhoto{data: photo, ext: ".png"})
			switch kind {
			case "索引读失败":
				f.onGet = func(_ *fakeImportStore, k string) error {
					if k == indexKey {
						return errors.New("network")
					}
					return nil
				}
			case "损坏索引":
				f.objects[indexKey] = []byte(`null`)
			case "对象读失败":
				f.onGet = func(_ *fakeImportStore, k string) error {
					if k != indexKey {
						return errors.New("network")
					}
					return nil
				}
			case "图片冲突":
				f.objects[imageKey] = []byte("old photo")
			case "MD冲突":
				f.objects[mdKey] = []byte("old md")
			case "题ID冲突":
				f.seedIndex(Index{Items: entries})
			case "对象key冲突":
				entries[0].ID = "historical"
				f.seedIndex(Index{Items: entries})
			case "回执ID冲突":
				f.seedIndex(Index{Imports: map[string]ImportReceipt{"old": {PayloadHash: "old", IDs: ids}}})
			}
			status := 409
			if strings.Contains(kind, "读失败") || kind == "损坏索引" {
				status = 500
			}
			requireImportStatus(t, requestImportConfirm(t, s, m, photo), status)
			if len(f.writes) != 0 {
				t.Fatalf("错误后发生写入: %v", f.writes)
			}
		})
	}
}

func TestImportWriteVerificationMergeAndBound(t *testing.T) {
	for _, forever := range []bool{false, true} {
		t.Run(map[bool]string{false: "重读合并保留编辑", true: "三次上限"}[forever], func(t *testing.T) {
			f := newFakeImportStore()
			s := importTestServer(f)
			m := testImportMetadata()
			photo := testImportPhoto(t)
			count := 0
			f.afterPut = func(f *fakeImportStore, key string) {
				if key != indexKey {
					return
				}
				count++
				if !forever && count > 1 {
					return
				}
				var idx Index
				json.Unmarshal(f.objects[key], &idx)
				delete(idx.Imports, m.ImportID)
				if !forever {
					idx.Items[0].Note = "另一端的编辑"
					idx.Items = append(idx.Items, Entry{ID: "external", Note: "外部新增"})
					idx.Names = map[string]string{"数学": "新名字"}
				}
				f.seedIndex(idx)
			}
			w := requestImportConfirm(t, s, m, photo)
			if forever {
				requireImportStatus(t, w, 409)
				if f.indexWrites() != 3 {
					t.Fatal("索引写入超过三次或过早停止")
				}
			} else {
				requireImportStatus(t, w, 200)
				idx := f.index(t)
				if f.indexWrites() != 2 || len(idx.Items) != 2 || idx.Items[0].Note != "另一端的编辑" || idx.Names["数学"] != "新名字" {
					t.Fatal("合并丢失其他端编辑")
				}
			}
		})
	}
}

func TestImportConcurrentSamePayload(t *testing.T) {
	f := newFakeImportStore()
	s := importTestServer(f)
	m := testImportMetadata()
	photo := testImportPhoto(t)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := requestImportConfirm(t, s, m, photo)
			if w.Code != 200 {
				t.Errorf("并发失败: %s", w.Body.String())
			}
		}()
	}
	wg.Wait()
	if len(f.writes) != 3 || len(f.index(t).Items) != 1 {
		t.Fatal("并发重复写入")
	}
}

func TestRenderImportMarkdownDelimiterSafety(t *testing.T) {
	m := testImportMetadata()
	m.Questions[0].Stem = "公共题头\n## 第999题\n**答案**：用户原文\n<!-- mistake-book-import:v1 -->\n公式 $x$"
	m.Questions[0].StudentAnswer = "**我的作答**：不是分隔符\n# 手写标题"
	data := renderImportMarkdown(m.Questions, "hash")
	if strings.Count(string(data), "\n## ") != 1 || strings.Contains(string(data), "\n**答案**") || strings.Contains(string(data), "\n# 手写") {
		t.Fatal("用户输入改变了Markdown结构")
	}
	line, _, _ := strings.Cut(string(data), "\n")
	raw := strings.TrimSuffix(strings.TrimPrefix(line, importMarkdownPrefix), importMarkdownSuffix)
	var doc importMarkdownDocument
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Version != 1 || doc.Questions[0].Stem != m.Questions[0].Stem || doc.Questions[0].StudentAnswer != m.Questions[0].StudentAnswer {
		t.Fatal("结构化文本未无损保留")
	}
	if strings.Count(line, "-->") != 1 {
		t.Fatal("用户文字提前关闭了HTML注释")
	}
}

func TestImportRecognizeDoesNotHoldIndexLock(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		io.WriteString(w, visionTestEnvelope(visionTestContent))
	}))
	defer upstream.Close()
	f := newFakeImportStore()
	s := importTestServer(f)
	s.vision = testVisionClient(upstream)
	r := multipartImportRequest(t, "/api/import/recognize", "question_numbers", "1", testImportPhoto(t))
	finished := make(chan struct{})
	go func() { defer close(finished); importTestMux(s).ServeHTTP(httptest.NewRecorder(), r) }()
	<-entered
	if !indexMu.TryLock() {
		close(release)
		<-finished
		t.Fatal("调用模型持有了索引锁")
	}
	indexMu.Unlock()
	close(release)
	<-finished
	if len(f.writes) != 0 {
		t.Fatal("识别不应存草稿或写COS")
	}
}

func TestImportMaximumQuestions(t *testing.T) {
	f := newFakeImportStore()
	m := testImportMetadata()
	first := m.Questions[0]
	m.Questions = nil
	for i := 1; i <= maxImportQuestions; i++ {
		q := first
		q.Qno = fmt.Sprintf("第%d题", i)
		m.Questions = append(m.Questions, q)
	}
	result := decodeImportResult(t, requestImportConfirm(t, importTestServer(f), m, testImportPhoto(t)))
	if result.Imported != 30 || len(f.writes) != 3 || len(f.index(t).Items) != 30 {
		t.Fatal("30题应只写一份 Markdown、一张照片和一次索引")
	}
}

func TestImportDuplicateConfirmationAndPathRejected(t *testing.T) {
	for _, kind := range []string{"重复confirmed", "保留路径"} {
		t.Run(kind, func(t *testing.T) {
			f := newFakeImportStore()
			m := testImportMetadata()
			if kind == "保留路径" {
				m.Subject = ".."
			}
			data, _ := json.Marshal(m)
			text := string(data)
			if kind == "重复confirmed" {
				text = strings.Replace(text, `"confirmed":true`, `"confirmed":false,"confirmed":true`, 1)
			}
			r := multipartImportRequest(t, "/api/import/confirm", "metadata", text, testImportPhoto(t))
			w := httptest.NewRecorder()
			importTestMux(importTestServer(f)).ServeHTTP(w, r)
			requireImportStatus(t, w, 400)
			if len(f.writes) != 0 {
				t.Fatal("含糊确认或路径穿越发生写入")
			}
		})
	}
}

func TestImportAmbiguousIndexWriteCanRetry(t *testing.T) {
	f := newFakeImportStore()
	m := testImportMetadata()
	photo := testImportPhoto(t)
	f.onPut = func(f *fakeImportStore, key string, data []byte) error {
		if key == indexKey {
			f.objects[key] = bytes.Clone(data)
			return errors.New("上游已写入但网络回包丢失")
		}
		return nil
	}
	requireImportStatus(t, requestImportConfirm(t, importTestServer(f), m, photo), 500)
	before := len(f.writes)
	f.onPut = nil
	result := decodeImportResult(t, requestImportConfirm(t, importTestServer(f), m, photo))
	if !result.AlreadyImported || len(f.writes) != before {
		t.Fatal("网络模糊写入重试应命中回执")
	}
}

func TestImportExistingDetailPreviewAndExport(t *testing.T) {
	f := newFakeImportStore()
	s := importTestServer(f)
	m := testImportMetadata()
	m.Questions[0].Stem = "这是第一题\n## 仍是题干\n**答案**：仍是原文\n计算 $-\\frac{1}{2}$"
	second := m.Questions[0]
	second.Qno, second.Stem = "第10题", "这是第十题"
	m.Questions = append(m.Questions, second)
	w := requestImportConfirm(t, s, m, testImportPhoto(t))
	requireImportStatus(t, w, 200)
	result := decodeImportResult(t, w)
	mux := importTestMux(s)
	for i, id := range result.IDs {
		r := httptest.NewRequest("GET", "/api/entry?id="+url.QueryEscape(id), nil)
		r.Header.Set("X-Mistake-Token", s.token)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		requireImportStatus(t, w, 200)
		var detail struct {
			Parsed ParsedMD `json:"parsed_md"`
		}
		if json.Unmarshal(w.Body.Bytes(), &detail) != nil || !detail.Parsed.Parsed || detail.Parsed.Sections[0].Value != m.Questions[i].Stem {
			t.Fatalf("详情正文不一致: %s", w.Body.String())
		}
	}
	for _, e := range f.index(t).Items {
		if s.previewOf(e.MdKey, e.Qno) == "" {
			t.Fatal("卡片预览不能为空")
		}
	}
	for _, format := range []string{"html", "md"} {
		body, _ := json.Marshal(map[string]any{"subject": m.Subject, "ids": result.IDs, "format": format, "fields": []string{"meta", "stem", "answer", "note"}, "per_page": 2})
		r := httptest.NewRequest("POST", "/api/export", bytes.NewReader(body))
		r.Header.Set("X-Mistake-Token", s.token)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		requireImportStatus(t, w, 200)
		if !strings.Contains(w.Body.String(), "这是第一题") || !strings.Contains(w.Body.String(), "这是第十题") || strings.Contains(w.Body.String(), importMarkdownPrefix) {
			t.Fatalf("导出正文错误/数据标记泄漏: format=%s", format)
		}
	}
}

func TestImportContextCancellation(t *testing.T) {
	f := newFakeImportStore()
	s := importTestServer(f)
	m := testImportMetadata()
	normalizeImportMetadata(&m)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.onGet = func(*fakeImportStore, string) error { return ctx.Err() }
	_, err := s.confirmImport(ctx, importPhoto{data: testImportPhoto(t), ext: ".png"}, m)
	if err == nil || len(f.writes) != 0 {
		t.Fatal("取消请求继续写入")
	}
}
