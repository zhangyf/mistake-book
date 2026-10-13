// ============================================================================
// restem —— 维护命令：替换导入格式 Markdown 里指定题号的题干。
// 结构化标记（首行 JSON）与可读正文由同一份 questions 数据重新渲染，保证一致；
// 渲染后立即做一次解析 round-trip 校验，失败就不写。
// ============================================================================

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

func runRestem(args []string) {
	fs := flag.NewFlagSet("restem", flag.ExitOnError)
	key := fs.String("md", "", "COS 上的 Markdown key（必填）")
	qno := fs.String("qno", "", "要替换题干的题号（必填）")
	stemFile := fs.String("stem-file", "", "新题干文本文件路径（UTF-8，必填；- 表示从 stdin 读）")
	dryRun := fs.Bool("dry-run", false, "只渲染并校验，不写 COS")
	fs.Parse(args)
	if *key == "" || *qno == "" || *stemFile == "" {
		fail("restem 需要 --md / --qno / --stem-file")
	}
	var stem []byte
	var err error
	if *stemFile == "-" {
		stem, err = io.ReadAll(os.Stdin)
	} else {
		stem, err = os.ReadFile(*stemFile)
	}
	if err != nil {
		fail("读题干失败: %v", err)
	}
	newStem := strings.TrimRight(string(stem), "\r\n")

	st, err := NewStorage()
	if err != nil {
		fail("%v", err)
	}
	ctx, cancel := newCtx()
	defer cancel()
	data, err := st.GetAll(ctx, *key)
	if err != nil {
		fail("下载失败: %v", err)
	}
	out, err := rewriteImportStem(data, *qno, newStem)
	if err != nil {
		fail("%v", err)
	}
	if *dryRun {
		output(map[string]interface{}{"success": true, "dry_run": true, "md": *key, "qno": *qno,
			"old_bytes": len(data), "new_bytes": len(out)})
		return
	}
	if err := st.PutBytes(ctx, *key, out); err != nil {
		fail("写回失败: %v", err)
	}
	back, err := st.GetAll(ctx, *key)
	if err != nil {
		fail("写回后回读失败: %v", err)
	}
	got, ok := parsedImportStem(back, *qno)
	if !ok {
		fail("写回后解析失败，请检查 %s", *key)
	}
	if got != newStem {
		fail("写回后回读的题干与预期不一致，请人工检查 %s", *key)
	}
	output(map[string]interface{}{"success": true, "md": *key, "qno": *qno, "bytes": len(out)})
}

// rewriteImportStem 纯函数：把导入格式 md 里 qno 的题干换成 newStem，
// 首行标记与正文一起重新渲染，并做 round-trip 校验。
func rewriteImportStem(md []byte, qno, newStem string) ([]byte, error) {
	text := string(md)
	if !strings.HasPrefix(text, importMarkdownPrefix) {
		return nil, errors.New("不是导入格式的 Markdown（缺少首行标记）")
	}
	end := strings.Index(text, importMarkdownSuffix)
	if end < 0 {
		return nil, errors.New("导入标记未闭合")
	}
	var doc importMarkdownDocument
	if err := json.Unmarshal([]byte(text[len(importMarkdownPrefix):end]), &doc); err != nil {
		return nil, fmt.Errorf("导入标记解析失败: %v", err)
	}
	if doc.Version != 1 {
		return nil, fmt.Errorf("不支持的标记版本 %d", doc.Version)
	}
	found := false
	for i := range doc.Questions {
		if doc.Questions[i].Qno == qno {
			doc.Questions[i].Stem = newStem
			found = true
		}
	}
	if !found {
		return nil, fmt.Errorf("标记里没有题号 %s", qno)
	}
	out := renderImportMarkdown(doc.Questions, doc.PayloadHash)
	if _, ok := parseImportedMarkdown(string(out), qno); !ok {
		return nil, errors.New("重渲染后 round-trip 解析失败")
	}
	return out, nil
}

// parsedImportStem 从 md 文本里解析出指定题号的题干。
func parsedImportStem(md []byte, qno string) (string, bool) {
	pm, ok := parseImportedMarkdown(string(md), qno)
	if !ok {
		return "", false
	}
	for _, sec := range pm.Sections {
		if matchSectionKey(sec.Key, stemKeys) {
			return sec.Value, true
		}
	}
	return "", false
}
