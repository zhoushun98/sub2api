# 上游文件钩子台账

本表列出 otok 对**上游文件**的全部改动。fork 自有文件（`otok_*`、`otok/` 目录）不在此列。

同步上游起冲突时，先以官方代码为准解决冲突，再按本表逐条把钩子补回去。

- 用 `git grep -n "otok" -- ':!docs' ':!*otok*'` 可以快速核对钩子是否都在。
- 基线：v0.2.25（`0f6671457`，即 tag 之后的 VERSION 同步提交）。

## 后端

| 文件 | 位置 | 改动 | 目的 |
| --- | --- | --- | --- |
| `backend/internal/payment/provider/factory.go` | `CreateProvider` 的 switch | `case payment.TypeOtokUSDT: return NewOtokUSDT(...)` | 注册 USDT provider |
| `backend/internal/service/payment_order.go` | `CreateOrder`，`createOrderInTx` 调用之后 | `payAmountStr, payAmount = otokCreateOrderPayAmount(...)` | 让返回给前端的应付金额、审计日志使用 USDT 唯一金额 |
| 同上 | `createOrderInTx`，`b.Save(ctx)` 之后、设置 `recharge_code` 之前 | `if order, err = s.otokPrepareUSDTOrderInTx(...); err != nil { return nil, err }` | 在同一事务内完成 USDT 报价、唯一金额预留，并改写 `pay_amount` / `expires_at` / `provider_snapshot` |
| 同上 | `selectCreateOrderInstance` 的 `SelectInstance` 调用 | 第 2 个参数 `""` 改为 `otokForcedProviderKey(req.PaymentType)` | USDT 订单只能选中 `otok_usdt` 实例。上游把 `supported_types` 为空的实例视为支持所有类型，不加限制会把 USDT 订单分给支付宝等实例 |
| `backend/internal/service/payment_config_providers.go` | `CreateProviderInstance`，易支付校验之后 | `s.otokValidateProviderInstance(ctx, 0, ...)` | 校验 USDT 实例：类型必须是 otok 类型；勾选的网络必须填地址；同时只能启用一个。非 otok 实例不能声明 otok 类型 |
| 同上 | `UpdateProviderInstance`，易支付校验之后 | `s.otokValidateProviderInstance(ctx, id, ...)` | 同上 |
| `backend/internal/service/upstream_billing_probe.go` | `fetchBilling`，`response_too_large` 判断之后、`404/405 → unsupported` 判断之前 | 3 行：`if otok := s.otokNewAPIBillingFallback(...); otok.handled { return otok.reply, otok.err }` | 上游不是 sub2api 时改走 new-api 的 `/api/log/token`：识别成功返回合成的 `sub2api.key_billing` 响应（状态 200），交回上游解析流程；失败返回原因码（如 `newapi_no_usage_log`、`http_error`），由上游 `probeLoadedAccount` 落库；不是 new-api 时原样落到 unsupported。v0.2.25 上游把探测拆成 `fetchBilling`（取数）+ `probeLoadedAccount`（落库），钩子随之从后者移到前者。账号编辑页「同步上游分组」的预览（`PreviewGroup`）也走 `fetchBilling`，对 new-api 账号会多请求一次 `/api/log/token`，成功时返回空分组信息（与没有钩子时一致），失败时提示读取失败 |
| `backend/internal/handler/handler.go` | `Handlers` 结构体末尾 | `Otok *OtokHandlers` | 挂载 fork handler |
| `backend/internal/handler/wire.go` | `ProvideSystemHandler` | `admin.NewSystemHandler(newOtokUpdateGuard(updateService), lockService)` | 禁用在线更新和回滚 |
| 同上 | `ProvideHandlers` 参数末尾和返回值 | 新增 `otokHandlers *OtokHandlers` 参数与 `Otok: otokHandlers` 字段 | wire 注入 |
| `backend/internal/handler/gateway_handler.go` | `Messages`，Anthropic 分支 `for {` 主循环之前 | `defer otokStartClaudeKeepalive(c, reqStream)()` | Claude 请求等上游超过 45 秒后先回 200 并定期写心跳，防止 Cloudflare 约 120 秒断开：非流式写空白（断开后上游照扣费），流式在拿到上游响应头之前写 SSE `event: ping`（否则客户端收到 524）。心跳不计入 `Size()` / `Written()`，失败切换等上游判断不变；实现在 `handler/otok_claude_nonstream_keepalive.go`、`handler/otok_claude_stream_keepalive.go` |
| `backend/internal/service/openai_gateway_forward.go` | `Forward` 的 HTTP 错误处理，`invalidEncryptedContentError :=` 的 `\|\|` 条件里 | 加一行 `otokIsInvalidEncryptedContextError(upstreamCode, upstreamMsg) \|\|` | 中转上游用 `encrypted_context_invalid` 或笼统的 `invalid_request` 报告加密上下文失效时，同样走上游的「剥掉密文、同号重试一次并记入失效列表」；实现在 `service/otok_encrypted_context.go` |
| `backend/internal/repository/channel_monitor_v2_repo.go` | `GetConfig` 扫描完成之后、`UpdateConfig` 序列化 `platforms` 之后 | 各加一行 `cfg.GroupIDs = otokLiveChannelMonitorV2GroupIDs(ctx, r.db, cfg.GroupIDs)` | 读写渠道监控 V2 配置时剔除已删除分组：上游删分组不清理 `group_ids`，设置面板又只列现存分组，残留的 id 取消不掉，已删除分组的卡片会一直留在渠道状态页（管理员视角）。勾选的分组全被删光时保留原值（空列表 = 全部分组）；实现在 `repository/otok_channel_monitor_v2.go` |
| `backend/internal/server/router.go` | `registerRoutes`，`RegisterPaymentRoutes` 之后 | `routes.RegisterOtokRoutes(...)` | 注册 fork 接口（USDT、联系我们、降智检测） |
| `backend/cmd/server/wire.go` | `wire.Build` | 加入 `service.OtokProviderSet`、`handler.OtokProviderSet` | wire 注入 |
| 同上 | `provideCleanup` 参数末尾 | `otokUSDT *service.OtokUSDTService` | 优雅停机 |
| 同上 | `provideCleanup` 参数末尾 | `otokQuality *service.OtokQualityService` | 降智检测调度优雅停机 |
| 同上 | `provideCleanup` 的并行清理步骤 | `{"OtokUSDTService", ...Stop()}`、`{"OtokQualityService", ...Stop()}` | 优雅停机 |
| `backend/cmd/server/wire_gen_test.go` | `provideCleanup` 的按位置调用 | 末尾补 `nil, // otokUSDT`、`nil, // otokQuality` | 适配新增参数 |
| `backend/cmd/server/wire_gen.go` | 生成文件 | `cd backend/cmd/server && ~/go/bin/wire` 重新生成 | 不要手改 |

