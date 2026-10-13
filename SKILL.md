---
title: "错题本 (mistake-book)"
summary: "从试卷照片中识别并摘出题目（含自己的作答与批改痕迹）， Markdown + 原图一起存腾讯云 COS，并维护全局索引 exam-bank/index.json，供后续按科目/试卷/对错筛选题目导出 PDF。"
read_when:
  - 老师发试卷/作业照片并要求摘题、提取题目
  - 老师说「这是数学/语文/物理…的第X题」
  - 老师要往错题本里加题、补题、查看已收录题目
  - 老师要按科目/试卷/对错筛选题目、导出成 PDF 练习卷
  - 老师提到 错题本、题目录入、试卷照片归档
---

# 错题本 (mistake-book)

照片 → 题目 Markdown + 原图 → COS → 全局索引。索引是后续导出 PDF 选题的唯一来源。

## Binary

Go 二进制，COS 读写走 `github.com/zhangyf/objstore`（与 jrp / study-tracker 同一个库），不依赖 node / python。

- Windows: `~/.workbuddy/skills/mistake-book/mistake-book.exe`
- macOS:   `~/.workbuddy/skills/mistake-book/mistake-book`

## 存储结构（COS 桶 `openclaw-backup-tx-1251036673` / ap-beijing）

```
exam-bank/
├── index.json                                   # 全局索引，所有题目条目（导出选题靠它）
└── {科目}/
    └── {YYYY-MM}-{试卷名}/
        ├── {YYYY-MM-DD}-{照片名}.md               # 一张照片一个 md（含该照片里的多道题）
        └── images/{YYYY-MM-DD}-{照片名}.jpg        # 原图
```

- 科目目录名、试卷名用原文（数学 / 第一章有理数测验），脚本自动做文件名安全化。
- 同一张照片后续补题（比如先问第 4 题、后来又问第 7 题）→ 追加到**同一个 md**，索引里多一条，不要新建文件。

## 索引条目字段（index.json 的 items[]）

```json
{
  "id": "数学-2026-09-21-IMG_20260921_222934-q4",
  "subject": "数学",
  "exam": "第一章有理数测验",
  "date": "2026-09-21",
  "photo": "IMG_20260921_222934",
  "qno": "第4题",
  "qtype": "选择题",
  "result": "correct",
  "tags": ["乘方", "底数判断"],
  "md_key": "exam-bank/数学/2026-09-第一章有理数测验/2026-09-21-IMG_20260921_222934.md",
  "image_key": "exam-bank/数学/2026-09-第一章有理数测验/images/2026-09-21-IMG_20260921_222934.jpg",
  "note": "选 C 正确；易错点：-3^4 底数是 3 不是 -3"
}
```

- `result`：`correct` / `wrong` / `unknown`（看不出批改结果就写 unknown，不要猜）。
- `tags`：知识点标签，导出时按知识点成卷用。
- 同名 id 重复录入 = 覆盖更新（改判对错、补笔记直接重录）。

## Markdown 模板

```markdown
---
subject: 数学
exam: 第一章有理数测验
date: 2026-09-21
photo: IMG_20260921_222934.jpg
---

## 第 4 题　选择题　✓

**题干**：对于 $-3^4$，下列叙述正确的是（　　）

- A. 表示 3 个 4 相乘的积的相反数
- B. 底数是 −3，指数是 4
- C. 表示 4 个 3 相乘的积的相反数
- D. 表示 4 个 −3 相乘的积

**我的作答**：C

**批改**：✓ 正确（红笔圈出 + 勾）

**错因/要点**：$-3^4$ 的底数是 3，负号在最外层；B 是陷阱项。
```

计算/解答题的作答按原卷步骤逐行抄，每步一行（后面导出 PDF 时一步一行排版）。

## 工作流

**1. 收集元数据。** 老师发照片时通常只给科目 + 题号。缺的就问，别猜：
- 试卷名（没有就问「这次是哪张卷子」，仍不知道写 `未命名`）
- 日期（默认取照片文件名里的日期 `IMG_20260921_…` → 2026-09-21；取不到用今天）
- 照片原始文件名（老师消息里的 `@image#1:IMG_xxx.jpg`，用它做文件标识，不要用 `clipboard-2026-…` 那种临时名）

