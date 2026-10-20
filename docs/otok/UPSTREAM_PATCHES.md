# 上游文件钩子台账

本表列出 otok 对**上游文件**的全部改动。fork 自有文件（`otok_*`、`otok/` 目录）不在此列。

同步上游起冲突时，先以官方代码为准解决冲突，再按本表逐条把钩子补回去。

- 用 `git grep -n "otok" -- ':!docs' ':!*otok*'` 可以快速核对钩子是否都在。
- 基线：v0.2.17（`b0a02a330`）。

## 后端

| 文件 | 位置 | 改动 | 目的 |
| --- | --- | --- | --- |
| `backend/internal/payment/provider/factory.go` | `CreateProvider` 的 switch | `case payment.TypeOtokUSDT: return NewOtokUSDT(...)` | 注册 USDT provider |
| `backend/internal/service/payment_order.go` | `CreateOrder`，`createOrderInTx` 调用之后 | `payAmountStr, payAmount = otokCreateOrderPayAmount(...)` | 让返回给前端的应付金额、审计日志使用 USDT 唯一金额 |
| 同上 | `createOrderInTx`，`b.Save(ctx)` 之后、设置 `recharge_code` 之前 | `if order, err = s.otokPrepareUSDTOrderInTx(...); err != nil { return nil, err }` | 在同一事务内完成 USDT 报价、唯一金额预留，并改写 `pay_amount` / `expires_at` / `provider_snapshot` |
| 同上 | `selectCreateOrderInstance` 的 `SelectInstance` 调用 | 第 2 个参数 `""` 改为 `otokForcedProviderKey(req.PaymentType)` | USDT 订单只能选中 `otok_usdt` 实例。上游把 `supported_types` 为空的实例视为支持所有类型，不加限制会把 USDT 订单分给支付宝等实例 |
| `backend/internal/service/payment_config_providers.go` | `CreateProviderInstance`，易支付校验之后 | `s.otokValidateProviderInstance(ctx, 0, ...)` | 校验 USDT 实例：类型必须是 otok 类型；勾选的网络必须填地址；同时只能启用一个。非 otok 实例不能声明 otok 类型 |
| 同上 | `UpdateProviderInstance`，易支付校验之后 | `s.otokValidateProviderInstance(ctx, id, ...)` | 同上 |
| `backend/internal/handler/handler.go` | `Handlers` 结构体末尾 | `Otok *OtokHandlers` | 挂载 fork handler |
| `backend/internal/handler/wire.go` | `ProvideSystemHandler` | `admin.NewSystemHandler(newOtokUpdateGuard(updateService), lockService)` | 禁用在线更新和回滚 |
| 同上 | `ProvideHandlers` 参数末尾和返回值 | 新增 `otokHandlers *OtokHandlers` 参数与 `Otok: otokHandlers` 字段 | wire 注入 |
| `backend/internal/server/router.go` | `registerRoutes`，`RegisterPaymentRoutes` 之后 | `routes.RegisterOtokRoutes(...)` | 注册 fork 接口（USDT、联系我们） |
| `backend/cmd/server/wire.go` | `wire.Build` | 加入 `service.OtokProviderSet`、`handler.OtokProviderSet` | wire 注入 |
| 同上 | `provideCleanup` 参数末尾 | `otokUSDT *service.OtokUSDTService` | 优雅停机 |
| 同上 | `provideCleanup` 的并行清理步骤 | `{"OtokUSDTService", ...Stop()}` | 优雅停机 |
| `backend/cmd/server/wire_gen_test.go` | `provideCleanup` 的按位置调用 | 末尾补一个 `nil, // otokUSDT` | 适配新增参数 |
| `backend/cmd/server/wire_gen.go` | 生成文件 | `cd backend && go generate ./cmd/server` 重新生成 | 不要手改 |

经核验以下上游文件**无需修改**：`payment/types.go`、`load_balancer.go`、`payment_fulfillment.go`、`payment_order_lifecycle.go`、`payment_config_limits.go`、`payment_config_service.go`、`update_service.go`。fork 通过 `init()`（`otok_usdt_register.go`）向上游的以下几张表追加 `otok_usdt` 条目，它们是 map，上游改名或改结构时需要跟着调整：

- `validProviderKeys`
- `providerSensitiveConfigFields`
- `providerPendingOrderProtectedConfigFields`

## CI

| 文件 | 位置 | 改动 | 目的 |
| --- | --- | --- | --- |
| `.github/workflows/backend-ci.yml` | `on:` | `push` + `pull_request` 改为 `push: tags: ['otok-v*']` + `workflow_dispatch` | 只在打 tag 或手动时跑，推送 `otok` 分支不触发 |
| `.github/workflows/security-scan.yml` | `on:` | `push` + `pull_request` + 每周 `schedule` 改为 `push: tags: ['otok-v*']` + `workflow_dispatch` | 同上 |

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
| `frontend/src/router/index.ts` | import 与 404 路由之前 | `...otokRoutes` | USDT 到账记录页、联系方式配置页 |
| `frontend/src/components/layout/AppSidebar.vue` | 管理端「订单管理」子菜单末尾 | `{ path: '/admin/orders/usdt-transfers', ... }` | 菜单入口 |
| 同上 | import 区末尾 | `import { OtokContactMenuItem, OtokContactIcon } from '@/otok/sidebar'` | — |
| 同上 | 管理员「我的账户」区、普通用户区的 `router-link` 循环之后（各 1 行） | `<OtokContactMenuItem />` | 「联系我们」入口，未配置时不渲染 |
| 同上 | `adminNavItems`，「公告」之后 | `{ path: '/admin/otok/contact', ... }` | 后台「联系方式」配置页入口。不要挪到 security-audit 与 redeem 之间（`integrationSurface.spec.ts` 对那一段做源码切片断言） |
| `frontend/src/i18n/locales/zh/index.ts`、`en/index.ts` | import 与 `export default` | `export default otokMergeLocale({ ...上游原样... }, otok)`（**改写了 2 行**） | 深合并 fork 文案。上游是浅展开，直接 `...otok` 会整块覆盖 `payment` 等对象 |
| `frontend/src/views/user/ChannelStatusView.vue` | V2 分支与 import | `<ChannelStatusV2View v-else />` 换成 `<OtokChannelStatusCardsView v-else />`，import 同步替换（**改写了上游 2 行**） | V2 模式下用户侧「渠道状态」改用 otok 卡片页。上游 `ChannelStatusV2View.vue` 本身未改动，还原这 2 行即可恢复 |
| `frontend/src/views/user/__tests__/ChannelStatusView.mode.spec.ts` | V2 视图的 mock | mock 目标改为 `../otok/OtokChannelStatusCardsView.vue` | 适配上面的钩子 |

说明：上游的 `components/admin/payment/AdminOrderDetail.vue` 当前没有被任何页面使用（后台订单详情是 `AdminOrdersView.vue` 内联的弹窗），所以没有改它。上游以后如果改用这个组件，需要把 `OtokUsdtOrderInfo` 移过去。

## CI

| 文件 | 说明 |
| --- | --- |
| `.github/workflows/otok-image.yml` | fork 新增文件，构建 GHCR 镜像 |
| `release.yml`、`cla.yml` | **未修改文件**，只在 GitHub 上执行 `gh workflow disable` |