经核验以下上游文件**无需修改**：`payment/types.go`、`load_balancer.go`、`payment_fulfillment.go`、`payment_order_lifecycle.go`、`payment_config_limits.go`、`payment_config_service.go`、`update_service.go`。fork 通过 `init()`（`otok_usdt_register.go`）向上游的以下几张表追加 `otok_usdt` 条目，它们是 map，上游改名或改结构时需要跟着调整：

- `validProviderKeys`
- `providerSensitiveConfigFields`
- `providerPendingOrderProtectedConfigFields`

## CI

| 文件 | 位置 | 改动 | 目的 |
| --- | --- | --- | --- |
| `.github/workflows/backend-ci.yml` | `on:` | `push` + `pull_request` 改为 `push: tags: ['otok-v*']` + `workflow_dispatch` | 只在打 tag 或手动时跑，推送 `otok` 分支不触发 |
| `.github/workflows/security-scan.yml` | `on:` | `push` + `pull_request` + 每周 `schedule` 改为 `push: tags: ['otok-v*']` + `workflow_dispatch` | 同上 |
| `.github/workflows/backend-ci.yml` | `jobs.shell` | 加一行 `if: github.repository != 'zhoushun98/sub2api'` | fork 里跳过 `shell` job。它只测上游的 Apple container 部署脚本（`deploy/apple-container.sh`，2026-07 上游新增）和 compose 部署脚本，fork 用不到；跑在 `macos-15` 上，常因分不到运行机排队 15 分钟后被取消。2026-11-23 站长决定以后都不跑 |