**2. 摘题。** 把题干、选项、图/表信息逐字提取；数学式子一律 LaTeX（行内 `$…$`，独立 `$$…$$`）。同时记录老师的作答过程和红笔批改痕迹（勾/叉/圈/问号/旁批）。

**3. 写 md。** 落到工作区 `outputs/`，按上面模板。**先把提取结果给老师看一眼再上传**。

**4. 上传。** 用 `mistake-book` 二进制（见命令参考），一次 `up` 把图片和 md 都传上去。

**5. 更新索引。** `index-add` 写条目。最后向老师回报：存到哪个路径、索引共几条、要不要补别的题。

## 命令参考

```bash
EB=~/.workbuddy/skills/mistake-book/mistake-book.exe   # macOS 用 mistake-book

# 先看会存到哪（不上传）
$EB path --subject 数学 --exam "第一章有理数测验" --date 2026-09-21 --photo IMG_20260921_222934

# 上传原图 + md（--photo 省略则取本地图片文件名；--expires 0 不生成预签名 URL）
$EB up --image <本地图片> --md <本地md> --subject 数学 --exam "第一章有理数测验" \
       --date 2026-09-21 --photo IMG_20260921_222934

# 写索引（单条 / 数组 / {"items":[]} 均可；--input - 从 stdin 读）
$EB index-add --input entry.json
echo '{"subject":"数学",...}' | $EB index-add --input -

# 删索引条目
$EB index-rm --id 数学-2026-09-21-IMG_20260921_222934-第4题

# 查
$EB list --subject 数学
$EB list --result wrong --limit 20
$EB search --kw 乘方
$EB get --key exam-bank/数学/2026-09-第一章有理数测验/2026-09-21-IMG_20260921_222934.md
$EB get --key <key> --output local.md

# 检查凭证 / 桶连通性（换机器先跑这个）
$EB doctor

# Web 服务（浏览 / 改 / 删）
$EB serve                                    # 本机 http://127.0.0.1:8081
$EB serve --port 8099 --dry-run              # 演练：改/删不写 COS
$EB serve --addr 0.0.0.0 --auth-token xxxx   # 对外暴露必须带 token
```

所有命令输出 JSON 到 stdout；错误走 stderr + 退出码 1。

## Web 服务

`mistake-book serve` 起一个纯 `net/http` 的服务（端口默认 8081），前端原生 JS + 内联 KaTeX，`go:embed` 打进二进制，**零外网 CDN**。

- **集中页**：各科错题本卡片（题数 / 对错 / 所属试卷）→ 点进科目。
- **详情页**：可按 **日期 / 知识点 / 题型 / 对错** 四个维度切换分组，支持按对错筛选、知识点多选、关键词搜索；点条目展开详情（左原图、右字段，LaTeX 由 KaTeX 渲染）；每条可改 result / tags / note，可删条目（只删索引，md 和原图保留）。
- API：`GET /api/subjects`、`GET /api/entries`、`GET|PUT|DELETE /api/entry`、`GET /api/image?key=`（后端代理，凭证不出后端）。
- 鉴权：`--auth-token`（或 `MISTAKE_AUTH_TOKEN`），非 loopback 监听没设 token 会拒绝启动。静态资源不鉴权，token 在页面输入后存 localStorage。
- ⚠️ **改了 `web/` 下任何文件都必须重新编译二进制**（embed 打进去的）。

### 服务器部署（154.8.216.179）

跟 jrp 同一套：交叉编译 → 传二进制 → systemd → nginx 反代 → Basic Auth。

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o mistake-book .
# 传二进制：腾讯云扫码横幅会破坏 scp，用管道
ssh -i <jrp_deploy> -o BatchMode=yes -o StrictHostKeyChecking=no root@154.8.216.179 \
  'cat > /usr/local/bin/mistake-book' < ./mistake-book
