-- otok: USDT 到账数据源支持币安充值记录（幂等，可重复执行）。
-- external_id 存币安充值记录的 id（source = 'binance'），按它幂等写入；链上来源保持空串。
ALTER TABLE otok_usdt_transfers ADD COLUMN IF NOT EXISTS external_id VARCHAR(64) NOT NULL DEFAULT '';
CREATE UNIQUE INDEX IF NOT EXISTS otok_usdt_transfers_external_uniq
    ON otok_usdt_transfers (source, external_id) WHERE external_id <> '';
