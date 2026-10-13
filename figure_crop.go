package main

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"sync"
)

// ============================================================================
// 裁图渲染（只给导出用）
//
// 网页里的裁图由前端用 CSS 按框裁（原图已经在浏览器里，即时、无损、不占服务端）。
// 但导出的 HTML 练习卷必须**内联**图片才离线可用，如果内联整张原图，一张照片
// 好几 MB、多道题共用同一张就要重复内联，卷子能涨到几十 MB，打印都费劲。
// 所以导出这条路走真裁：解码原图 → 切出框 → 重新编码成小图，再 base64 内联。
//
// 只读 COS、不写任何对象：框本身留在 md 里，随时可以重框。
// ============================================================================

const (
	figureJPEGQuality = 85
	figureCacheMax    = 48
	figureMaxPixels   = 60000000 // 解不开或过大的原图直接放弃裁图（降级成文字）
)

type cropAsset struct {
	data  []byte
	ctype string
}

var figureCropCache = struct {
	sync.Mutex
	items map[string]cropAsset
	order []string
}{items: map[string]cropAsset{}}

func figureAssetKey(resolvedKey string, fig FigureRef) string {
	return fmt.Sprintf("%s|%.4f|%.4f|%.4f|%.4f", resolvedKey, fig.X, fig.Y, fig.W, fig.H)
}

func cropCacheGet(k string) (cropAsset, bool) {
	figureCropCache.Lock()
	defer figureCropCache.Unlock()
	a, ok := figureCropCache.items[k]
	return a, ok
}

func cropCachePut(k string, a cropAsset) {
	figureCropCache.Lock()
	defer figureCropCache.Unlock()
	if _, ok := figureCropCache.items[k]; !ok {
		figureCropCache.order = append(figureCropCache.order, k)
	}
	figureCropCache.items[k] = a
	for len(figureCropCache.order) > figureCacheMax {
		old := figureCropCache.order[0]
		figureCropCache.order = figureCropCache.order[1:]
		delete(figureCropCache.items, old)
	}
}

// cropFigure 把原图按框裁出来，重新编码。PNG 原图输出 PNG（保透明），其余输出 JPEG。
func cropFigure(src []byte, fig FigureRef) (cropAsset, error) {
	if fig.W < figureMinSide || fig.H < figureMinSide || fig.X < 0 || fig.Y < 0 ||
		fig.X+fig.W > 1.0001 || fig.Y+fig.H > 1.0001 {
		return cropAsset{}, errors.New("裁剪框无效")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(src))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return cropAsset{}, errors.New("原图无法识别")
	}
	if int64(cfg.Width)*int64(cfg.Height) > figureMaxPixels {
		return cropAsset{}, errors.New("原图过大，放弃裁图")
	}
	img, _, err := image.Decode(bytes.NewReader(src))
	if err != nil {
		return cropAsset{}, err
	}
	b := img.Bounds()
	x0, y0, x1, y1, ok := fig.cropRect(b.Dx(), b.Dy())
	if !ok {
		return cropAsset{}, errors.New("裁剪框无效")
	}
	rect := image.Rect(b.Min.X+x0, b.Min.Y+y0, b.Min.X+x1, b.Min.Y+y1)
	cropped := img
	// JPEG 解出来是 *image.YCbCr，SubImage 零拷贝；其余格式老实拷一次。
	if sub, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok && format == "jpeg" {
		cropped = sub.SubImage(rect)
	} else {
		dst := image.NewRGBA(image.Rect(0, 0, x1-x0, y1-y0))
		draw.Draw(dst, dst.Bounds(), img, rect.Min, draw.Src)
		cropped = dst
	}
	var buf bytes.Buffer
	if format == "png" {
		if err := png.Encode(&buf, cropped); err != nil {
			return cropAsset{}, err
		}
		return cropAsset{data: buf.Bytes(), ctype: "image/png"}, nil
	}
	if err := jpeg.Encode(&buf, cropped, &jpeg.Options{Quality: figureJPEGQuality}); err != nil {
		return cropAsset{}, err
	}
	return cropAsset{data: buf.Bytes(), ctype: "image/jpeg"}, nil
}
