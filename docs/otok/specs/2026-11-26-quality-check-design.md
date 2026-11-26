# 降智检测改为自定义条目

- 日期：2026-11-26
- 状态：已实现，待上线（计划版本 `0.2.25-otok.2`）
- 关联：`docs/otok/README.md`「降智检测」一段（运维要点与代码位置）

## 背景与目的

原来的降智检测（鹈鹕测试，0.2.17-otok.10 起）是全自动的：后台只能勾选 OpenAI 分组，系统自建内部用户 `pelican-probe@otok.internal`，每个分组自动开一把 0.0001 倍率的 key，固定走本站环回 `/v1/responses`，全站共用一个提示词；用户从渠道状态卡片上的按钮打开弹窗查看。

这套方案的问题：

- 只能测本站的 OpenAI 分组，Claude 分组、外部上游都测不了。
- 内部账号和极低倍率的 key 是一类长期存在的特殊数据，删错、禁用错都会让测试整体失败，key 泄露也有风险。
- 提示词全站只有一个，换题后新旧结果混在一起。

站长要求整个去掉，换成**自定义条目**：管理员在后台自己加条目，每条自己填名称、URL、key、提示词；用户侧边栏新增一个菜单查看结果。

## 目标

1. 一个条目 = 一个接口地址 + 一把 key + 1～5 个模型 + 一份提示词，按固定间隔各跑一次，保留最近 N 轮供对比。
2. 支持 OpenAI Responses、Chat Completions、Anthropic Messages 三种协议，按 URL 路径自动识别。
3. 既能测本站分组（填环回地址），也能直接测外部上游。
4. 用户能看到条目名称、模型和输出，看不到提示词、URL、key 和可能带出上游信息的错误细节（提示词 0.2.25-otok.3 起对用户隐藏）。

## 非目标

- 条目不关联本站分组，不做按分组的可见性控制：所有登录用户看到全部启用的条目。
- 不做自动评分或判定，只提供对比素材。
- 不对 URL 做 SSRF 拦截：只有管理员能配，测本站的主要用法就是 `http://127.0.0.1:8080/v1/...`。
- 不迁移旧方案的数据，也不自动清理旧表、旧 settings 和内部账号。

## 方案取舍

| 问题 | 做法 | 理由 |
| --- | --- | --- |
| 协议怎么定 | URL 填完整接口地址，按路径结尾识别 | 少一个容易填错的下拉框；`/v1/messages/count_tokens` 之类的非生成接口自然被拒 |
| 思考强度、输出上限等参数 | 思考强度用下拉（可选「不发送」，新条目默认 medium），其余用「附加请求参数」JSON 对象浅合并 | 下拉覆盖最常见的用法；JSON 框兜住输出上限、Claude thinking 等写法随模型变化的参数，不用每种都做表单 |
| key 怎么存 | 用上游现成的 `SecretEncryptor` 加密后存进 settings，回显只给打码值，留空 = 不改 | 库备份里不留明文；不新建表，不加依赖 |
| 旧页面覆盖新配置 | 配置带 `version`，版本不一致返回 409 | 旧方案「后台页开着时保存会用旧表单覆盖」的坑，在新方案里会变成误删条目和结果 |
| 改了提示词后的对比 | 每轮存提示词快照，只在后台「查看结果」里可展开查看 | 避免把不同题目的结果当成同一题对比 |
| 输出不是网页 | 能抽出 HTML / SVG 就沙箱预览，抽不出就显示文本 | 提示词自定义后输出形式不固定 |
| 用户侧信息范围 | 只给归并后的错误类型，不给 URL、key、附加参数、错误原文、HTTP 状态码、上游返回的模型名 | URL 可能直连上游，错误原文常带上游域名 |
| 旧方案 | 代码全部删除；旧表、旧 settings、内部账号这次不动 | 保留回滚到 0.2.25-otok.1 的退路，上线稳定后手工清理 |

## 设计要点

### 配置

存在 settings `otok_quality`（JSON）：`version`、`enabled`、`interval_minutes`（5～1440，默认 60）、`timeout_seconds`（60～900，默认 480）、`concurrency`（1～16，默认 2）、`keep_rounds`（1～50，默认 10）、服务端内部用的 `next_item_id`，以及最多 50 个条目。条目字段：服务端分配的稳定 `id`、名称（1～64 字）、启用、URL、key 密文、模型、思考强度、提示词（≤8000 字）、附加请求参数（≤4000 字节）。

- 提交 `id = 0` 表示新增，由服务端分配；提交已存配置里没有的 id 被拒。
- 保存后删掉不再保留的条目的结果。保留名单只含「保存前就有、这次仍保留」的 id，所以配置被重置后，新条目不会接到旧 id 残留的结果上。
- 存新 key 要求加密密钥是显式配置的，否则重启后密钥变化会让所有 key 失效。
- 已存配置读不出来时回退默认值（总开关关），后台提示保存会覆盖并清空历史结果。

