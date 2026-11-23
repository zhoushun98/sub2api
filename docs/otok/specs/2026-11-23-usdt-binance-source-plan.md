# USDT 币安数据源 实施计划

> 执行方式：由主会话按任务逐个实现（站长已授权直接开工），全部完成后发 tag `otok-v0.2.24.2`，生产上线等站长通知。

**目标**：USDT 到账改为以币安充值记录为准（含链下转账），链上监听保留为可切回的备用。

**架构**：在 `otokchain` 新增只读币安客户端，`OtokUSDTService` 按渠道配置 `depositSource` 选择发现付款的方式；复核方式按转账记录的 `source` 决定。转账表新增 `external_id` 列存放币安充值 id。

**技术栈**：Go（标准库 `crypto/hmac`，复用 `httpclient`、`shopspring/decimal`）、PostgreSQL 原生 SQL、Vue 3。

**设计文档**：`docs/otok/specs/2026-11-23-usdt-binance-source-design.md`

## 全局约束

- 不改 Ent schema；上游文件不新增钩子（新增代码都放在 otok 文件里）；不加 Go / npm 依赖。
- 迁移 `otok_0005_usdt_binance.sql` 必须幂等，发布后不能再改。
- `depositSource` 缺省为 `chain`；`binance` 模式下 `binanceApiKey`、`binanceApiSecret` 必填。
- 只有币安 `status ∈ {1, 6}` 才算到账；2、7 转待审核；0 等待；8 或 `travelRuleStatus=1` 视为受阻。
- 网络映射写死：`BSC→bep20`、`ETH→erc20`、`MATIC→polygon`、`TRX→trc20`。
- 环境变量：`OTOK_USDT_BINANCE_PROXY`、`OTOK_USDT_BINANCE_API_URL`（仅测试用）。
- 中英文 i18n key 一一对应；commit message 用 `feat(otok): …` 格式。

## 实施时重点检查

1. 每 15 秒重拉一次不能刷新 `updated_at`，否则会破坏 credit_failed 的退避和「消失 15 分钟后解绑」的判断。用例：重复写入同一条记录后，`updated_at` 不变。
2. 链下编号的各种输入写法（带或不带前缀、中文前缀、前后空格、全角空格）都能识别；乱填的字符串返回 `USDT_TX_INVALID`，不能触发对币安的查询。
3. 签名 query 里的参数必须按实际发送的顺序签名；URL 编码后的 `txId`（含空格）要和签名用的字符串一致。用例：带空格的 txId，签名与币安官方示例的算法一致。
4. 币安返回错误 JSON（`{"code":-2015,...}`，HTTP 401/403/451）时，错误信息里带上 code 和 msg，但不能带 query、签名和 key。
5. 已绑定的币安记录在切回 `chain` 后仍向币安复核（按 `source` 判断，不看开关）。

---

### Task 1：币安客户端（`internal/pkg/otokchain/binance.go`）

- `type BinanceDeposit struct { ID, Amount, Coin, Network, Address, TxID, SourceAddress string; Status, TransferType, TravelRuleStatus int; InsertTime, CompleteTime int64; ConfirmTimes string }`
- `NewBinanceClient(baseURL, apiKey, apiSecret string, hc *http.Client) (*BinanceClient, error)`，`baseURL` 为空时用 `https://api.binance.com`。
- `(*BinanceClient).Deposits(ctx, start, end time.Time) ([]BinanceDeposit, error)`：`coin=USDT&includeSource=true&limit=1000`，用 `offset` 翻页，最多 3 页。
- `(*BinanceClient).DepositsByTxID(ctx, txID string) ([]BinanceDeposit, error)`
- `BinanceNetwork(code string) (Network, bool)`、`ParseOffChainID(input string) (string, bool)`（返回纯数字编号）、`OffChainTxID(id string) string`（返回 `"Off-chain transfer " + id`）。
- `(d BinanceDeposit) Transfer(n Network, logIndex int64) (Transfer, error)`：把金额字符串按网络精度换算成 raw，`BlockTime=insertTime`、`From=SourceAddress`、`ToNorm` 为规范化后的地址；链上 `TxHash` 走 `NormalizeTxHash`，链下保留 txId 原文。
- 测试（`binance_test.go`，`unit` 标签）：签名对照官方示例向量；用 httptest 校验 query、请求头、错误 JSON 和翻页；网络映射；链下编号解析表；金额换算（`"7.52"`→752 分，`"9.935984"`→不是整分）。

### Task 2：渠道配置（`provider/otok_usdt.go`、`service/otok_usdt_register.go`）

- 常量 `OtokUSDTConfigDepositSource="depositSource"`、`OtokUSDTConfigBinanceAPIKey="binanceApiKey"`、`OtokUSDTConfigBinanceAPISecret="binanceApiSecret"`；值 `OtokUSDTSourceChain="chain"`、`OtokUSDTSourceBinance="binance"`。
- `OtokUSDTConfig` 增加 `DepositSource string`、`BinanceAPIKey string`、`BinanceAPISecret string`；解析时缺省为 chain，其他值报错，binance 模式缺 key 报错。`instanceConfig` 的兜底分支同样带上这三项。
- 两个 key 登记为敏感字段：`binanceapikey`、`binanceapisecret`。
- 测试：在现有表驱动用例里补 binance 的 4 种情况；扩展 `TestOtokUSDTSensitiveFieldsCoveredByAudit`。

