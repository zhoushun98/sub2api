# otok 二开说明

本仓库 `zhoushun98/sub2api` 是 [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api) 的 fork，在官方版本上增加 otok 定制功能。目前包含：

- **USDT 链上充值**：支持 TRC20、BSC（BEP20）、Polygon、Ethereum（ERC20）。到账数据源可以选链上节点或币安充值记录（能识别币安用户之间的链下转账），详见 [USDT_PAYMENT.md](USDT_PAYMENT.md)。
- **渠道状态卡片页**：V2 监控模式下，用户侧「渠道状态」（`/monitor`）改为按厂商分组的卡片页，每个分组一张卡，显示缓存率 / 可用率 / 首 Token 和近 N 个时间段的状态条。数据全部复用上游 V2 被动统计接口，后端无改动。代码在 `frontend/src/otok/monitorCards.ts`、`components/otok/monitor/`、`views/user/otok/`。
  - 「有没有流量」只看 `success_rate` / `error_rate`，不看 `request_count`（0.2.26-otok.2 起）：用户侧计数被脱敏为 0；被忽略类别的错误（如余额不足 403）不计入 `error_rate`，窗口内只有这类错误时两个比率都是 0，若再看 `request_count`，管理员视角会显示可用率 100%「健康」，而用户视角是「无数据」。
- **模型广场按平台展示**：上游的模型广场（`/model-plaza`）按分组分节，有平台 / 分组 / 倍率三行筛选，每个分组一张表，同时列实付价、官方价和折扣倍率，分组多了很乱。fork 换成按平台展示：只保留平台筛选和模型名搜索，只显示标准价（输入 / 输出 / 缓存写入 / 缓存读取，有上下文分档时每档一行），页面注明「实际扣费 = 标准价 × 所选分组倍率」。后端和接口没有改动，开关、登录要求、顶部价格说明仍在后台「系统设置 → 模型广场」配置。
  - 标准价取接口里的 `pricing`（计费实际用的渠道价，未乘倍率），没有时退回 `official_pricing`；不直接用官方价，是因为国产模型的渠道价按人民币数字填写，只有渠道价才满足「实付 = 标准价 × 倍率」。缓存写入价为 0（DeepSeek、GLM）显示「-」。
  - 同一平台同名模型在各分组的价格（展示价、分时倍率、思考等级倍率）完全相同时只列一行、不显示分组；不同时每个价格版本各一行，标出「分组：A、B」。有价格的版本存在时，没有价格的版本不显示。
  - 渠道定价配了分时倍率的模型（只对 token 计费生效，与后端 `calculateTokenCost` 一致），基础价那行标「其他时段」，下面按倍率各加一行时段价（同倍率的时段合并，如「工作日 09:00–12:00、14:00–18:00 ×2」，价格已乘倍率）。
  - 代码在 `frontend/src/otok/modelPlaza.ts`、`components/otok/modelPlaza/OtokModelPlazaContent.vue`。