# 顺序必须 stop → cp → start，否则 Text file busy
```

- 端口 **8081**（8080 已被 jrp 占），Go 只听 `127.0.0.1`，TLS 和登录由 nginx 承担。
- 外网入口 **`https://mistake.110105.xyz`**（子域 DNS + 证书由老师申请，证书放 `/etc/nginx/ssl/tencent/`）。
- 凭证：systemd 用 `EnvironmentFile=/etc/mistake-book/mistake.env`（600）注入 `TENCENT_COS_*`。
- Basic Auth：`/etc/nginx/.mistake_htpasswd`（与 jrp 独立）。
- 改 `nginx.conf` 前**先备份**并 `nginx -t`（jrp 在同一份配置里，改坏会连带影响）。

## 提取规则（硬性）

1. **数学式子用 LaTeX**，不写成纯文本（`$\dfrac{16}{25}$` 不是 `16/25`）。
2. **字迹不确定要标注**，写 `〔?〕` 并说明，不要凭语感补字。
3. **不臆造**：照片上没有的分数、批改标记、知识点，一律不写；`result` 看不出就 `unknown`。
4. **原图必传**，md 里通过 `image_key` 关联，不单独存裁剪图。
5. **批改痕迹要还原**：勾/叉/圈/问号/老师旁批都写进「批改」行，这是判断错因的依据。
6. **老师写的作答和正确答案分开记**：作答归「我的作答」，对错归「批改」，别混。

## Critical Rules

1. **凭证在 `~/.workbuddy/cos-credentials/`**（与 jrp / study-tracker 同一份）。绝不要把凭证写进任何 marketplace 管理的 skill 目录（skill 更新会整目录覆盖，2026-09-10 丢过一次）。二进制解析顺序：环境变量 `TENCENT_COS_*` → `.env` 明文 → `.env.enc` 解密（密钥 = SHA-256(hostname:username:凭证目录)，与 jrp 一致）；可用 `MISTAKE_BOOK_COS_SKILL_DIR` 改凭证目录。
2. **COS 读写一律走 objstore**，不自己签名、不调 cos_node.mjs、不写 python。
3. **索引是导出 PDF 的唯一选题来源**，每录一题必须 `index-add`，不能只传文件。
4. **一张照片 = 一个 md**，同照片补题追加，不新建文件。
5. **上传前先给老师看提取结果**，确认无误再落 COS。
6. 报告/临时文件放在工作区 `outputs/` 下，否则 present_files 不触发。

## 导出 PDF（接口预留，尚未实现）

后续实现时走：按 index.json 筛条目 → 取 md → 渲染 HTML（内联 CSS，不依赖外网 CDN）→ Chrome/Edge headless `--print-to-pdf`。
环境已具备：`/c/Program Files/Google/Chrome/Application/chrome.exe`、`/c/Program Files (x86)/Microsoft/Edge/Application/msedge.exe`，PyMuPDF 已装（拼版/页码/加页眉）。
等老师给具体布局（A4 几栏、要不要留作答空白、要不要附答案、按知识点还是按时间排序）再开工。

## 源码与编译

| | Windows（工作机） | macOS（家用机） |
|---|---|---|
| 源码目录 | `C:\Users\efrainzhang\mistake-book` | `~/mistake-book` |
| 二进制 | `~/.workbuddy/skills/mistake-book/mistake-book.exe` | `~/.workbuddy/skills/mistake-book/mistake-book` |
| 编译 | `go build -o ~/.workbuddy/skills/mistake-book/mistake-book.exe .` | `GOPROXY=goproxy.cn go build -o ~/.workbuddy/skills/mistake-book/mistake-book .` |

- 依赖只有 `github.com/zhangyf/objstore v0.17.0`（模块缓存里已有），无网络也能编：`GOPROXY=off GOFLAGS=-mod=mod go build ...`
- GitHub: https://github.com/zhangyf/mistake-book (public)，与 jrp / study-tracker 一致。Mac 那边 `git clone` 到 `~/mistake-book` 即可共用源码。

## 环境备注

- Windows（工作机）：`go` 在 PATH（go1.26.5）；bash 里直接跑 `.exe` 即可。
- macOS（家用机）：用 `~/.workbuddy/binaries/go/bin` 里的 go，配 `GOPROXY=goproxy.cn`；二进制名不带 `.exe`。
- 换机器先跑 `mistake-book doctor`，能打印桶名和索引条数就说明凭证通了。
