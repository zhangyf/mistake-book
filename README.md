# mistake-book

错题本命令行工具：把试卷照片摘出的题目 Markdown + 原图上传到腾讯云 COS，并维护全局索引
`exam-bank/index.json`，供后续筛选题目导出 PDF 练习卷。

配合 WorkBuddy skill `mistake-book` 使用（AI 负责看图摘题，本工具负责存档与索引）。

## 网页编辑

点开某道题 →「编辑」可改章节 / 出处 / 知识点 / 错因要点 / 备注，以及**题干**和**我的作答**。
题干与作答存在 Markdown 里（不在索引里），保存时会重写对应的 md 对象：新导入格式同步改写
首行结构化标记与正文，旧的手写格式只替换该题块里的对应段落，其余内容逐行保留；写回前做
解析 round-trip 校验，失败不落盘，未改动则完全不写。维护命令 `mistake-book restem` 走同一套逻辑。

## 网页照片导入

在首页或科目页点击「导入照片」：每批最多20张 JPEG/PNG，每张最多10MiB、4000万像素；逐张填写题号（例如 `第4题、第10题(1)`），最多同时识别2张。识别结果只是草稿，需对照原图编辑、勾选核对并逐图点击「确认入库」。照片及 Markdown 只有确认后才写入 COS，一张照片仍对应一个 Markdown，多道题共用原图。

- 草稿仅保留当前页面，不写浏览器持久存储、不上传 COS；离开或刷新前提醒。识别会将原图发给已配置的 TokenHub。
- 题目里的图形（数轴、示意图、表格等）由模型转成等宽字符画，放在题干的三个反引号围栏块里；网页详情、导入预览和导出的 HTML 练习卷都按 `<pre>` 等宽显示，列表卡片预览显示为「（图示）」。
- 首次确认会冻结该图请求内容。结果不确定时只能原样重试，持久回执避免重复写入；明确的输入校验失败可恢复编辑。成功后锁定照片。
- 新导入文件名使用随机导入编号，沿用 `exam-bank/{科目}/{年月}-{章节}/` 目录，不覆盖已有题目。Markdown 保留可阅读正文及无损结构化标记，网页按精确题号取题。
- `--dry-run` 确认不写原图、Markdown 或索引；识别仍会调用模型并可能计费，页面会标明「演练，未入库」。

### 模型配置与上线前提

服务单独读取 `MISTAKE_LLM_ENDPOINT`、`MISTAKE_LLM_API_KEY`、`MISTAKE_LLM_MODEL`（默认 `deepseek-v4.1-flash`）。endpoint 必须显式配置为已有 TokenHub HTTPS 完整 chat/completions 地址；不自动读取或修改 jrp 的配置，不把密钥发送到浏览器。未配置时原有浏览/编辑照常，识别显示未启用。

上线需单独授权并配置服务环境变量、nginx `client_max_body_size 12m` 及至少120秒的代理读取超时。识别本身90秒超时，不自动重试付费请求。不能只替换二进制却未配置模型便声称识别已启用。

索引读取仅在 COS 明确返回对象不存在时初始化；权限、网络、空内容或损坏 JSON 均停止写入。新索引增加 `imports` 回执字段，因此所有参与写入的 CLI/服务应同步升级，旧版写入可能丢弃回执。当前进程内串行化加写后校验，不是跨进程 CAS；应避免与旧 CLI 或其他服务实例同时写索引。上传中断可能留下未索引对象，原样重试可复用，不自动删除。

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