- **联系我们**：侧边栏「联系我们」弹窗，显示微信客服二维码、QQ 交流群（二维码 + 群号）和邮箱。内容在后台「联系方式」页（`/admin/otok/contact`）配置，存在 settings 表的 `otok_contact` 一行（JSON，二维码为 base64），不进 public settings。后端就地构造在 `routes/otok.go`，不经 wire。代码在 `service/otok_contact.go`、`handler/otok_contact_handler.go`、`frontend/src/otok/contact.ts`、`components/otok/contact/`。
- **上游声明倍率适配 new-api**：上游的「上游声明倍率」探测只认 sub2api 上游（`GET /v1/sub2api/billing`）。返回 404/405 时，fork 改用账号 apikey 调 new-api 的 `GET /api/log/token`，取最近一条消费日志（`type=2`）里的 `group_ratio`，合成为 `sub2api.key_billing` 响应交回原流程；之后的快照、展示、调度和写回都复用上游代码，前端无改动。令牌还没有消费日志时，记为 failed，原因码 `newapi_no_usage_log`。代码在 `service/otok_upstream_billing_newapi.go`。
- **降智检测（自定义条目）**：用户侧边栏「降智检测」菜单（`/otok/quality`）。管理员在后台自己建条目，每条填名称、接口 URL、API key、模型（1～5 个）和提示词；后台按固定间隔让每个模型用同一份提示词各请求一次，用户按条目切换，对比不同时间的输出，判断渠道有没有降智。0.2.25-otok.2 起取代原来的「鹈鹕测试」（内部账号 + 渠道状态卡片上的按钮），旧方案的代码已删除。
  - 配置在后台「渠道管理 → 降智检测」（`/admin/otok/quality`），存在 settings 表的 `otok_quality` 一行（JSON）。全局项有总开关、间隔（5～1440 分钟，默认 60）、单次超时（60～900 秒，默认 480）、并发（1～16，默认 2）、每个条目保留的轮数（1～50，默认 10）；条目最多 50 条，数组顺序就是用户页的展示顺序。条目 id 由服务端分配、保存后不再变，结果表靠它关联。
  - 协议按 URL 路径自动识别：去掉结尾的 `/` 后以 `/responses`、`/chat/completions`、`/messages` 结尾，分别按 OpenAI Responses、Chat Completions、Anthropic Messages 发流式请求；其他路径（包括 `/v1/messages/count_tokens`）保存时被拒。每个条目还可以选思考强度（可「不发送」；messages 协议不适用）和填附加请求参数（JSON 对象，顶层浅合并进请求体，用来调输出上限、给 Claude 开 thinking 等）；`model`、`stream` 和提示词字段始终以条目配置为准，附加参数覆盖不了。
  - 测本站分组时 URL 填 `http://127.0.0.1:8080/v1/responses`（或 `/v1/chat/completions`、`/v1/messages`），不经 Cloudflare。URL 允许 `http://` 和内网 / 环回地址，不做 SSRF 拦截（只有管理员能配）；请求不走环境代理、不跟随重定向，不设定制的 `User-Agent`，也不发 `X-Client-Request-ID`（本站网关会把这两个头转给上游）。
  - key 用上游的 `SecretEncryptor`（`TOTP_ENCRYPTION_KEY`）加密后存库，后台回显只给打码值，保存时留空表示沿用。加密密钥不是显式配置的实例拒绝保存新 key（`OTOK_QUALITY_ENCRYPTION_KEY_REQUIRED`）；换了加密密钥后旧密文解不开，后台提示重填，调度时这些条目的结果记为 `key_error`。URL 和附加请求参数明文保存并原样记入审计日志，凭证只能填在 API key 一栏（审计日志会擦掉 `api_key`）。
  - 配置带 `version` 乐观锁：页面保存时带回读取时的版本，库里已被别处改过就返回 409 `OTOK_QUALITY_CONFIG_CONFLICT`，避免旧页面覆盖新配置。保存后，被删除条目的历史结果（连同还在排队的请求）一并删除；已存配置读不出来时按默认值（总开关关、没有条目）运行，后台页顶部提示「保存会覆盖并清空历史结果」。
  - 代价：经本站网关时，这些请求和真实用户一样进使用记录和渠道状态（V2）统计，按 key 所属用户的正常倍率扣费；流量小的分组首 Token、耗时会被拉高。余额用完后结果全部显示为失败，用户看得到。开了「仅限 Claude Code / Codex 客户端」的分组或账号测不了，会显示请求被拒。建议在一个专门的用户下给每个要测的分组建 key，余额留足，用户并发上限不低于降智检测的并发数。
  - 局限：提示词固定、定时发送，本身就能被上游识别出来，所以只能发现「无差别」的降智，防不了针对这类请求的作弊。
  - 结果存在 `otok_quality_rounds` / `otok_quality_runs`（迁移 `otok_0006`），每轮存一份提示词快照（只在后台「查看结果」里显示，用户端接口不返回提示词）；每个条目只保留最近 N 轮。临时失败（429、5xx、连接失败、流中断）重试 1 次；超时不重试，因为我们断开后网关仍会把上游跑完并计费。错误类型在落库时就归并成 `http_429` / `http_5xx` / `http_4xx` / `http_3xx` 等，精确状态码只存在 `http_status` 列；错误原文里出现的 key 替换成 `***`。
  - 可见范围：所有登录用户都能看到「总开关开且条目启用」的全部条目（名称、模型、输出、耗时和归并后的错误类型），看不到提示词、URL、key、附加参数、错误原文、HTTP 状态码和上游返回的模型名，这些只在后台「查看结果」里显示（0.2.25-otok.3 起提示词也对用户隐藏）。没有可见条目时侧边栏不显示菜单。后台「立即运行」对未启用的条目、总开关关闭时也能用，方便先试再开。
  - 预览（0.2.25-otok.3 起允许脚本）：模型常用 JavaScript 写动画，而嵌在页面里的 `srcdoc` 会继承站点 CSP（`script-src` 带 nonce），脚本一律被拦。所以能抽出 HTML / SVG 时，详情接口另给一个独立预览地址 `GET /api/v1/otok/quality/preview/:id?token=<过期时间>.<签名>`，前端用 `<iframe sandbox="allow-scripts" src=… referrerpolicy="no-referrer">` 加载。
    - 签名是 HMAC-SHA256（密钥从 JWT 密钥加用途前缀派生），只在用户端 / 管理端的详情接口里下发（用户端下发前已检查条目可见），有效期 2 小时；前端详情缓存 1 小时，过期后（包括关掉再打开预览时）重新拉取换新地址。iframe 发请求带不了 `Authorization` 头，所以这个地址不走登录鉴权，签名不对、过期、记录不存在或没有 HTML 一律 404（条目禁用后不再复查可见性，已下发的地址最长 2 小时内仍能打开；内容本来就是登录用户都能看的输出，条目删除后结果随之删除、立即 404）；按 IP 限流（复用面板公开接口的 `public_ip_rpm`，默认每分钟 300 次）。nginx 日志会把 `token=` 打码。
    - 响应头给出专属 CSP，覆盖站点 CSP：`default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; media-src data: blob:; connect-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'; sandbox allow-scripts`，并把 `X-Frame-Options` 改成 `SAMEORIGIN`、`Cache-Control` 带 `no-transform`（不让 Cloudflare 等中间层改写 HTML，代价是 Cloudflare 可能不再压缩这条响应；2026-11-27 核实 Rocket Loader 未开启）。输出没有 doctype（只有 `<html>`、片段、截断的文档）时在前面补 `<!DOCTYPE html>`，按标准模式渲染。脚本能跑，但发不出任何网络请求、加载不了外部资源（CDN 脚本也不行）；`sandbox` 不给 `allow-same-origin`，文档是不透明来源，读不到本站的 localStorage / cookie，直接打开这个地址也一样。
    - 剩余风险：脚本（或 `<meta refresh>`、链接）能让预览框自己跳转，CSP 的 `connect-src` 管不到导航，但浏览器会按父页面 CSP 的 `frame-src` 检查子框架的跳转，站点白名单以外的地址会被拦（未在各浏览器实测），即使跳走也仍在沙箱里、拿不到本站任何信息；模型写出死循环时可能卡住浏览器标签页。内容来自站长自己配置的上游，接受这两点。
    - 提示词不对用户返回，但后台「填入示例提示词」的内容写在前端代码里，谁都能看到；模型也可能在输出里复述题目。要保密就自己写提示词。
    - 所以提示词要要求只用内联 SVG / CSS / JavaScript、不引用 CDN。抽不出 HTML 就直接显示文本。后台的「填入示例提示词」就是原来的鹈鹕提示词（已改为允许内联脚本）。
    - 代码：`service/otok_quality_preview.go`（签名、CSP、补 doctype）、`handler/otok_quality_handler.go` 的 `Preview`、`server/routes/otok.go`。
  - 环境变量：`OTOK_QUALITY_DISABLED=1` 关闭本实例的调度。旧的 `OTOK_PELICAN_DISABLED`、`OTOK_PELICAN_BASE_URL` 不再读取。
  - 旧数据：旧方案的表 `otok_pelican_rounds` / `otok_pelican_runs`（迁移 `otok_0003`，已发布不能删）、settings 里的 `otok_pelican` / `otok_pelican_probe_user_id`、内部用户 `pelican-probe@otok.internal` 及其 `otok-pelican-*` key 都没有自动清理，留着是为了能回滚到 0.2.25-otok.1。确认不回滚后手工清理（先备份）：先在后台禁用这个用户和它的 key（专属倍率 0.0001，留着有风险；走后台才能即时生效），再删两条 settings、DROP 两张旧表。回滚到旧版时，旧总开关若还开着，约 1 分钟后会恢复旧的定时测试。
  - 代码在 `service/otok_quality_*.go`、`handler/otok_quality_handler.go`、`migrations/otok_0006_quality.sql`、`frontend/src/otok/quality.ts`、`api/otok/quality.ts`、`components/otok/quality/`、`views/user/otok/OtokQualityView.vue`、`views/admin/otok/OtokQualitySettingsView.vue`。设计稿见 [specs/2026-11-26-quality-check-design.md](specs/2026-11-26-quality-check-design.md)。
