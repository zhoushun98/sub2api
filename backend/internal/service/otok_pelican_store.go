package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// otok: 鹈鹕测试结果的读写（原生 SQL）。

const (
	otokPelicanStatusQueued    = "queued"
	otokPelicanStatusRunning   = "running"
	otokPelicanStatusSucceeded = "succeeded"
	otokPelicanStatusFailed    = "failed"

	otokPelicanTriggerSchedule = "schedule"
	otokPelicanTriggerManual   = "manual"
)

// OtokPelicanRunSummary 是列表里的一次模型请求，不含生成内容。
type OtokPelicanRunSummary struct {
	ID              int64      `json:"id"`
	Model           string     `json:"model"`
	Status          string     `json:"status"`
	Attempt         int        `json:"attempt"`
	DurationMs      *int       `json:"duration_ms"`
	FirstTokenMs    *int       `json:"first_token_ms"`
	OutputTokens    *int       `json:"output_tokens"`
	ReasoningTokens *int       `json:"reasoning_tokens"`
	HasHTML         bool       `json:"has_html"`
	ErrorCode       string     `json:"error_code"`
	ErrorMessage    string     `json:"error_message"`
	StartedAt       *time.Time `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at"`
}

// OtokPelicanRound 是一轮测试（弹窗左侧的一行）。
type OtokPelicanRound struct {
	ID         int64                   `json:"id"`
	GroupID    int64                   `json:"group_id"`
	Trigger    string                  `json:"trigger"`
	StartedAt  time.Time               `json:"started_at"`
	FinishedAt *time.Time              `json:"finished_at"`
	Runs       []OtokPelicanRunSummary `json:"runs"`
}

// OtokPelicanRunDetail 带生成内容，打开某个模型 tab 时才拉取。
type OtokPelicanRunDetail struct {
	OtokPelicanRunSummary
	RoundID         int64  `json:"round_id"`
	GroupID         int64  `json:"group_id"`
	ReasoningEffort string `json:"reasoning_effort"`
	ResponseModel   string `json:"response_model"`
	InputTokens     *int   `json:"input_tokens"`
	HTTPStatus      *int   `json:"http_status"`
	OutputText      string `json:"output_text"`
	OutputHTML      string `json:"output_html"`
}

type otokPelicanGroupState struct {
	LastStartedAt time.Time
	Unfinished    bool
}

type otokPelicanRunRef struct {
	ID    int64
	Model string
}

// ErrOtokPelicanRunNotFound 表示记录不存在（或已被清理）。
var ErrOtokPelicanRunNotFound = errors.New("pelican run not found")

