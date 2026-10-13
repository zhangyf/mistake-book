package main

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// ============================================================================
// figure 块 —— 老师在原图上框选出来的「题图」（2026-10-13 加）
//
// 设计：不新增图片对象，只记「哪张原图 + 框了哪一块」，渲染时按框裁切。
// 这样裁图可逆、可重调，旧题目不用重新导入就能用，也不会留下孤儿文件，
// 更不用在导入确认那条「写前全检查、失败零写入」的事务链路上加东西。
//
// 格式（题干里独占一段）：
//
//	```figure
//	exam-bank/数学/2026-10-第三章有理数/images/2026-10-13-import-9f44.jpg 0.1200 0.3400 0.5500 0.2100 1.6180
//	```
//
// 六个字段：COS key / x / y / w / h / 裁剪区宽高比，坐标都是相对原图归一化的
// （左上角为 0,0，右下角为 1,1）。宽高比 = (w × 原图宽) ÷ (h × 原图高)，存下来
// 是为了渲染时不用先等原图加载完就能定好高度，避免页面跳动。
//
// key 允许写 @photo —— 表示「本 md 对应的那张原图」。网页导入的草稿阶段还不知道
// 最终 key（key 由 科目/章节/日期/导入编号 拼出来），所以草稿里一律写 @photo，
// 由 confirmImport 在算 payload hash 之前替换成真实 key（见 resolvePhotoAlias），
// 这样重试幂等、落盘的 md 里永远是真实 key。
//
// 解析不出来（字段缺失、坐标越界、key 非法）一律**按普通字符画块降级渲染**：
// 宁可显示得难看，也不能因为手改出一个非法块就让老师存不了题。
// ============================================================================

const (
	// figureFenceInfo 是围栏语言标记。
	figureFenceInfo = "figure"
	// figurePhotoAlias 指代「本 md 对应的原图」。
	figurePhotoAlias = "@photo"
	// figureMinSide 太小的框没意义，也会被放大到糊。
	figureMinSide = 0.01
	// figureMaxRatio / figureMinRatio 限制宽高比，挡掉极端值导致的巨幅渲染。
	figureMaxRatio = 20.0
	figureMinRatio = 0.05
)

var (
	fenceOpenRe   = regexp.MustCompile("^`{3,}")
	figureOpenRe  = regexp.MustCompile(`(?i)^` + "`{3,}" + `\s*figure\s*$`)
	figureKeyRe   = regexp.MustCompile(`^exam-bank/[^\s#\\]+\.(?:jpe?g|png|webp|gif|heic)$`)
	figureUnitRe  = regexp.MustCompile(`^\d(?:\.\d{1,4})?$`)      // 0 ~ 1
	figureRatioRe = regexp.MustCompile(`^\d{1,2}(?:\.\d{1,4})?$`) // 0 ~ 99.9999
	figureSpaceRe = regexp.MustCompile(`[\s　]+`)
)