- **Claude 非流式保活**：otokapi.com 前面的 Cloudflare 在源站约 125 秒没有任何响应时会断开（客户端收到 524）。非流式请求要等上游生成完才有响应，长输出经常超时；断开后网关不记账、不扣用户费，但上游照样跑完并向我们扣费。
  - 做法：`/v1/messages` 的非流式请求等上游超过 45 秒后，先回 200 头，再每 45 秒写一个空白（JSON 允许前导空白），最终正文仍是一份完整 JSON。45 秒内完成的请求完全不受影响；流式请求见下一条。
  - 心跳字节不计入 `Size()` / `Written()`，handler 里「已写出内容则不再切换账号」等上游判断照常工作，心跳期间上游失败仍会切换账号。
  - 代价：心跳开始后状态码固定为 200，之后的错误以 200 + 错误 JSON 返回（与上游生图 JSON 保活的取舍相同）；同时按非流式带内错误标记给 ops，错误日志里的状态码、错误类型与关闭保活时一致。
  - 只覆盖 Anthropic 平台分组的 `/v1/messages`；OpenAI 的非流式请求也会被 125 秒断开，但网关对上游走流式，断开后上游即停止生成，不会多扣费。
  - 环境变量：`OTOK_CLAUDE_NONSTREAM_KEEPALIVE_SECONDS` 调整间隔（5～90 秒，默认 45），设为 `0` 关闭。
  - 代码在 `handler/otok_claude_nonstream_keepalive.go`，钩子在 `gateway_handler.go` 的 `Messages`。
