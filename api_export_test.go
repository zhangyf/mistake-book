package main

import (
	"strings"
	"testing"
)

func TestHTMLValueRendersFigureFence(t *testing.T) {
	out := htmlValue("题干开头\n```\n <b>&\n ─●─▶\n```\n- 列表项\n后文段落")
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
	if out := htmlValue("```\n未闭合图"); !strings.Contains(out, "</pre>") {
		t.Fatalf("未闭合围栏也应输出闭合 pre: %s", out)
	}
}