### Task 3：迁移与存储（`migrations/otok_0005_usdt_binance.sql`、`otok_usdt_store.go`）

- 迁移内容见设计文档第 9 节；集成测试的迁移列表加上 0005。
- `otokUSDTTransfer` 增加 `ExternalID string`，同步修改列清单和扫描函数；新增 `otokSourceBinance = "binance"`。
- `otokUpsertBinanceTransfer(ctx, ex, t otokchain.Transfer, externalID, tokenContract string, decimals int32) (*otokUSDTTransfer, error)`：冲突时不改任何业务字段，也不动 `updated_at`。
- 集成测试：同一条记录重复写入后 `updated_at` 不变；`external_id` 唯一。

### Task 4：轮询、匹配与复核（`otok_usdt_binance.go`，`watcher` / `service` 做最小改动）

- `(*OtokUSDTService).binanceClient(ucfg) (*otokchain.BinanceClient, error)`：读取 `OTOK_USDT_BINANCE_PROXY` 和 `OTOK_USDT_BINANCE_API_URL`，按 key 和代理缓存，不跟随重定向。
- `scanAndMatch`：当前启用实例为 binance 模式时，改调 `scanBinance(ctx, ucfg, reservations)`。按配置的收款地址过滤，跳过 status 2 和 7，按 `external_id` 写入，同一 txId 下的 `log_index` 依次用 -1、-2…，游标行 `('binance','*')`，最后调 `matchTransfers`。
- `otokTransferAutoMatchable` 和 `otokBoundTransferMismatch`：`source=binance` 时不比较网络。
- `creditBoundTransfer`：`source=binance` 时改调 `verifyOnBinance`，返回值沿用 `otokChainCheck`。status 8 / travel rule 返回 error，用来写 `last_error`；status 0 视为未确认，不写错误。
- 集成测试（用 httptest 模拟币安）：链下转账能匹配并绑定；跨网络能匹配；status 0 保持 bound 且没有 last_error；status 8 写入 last_error，并计入 Health 的 CreditBlocked；status 2 解绑并转 review；切回 chain 后仍向币安复核。

### Task 5：用户提交与管理员补单（`otok_usdt_service.go`）

- `fetchTxTransfers` 按当前模式分支：binance 模式下先判断是不是链下编号，是就转成 txId，否则按哈希规范化，然后调 `DepositsByTxID`；查不到且输入是链下编号时，再在订单时间窗内的充值里按后缀兜底查找。chain 模式下输入链下编号返回 `USDT_OFFCHAIN_UNSUPPORTED`。
- `SubmitUserTx` / `AdminBindTx` 不再先调用 `NormalizeTxHash`，改为使用新函数 `otokNormalizeTxInput(mode, network, input) (txID string, offChain bool, err error)`。
- `OtokUSDTOrderDetail` 增加 `DepositSource string \`json:"deposit_source"\``。
- 集成测试：用户提交链下编号成功绑定；提交别人的编号被拒，转账状态不变；管理员可以把链下转账补到已取消的订单（只验证绑定 + amount_override，不走上游回调）。

### Task 6：前端

- `otok/payment.ts` 增加 3 个配置字段（`depositSource` 用 options 下拉）；i18n 中英文补 `field_depositSource*`、`field_binanceApiKey*`、`field_binanceApiSecret*`。
- `api/otok/usdt.ts` 增加 `deposit_source`；`OtokUsdtTransferPanel.vue` 在 binance 模式下改占位文案、加提示行；`OtokUsdtTransfersView.vue` 增加来源「币安」、链下记录不显示浏览器链接、游标 `binance` 显示为「币安 API」；`OtokUsdtOrderInfo.vue` 补单输入框的占位改为同时接受链下编号。
- 测试：扩展 `OtokUsdtTransferPanel.spec.ts`（binance 模式下显示提示）、`payment.spec.ts`（字段存在）；跑 i18n 完整性检查、`vue-tsc`、`vite build`。

### Task 7：balance-monitor（`balance-monitor/upstream.go`）

- 新上游类型 `binance`，字段 `api_key`、`api_secret`、`proxy`；调用 `POST /sapi/v1/asset/get-funding-asset`（`asset=USDT`），返回 `free+locked+freeze`；`threshold` 可设为 0。
- 测试：用 httptest 校验签名和余额解析；README 和 `config.example.json` 补示例。

### Task 8：文档、验证与发版

- 更新 `USDT_PAYMENT.md`（数据源、配置、排障）和上级 `CLAUDE.md`（定制功能表、运维要点、回滚说明，标为「已发版、未上线」）。
- 全量跑：`go test -tags=unit ./...`、集成测试 `-run Otok`、`golangci-lint`、前端三件套。
- 提交（`feat(otok): …`）→ `git push origin otok` → `git tag -a otok-v0.2.24.2 -m "otok 0.2.24.2：USDT 到账数据源支持币安 API（含链下转账）"` → 推 tag → 盯 CI 跑完。