- **Claude 流式保活（等上游响应头阶段）**：上游自带的流式心跳只在等并发槽时和上游开始回流之后生效；从发出上游请求到收到响应头之间网关不写任何字节，上游排队或卡住超过约 120 秒时 Cloudflare 直接给客户端回 524（nginx 记 499、`rt≈125`、`resp_bytes=0`）。2026-11-13 一天约 100 条，主要是 ZY-Claude满分，这类请求不记账、不扣用户费。
  - 做法：`/v1/messages` 的流式请求等上游响应头超过 45 秒后，先回 200 + `text/event-stream`，再每 45 秒写一个 Anthropic 标准的 `event: ping`。handler 或上游一开始写真实内容就停止心跳（之后由上游的 `gateway.stream_keepalive_interval` 接手），所有写入在同一把锁下串行，心跳不会插进事件中间。45 秒内拿到响应头的请求完全不受影响。
  - 心跳字节同样不计入 `Size()` / `Written()`，心跳期间上游失败仍会切换账号；等并发槽时 concurrency helper 写的 ping 照常放行、不停心跳。
  - 代价：心跳开始后状态码固定为 200，之后 handler 要返回的 4xx/5xx JSON 错误就地改写成 `event: error` 帧（`{"type":"error","error":{...}}`），并作为流内错误标记给 ops，错误日志里的状态码、错误类型、消息与关闭保活时一致。客户端拿到的是流内错误而不是 HTTP 5xx，SDK 不一定会自动重试。
  - 环境变量：`OTOK_CLAUDE_STREAM_KEEPALIVE_SECONDS` 调整间隔（5～90 秒，默认 45），设为 `0` 关闭。
  - 代码在 `handler/otok_claude_stream_keepalive.go`，与非流式保活共用 `gateway_handler.go` 的同一行钩子 `otokStartClaudeKeepalive`。