func otokPelicanGroupStates(ctx context.Context, db *sql.DB, groupIDs []int64) (map[int64]otokPelicanGroupState, error) {
	out := map[int64]otokPelicanGroupState{}
	if len(groupIDs) == 0 {
		return out, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT group_id, MAX(started_at), BOOL_OR(finished_at IS NULL)
		FROM otok_pelican_rounds
		WHERE group_id = ANY($1)
		GROUP BY group_id`, pq.Array(groupIDs))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var gid int64
		var st otokPelicanGroupState
		if err := rows.Scan(&gid, &st.LastStartedAt, &st.Unfinished); err != nil {
			return nil, err
		}
		out[gid] = st
	}
	return out, rows.Err()
}

// otokPelicanCreateRound 在一个事务里插入轮次和每个模型的 queued 记录。
func otokPelicanCreateRound(ctx context.Context, db *sql.DB, groupID int64, trigger, effort string, models []string) (int64, []otokPelicanRunRef, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var roundID int64
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO otok_pelican_rounds (group_id, trigger) VALUES ($1, $2) RETURNING id`,
		groupID, trigger).Scan(&roundID); err != nil {
		return 0, nil, err
	}
	refs := make([]otokPelicanRunRef, 0, len(models))
	for _, m := range models {
		var id int64
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO otok_pelican_runs (round_id, group_id, model, reasoning_effort)
			VALUES ($1, $2, $3, $4) RETURNING id`,
			roundID, groupID, m, effort).Scan(&id); err != nil {
			return 0, nil, err
		}
		refs = append(refs, otokPelicanRunRef{ID: id, Model: m})
	}
	if err := tx.Commit(); err != nil {
		return 0, nil, err
	}
	return roundID, refs, nil
}

// otokPelicanMarkRunning 只在第一次尝试时写入 started_at，耗时按整次（含重试）计算。
func otokPelicanMarkRunning(ctx context.Context, db *sql.DB, runID int64, attempt int) error {
	_, err := db.ExecContext(ctx, `
		UPDATE otok_pelican_runs
		SET status = 'running', attempt = $2, started_at = COALESCE(started_at, NOW())
		WHERE id = $1`, runID, attempt)
	return err
}

func otokPelicanFinishRun(ctx context.Context, db *sql.DB, runID int64, attempt int, res otokPelicanProbeResult, html string) error {
	status := otokPelicanStatusSucceeded
	if !res.ok() {
		status = otokPelicanStatusFailed
	}
	var httpStatus *int
	if res.HTTPStatus > 0 {
		v := res.HTTPStatus
		httpStatus = &v
	}
	_, err := db.ExecContext(ctx, `
		UPDATE otok_pelican_runs SET
			status = $2, attempt = $3, finished_at = NOW(),
			duration_ms = (EXTRACT(EPOCH FROM (NOW() - COALESCE(started_at, NOW()))) * 1000)::INTEGER,
			first_token_ms = $4, http_status = $5, response_model = $6,
			input_tokens = $7, output_tokens = $8, reasoning_tokens = $9,
			output_text = $10, output_html = $11, error_code = $12, error_message = $13
		WHERE id = $1`,
		runID, status, attempt, res.FirstTokenMs, httpStatus, truncateOtokPelicanBytes(res.ResponseModel, 128),
		res.InputTokens, res.OutputTokens, res.ReasoningTokens,
		truncateOtokPelicanBytes(res.Text, otokPelicanMaxStoreBytes), truncateOtokPelicanBytes(html, otokPelicanMaxStoreBytes),
		truncateOtokPelicanBytes(res.ErrCode, 64), res.ErrMessage)
	return err
}

// otokPelicanFailRun 把未完成的记录标记为失败（停机、配置变更等）。
func otokPelicanFailRun(ctx context.Context, db *sql.DB, runID int64, code, msg string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE otok_pelican_runs
		SET status = 'failed', finished_at = NOW(), error_code = $2, error_message = $3,
			duration_ms = CASE WHEN started_at IS NULL THEN NULL
				ELSE (EXTRACT(EPOCH FROM (NOW() - started_at)) * 1000)::INTEGER END
		WHERE id = $1 AND status IN ('queued', 'running')`, runID, code, msg)
	return err
}

func otokPelicanFinishRoundIfDone(ctx context.Context, db *sql.DB, roundID int64) error {
	_, err := db.ExecContext(ctx, `
		UPDATE otok_pelican_rounds r SET finished_at = NOW()
		WHERE r.id = $1 AND r.finished_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM otok_pelican_runs WHERE round_id = r.id AND status IN ('queued', 'running'))`,
		roundID)
	return err
}

