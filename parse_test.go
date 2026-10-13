package main

import (
	"strings"
	"testing"
)

const sampleMD = `---
subject: 数学
exam: 未命名
date: 2026-09-21
photo: IMG_20260921_222934.jpg
---

## 第 4 题　选择题　✓

**题干**：对于 $-3^4$，下列叙述正确的是（　　）

- A. 表示 3 个 4 相乘的积的相反数
- B. 底数是 −3，指数是 4

**我的作答**：C

**批改**：✓ 正确（红笔圈出作答并打勾）

**错因/要点**：$-3^4$ 的底数是 3。B 是陷阱项。
`

const multiMD = `## 第 1 题　填空题

**题干**：$(-2)^5 = $ ____

**我的作答**：$(-2)^5$

## 第 2 题　计算题

**题干**：计算 $-32\dfrac{16}{25}\div(-32)$

**我的作答**：$=1\dfrac{1}{50}$

**错因**：带分数拆分符号写错
`

func TestParseSingle(t *testing.T) {
	p := ParseMarkdown(sampleMD, "第4题")
	if !p.Parsed {
		t.Fatalf("应解析成功，raw=%q", p.Raw)
	}
	if p.Heading != "第 4 题　选择题　✓" {
		t.Errorf("heading=%q", p.Heading)
	}
	want := []string{"题干", "我的作答", "批改", "错因/要点"}
	if len(p.Sections) != len(want) {
		t.Fatalf("sections=%d", len(p.Sections))
	}
	for i, k := range want {
		if p.Sections[i].Key != k {
			t.Errorf("第%d个字段=%q，期望%q", i, p.Sections[i].Key, k)
		}
	}
	if p.Sections[1].Value != "C" {
		t.Errorf("我的作答=%q", p.Sections[1].Value)
	}
	// LaTeX 必须原样保留，交给前端 KaTeX
	if !strings.Contains(p.Sections[0].Value, `$-3^4$`) {
		t.Errorf("题干里的 LaTeX 丢了: %q", p.Sections[0].Value)
	}
	// 题干要包含选项行
	if !strings.Contains(p.Sections[0].Value, "- A.") {
		t.Errorf("题干丢了选项: %q", p.Sections[0].Value)
	}
}

func TestParseMultiPicksRight(t *testing.T) {
	p := ParseMarkdown(multiMD, "第2题")
	if !p.Parsed {
		t.Fatalf("应解析成功")
	}
	if p.Heading != "第 2 题　计算题" {
		t.Errorf("挑错块了: heading=%q", p.Heading)
	}
	if p.Sections[0].Value == "" || !strings.Contains(p.Sections[0].Value, `\dfrac`) {
		t.Errorf("题干不对: %q", p.Sections[0].Value)
	}
}

func TestParseFallback(t *testing.T) {
	// 完全没有字段标签 → 降级，不白屏
	p := ParseMarkdown("## 第 3 题\n\n随便写的一段话，没有标准字段", "第3题")
	if p.Parsed {
		t.Errorf("不应标记为解析成功")
	}
	if p.Raw == "" {
		t.Errorf("降级时必须带回原文")
	}
}

func TestParseEmpty(t *testing.T) {
	p := ParseMarkdown("", "第1题")
	if p.Parsed {
		t.Errorf("空文档不应解析成功")
	}
}

func TestParseUnknownLabelKept(t *testing.T) {
	// 未知标签不能丢内容
	p := ParseMarkdown("## 第 5 题\n**题干**：x\n**老师评语**：写得不错", "第5题")
	if !p.Parsed {
		t.Fatalf("应解析成功")
	}
	if len(p.Sections) != 2 || p.Sections[1].Key != "老师评语" {
		t.Errorf("未知标签丢了: %+v", p.Sections)
	}
}