- **识别中转上游的加密上下文失效报错**：同一个 Codex 会话中途换到另一家上游（故障转移或调度换号）时，请求里带的 `reasoning.encrypted_content` 是原来那家生成的，新上游解不开。上游代码只在收到 `code=invalid_encrypted_content` 时才会剥掉密文、同号重试一次，并记下这个会话里失效的密文（之后的请求直接剥掉），而部分中转上游换了写法，于是 400 被原样透传，用户在 Codex 里看到任务做到一半就报错。
  - fork 把两种写法也当成同一类错误：`code=encrypted_context_invalid`（`could not validate encrypted continuation context`），以及 `code=invalid_request` 加笼统文案 `The request could not be processed. Please check the request parameters.`。
  - 后一种含义不明确，但只有请求里确实带了加密推理 / compaction 项时才会重试；判断错了的代价是多一次很快返回的 400，以及该会话之后不再带这批密文。
  - 只接了 HTTP 路径（线上 OpenAI 账号都不走 WS）。`Encrypted function output…`、`amsg_*` 条目、tool history 之类的密文报错不在现有剥离范围内，没有处理。
  - 代码在 `service/otok_encrypted_context.go`，钩子在 `openai_gateway_forward.go` 的 `Forward`。
- **渠道监控剔除已删除分组**：上游删除分组（软删除）时不会把它从渠道监控 V2 的「监控分组」勾选（`channel_monitor_v2_config.group_ids`）里移掉；设置面板只列现存分组，残留的 id 看不到也取消不了，而 matrix 查询不过滤已删除分组、管理员视角又不按可用分组收窄，于是已删除分组的卡片一直留在渠道状态页。fork 在读、写配置时都剔掉已删除的分组 id（停用的分组保留）；勾选的分组全被删光时保留原值，因为空列表在上游表示「全部分组」。代码在 `repository/otok_channel_monitor_v2.go`，钩子在 `channel_monitor_v2_repo.go` 的 `GetConfig`、`UpdateConfig`。
- **禁用后台「在线更新」**：定制版只能通过镜像升级，防止被官方二进制覆盖。
- **GHCR 自动构建镜像**：`.github/workflows/otok-image.yml`。

本文是二开规范，目标是**改动集中、冲突可控，能够长期跟进官方上游**。所有对上游文件的改动都登记在 [UPSTREAM_PATCHES.md](UPSTREAM_PATCHES.md)。

## 分支与远端

| 名称 | 说明 |
| --- | --- |
| `origin` | fork：`git@github.com:zhoushun98/sub2api.git` |
| `upstream` | 官方：`https://github.com/Wei-Shaw/sub2api.git` |
| `main` | 官方镜像，**不提交任何定制代码** |
| `otok` | 定制分支，镜像从这里构建 |

首次克隆后执行：

```bash
git remote add upstream https://github.com/Wei-Shaw/sub2api.git
git fetch upstream --tags
git config rerere.enabled true   # 记住冲突的解决方式，下次同步自动套用
```

## 命名空间约定

所有定制代码都带 `otok` 前缀或放在 `otok` 目录下，`git grep -n otok` 就能找全：

