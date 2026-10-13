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
