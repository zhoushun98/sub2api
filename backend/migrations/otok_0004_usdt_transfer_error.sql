-- otok: 记录已绑定转账最近一次入账失败的原因（幂等，可重复执行）
-- 链上复核（查回执 / 交易）出错时转账保持 bound 持续重试，不计入 credit_attempts；
-- 这里记下错误供后台展示，下一次复核成功即清空。
ALTER TABLE otok_usdt_transfers ADD COLUMN IF NOT EXISTS last_error TEXT NOT NULL DEFAULT '';
ALTER TABLE otok_usdt_transfers ADD COLUMN IF NOT EXISTS last_error_at TIMESTAMPTZ;
