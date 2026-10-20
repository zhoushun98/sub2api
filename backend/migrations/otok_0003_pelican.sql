-- otok: 渠道状态卡片「降智检测」（鹈鹕测试）（幂等，可重复执行）
--
-- 每个启用分组按间隔跑一轮：一轮对每个配置模型各发一次请求，生成「鹈鹕骑自行车」的 HTML。
-- rounds 对应弹窗左侧的时间列表，runs 对应右侧的模型 tab。按分组只保留最近 N 轮，runs 随轮次级联删除。

CREATE TABLE IF NOT EXISTS otok_pelican_rounds (
    id          BIGSERIAL PRIMARY KEY,
    group_id    BIGINT      NOT NULL,
    trigger     VARCHAR(16) NOT NULL DEFAULT 'schedule', -- schedule / manual
    started_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS otok_pelican_rounds_group_started_idx
    ON otok_pelican_rounds (group_id, started_at DESC);

CREATE TABLE IF NOT EXISTS otok_pelican_runs (
    id               BIGSERIAL PRIMARY KEY,
    round_id         BIGINT       NOT NULL REFERENCES otok_pelican_rounds (id) ON DELETE CASCADE,
    group_id         BIGINT       NOT NULL,
    model            VARCHAR(128) NOT NULL,
    status           VARCHAR(16)  NOT NULL DEFAULT 'queued', -- queued / running / succeeded / failed
    attempt          SMALLINT     NOT NULL DEFAULT 0,
    reasoning_effort VARCHAR(16)  NOT NULL DEFAULT '',
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

CREATE INDEX IF NOT EXISTS otok_pelican_runs_round_idx ON otok_pelican_runs (round_id);

CREATE INDEX IF NOT EXISTS otok_pelican_runs_pending_idx
    ON otok_pelican_runs (created_at) WHERE status IN ('queued', 'running');