| 位置 | 约定 |
| --- | --- |
| 后端文件 | `otok_*.go`，独立的包放 `internal/pkg/otokchain/` |
| 数据库 | 表名 `otok_*`，迁移文件 `backend/migrations/otok_NNNN_*.sql` |
| 支付渠道 | provider key 为 `otok_usdt`；支付类型为 `otok_usdt_trc20` / `otok_usdt_bep20` / `otok_usdt_polygon` / `otok_usdt_erc20`。不要用 `usdt_trc20`，它已被上游易支付的自定义方式占用 |
| 前端 | `src/otok/`、`src/api/otok/`、`src/components/otok/`、`src/views/**/otok/` |
| i18n | `src/i18n/locales/{zh,en}/otok.ts`，由 `index.ts` 深合并进语言包；两个文件的 key 必须一致 |
| 接口 | 支付相关：用户端 `/api/v1/payment/otok/...`，管理端 `/api/v1/admin/payment/otok/...`；其他：用户端 `/api/v1/otok/...`，管理端 `/api/v1/admin/otok/...` |
| 设置项 | settings 表的 key 统一加 `otok_` 前缀（如 `otok_contact`），不往上游 public settings 里加字段 |
| 环境变量 | `OTOK_*` |
| 文档 | `docs/otok/`。上游的 `.gitignore` 忽略了整个 `docs/*`，新增文档要用 `git add -f`，不要改 `.gitignore` |

## 改动规则

1. **上游文件只放钩子**。
   - 每处改动 1～3 行，只调用 fork 文件里的函数，并在行尾标注 `// otok`；Vue 模板里用 `<!-- otok -->`。
   - 尽量只「新增行」，不改写上游原有的行，合并时冲突最少。
   - 新增或调整钩子后，同步更新 [UPSTREAM_PATCHES.md](UPSTREAM_PATCHES.md)。
2. **不改 Ent schema**。
   - Ent 新增一张表会改动数万行生成代码，同步上游时几乎必然冲突。
   - 新表用 SQL 迁移建表，读写用原生 SQL（`entClient` / `tx` 的 `ExecContext` / `QueryContext`）。
3. **迁移文件发布后不能再改**。
   - 迁移器会校验 checksum，变更请新建 `otok_0002_*.sql`。
   - 迁移必须幂等（`IF NOT EXISTS`）。
   - `otok_` 前缀的文件排在所有数字编号的上游迁移之后。
4. **不新增 Go / npm 依赖**，避免 `go.mod`、`go.sum`、`pnpm-lock.yaml` 产生冲突。
5. **不手改 `backend/cmd/server/VERSION`**。版本号由 CI 注入，格式为 `<上游版本>-otok.<N>`。
6. **commit message** 统一用 `feat(otok): …`、`fix(otok): …`、`docs(otok): …`，`git log --grep otok` 即可列出全部定制提交。
7. **`cmd/server/wire_gen.go` 用 wire 重新生成**：
   - 执行 `cd backend/cmd/server && ~/go/bin/wire`（`go generate` 走 `go run`，而 `go.sum` 不含 wire CLI 的依赖，会报 missing go.sum entry；不要为此改 `go.sum`）。
   - 不要用 `make generate`，它会连 Ent 一起重新生成。
8. **fork 上的 Actions**：
   - `release.yml`、`cla.yml` 已在 GitHub 上 disable（文件本身不改）。前者会被 `v*` tag 触发官方发布流程，还会把 VERSION 回写到 main。
   - `backend-ci.yml`、`security-scan.yml`、`otok-image.yml` 都只在 push `otok-v*` tag 或手动触发时运行，推送 `otok` 分支不触发任何 workflow。前两个是上游文件，触发条件的改动登记在 [UPSTREAM_PATCHES.md](UPSTREAM_PATCHES.md)。
   - 日常提交前在本地跑测试（见「同步上游」第 3 步的命令）。
   - 不要把上游的 tag 推到 origin。

## 同步上游

按官方发布的 tag 合并，**不要 rebase**：

