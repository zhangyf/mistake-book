---
title: "错题本 (mistake-book)"
summary: "从作业/试卷照片中识别并摘出题目（含自己的作答）， Markdown + 原图一起存腾讯云 COS，并维护全局索引 exam-bank/index.json，供后续按科目/章节筛选题目导出练习卷。"
read_when:
  - 老师发作业/试卷照片并要求摘题、提取题目
  - 老师说「这是数学/语文/物理…的第X题」
  - 老师要往错题本里加题、补题、查看已收录题目
  - 老师要按科目/章节筛选题目、导出成练习卷
  - 老师提到 错题本、题目录入、作业照片归档
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
    └── {YYYY-MM}-{章节}/
        ├── {YYYY-MM-DD}-{照片名}.md               # 一张照片一个 md（含该照片里的多道题）
        └── images/{YYYY-MM-DD}-{照片名}.jpg        # 原图
```

- 科目目录名、章节用原文（数学 / 第三章 有理数），自动做文件名安全化。
- 同一张照片后续补题（比如先问第 4 题、后来又问第 7 题）→ 追加到**同一个 md**，索引里多一条，不要新建文件。

## 索引条目字段（index.json 的 items[]）

```json
{
  "id": "数学-2026-09-21-IMG_20260921_222934-q4",
  "subject": "数学",
  "exam": "第三章 有理数",
  "date": "2026-09-21",
  "photo": "IMG_20260921_222934",
  "qno": "第4题",
  "qtype": "选择题",
  "result": "correct",
  "source": "校内考试",
  "analysis": "$-3^4$ 的底数是 3，负号在最外层；B 是陷阱项。",
  "tags": ["乘方", "底数判断"],
  "md_key": "exam-bank/数学/2026-09-第三章有理数/2026-09-21-IMG_20260921_222934.md",
  "image_key": "exam-bank/数学/2026-09-第三章有理数/images/2026-09-21-IMG_20260921_222934.jpg",
  "note": "选 C 正确；易错点：-3^4 底数是 3 不是 -3"
}
```

- `exam`：**章节**（不是「试卷」）。老师填的是「第几章作业」这类东西（2026-10-13 老师纠正：这里本来就该是章节）。它同时是 COS 路径 `{YYYY-MM}-{章节}/` 的一段，改它会换目录，重录等于新建一套路径。
  - JSON 键名仍是 `exam`（索引和路径都靠它，改名要迁移数据，不值当）；CLI 两个 flag 同义：`--exam` / `--chapter`。
- `result`：`correct` / `wrong` / `unknown`。**录入时不用太纠结**——网页上不逐题显示对错（错题本里都是要重做的），只在工具栏「按对错」分组/筛选时用得到。
- `source`：错题出处，只能是 `校内作业` / `课外班作业` / `校内考试` / `自我复习`（网页上是下拉选择；老师没说就留空，别猜）。
- `analysis`：错因要点。**留空时展示 md 里解析出的「错因/要点」；一旦填了就覆盖 md 那份**（老师会在网页上手改，所以录入时可以不填，让 md 说话）。
- `tags`：知识点标签，导出时按知识点成卷用。
- 同名 id 重复录入 = 覆盖更新（补章节、补笔记直接重录）。
- 索引里还有顶层 `names` 字段：`{"数学": "初一数学"}`，科目 → 错题本显示名，
  网页卡片右上角「改名」写这里；没有这个键或值为空 = 卡片直接显示科目名。

## Markdown 模板（`exam` = 章节，例如「第三章 有理数」）

```markdown
---
subject: 数学
exam: 第三章 有理数
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

**错因/要点**：$-3^4$ 的底数是 3，负号在最外层；B 是陷阱项。
```

- 计算/解答题的作答按原卷步骤逐行抄，每步一行（导出时一步一行排版）。
- **不写「批改」字段**（2026-10-13 老师明确要求去掉）：对错一律用索引的 `result` 表达，
  md 里不再记第二遍。历史 md 里已有的 `**批改**：` 行，解析时会被直接丢掉。
  红笔痕迹（勾/叉/圈/问号）只用来判断 `result`，判断完就丢，不落 md。

## 工作流

**1. 收集元数据。** 老师发照片时通常只给科目 + 题号。缺的就问，别猜：
- 章节（老师会填「第几章作业」之类；不知道就问，仍不知道写 `未命名`）
- 日期（默认取照片文件名里的日期 `IMG_20260921_…` → 2026-09-21；取不到用今天）
- 照片原始文件名（老师消息里的 `@image#1:IMG_xxx.jpg`，用它做文件标识，不要用 `clipboard-2026-…` 那种临时名）

**2. 摘题。** 把题干、选项、图/表信息逐字提取；数学式子一律 LaTeX（行内 `$…$`，独立 `$$…$$`）。同时看红笔批改痕迹（勾/叉/圈/问号/旁批）判断对错 —— 判断结果写进索引的 `result`，不写进 md。

