package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRewriteImportStemRoundTrip(t *testing.T) {
	orig := renderImportMarkdown([]importQuestion{
		{Qno: "第4题", Qtype: "解答题", Stem: "旧题干", StudentAnswer: "作答", Chapter: "有理数", Source: "课外班作业"},
		{Qno: "第7题", Qtype: "选择题", Stem: "另一题\n```\n图\n```", StudentAnswer: "B"},
	}, "deadbeef")

	got, err := rewriteImportStem(orig, "第4题", "新题干\n```\n────●──▶\n 1  2\n```")
	if err != nil {
		t.Fatalf("rewriteImportStem 失败: %v", err)
	}
	stem, ok := parsedImportStem(got, "第4题")
	if !ok || stem != "新题干\n```\n────●──▶\n 1  2\n```" {
		t.Fatalf("第4题题干未正确替换: %q (ok=%v)", stem, ok)
	}
	other, ok := parsedImportStem(got, "第7题")
	if !ok || !strings.Contains(other, "图") {
		t.Fatalf("其它题不应受影响: %q (ok=%v)", other, ok)
	}
	// 作答等字段保持不变
	if !bytes.Contains(got, []byte("作答")) || !bytes.Contains(got, []byte("deadbeef")) {
		t.Fatalf("作答或回执哈希丢失: %s", got)
	}
}

func TestRewriteImportStemRejects(t *testing.T) {
	if _, err := rewriteImportStem([]byte("## 第4题\n普通 md"), "第4题", "x"); err == nil {
		t.Fatal("非导入格式应报错")
	}
	orig := renderImportMarkdown([]importQuestion{{Qno: "第4题", Stem: "旧"}}, "h")
	if _, err := rewriteImportStem(orig, "第9题", "x"); err == nil {
		t.Fatal("缺题号应报错")
	}
}

func TestRewriteLegacyFieldsKeepsOtherContent(t *testing.T) {
	stem := "改后的题干\n```\n────●──▶\n 1  2\n```"
	answer := "改后的作答"
	out, err := rewriteMarkdownFields([]byte(multiMD), "第2题", &stem, &answer)
	if err != nil {
		t.Fatalf("rewriteMarkdownFields 失败: %v", err)
	}
	p := ParseMarkdown(string(out), "第2题")
	var gotStem, gotAns string
	for _, sec := range p.Sections {
		if matchSectionKey(sec.Key, stemKeys) {
			gotStem = sec.Value
		}
		if matchSectionKey(sec.Key, answerKeys) {
			gotAns = sec.Value
		}
	}
	if gotStem != stem || gotAns != answer {
		t.Fatalf("第2题改写错误:\n stem=%q\n ans=%q", gotStem, gotAns)
	}
	// 第1题必须原样保留
	p1 := ParseMarkdown(string(out), "第1题")
	if len(p1.Sections) == 0 || p1.Sections[0].Value != "$(-2)^5 = $ ____" {
		t.Fatalf("第1题被影响了: %+v", p1.Sections)
	}
	if !strings.Contains(string(out), "**错因**：带分数拆分符号写错") {
		t.Fatalf("第2题的其它字段丢了: %s", out)
	}
}

func TestRewriteLegacySingleBlockAndVerify(t *testing.T) {
	stem := "新题干"
	out, err := rewrittenVerified([]byte(sampleMD), "第4题", &stem, nil)
	if err != nil {
		t.Fatalf("rewrittenVerified 失败: %v", err)
	}
	if !strings.Contains(string(out), "**批改**：✓ 正确") {
		t.Fatalf("批改字段应保留（解析时丢弃，原文不动）: %s", out)
	}
	if !strings.Contains(string(out), "photo: IMG_20260921_222934.jpg") {
		t.Fatalf("题头 frontmatter 丢了: %s", out)
	}
}

func TestRewriteImportBothFields(t *testing.T) {
	orig := renderImportMarkdown([]importQuestion{{Qno: "第4题", Stem: "旧题干", StudentAnswer: "旧作答"}}, "h")
	s, a := "新题干", "新作答"
	out, err := rewriteMarkdownFields(orig, "第4题", &s, &a)
	if err != nil {
		t.Fatalf("失败: %v", err)
	}
	pm, ok := parseImportedMarkdown(string(out), "第4题")
	if !ok {
		t.Fatal("重渲染后解析失败")
	}
	var gotStem, gotAns string
	for _, sec := range pm.Sections {
		if matchSectionKey(sec.Key, stemKeys) {
			gotStem = sec.Value
		}
		if matchSectionKey(sec.Key, answerKeys) {
			gotAns = sec.Value
		}
	}
	if gotStem != s || gotAns != a {
		t.Fatalf("导入格式改写错误: %q / %q", gotStem, gotAns)
	}
}

func TestRewriteMissingQuestionRejected(t *testing.T) {
	stem := "x"
	if _, err := rewriteMarkdownFields([]byte(multiMD), "第9题", &stem, nil); err == nil {
		t.Fatal("旧格式缺题号应报错")
	}
	if _, err := rewrittenVerified([]byte(sampleMD), "第4题", &stem, nil); err != nil {
		t.Fatalf("单题块应可改写: %v", err)
	}
}