// FigureRef 是一个裁图引用。
type FigureRef struct {
	Key   string  `json:"key"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	W     float64 `json:"w"`
	H     float64 `json:"h"`
	Ratio float64 `json:"ratio"`
}

// fenceBlock 是文本里的一段围栏块（行号区间含首尾）。
type fenceBlock struct {
	Start, End int
	Info       string
	Body       []string
	Closed     bool
}

// isFenceLine 判断整行是否围栏行（``` 开头，可带语言标记）。
func isFenceLine(line string) bool {
	return fenceOpenRe.MatchString(strings.TrimSpace(line))
}

// fenceInfo 取围栏起始行的语言标记（```figure → "figure"；``` → ""）。
func fenceInfo(line string) string {
	t := strings.TrimSpace(line)
	t = strings.TrimLeft(t, "`")
	return strings.TrimSpace(t)
}

// isFigureFenceLine 判断围栏起始行是否声明为 figure。
func isFigureFenceLine(line string) bool {
	return figureOpenRe.MatchString(strings.TrimSpace(line))
}

// validFigureKey 校验 key 是错题本目录下的图片对象。
func validFigureKey(key string) bool {
	return figureKeyRe.MatchString(key)
}

// parseFigureBody 解析 figure 块体：取第一条非空行，须恰好六个字段。
func parseFigureBody(body []string) (FigureRef, bool) {
	var line string
	for _, ln := range body {
		if t := strings.TrimSpace(ln); t != "" {
			line = t
			break
		}
	}
	if line == "" {
		return FigureRef{}, false
	}
	fields := figureSpaceRe.Split(line, -1)
	if len(fields) != 6 {
		return FigureRef{}, false
	}
	key := fields[0]
	if key != figurePhotoAlias && !validFigureKey(key) {
		return FigureRef{}, false
	}
	for i := 1; i <= 5; i++ {
		if i <= 4 && !figureUnitRe.MatchString(fields[i]) {
			return FigureRef{}, false
		}
		if i == 5 && !figureRatioRe.MatchString(fields[i]) {
			return FigureRef{}, false
		}
	}
	nums := make([]float64, 5)
	for i := 1; i <= 5; i++ {
		n, err := strconv.ParseFloat(fields[i], 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return FigureRef{}, false
		}
		nums[i-1] = n
	}
	fig := FigureRef{Key: key, X: nums[0], Y: nums[1], W: nums[2], H: nums[3], Ratio: nums[4]}
	if fig.X+fig.W > 1.0001 || fig.Y+fig.H > 1.0001 || fig.W < figureMinSide || fig.H < figureMinSide {
		return FigureRef{}, false
	}
	if fig.Ratio < figureMinRatio || fig.Ratio > figureMaxRatio {
		return FigureRef{}, false
	}
	return fig, true
}

// figureBlockText 生成一个 figure 块（含首尾围栏），字段格式与前端一致。
func figureBlockText(fig FigureRef) string {
	return "```" + figureFenceInfo + "\n" +
		fmt.Sprintf("%s %.4f %.4f %.4f %.4f %.4f", fig.Key, fig.X, fig.Y, fig.W, fig.H, fig.Ratio) +
		"\n```"
}

// scanFences 扫出文本里所有围栏块。未闭合的围栏延伸到文末（与前端渲染一致）。
func scanFences(text string) []fenceBlock {
	lines := strings.Split(text, "\n")
	var out []fenceBlock
	for i := 0; i < len(lines); {
		if !isFenceLine(lines[i]) {
			i++
			continue
		}
		blk := fenceBlock{Start: i, Info: fenceInfo(lines[i]), End: len(lines) - 1}
		closeAt := -1
		for j := i + 1; j < len(lines); j++ {
			if isFenceLine(lines[j]) {
				closeAt = j
				break
			}
		}
		if closeAt >= 0 {
			blk.Closed = true
			blk.Body = lines[i+1 : closeAt]
			blk.End = closeAt
			i = closeAt + 1
		} else {
			blk.Body = lines[i+1:]
			i = len(lines)
		}
		out = append(out, blk)
	}
	return out
}

// figureBlocks 取出文本里所有**解析成功**的 figure 块。
func figureBlocks(text string) []fenceBlock {
	var out []fenceBlock
	for _, blk := range scanFences(text) {
		if !strings.EqualFold(blk.Info, figureFenceInfo) {
			continue
		}
		if _, ok := parseFigureBody(blk.Body); ok {
			out = append(out, blk)
		}
	}
	return out
}

// resolveFigureKey 把 @photo 换成真实原图 key；其余 key 原样返回（校验交给调用方）。
func resolveFigureKey(key, photoKey string) (string, bool) {
	if key == figurePhotoAlias {
		if photoKey == "" || !validFigureKey(photoKey) {
			return "", false
		}
		return photoKey, true
	}
	if !validFigureKey(key) {
		return "", false
	}
	return key, true
}

// resolvePhotoAlias 把文本里 figure 块的 @photo 替换成真实 key（导入确认时用一次）。
// 只动能解析成功的 figure 块，其它行逐字保留。
func resolvePhotoAlias(text, photoKey string) string {
	if !strings.Contains(text, figurePhotoAlias) {
		return text
	}
	lines := strings.Split(text, "\n")
	for _, blk := range scanFences(text) {
		if !strings.EqualFold(blk.Info, figureFenceInfo) {
			continue
		}
		fig, ok := parseFigureBody(blk.Body)
		if !ok || fig.Key != figurePhotoAlias || photoKey == "" || !validFigureKey(photoKey) {
			continue
		}
		for i := blk.Start + 1; i <= blk.End; i++ {
			if strings.TrimSpace(lines[i]) == "" {
				continue
			}
			fields := figureSpaceRe.Split(strings.TrimSpace(lines[i]), -1)
			fields[0] = photoKey
			lines[i] = strings.Join(fields, " ")
			break
		}
	}
	return strings.Join(lines, "\n")
}

// cropRect 把归一化框换算成像素矩形（半开区间，已夹到图内，保证至少 1px）。
func (f FigureRef) cropRect(iw, ih int) (x0, y0, x1, y1 int, ok bool) {
	if iw <= 0 || ih <= 0 {
		return 0, 0, 0, 0, false
	}
	clamp := func(v, max float64) int {
		if v < 0 {
			return 0
		}
		if v > max {
			return int(max)
		}
		return int(v)
	}
	x0 = clamp(f.X*float64(iw), float64(iw))
	y0 = clamp(f.Y*float64(ih), float64(ih))
	x1 = clamp((f.X+f.W)*float64(iw), float64(iw))
	y1 = clamp((f.Y+f.H)*float64(ih), float64(ih))
	if x1 <= x0 {
		x1 = x0 + 1
	}
	if y1 <= y0 {
		y1 = y0 + 1
	}
	if x1 > iw {
		x1, x0 = iw, iw-1
	}
	if y1 > ih {
		y1, y0 = ih, ih-1
	}
	return x0, y0, x1, y1, true
}

// figureMarkdownLink 把裁图块渲染成 Markdown 图片链接：`![图示](url#fig=x,y,w,h)`。
// 只有 md 导出用；坐标放 #fragment 里，既能被本工具再解析，也不影响图片本身打开。
func figureMarkdownLink(url string, fig FigureRef) string {
	return fmt.Sprintf("![图示](%s#fig=%.4f,%.4f,%.4f,%.4f)", url, fig.X, fig.Y, fig.W, fig.H)
}

// parseFigureFragment 解析 `url#fig=x,y,w,h`（也接受 md 图片语法 `![图示](url#fig=...)`）。
func parseFigureFragment(raw string) (string, FigureRef, bool) {
	s := strings.TrimSpace(raw)
	if i := strings.LastIndex(s, "]("); i >= 0 {
		s = s[i+2:]
		if j := strings.IndexByte(s, ')'); j >= 0 {
			s = s[:j]
		}
	}
	i := strings.LastIndex(s, "#fig=")
	if i < 0 {
		return s, FigureRef{}, false
	}
	parts := strings.Split(s[i+5:], ",")
	if len(parts) != 4 {
		return s, FigureRef{}, false
	}
	var nums [4]float64
	for j, p := range parts {
		p = strings.TrimSpace(p)
		if !figureUnitRe.MatchString(p) {
			return s, FigureRef{}, false
		}
		n, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return s, FigureRef{}, false
		}
		nums[j] = n
	}
	fig := FigureRef{X: nums[0], Y: nums[1], W: nums[2], H: nums[3]}
	if fig.W < figureMinSide || fig.H < figureMinSide || fig.X+fig.W > 1.0001 || fig.Y+fig.H > 1.0001 {
		return s, FigureRef{}, false
	}
	return s[:i], fig, true
}
