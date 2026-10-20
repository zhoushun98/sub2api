# otok 二开说明

本仓库 `zhoushun98/sub2api` 是 [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api) 的 fork，在官方版本上增加 otok 定制功能。目前包含：

- **USDT 链上充值**：支持 TRC20、BSC（BEP20）、Polygon、Ethereum（ERC20），详见 [USDT_PAYMENT.md](USDT_PAYMENT.md)。
- **渠道状态卡片页**：V2 监控模式下，用户侧「渠道状态」（`/monitor`）改为按厂商分组的卡片页，每个分组一张卡，显示缓存率 / 可用率 / 首 Token 和近 N 个时间段的状态条。数据全部复用上游 V2 被动统计接口，后端无改动。代码在 `frontend/src/otok/monitorCards.ts`、`components/otok/monitor/`、`views/user/otok/`。
- **联系我们**：侧边栏「联系我们」弹窗，显示微信客服二维码、QQ 交流群（二维码 + 群号）和邮箱。内容在后台「联系方式」页（`/admin/otok/contact`）配置，存在 settings 表的 `otok_contact` 一行（JSON，二维码为 base64），不进 public settings。后端就地构造在 `routes/otok.go`，不经 wire。代码在 `service/otok_contact.go`、`handler/otok_contact_handler.go`、`frontend/src/otok/contact.ts`、`components/otok/contact/`。
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
   - 执行 `cd backend && go generate ./cmd/server`。
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
git merge v0.2.18            # 换成要同步的官方 tag
```

有冲突时：

1. 按 [UPSTREAM_PATCHES.md](UPSTREAM_PATCHES.md) 逐条核对：上游代码以官方为准，再把 otok 钩子补回去。
2. 重新生成 wire：`cd backend && go generate ./cmd/server`。
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
