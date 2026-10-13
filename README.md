# mistake-book

错题本命令行工具：把试卷照片摘出的题目 Markdown + 原图上传到腾讯云 COS，并维护全局索引
`exam-bank/index.json`，供后续筛选题目导出 PDF 练习卷。

配合 WorkBuddy skill `mistake-book` 使用（AI 负责看图摘题，本工具负责存档与索引）。

## COS 存储结构

```
exam-bank/
├── index.json                                   # 全局索引，一题一条
└── {科目}/
    └── {YYYY-MM}-{试卷名}/
        ├── {YYYY-MM-DD}-{照片名}.md               # 一张照片一个 md
        └── images/{YYYY-MM-DD}-{照片名}.jpg        # 原图
```

## 命令

| 命令 | 说明 |
|---|---|
| `path` | 计算（不上传）md / 原图的 COS key |
| `up` | 上传原图 + md |
| `index-add` | 合并条目到索引（同 id 视为更新，标签取并集） |
| `index-rm` | 按 id 删除索引条目 |
| `list` | 列出条目，可按 `--subject` / `--exam` / `--result` / `--limit` 过滤 |
| `search` | 关键词检索（匹配整条记录的任意字段） |
| `get` | 下载并打印对象内容（`--output` 可写文件） |
| `doctor` | 检查凭证与桶连通性 |

所有命令输出 JSON 到 stdout，错误走 stderr + 退出码 1。

```bash
mistake-book up --image photo.jpg --md q4.md --subject 数学 --exam "第一章有理数测验" \
                --date 2026-09-21 --photo IMG_20260921_222934
mistake-book index-add --input entry.json
mistake-book list --subject 数学 --result wrong
```

## 凭证

解析顺序：

1. 环境变量 `TENCENT_COS_SECRET_ID` / `TENCENT_COS_SECRET_KEY` / `TENCENT_COS_REGION` / `TENCENT_COS_BUCKET`
2. `~/.workbuddy/cos-credentials/.env`（明文）
3. `~/.workbuddy/cos-credentials/.env.enc`（AES-256-GCM，密钥 = SHA-256(hostname:username:凭证目录)）

凭证目录可用 `MISTAKE_BOOK_COS_SKILL_DIR` 覆盖。与 jrp / study-tracker 共用同一份凭证。

## 编译

依赖只有 `github.com/zhangyf/objstore`。

```bash
# Windows
go build -o ~/.workbuddy/skills/mistake-book/mistake-book.exe .

# macOS
GOPROXY=goproxy.cn go build -o ~/.workbuddy/skills/mistake-book/mistake-book .
```