## 前端

| 文件 | 位置 | 改动 | 目的 |
| --- | --- | --- | --- |
| `frontend/src/components/payment/providerConfig.ts` | 文件头 import | `import { OTOK_PROVIDER_CONFIG_FIELDS, OTOK_PROVIDER_SUPPORTED_TYPES } from '@/otok/payment'` | — |
| 同上 | `PROVIDER_SUPPORTED_TYPES` 末尾 | `...OTOK_PROVIDER_SUPPORTED_TYPES` | 后台服务商对话框可选 USDT 网络 |
| 同上 | `PROVIDER_CONFIG_FIELDS` 末尾 | `...OTOK_PROVIDER_CONFIG_FIELDS` | USDT 配置字段 |
| `frontend/src/components/payment/PaymentMethodSelector.vue` | import 与 `METHOD_ICONS` 末尾 | `...OTOK_METHOD_ICONS` | 各网络显示所在链的图标（TRON / BNB / POL / ETH），文件在 `src/assets/icons/otok/` |
| `frontend/src/components/payment/ProviderCard.vue` | `PROVIDER_KEY_LABELS` 末尾 | `otok_usdt: 'admin.settings.payment.providerOtokUsdt'` | 服务商卡片名称 |
| `frontend/src/views/admin/SettingsView.vue` | `allPaymentTypes` 末尾 | `{ value: "otok_usdt", ... }` | 「启用的服务商」开关 |
| 同上 | `providerKeyOptions` 末尾 | `{ value: "otok_usdt", ... }` | 新建服务商时可选 |
| `frontend/src/views/user/PaymentView.vue` | `paymentPhase === 'paying'` 模板 | 新增 `<OtokUsdtTransferPanel v-if=...>`，并给原 `<PaymentStatusPanel` 加一行 `v-else` | USDT 订单使用专用付款面板。上游面板倒计时一到就判定过期，金额也按 `$` 显示，不适合链上宽限期 |
| 同上 | 充值汇总卡片之后、提交按钮之前 | `<OtokUsdtQuote v-if=... :amount="totalAmount" />` | 显示预估 USDT |
| 同上 | 订阅手续费卡片之后、提交按钮之前 | `<OtokUsdtQuote v-if=... :amount="subTotalAmount" />` | 同上 |
| 同上 | import | 3 行 otok import | — |
| 同上 | `const feeRate = computed(...)` | 选中 USDT 方式时返回 0（**改写了上游 1 行**） | USDT 渠道单独计费，不显示全局手续费 |
| `frontend/src/views/admin/orders/AdminOrdersView.vue` | 订单详情弹窗「操作日志」之前 | `<OtokUsdtOrderInfo v-if=... />` | 链上信息与手动补单 |
| 同上 | import | 2 行 otok import | — |
| 同上 | `paymentTypeFilterOptions` 末尾 | `...OTOK_USDT_PAYMENT_TYPES.map(...)` | 按 USDT 网络筛选订单 |
| `frontend/src/router/index.ts` | import 与 404 路由之前 | `...otokRoutes` | USDT 到账记录页、联系方式配置页、降智检测配置页（`/admin/otok/quality`）、用户侧降智检测页（`/otok/quality`） |
| `frontend/src/components/layout/AppSidebar.vue` | 管理端「订单管理」子菜单末尾 | `{ path: '/admin/orders/usdt-transfers', ... }` | 菜单入口 |
| 同上 | import 区末尾 | `import { OtokContactMenuItem, OtokContactIcon, OtokQualityIcon, otokQualityMenuFlag } from '@/otok/sidebar'` | — |
| 同上 | 管理员「我的账户」区、普通用户区的 `router-link` 循环之后（各 1 行） | `<OtokContactMenuItem />` | 「联系我们」入口，未配置时不渲染 |
| 同上 | `adminNavItems`，「公告」之后 | `{ path: '/admin/otok/contact', ... }` | 后台「联系方式」配置页入口。不要挪到 security-audit 与 redeem 之间（`integrationSurface.spec.ts` 对那一段做源码切片断言） |
| 同上 | `buildSelfNavItems`，「渠道状态」（`/monitor`）之后 | `{ path: '/otok/quality', ..., icon: OtokQualityIcon, featureFlag: otokQualityMenuFlag }` | 用户侧「降智检测」入口，普通用户菜单和管理员「我的账户」区都会出现。`otokQualityMenuFlag` 取值时自己触发一次 `/api/v1/otok/quality/items` 请求并缓存结果，没有可见条目（或还没加载完）时返回 `false`，菜单不显示 |
| 同上 | `adminNavItems`，「渠道管理」children 里「渠道监控」之后 | `{ path: '/admin/otok/quality', ..., icon: OtokQualityIcon }` | 后台「降智检测」配置页入口。不挂 `flagChannelMonitor`：新方案与渠道监控无关 |
| `frontend/src/i18n/locales/zh/index.ts`、`en/index.ts` | import 与 `export default` | `export default otokMergeLocale({ ...上游原样... }, otok)`（**改写了 2 行**） | 深合并 fork 文案。上游是浅展开，直接 `...otok` 会整块覆盖 `payment` 等对象 |
| `frontend/src/views/user/ChannelStatusView.vue` | V2 分支与 import | `<ChannelStatusV2View v-else />` 换成 `<OtokChannelStatusCardsView v-else />`，import 同步替换（**改写了上游 2 行**） | V2 模式下用户侧「渠道状态」改用 otok 卡片页。上游 `ChannelStatusV2View.vue` 本身未改动，还原这 2 行即可恢复 |
| `frontend/src/views/ModelPlazaView.vue` | import | `ModelPlazaContent` 的 import 改指向 `@/components/otok/modelPlaza/OtokModelPlazaContent.vue`（**改写了上游 1 行**） | 模型广场改为按平台展示（props 与上游组件相同，模板不用改）。上游 `components/modelPlaza/` 本身未改动，还原这 1 行即可恢复 |
| `frontend/src/views/user/__tests__/ChannelStatusView.mode.spec.ts` | V2 视图的 mock | mock 目标改为 `../otok/OtokChannelStatusCardsView.vue` | 适配上面的钩子 |
| `frontend/src/views/admin/__tests__/AccountsView.sparkShadow.spec.ts` | `仅将具有安全 base_url 的 API Key 账号名称链接到站点主页` 用例 | 提示框 class 的断言 `arrayContaining(['self-start'])` 改为 `arrayContaining(['-ml-1'])`（**改写了上游 1 行**，临时） | 上游 v0.2.25 的 `a95c9e263` 把 `AccountsView.vue` 账号名提示框的 `self-start` 去掉了，但没更新这条断言，上游自己的全量 vitest 也挂；`otok image` 构建跑全量 `test:run`，不改就发不了版。改法与上游待合并的 PR #9061 完全一致，上游合并后同步时直接取上游版本，并删掉本行 |

说明：上游的 `components/admin/payment/AdminOrderDetail.vue` 当前没有被任何页面使用（后台订单详情是 `AdminOrdersView.vue` 内联的弹窗），所以没有改它。上游以后如果改用这个组件，需要把 `OtokUsdtOrderInfo` 移过去。

## CI

| 文件 | 说明 |
| --- | --- |
| `.github/workflows/otok-image.yml` | fork 新增文件，构建 GHCR 镜像 |
| `release.yml`、`cla.yml` | **未修改文件**，只在 GitHub 上执行 `gh workflow disable` |
