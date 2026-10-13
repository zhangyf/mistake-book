package main

import (
	"strings"
	"testing"
)

func TestHTMLValueRendersFigureFence(t *testing.T) {
	out := htmlValue("题干开头\n```\n <b>&\n ─●─▶\n```\n- 列表项\n后文段落", nil)
	if !strings.Contains(out, `<pre class="fig">`) {
		t.Fatalf("围栏应渲染为 pre.fig: %s", out)
	}
	if strings.Contains(out, "<b>") {
		t.Fatalf("字符画内容必须转义: %s", out)
	}
	if !strings.Contains(out, "&lt;b&gt;&amp;") {
		t.Fatalf("字符画内容转义不正确: %s", out)
	}
	if strings.Contains(out, "```") {
		t.Fatalf("围栏标记不应出现在输出里: %s", out)
	}
	if !strings.Contains(out, "<li>列表项</li>") || strings.Count(out, "<p>") != 2 {
		t.Fatalf("围栏外的段落与列表应照常渲染: %s", out)
	}
	if out := htmlValue("```\n未闭合图", nil); !strings.Contains(out, "</pre>") {
		t.Fatalf("未闭合围栏也应输出闭合 pre: %s", out)
	}
}

// figure 块能解析时渲染成裁好的题图；解析不出、或拿不到图，都退回字符画。
func TestHTMLValueRendersFigureCrop(t *testing.T) {
	key := "exam-bank/数学/2026-10-第三章有理数/images/2026-10-13-import-9f44.jpg"
	text := "如图，数轴上表示的数是多少？\n" + figureBlockText(FigureRef{
		Key: key, X: 0.1, Y: 0.2, W: 0.5, H: 0.25, Ratio: 1.6,
	})

	var asked []FigureRef
	src := "data:image/jpeg;base64,AAAA"
	got := htmlValue(text, func(ref FigureRef) (string, bool) {
		asked = append(asked, ref)
		return `<div class="figcrop"><img src="` + src + `"></div>`, true
	})
	if len(asked) != 1 || asked[0].Key != key || asked[0].W != 0.5 {
		t.Fatalf("应把解析出的框交给渲染器: %+v", asked)
	}
	if !strings.Contains(got, `class="figcrop"`) || !strings.Contains(got, src) {
		t.Fatalf("figure 块应渲染成裁好的题图: %s", got)
	}
	if strings.Contains(got, "```") {
		t.Fatalf("围栏标记不应出现在输出里: %s", got)
	}

	// 渲染器说拿不到图 → 按字符画降级，内容不能丢
	fallback := htmlValue(text, func(FigureRef) (string, bool) { return "", false })
	if !strings.Contains(fallback, `<pre class="fig">`) || !strings.Contains(fallback, key) {
		t.Fatalf("取不到图时应降级为字符画并保留原文: %s", fallback)
	}

	// 非法块（坐标越界）连渲染器都不该被调用
	bad := "```figure\n" + key + " 0.9000 0.2000 0.5000 0.2500 1.6000\n```"
	calls := 0
	out := htmlValue(bad, func(FigureRef) (string, bool) { calls++; return "X", true })
	if calls != 0 || !strings.Contains(out, `<pre class="fig">`) {
		t.Fatalf("非法框不应进入渲染器，且应降级显示: %s", out)
	}
}

// md 导出把 figure 块换成带坐标片段的图片链接，其余行原样保留。
func TestMDValueRewritesFigure(t *testing.T) {
	key := "exam-bank/数学/2026-10-第三章有理数/images/2026-10-13-import-9f44.jpg"
	ref := FigureRef{Key: key, X: 0.1, Y: 0.2, W: 0.5, H: 0.25, Ratio: 1.6}
	text := "第一行\n" + figureBlockText(ref) + "\n第三行"

	got := mdValue(text, func(got FigureRef) (string, bool) {
		return figureMarkdownLink("https://example.com/a.jpg", got), true
	})
	if !strings.Contains(got, "![图示](https://example.com/a.jpg#fig=0.1000,0.2000,0.5000,0.2500)") {
		t.Fatalf("figure 块应换成图片链接: %s", got)
	}
	if !strings.Contains(got, "第一行") || !strings.Contains(got, "第三行") || strings.Contains(got, "```") {
		t.Fatalf("其余内容应逐行保留、围栏应消失: %s", got)
	}

	// 拿不到链接就原样保留块（宁可难看也别丢信息）
	kept := mdValue(text, func(FigureRef) (string, bool) { return "", false })
	if kept != text {
		t.Fatalf("取不到链接时应原样保留: %s", kept)
	}
}
