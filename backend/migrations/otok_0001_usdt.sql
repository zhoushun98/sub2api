-- otok: USDT 链上充值（fork 定制，见 docs/otok/USDT_PAYMENT.md）。
-- 文件名以 otok_ 开头，按字典序排在所有上游数字迁移之后，且永远不会与上游重名。
-- 已发布后不可修改；后续变更新建 otok_0002_*.sql。

-- 每笔 USDT 订单的唯一金额预留。active 期间同一收款地址的同一金额只属于一笔订单。
CREATE TABLE IF NOT EXISTS otok_usdt_orders (
    order_id            BIGINT PRIMARY KEY REFERENCES payment_orders(id) ON DELETE CASCADE,
    out_trade_no        VARCHAR(64)   NOT NULL,
    instance_id         VARCHAR(64)   NOT NULL DEFAULT '',
    network             VARCHAR(16)   NOT NULL,
    token_contract      VARCHAR(64)   NOT NULL,
    decimals            INTEGER       NOT NULL,
    to_address          VARCHAR(64)   NOT NULL,
    -- 匹配键：EVM 为小写地址，TRON 保持 base58 原样（区分大小写）。
    to_address_norm     VARCHAR(64)   NOT NULL,
    base_amount_cents   BIGINT        NOT NULL,
    amount_cents        BIGINT        NOT NULL,
    station_amount      NUMERIC(20,2) NOT NULL,
    exchange_rate       NUMERIC(20,8) NOT NULL,
    fee_rate            NUMERIC(10,4) NOT NULL DEFAULT 0,
    -- pending: 等待转账；bound: 已匹配链上转账；credited: 已入账；released: 预留已释放
    status              VARCHAR(16)   NOT NULL DEFAULT 'pending',
    active              BOOLEAN       NOT NULL DEFAULT TRUE,
    expires_at          TIMESTAMPTZ   NOT NULL,
    reserved_until      TIMESTAMPTZ   NOT NULL,
    matched_transfer_id BIGINT,
    created_at          TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS otok_usdt_orders_active_amount_uniq
    ON otok_usdt_orders (network, to_address_norm, amount_cents) WHERE active;
CREATE INDEX IF NOT EXISTS otok_usdt_orders_active_idx
    ON otok_usdt_orders (network, to_address_norm, reserved_until) WHERE active;
CREATE INDEX IF NOT EXISTS otok_usdt_orders_out_trade_no_idx
    ON otok_usdt_orders (out_trade_no);

-- 观察到的全部 USDT 入账转账。一笔链上转账只记一次，最多绑定一笔订单。
CREATE TABLE IF NOT EXISTS otok_usdt_transfers (
    id              BIGSERIAL     PRIMARY KEY,
    network         VARCHAR(16)   NOT NULL,
    tx_hash         VARCHAR(80)   NOT NULL,
    log_index       BIGINT        NOT NULL DEFAULT 0,
    block_number    BIGINT        NOT NULL DEFAULT 0,
    block_hash      VARCHAR(80)   NOT NULL DEFAULT '',
    block_time      TIMESTAMPTZ,
    from_address    VARCHAR(64)   NOT NULL DEFAULT '',
    to_address_norm VARCHAR(64)   NOT NULL,
    token_contract  VARCHAR(64)   NOT NULL,
    amount_raw      NUMERIC(78,0) NOT NULL,
    decimals        INTEGER       NOT NULL,
    order_id        BIGINT        REFERENCES payment_orders(id) ON DELETE SET NULL,
    -- watcher: 自动扫描；user: 用户提交 TxHash；admin: 管理员补单
    source          VARCHAR(16)   NOT NULL DEFAULT 'watcher',
    -- seen / bound / credited / credit_failed / review / ignored / dropped
    status          VARCHAR(16)   NOT NULL DEFAULT 'seen',
    note            TEXT          NOT NULL DEFAULT '',
    credit_attempts INTEGER       NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS otok_usdt_transfers_tx_uniq
    ON otok_usdt_transfers (network, tx_hash, log_index);
CREATE UNIQUE INDEX IF NOT EXISTS otok_usdt_transfers_order_uniq
    ON otok_usdt_transfers (order_id) WHERE order_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS otok_usdt_transfers_status_idx
    ON otok_usdt_transfers (status, network);
CREATE INDEX IF NOT EXISTS otok_usdt_transfers_created_idx
    ON otok_usdt_transfers (created_at DESC);

-- 扫描游标与健康状态。EVM 按网络整体扫描（address_norm = '*'），TRON 按地址扫描。
CREATE TABLE IF NOT EXISTS otok_usdt_scan_cursors (
    network         VARCHAR(16) NOT NULL,
    address_norm    VARCHAR(64) NOT NULL,
    last_block      BIGINT      NOT NULL DEFAULT 0,
    last_ts_ms      BIGINT      NOT NULL DEFAULT 0,
    last_success_at TIMESTAMPTZ,
    last_error      TEXT        NOT NULL DEFAULT '',
    last_error_at   TIMESTAMPTZ,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (network, address_norm)
);
