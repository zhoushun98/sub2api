-- otok: 降智检测（自定义条目）结果表（幂等，可重复执行）
--
-- 条目配置在 settings 的 otok_quality 里，item_id 是配置里服务端分配的条目 id。
-- 一轮对条目的每个模型各发一次请求；prompt 是本轮所用提示词的快照。按条目只保留最近 N 轮，runs 随轮次级联删除。
-- 旧表 otok_pelican_* 保留，供回滚到旧版使用。

CREATE TABLE IF NOT EXISTS otok_quality_rounds (
    id          BIGSERIAL PRIMARY KEY,
    item_id     BIGINT      NOT NULL,
    trigger     VARCHAR(16) NOT NULL DEFAULT 'schedule', -- schedule / manual
    prompt      TEXT        NOT NULL DEFAULT '',
    started_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS otok_quality_rounds_item_started_idx
    ON otok_quality_rounds (item_id, started_at DESC);

CREATE TABLE IF NOT EXISTS otok_quality_runs (
    id               BIGSERIAL PRIMARY KEY,
    round_id         BIGINT       NOT NULL REFERENCES otok_quality_rounds (id) ON DELETE CASCADE,
    item_id          BIGINT       NOT NULL,
    model            VARCHAR(128) NOT NULL,
    protocol         VARCHAR(16)  NOT NULL DEFAULT '', -- responses / chat / messages
    status           VARCHAR(16)  NOT NULL DEFAULT 'queued', -- queued / running / succeeded / failed
    attempt          SMALLINT     NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    started_at       TIMESTAMPTZ,
    finished_at      TIMESTAMPTZ,
    duration_ms      INTEGER,
    first_token_ms   INTEGER,
    http_status      INTEGER,
    response_model   VARCHAR(128) NOT NULL DEFAULT '',
    input_tokens     INTEGER,
    output_tokens    INTEGER,
    reasoning_tokens INTEGER,
    output_text      TEXT         NOT NULL DEFAULT '',
    output_html      TEXT         NOT NULL DEFAULT '',
    error_code       VARCHAR(64)  NOT NULL DEFAULT '',
    error_message    TEXT         NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS otok_quality_runs_round_idx ON otok_quality_runs (round_id);

CREATE INDEX IF NOT EXISTS otok_quality_runs_pending_idx
    ON otok_quality_runs (created_at) WHERE status IN ('queued', 'running');