### 请求

| 协议 | 路径结尾 | 请求体默认值 | 思考强度 | 鉴权头 |
| --- | --- | --- | --- | --- |
| responses | `/responses` | `{model, input, stream: true, store: false}` | `reasoning.effort` | `Authorization: Bearer` |
| chat | `/chat/completions` | `{model, messages, stream: true, stream_options: {include_usage: true}}` | `reasoning_effort` | `Authorization: Bearer` |
| messages | `/messages` | `{model, max_tokens: 16000, messages, stream: true}` | 不适用 | `x-api-key` + `anthropic-version` |

- 合并顺序：默认值 ← 思考强度 ← 附加请求参数（顶层浅合并）← 强制字段（`model`、`stream`、提示词字段，chat 的 `stream_options.include_usage`）。
- 不设定制的 `User-Agent`、不发 `X-Client-Request-ID`：本站网关会把这两个头转给上游，不应带出站点名和用途。
- 不走环境代理（环回请求不能被 `HTTP_PROXY` 转走）、不跟随重定向、响应体总量限 32MB。
- 429、5xx、连接失败、流中断重试 1 次；超时不重试，因为断开后网关仍会把上游跑完并计费。
- 错误类型在落库时归并（`http_429` / `http_5xx` / `http_4xx` / `http_3xx`、`unexpected_response`、`stream_interrupted`、`response_incomplete`、`key_error` 等），精确状态码只存在 `http_status` 列；错误原文里的 key 替换成 `***`。
- 流内错误帧三种写法（事件名 `error`、data 的 `type == "error"`、data 顶层有 `error`）对三种协议通用，本站 `/v1/messages` 三种都会出现。chat 收到 `finish_reason` 后继续读到 `[DONE]`，因为 usage 在之后单独一块；messages 的输入 token 在 `message_delta` 里大于 0 时覆盖，本站把 OpenAI 分组转成 Messages 流时只在那里给。

### 存储与调度

- 迁移 `otok_0006_quality.sql` 新建 `otok_quality_rounds`（含 `prompt` 快照）和 `otok_quality_runs`（按 `item_id` 关联，带 `protocol`），小表，不用预建。全文不能出现 `CONCURRENTLY` 字样（迁移器对全文匹配）。
- 调度沿用旧骨架：leader 锁 `otok:quality:scheduler:leader`、每分钟检查、全局并发队列、按条目保留最近 N 轮、停机时把未完成的标成 `shutdown`。依赖只剩 settings、DB、config、加密器和 leader 锁。
- 「立即运行」允许条目未启用、总开关关闭，方便先试再开。
- `OTOK_QUALITY_DISABLED=1` 关闭本实例调度。

### 接口与前端

- 用户端 `/api/v1/otok/quality`：条目列表（只含总开关开且启用的）、某条目的轮次、某次运行的详情；不可见条目一律 404。
- 管理端 `/api/v1/admin/otok/quality`：配置读写、状态、立即运行，以及不做可见性过滤、带错误原文等字段的轮次和运行详情。审计日志按键名擦掉 `api_key`。
- 用户侧边栏「渠道状态」之后加「降智检测」，`featureFlag` 在取值时自己拉一次条目列表并缓存，没有可见条目时不显示。
- 预览（0.2.25-otok.3 起）：详情接口给出带 HMAC 签名（2 小时有效）的独立预览地址，前端用 `<iframe sandbox="allow-scripts" src referrerpolicy="no-referrer">` 加载；响应头的专属 CSP 允许内联脚本和样式、禁止一切网络请求与外部资源，并带 `sandbox allow-scripts`（不透明来源，读不到登录信息）和 `frame-ancestors 'self'`。最初的 `sandbox="" srcdoc` 方案会继承站点 CSP，模型用 JavaScript 写的动画放不出来。输出没有 doctype 时响应前补 `<!DOCTYPE html>`（与原方案一样按标准模式渲染）。预览接口按 IP 限流，签名有效期内不复查条目是否仍启用。其余文本一律插值，不用 `v-html`。

## 已知限制

- 提示词固定、定时发送，上游能识别出这类请求，所以只能发现无差别的降智，防不了针对性作弊。
- 经本站网关的请求按 key 所属用户的正常倍率扣费，进使用记录和渠道状态统计，会拉高小流量分组的首 Token 和耗时；余额用完后结果全部显示失败，用户可见。
- 开了「仅限 Claude Code / Codex 客户端」的分组或账号测不了。
- 乐观锁只在进程内串行化「读版本 → 写入」，settings 没有 compare-and-set；单实例部署不受影响。
- 保存时清理结果在写入配置之后执行，期间调度器若恰好给新条目建了一轮，会被一并删掉（最多少一轮结果）。
- URL 和附加请求参数明文保存并进审计日志，凭证只能填在 API key 一栏。
