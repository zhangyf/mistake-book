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
	out, err := rewrittenVerified(data, *qno, &newStem, nil)
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
	got, ok := parsedMDSection(back, *qno, stemKeys)
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
	return rewriteMarkdownFields(md, qno, &newStem, nil)
}

// rewriteMarkdownFields 按题号改写 md 里的题干（setStem）与我的作答（setAnswer）。
// 传 nil 表示不改该项。导入格式走首行标记，旧的手写格式按段落替换；
// 两种都在返回前做一次解析 round-trip 校验。
func rewriteMarkdownFields(md []byte, qno string, setStem, setAnswer *string) ([]byte, error) {
	if strings.HasPrefix(string(md), importMarkdownPrefix) {
		return rewriteImportFields(md, qno, setStem, setAnswer)
	}
	return rewriteLegacyFields(md, qno, setStem, setAnswer)
}

func rewriteImportFields(md []byte, qno string, setStem, setAnswer *string) ([]byte, error) {
	text := string(md)
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
			if setStem != nil {
				doc.Questions[i].Stem = *setStem
			}
			if setAnswer != nil {
				doc.Questions[i].StudentAnswer = *setAnswer
			}
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

// rewriteLegacyFields 改旧的手写格式 md：只动指定题号块里的目标字段段落，
// 其余内容（题头、其它字段、其它题）逐行原样保留。
func rewriteLegacyFields(md []byte, qno string, setStem, setAnswer *string) ([]byte, error) {
	lines := strings.Split(string(md), "\n")
	var heads []int
	for i, ln := range lines {
		if headingRe.MatchString(ln) {
			heads = append(heads, i)
		}
	}
	start, end := 0, len(lines)
	switch {
	case len(heads) == 0:
		// 无标题：整篇就是一个题块
	case len(heads) == 1:
		start = heads[0]
	default:
		want := fullSpaceRe.ReplaceAllString(strings.TrimSpace(qno), "")
		picked := -1
		for _, i := range heads {
			head := strings.TrimSpace(headingRe.FindStringSubmatch(lines[i])[1])
			if want != "" && strings.Contains(fullSpaceRe.ReplaceAllString(head, ""), want) {
				picked = i
				break
			}
		}
		if picked < 0 {
			return nil, fmt.Errorf("md 里没有题号 %s 对应的题块", qno)
		}
		start = picked
		for _, i := range heads {
			if i > picked {
				end = i
				break
			}
		}
	}

	set := func(label string, value *string) error {
		if value == nil {
			return nil
		}
		for i := start; i < end; i++ {
			m := fieldRe.FindStringSubmatch(lines[i])
			if m == nil || strings.TrimSpace(m[1]) != label {
				continue
			}
			last := i + 1
			for last < end && fieldRe.FindStringSubmatch(lines[last]) == nil {
				last++
			}
			// 旧值可能在字段行行内（**题干**：xxx），所以整行重置后再插新值
			head := "**" + label + "**："
			lines = append(lines[:i], append([]string{head, *value}, lines[last:]...)...)
			return nil
		}
		return fmt.Errorf("题号 %s 的段落里没有「%s」字段", qno, label)
	}
	if err := set("题干", setStem); err != nil {
		return nil, err
	}
	if err := set("我的作答", setAnswer); err != nil {
		return nil, err
	}
	return []byte(strings.Join(lines, "\n")), nil
}

// rewrittenVerified 改写并校验：写回前先确认解析出来的内容与目标一致。
func rewrittenVerified(md []byte, qno string, setStem, setAnswer *string) ([]byte, error) {
	out, err := rewriteMarkdownFields(md, qno, setStem, setAnswer)
	if err != nil {
		return nil, err
	}
	pm := ParseMarkdown(string(out), qno)
	for label, want := range map[string]*string{"题干": setStem, "我的作答": setAnswer} {
		if want == nil {
			continue
		}
		keys := stemKeys
		if label == "我的作答" {
			keys = answerKeys
		}
		got := ""
		for _, sec := range pm.Sections {
			if matchSectionKey(sec.Key, keys) {
				got = sec.Value
				break
			}
		}
		if got != *want {
			return nil, fmt.Errorf("%s 改写后回读不一致（解析长度 %d，期望 %d）", label, len(got), len(*want))
		}
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

// parsedMDSection 用通用解析器取指定题号某个键的内容（新/旧格式都行）。
func parsedMDSection(md []byte, qno string, keys []string) (string, bool) {
	pm := ParseMarkdown(string(md), qno)
	for _, sec := range pm.Sections {
		if matchSectionKey(sec.Key, keys) {
			return sec.Value, true
		}
	}
	return "", false
}
