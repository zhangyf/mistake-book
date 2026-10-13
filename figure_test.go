package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

const testFigureKey = "exam-bank/数学/2026-10-第三章有理数/images/2026-10-13-import-9f44.jpg"

func TestParseFigureBody(t *testing.T) {
	cases := []struct {
		name string
		body []string
		ok   bool
	}{
		{"正常", []string{testFigureKey + " 0.1200 0.3400 0.5500 0.2100 1.6180"}, true},
		{"允许前后空行", []string{"", testFigureKey + " 0 0 1 1 2.0000", ""}, true},
		{"字段不足", []string{testFigureKey + " 0.1 0.2 0.5"}, false},
		{"字段多余", []string{testFigureKey + " 0.1 0.2 0.5 0.2 1.6 9"}, false},
		{"key 越界目录", []string{"other/x.jpg 0.1 0.2 0.5 0.2 1.6"}, false},
		{"key 非图片", []string{"exam-bank/a/b.md 0.1 0.2 0.5 0.2 1.6"}, false},
		{"key 带空格注入", []string{"exam-bank/a.jpg\"onload=x 0.1 0.2 0.5 0.2 1.6"}, false},
		{"坐标越界", []string{testFigureKey + " 0.9000 0.2 0.5000 0.2 1.6"}, false},
		{"负数", []string{testFigureKey + " -0.1 0.2 0.5 0.2 1.6"}, false},
		{"科学计数法", []string{testFigureKey + " 1e-1 0.2 0.5 0.2 1.6"}, false},
		{"小数位过多", []string{testFigureKey + " 0.12345 0.2 0.5 0.2 1.6"}, false},
		{"宽高过小", []string{testFigureKey + " 0.1 0.2 0.001 0.2 1.6"}, false},
		{"宽高比过大", []string{testFigureKey + " 0.1 0.2 0.5 0.2 99.0"}, false},
		{"宽高比过小", []string{testFigureKey + " 0.1 0.2 0.5 0.2 0.01"}, false},
		{"空块", []string{"", "  "}, false},
	}
	for _, c := range cases {
		_, ok := parseFigureBody(c.body)
		if ok != c.ok {
			t.Errorf("%s: 期望 ok=%v，实得 %v", c.name, c.ok, ok)
		}
	}

	ref, ok := parseFigureBody([]string{"@photo 0.1200 0.3400 0.5500 0.2100 1.6180"})
	if !ok || ref.Key != figurePhotoAlias {
		t.Fatalf("@photo 占位应解析成功: %+v ok=%v", ref, ok)
	}
}

func TestFigureBlockRoundTrip(t *testing.T) {
	ref := FigureRef{Key: testFigureKey, X: 0.12, Y: 0.34, W: 0.55, H: 0.21, Ratio: 1.618}
	text := figureBlockText(ref)
	if !strings.HasPrefix(text, "```figure\n") || !strings.HasSuffix(text, "\n```") {
		t.Fatalf("块首尾围栏不对: %q", text)
	}
	if !strings.Contains(text, "0.1200 0.3400 0.5500 0.2100 1.6180") {
		t.Fatalf("字段格式不对: %q", text)
	}
	got, ok := parseFigureBody(strings.Split(text, "\n")[1:2])
	if !ok || got != ref {
		t.Fatalf("往返解析应一致: %+v ok=%v", got, ok)
	}
}

func TestScanFencesAndFigureBlocks(t *testing.T) {
	text := "开头\n```\n字符画\n```\n" + figureBlockText(FigureRef{
		Key: testFigureKey, X: 0.1, Y: 0.1, W: 0.5, H: 0.5, Ratio: 1,
	}) + "\n结尾\n```figure\n不是合法块"

	blocks := scanFences(text)
	if len(blocks) != 3 {
		t.Fatalf("应扫出 3 段围栏: %+v", blocks)
	}
	if blocks[0].Info != "" || !blocks[0].Closed {
		t.Fatalf("第一段应是普通字符画: %+v", blocks[0])
	}
	if blocks[1].Info != "figure" {
		t.Fatalf("第二段应是 figure: %+v", blocks[1])
	}
	if blocks[2].Closed {
		t.Fatalf("末段未闭合: %+v", blocks[2])
	}
	figs := figureBlocks(text)
	if len(figs) != 1 || figs[0].Start != blocks[1].Start {
		t.Fatalf("只应认出解析成功的那一个 figure 块: %+v", figs)
	}
}

