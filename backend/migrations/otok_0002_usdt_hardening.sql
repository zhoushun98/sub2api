-- otok: USDT 链上收款安全加固（幂等，可重复执行）

-- 1. 金额唯一性改为按收款地址跨网络计算。
--    多条 EVM 网络通常共用同一个收款地址，原先按 (network, address, amount) 唯一时，
--    用户付错网络的转账可能匹配到另一网络上同金额的其他用户订单。
--    TRON（Base58）与 EVM（0x 小写）地址格式不同，不会互相冲突。
DROP INDEX IF EXISTS otok_usdt_orders_active_amount_uniq;
CREATE UNIQUE INDEX IF NOT EXISTS otok_usdt_orders_active_addr_amount_uniq
    ON otok_usdt_orders (to_address_norm, amount_cents) WHERE active;

-- 2. 管理员补单允许链上金额与订单不一致；其余绑定在入账前必须金额精确一致。
ALTER TABLE otok_usdt_transfers ADD COLUMN IF NOT EXISTS amount_override BOOLEAN NOT NULL DEFAULT FALSE;