// otokPelicanPrune 按分组只保留最近 keep 个已结束的轮次，runs 随外键级联删除。
func otokPelicanPrune(ctx context.Context, db *sql.DB, groupID int64, keep int) (int64, error) {
	res, err := db.ExecContext(ctx, `
		DELETE FROM otok_pelican_rounds
		WHERE group_id = $1 AND finished_at IS NOT NULL
		  AND id NOT IN (
			SELECT id FROM otok_pelican_rounds
			WHERE group_id = $1 AND finished_at IS NOT NULL
			ORDER BY started_at DESC, id DESC
			LIMIT $2)`, groupID, keep)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// otokPelicanFailStale 清理进程崩溃留下的未完成记录：
// running 超过 runningAge、queued 超过 queuedAge 或早于 before 创建的都记为 interrupted。
func otokPelicanFailStale(ctx context.Context, db *sql.DB, runningAge, queuedAge time.Duration, before time.Time) (int64, error) {
	res, err := db.ExecContext(ctx, `
		UPDATE otok_pelican_runs
		SET status = 'failed', finished_at = NOW(), error_code = 'interrupted',
			error_message = 'probe was interrupted (process restarted or stuck)'
		WHERE status IN ('queued', 'running')
		  AND (created_at < $3
		       OR (status = 'running' AND started_at < NOW() - make_interval(secs => $1))
		       OR (status = 'queued' AND created_at < NOW() - make_interval(secs => $2)))`,
		runningAge.Seconds(), queuedAge.Seconds(), before)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if _, err := db.ExecContext(ctx, `
		UPDATE otok_pelican_rounds r SET finished_at = NOW()
		WHERE r.finished_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM otok_pelican_runs WHERE round_id = r.id AND status IN ('queued', 'running'))`); err != nil {
		return n, err
	}
	return n, nil
}

// otokPelicanListRounds 返回分组最近的轮次（新的在前），不含生成内容。
func otokPelicanListRounds(ctx context.Context, db *sql.DB, groupID int64, limit int) ([]OtokPelicanRound, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, group_id, trigger, started_at, finished_at
		FROM otok_pelican_rounds
		WHERE group_id = $1
		ORDER BY started_at DESC, id DESC
		LIMIT $2`, groupID, limit)
	if err != nil {
		return nil, err
	}
	rounds := []OtokPelicanRound{}
	index := map[int64]int{}
	ids := []int64{}
	for rows.Next() {
		var r OtokPelicanRound
		var finished sql.NullTime
		if err := rows.Scan(&r.ID, &r.GroupID, &r.Trigger, &r.StartedAt, &finished); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if finished.Valid {
			t := finished.Time
			r.FinishedAt = &t
		}
		r.Runs = []OtokPelicanRunSummary{}
		index[r.ID] = len(rounds)
		ids = append(ids, r.ID)
		rounds = append(rounds, r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	if len(ids) == 0 {
		return rounds, nil
	}

	runRows, err := db.QueryContext(ctx, `
		SELECT round_id, id, model, status, attempt, duration_ms, first_token_ms, output_tokens, reasoning_tokens,
			output_html <> '', error_code, error_message, started_at, finished_at
		FROM otok_pelican_runs
		WHERE round_id = ANY($1)
		ORDER BY id`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer func() { _ = runRows.Close() }()
	for runRows.Next() {
		var roundID int64
		var s OtokPelicanRunSummary
		var dur, ftm, out, reason sql.NullInt64
		var started, finished sql.NullTime
		if err := runRows.Scan(&roundID, &s.ID, &s.Model, &s.Status, &s.Attempt, &dur, &ftm, &out, &reason,
			&s.HasHTML, &s.ErrorCode, &s.ErrorMessage, &started, &finished); err != nil {
			return nil, err
		}
		s.DurationMs, s.FirstTokenMs, s.OutputTokens, s.ReasoningTokens = otokNullInt(dur), otokNullInt(ftm), otokNullInt(out), otokNullInt(reason)
		s.StartedAt, s.FinishedAt = otokNullTime(started), otokNullTime(finished)
		if i, ok := index[roundID]; ok {
			rounds[i].Runs = append(rounds[i].Runs, s)
		}
	}
	return rounds, runRows.Err()
}

func otokPelicanGetRun(ctx context.Context, db *sql.DB, runID int64) (*OtokPelicanRunDetail, error) {
	var d OtokPelicanRunDetail
	var dur, ftm, out, reason, in, httpStatus sql.NullInt64
	var started, finished sql.NullTime
	err := db.QueryRowContext(ctx, `
		SELECT id, round_id, group_id, model, status, attempt, reasoning_effort, response_model,
			duration_ms, first_token_ms, input_tokens, output_tokens, reasoning_tokens, http_status,
			output_text, output_html, error_code, error_message, started_at, finished_at
		FROM otok_pelican_runs WHERE id = $1`, runID).Scan(
		&d.ID, &d.RoundID, &d.GroupID, &d.Model, &d.Status, &d.Attempt, &d.ReasoningEffort, &d.ResponseModel,
		&dur, &ftm, &in, &out, &reason, &httpStatus,
		&d.OutputText, &d.OutputHTML, &d.ErrorCode, &d.ErrorMessage, &started, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrOtokPelicanRunNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get pelican run: %w", err)
	}
	d.DurationMs, d.FirstTokenMs, d.InputTokens, d.OutputTokens, d.ReasoningTokens, d.HTTPStatus =
		otokNullInt(dur), otokNullInt(ftm), otokNullInt(in), otokNullInt(out), otokNullInt(reason), otokNullInt(httpStatus)
	d.StartedAt, d.FinishedAt = otokNullTime(started), otokNullTime(finished)
	d.HasHTML = d.OutputHTML != ""
	return &d, nil
}

func otokNullInt(v sql.NullInt64) *int {
	if !v.Valid {
		return nil
	}
	i := int(v.Int64)
	return &i
}

func otokNullTime(v sql.NullTime) *time.Time {
	if !v.Valid {
		return nil
	}
	t := v.Time
	return &t
}