**3. 写 md。** 落到工作区 `outputs/`，按上面模板。**先把提取结果给老师看一眼再上传**。

**4. 上传。** 用 `mistake-book` 二进制（见命令参考），一次 `up` 把图片和 md 都传上去。

**5. 更新索引。** `index-add` 写条目。最后向老师回报：存到哪个路径、索引共几条、要不要补别的题。

## 命令参考

```bash
EB=~/.workbuddy/skills/mistake-book/mistake-book.exe   # macOS 用 mistake-book

# 先看会存到哪（不上传）
$EB path --subject 数学 --exam "第三章 有理数" --date 2026-09-21 --photo IMG_20260921_222934

# 上传原图 + md（--photo 省略则取本地图片文件名；--expires 0 不生成预签名 URL）
$EB up --image <本地图片> --md <本地md> --subject 数学 --exam "第三章 有理数" \
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
$EB get --key exam-bank/数学/2026-09-第三章有理数/2026-09-21-IMG_20260921_222934.md
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

- **集中页**：各科错题本卡片（题数 / 最近日期 / 含哪些章节），卡片右上角「改名」给错题本起名（如「初一数学」），留空恢复默认显示科目名。**不再显示对/错计数**（错题本里全是要重做的）。
- **详情页**：按 **日期 / 知识点 / 题型 / 出处**（按对错仍在，但见下）切换分组，**题按卡片罗列、卡片上直接显示题干预览**（预览由后端从 md 里取 90 字并缓存，公式照样走 KaTeX）；支持知识点多选、关键词搜索；点卡片展开详情（左原图、右字段，LaTeX 由 KaTeX 渲染）。**详情默认只读**，点「编辑」才进编辑表单（每个输入框都有标签：章节 / 出处 / 知识点 / 错因要点 / 备注）。
- **可改的字段**：章节 `exam`、出处 `source`（四选一下拉）、错因要点 `analysis`、知识点 `tags`、备注 `note`。保存后不整页刷新，只重拉列表 + 重开当前详情（回到查看态）。可删条目（只删索引，md 和原图保留）。
- **不逐题显示「对错」**（2026-10-13 老师：错题本里全是要重做的，标对错没意义）：列表不画对错圆点、详情不显示、编辑表单没这一项、导出元信息表里也没有「结果」行。`result` 字段本身仍在索引里，工具栏「按对错」分组和筛选下拉保留（万一以后混进做对的题要筛）。
- **导出**：详情页工具栏「导出」按钮，弹窗里选范围（当前筛选结果 / 本科目全部）、格式、内容。见下面「导出」。
- API：`GET /api/subjects`、`GET /api/entries`、`PUT /api/subject`（给错题本改名，`{"subject","name"}`，name 空 = 恢复默认）、`GET|PUT|DELETE /api/entry`、`GET /api/image?key=`（后端代理，凭证不出后端）、`POST /api/export`。
- 鉴权：`--auth-token`（或 `MISTAKE_AUTH_TOKEN`），非 loopback 监听没设 token 会拒绝启动。静态资源不鉴权，token 在页面输入后存 localStorage。
  - ⚠️ 前端发的是 **`X-Mistake-Token` 头，不是 `Authorization`**：线上前面还有一层 nginx Basic Auth，浏览器会自动给同源请求带 Basic 凭据，fetch 再设 `Authorization` 会把 Basic 覆盖掉，nginx 直接回 401（2026-10-13 部署时踩到：首页 200 但 /api 全 401）。
- ⚠️ **改了 `web/` 下任何文件都必须重新编译二进制**（embed 打进去的）。

### 服务器部署（154.8.216.179）—— 已上线

线上地址 **https://mistake.110105.xyz**（2026-10-13 上线）。凭据见 `~/.workbuddy/skills/mistake-book/DEPLOY.md`（本地，**不进 git**）。

跟 jrp 同一套：交叉编译 → 传二进制 → systemd → nginx 反代 → Basic Auth。

**重新部署**（改了 Go 代码或 `web/` 之后必须走一遍）：

```bash
cd ~/mistake-book   # Windows: C:\Users\efrainzhang\mistake-book
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOFLAGS=-mod=mod go build -trimpath -ldflags="-s -w" -o /tmp/mistake-book-linux .

KEY=~/.workbuddy/../WorkBuddy/2026-09-20-09-52-09/outputs/deploy/ssh/jrp_deploy
ssh -i $KEY -o BatchMode=yes -o StrictHostKeyChecking=no root@154.8.216.179 'systemctl stop mistake-book'
ssh -i $KEY -o BatchMode=yes -o StrictHostKeyChecking=no root@154.8.216.179 'cat > /usr/local/bin/mistake-book' < /tmp/mistake-book-linux
ssh -i $KEY -o BatchMode=yes -o StrictHostKeyChecking=no root@154.8.216.179 \
  'chmod +x /usr/local/bin/mistake-book; systemctl start mistake-book; sleep 2; systemctl is-active mistake-book'