func TestResolvePhotoAlias(t *testing.T) {
	photo := "exam-bank/数学/2026-10-未命名/images/2026-10-13-import-9f44.jpg"
	text := "看图\n" + figureBlockText(FigureRef{Key: figurePhotoAlias, X: 0.1, Y: 0.2, W: 0.5, H: 0.2, Ratio: 1.5}) + "\n后文"
	got := resolvePhotoAlias(text, photo)
	if !strings.Contains(got, photo) || strings.Contains(got, figurePhotoAlias) {
		t.Fatalf("@photo 应被替换成真实 key: %s", got)
	}
	if !strings.Contains(got, "看图") || !strings.Contains(got, "后文") {
		t.Fatalf("其余内容不能动: %s", got)
	}
	if _, ok := parseFigureBody(strings.Split(got, "\n")[2:3]); !ok {
		t.Fatalf("替换后仍应是合法块: %s", got)
	}

	// 已经是真实 key 的不动；原图 key 为空时不动（宁可留着 @photo 也别写坏）
	real := figureBlockText(FigureRef{Key: testFigureKey, X: 0.1, Y: 0.2, W: 0.5, H: 0.2, Ratio: 1.5})
	if resolvePhotoAlias(real, photo) != real {
		t.Fatal("真实 key 不应被改写")
	}
	if resolvePhotoAlias(text, "") != text {
		t.Fatal("原图 key 缺失时应保持原样")
	}
}

func TestFigureRefCropRect(t *testing.T) {
	ref := FigureRef{X: 0.1, Y: 0.2, W: 0.5, H: 0.25}
	x0, y0, x1, y1, ok := ref.cropRect(100, 50)
	if !ok || x0 != 10 || y0 != 10 || x1 != 60 || y1 != 22 {
		t.Fatalf("像素框换算不对: %d,%d,%d,%d ok=%v", x0, y0, x1, y1, ok)
	}
	// 极端值必须被夹到图内，且至少 1px
	_, _, x1b, y1b, ok := (FigureRef{X: 0.99, Y: 0.99, W: 1, H: 1}).cropRect(10, 10)
	if !ok || x1b != 10 || y1b != 10 {
		t.Fatalf("应夹到图内: %d,%d", x1b, y1b)
	}
	if _, _, _, _, ok := ref.cropRect(0, 0); ok {
		t.Fatal("零尺寸图应返回 ok=false")
	}
}

func TestCropFigure(t *testing.T) {
	src := testPNG(t, 40, 30)
	asset, err := cropFigure(src, FigureRef{X: 0.25, Y: 0.2, W: 0.5, H: 0.6, Ratio: 1.11})
	if err != nil {
		t.Fatalf("裁图失败: %v", err)
	}
	if asset.ctype != "image/png" {
		t.Fatalf("PNG 原图应输出 PNG: %s", asset.ctype)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(asset.data))
	if err != nil {
		t.Fatalf("裁出来的图应能解码: %v", err)
	}
	if format != "png" || cfg.Width != 20 || cfg.Height != 18 {
		t.Fatalf("裁出尺寸应为 20x18，实得 %dx%d (%s)", cfg.Width, cfg.Height, format)
	}
	if _, err := cropFigure(src, FigureRef{X: 0, Y: 0, W: 0, H: 0, Ratio: 1}); err == nil {
		t.Fatal("非法框应报错")
	}
	if _, err := cropFigure([]byte("not an image"), FigureRef{X: 0, Y: 0, W: 1, H: 1, Ratio: 1}); err == nil {
		t.Fatal("坏图应报错")
	}
}

func TestFigureFragmentRoundTrip(t *testing.T) {
	ref := FigureRef{X: 0.12, Y: 0.34, W: 0.55, H: 0.21}
	link := figureMarkdownLink("https://example.com/a.jpg", ref)
	url, got, ok := parseFigureFragment(link)
	if !ok || url != "https://example.com/a.jpg" || got != ref {
		t.Fatalf("坐标片段往返应一致: %q %+v ok=%v", url, got, ok)
	}
	if _, _, ok := parseFigureFragment("https://example.com/a.jpg"); ok {
		t.Fatal("没有 #fig= 的链接不应被当成裁图")
	}
}

func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 6), G: uint8(y * 8), B: 20, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("构造测试图失败: %v", err)
	}
	return buf.Bytes()
}