```bash
export GIT_HTTP_LOW_SPEED_LIMIT=1000 GIT_HTTP_LOW_SPEED_TIME=30
git fetch upstream --tags
git checkout main && git merge --ff-only upstream/main && git push origin main   # 可选：更新镜像分支
git checkout otok
git merge v0.2.19            # 换成要同步的官方 tag（实际合并 tag 之后的 VERSION 同步提交，见下文）
```

上游的 `VERSION` 是在打 tag **之后**才由 `chore: sync VERSION to <版本> [skip ci]` 提交同步的，tag 本身的 `VERSION` 还是上一版。发版时 CI 要求 tag 里的上游版本等于 `VERSION`，所以实际合并的是 tag 之后这一条同步提交（`git log --oneline v<新版本>..upstream/main` 找到它，确认它和 tag 之间没有别的提交），不要手改 `VERSION`。

有冲突时：

1. 按 [UPSTREAM_PATCHES.md](UPSTREAM_PATCHES.md) 逐条核对：上游代码以官方为准，再把 otok 钩子补回去。
2. 重新生成 wire：`cd backend/cmd/server && ~/go/bin/wire`。
3. 全量验证：

```bash
cd backend
go test -tags=unit ./...
go test -tags=integration -run Otok ./internal/service/     # 需要本机有 Docker
golangci-lint run ./...
cd ../frontend
pnpm install --frozen-lockfile
pnpm run typecheck && pnpm run test:run && pnpm run build
```

4. 重点回归：
   - 上游有没有改动 `payment_order.go` 的下单流程。
   - `toPaid` 的状态判断与 5 分钟宽限期。
   - `payment.Provider` 接口。
   - `PaymentView.vue` 的付款面板。
   - 这些都是 USDT 功能依赖的上游行为。
5. 推送 `otok` 分支（不会触发 CI）。需要先出测试镜像时，在 Actions 页面对 `otok` 分支手动运行 `otok image`，得到 `ghcr.io/zhoushun98/sub2api:otok`。确认无误后再打正式 tag（见下节）。

## 发版

镜像由 `.github/workflows/otok-image.yml` 构建并推送到 GHCR：

| 触发方式 | 平台 | 镜像 tag |
| --- | --- | --- |
| push tag `otok-v<上游版本>.<N>` | amd64 + arm64 | `<上游版本>-otok.<N>`、`latest`、`sha-<sha7>` |
| 手动触发，选 `otok` 分支 | amd64 | `otok`、`<VERSION>-otok.dev.<sha7>`、`sha-<sha7>` |
| 手动触发，选 tag | 同 tag 构建 | 同 tag 构建 |

推送 `otok` 分支不会构建镜像。

正式发版：

```bash
git tag -a otok-v0.2.17.1 -m "otok 0.2.17.1：USDT 链上充值"
git push origin otok-v0.2.17.1
```

- tag 中的上游版本必须与 `backend/cmd/server/VERSION` 一致，否则 CI 直接报错。
- 同一个上游版本再次发版时，把 `N` 加 1。
- 同步到新的上游版本后，`N` 从 1 重新开始。

首次推送镜像后，需要在 GitHub → Packages → `sub2api` → Package settings 里把可见性改成 **Public**；或者在服务器上用只读 PAT（`read:packages`）执行一次 `docker login ghcr.io`。

## 部署与回滚

- **部署**：把 compose 里的 `image` 换成 `ghcr.io/zhoushun98/sub2api:<版本>`，然后执行 `docker compose pull && docker compose up -d --no-deps --force-recreate sub2api`。
- **首次升级前必须做全库备份**。迁移只新增 `otok_*` 表，不改上游表结构。
- **回滚**：把镜像换回 `weishaw/sub2api:<版本>` 即可。fork 新增的表留在库里，不影响官方版本运行。
- 定制版禁用了后台「在线更新」，点击后会提示通过镜像升级；后台仍会显示上游的新版本，可作为同步上游的提醒。