```

验证不看服务是否活着（可能跑着旧二进制），用特征串 + 真请求：
```bash
ssh -i $KEY root@154.8.216.179 'strings /usr/local/bin/mistake-book | grep -c X-Mistake-Token'
curl -s https://mistake.110105.xyz/api/subjects -u teacher:<密码> -H "X-Mistake-Token: <token>"
```

**踩过的坑**：
1. `/etc/nginx/.mistake_htpasswd` 必须 `chown root:nginx` + `640` —— root:root 640 时 nginx worker 读不到，所有请求变 500（`open() failed (13: Permission denied)`）。
2. 传文件用 `ssh host 'cat > x' < file` 管道，scp 会被腾讯云扫码横幅破坏。
3. 换二进制必须 stop → cp → start，否则 `Text file busy`。
4. `nginx.conf` 是 jrp 整份接管的，改前先 `cp` 备份再 `nginx -t`（改坏会连带 jrp）。
5. 证书是 TrustAsia DV，90 天，2027-01-10 到期，到期前要重新申请。

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
5. **对错只落在索引的 `result`**：md 里不写「批改」行。红笔痕迹（勾/叉/圈/问号）是判断 `result` 的依据，判断完不落盘。
6. **老师写的作答和正确答案分开记**：作答归「我的作答」，标准答案归「答案/正解」，别混。

## Critical Rules

1. **凭证在 `~/.workbuddy/cos-credentials/`**（与 jrp / study-tracker 同一份）。绝不要把凭证写进任何 marketplace 管理的 skill 目录（skill 更新会整目录覆盖，2026-09-10 丢过一次）。二进制解析顺序：环境变量 `TENCENT_COS_*` → `.env` 明文 → `.env.enc` 解密（密钥 = SHA-256(hostname:username:凭证目录)，与 jrp 一致）；可用 `MISTAKE_BOOK_COS_SKILL_DIR` 改凭证目录。
2. **COS 读写一律走 objstore**，不自己签名、不调 cos_node.mjs、不写 python。
3. **索引是导出 PDF 的唯一选题来源**，每录一题必须 `index-add`，不能只传文件。
4. **一张照片 = 一个 md**，同照片补题追加，不新建文件。
5. **上传前先给老师看提取结果**，确认无误再落 COS。
6. 报告/临时文件放在工作区 `outputs/` 下，否则 present_files 不触发。

## 导出

网页上点「导出」即可（后端 `POST /api/export`），范围、格式、内容、每页题数都能选。

**格式**

| format | 产物 | 说明 |
|---|---|---|
| `html`（`pdf` 是别名） | `.html` | **练习卷排版**（老师 2026-10-13 拍板）：元数据表格 + 题干大块（下方虚线留白重做）+ 备注最下面；「我的作答/错因要点/答案正解/原图」勾了才出现在题干块下面的**参考区**。`per_page` 控制每页 1/2/3 道。KaTeX 的 CSS+JS+**20 个 woff2 字体全部 base64 内联**，断网也能看公式。**不自动弹打印**——页面右下角有「打印」按钮，点它 → 打印对话框里选「另存为 PDF」。不做真·PDF 生成（中文字体嵌 PDF 要几 MB，纯 Go 无依赖不划算） |
| `md` | `.md` | 纯文本，方便贴到别处 / 导入别的工具。原图放 **7 天有效的 COS 预签名链接**（不往 md 里塞 base64），per_page 对它无效 |

**内容**（`fields`，可多选）：`meta` 元信息 / `stem` 题干 / `answer` 我的作答 / `analysis` 错因要点 / `solution` 答案正解 / `note` 备注 / `image` 原图 / `other` md 里的其他字段。
网页默认只勾 `meta / stem / note`（重做练习卷的配法，作答和错因是答案，默认不带）。

**范围**：`ids` 传空 = 导出该科目全部（按日期排序）；传 id 数组 = 只导出这些、按给定顺序（前端「当前筛选结果」走这条）。

```bash
curl -X POST -H 'X-Mistake-Token: <token>' -H 'Content-Type: application/json' \
  -d '{"subject":"数学","format":"html","per_page":2,"fields":["meta","stem","note"],"ids":null}' \
  https://mistake.110105.xyz/api/export -o 练习卷.html
```

错因要点取值优先级：索引里的 `analysis`（老师手改过的）> md 里解析出的「错因/要点」。
md 字段别名容错：`错因/要点`＝`错因要点`＝`错因`＝`要点`；`答案`＝`正解`＝`正确答案`。

> 按固定版式批量出卷已经做了（上面的练习卷排版）。再往后要做「A4 几栏、
> 按知识点重排、题间不留白压缩成小抄」这类，等老师给具体版式再说。

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
