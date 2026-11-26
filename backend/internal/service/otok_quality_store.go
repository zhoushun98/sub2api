package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// otok: 降智检测结果的读写（原生 SQL）。

const (
	otokQualityStatusQueued    = "queued"
	otokQualityStatusRunning   = "running"
	otokQualityStatusSucceeded = "succeeded"
	otokQualityStatusFailed    = "failed"

	otokQualityTriggerSchedule = "schedule"
	otokQualityTriggerManual   = "manual"
)

// OtokQualityRunSummary 是用户可见的一次模型请求摘要，不含生成内容、错误原文和上游信息。
type OtokQualityRunSummary struct {
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
	StartedAt       *time.Time `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at"`
}

// OtokQualityAdminRunSummary 在用户摘要之外带上错误原文、状态码、上游返回的模型名和协议。
type OtokQualityAdminRunSummary struct {
	OtokQualityRunSummary
	ErrorMessage  string `json:"error_message"`
	HTTPStatus    *int   `json:"http_status"`
	ResponseModel string `json:"response_model"`
	Protocol      string `json:"protocol"`
}

// OtokQualityRoundInfo 是一轮的基本信息。
type OtokQualityRoundInfo struct {
	ID         int64      `json:"id"`
	ItemID     int64      `json:"item_id"`
	Trigger    string     `json:"trigger"`
	Prompt     string     `json:"prompt"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

// OtokQualityRound 是用户端的一轮。
type OtokQualityRound struct {
	OtokQualityRoundInfo
	Runs []OtokQualityRunSummary `json:"runs"`
}

// OtokQualityAdminRound 是管理端的一轮。
type OtokQualityAdminRound struct {
	OtokQualityRoundInfo
	Runs []OtokQualityAdminRunSummary `json:"runs"`
}

// OtokQualityRunDetail 是用户端的单条记录（含生成内容）。
type OtokQualityRunDetail struct {
	OtokQualityRunSummary
	RoundID     int64  `json:"round_id"`
	ItemID      int64  `json:"item_id"`
	InputTokens *int   `json:"input_tokens"`
	OutputText  string `json:"output_text"`
	OutputHTML  string `json:"output_html"`
}

// OtokQualityAdminRunDetail 是管理端的单条记录。
type OtokQualityAdminRunDetail struct {
	OtokQualityAdminRunSummary
	RoundID     int64  `json:"round_id"`
	ItemID      int64  `json:"item_id"`
	InputTokens *int   `json:"input_tokens"`
	OutputText  string `json:"output_text"`
	OutputHTML  string `json:"output_html"`
}

// UserView 去掉用户不该看到的字段。
func (r OtokQualityAdminRound) UserView() OtokQualityRound {
	out := OtokQualityRound{OtokQualityRoundInfo: r.OtokQualityRoundInfo, Runs: make([]OtokQualityRunSummary, 0, len(r.Runs))}
	for _, run := range r.Runs {
		out.Runs = append(out.Runs, run.OtokQualityRunSummary)
	}
	return out
}

// UserView 去掉用户不该看到的字段。
func (d OtokQualityAdminRunDetail) UserView() OtokQualityRunDetail {
	return OtokQualityRunDetail{
		OtokQualityRunSummary: d.OtokQualityRunSummary,
		RoundID:               d.RoundID,
		ItemID:                d.ItemID,
		InputTokens:           d.InputTokens,
		OutputText:            d.OutputText,
		OutputHTML:            d.OutputHTML,
	}
}

type otokQualityItemState struct {
	LastStartedAt time.Time
	Unfinished    bool
}

type otokQualityRunRef struct {
	ID    int64
	Model string
}

// ErrOtokQualityRunNotFound 表示记录不存在（或已被清理）。
var ErrOtokQualityRunNotFound = errors.New("quality run not found")

func otokQualityItemStates(ctx context.Context, db *sql.DB, itemIDs []int64) (map[int64]otokQualityItemState, error) {
	out := map[int64]otokQualityItemState{}
	if len(itemIDs) == 0 {
		return out, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT item_id, MAX(started_at), BOOL_OR(finished_at IS NULL)
		FROM otok_quality_rounds
		WHERE item_id = ANY($1)
		GROUP BY item_id`, pq.Array(itemIDs))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var st otokQualityItemState
		if err := rows.Scan(&id, &st.LastStartedAt, &st.Unfinished); err != nil {
			return nil, err
		}
		out[id] = st
	}
	return out, rows.Err()
}

// otokQualityCreateRound 在一个事务里插入轮次（含提示词快照）和每个模型的 queued 记录。
func otokQualityCreateRound(ctx context.Context, db *sql.DB, itemID int64, trigger, protocol, prompt string, models []string) (int64, []otokQualityRunRef, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var roundID int64
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO otok_quality_rounds (item_id, trigger, prompt) VALUES ($1, $2, $3) RETURNING id`,
		itemID, trigger, prompt).Scan(&roundID); err != nil {
		return 0, nil, err
	}
	refs := make([]otokQualityRunRef, 0, len(models))
	for _, m := range models {
		var id int64
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO otok_quality_runs (round_id, item_id, model, protocol)
			VALUES ($1, $2, $3, $4) RETURNING id`,
			roundID, itemID, m, protocol).Scan(&id); err != nil {
			return 0, nil, err
		}
		refs = append(refs, otokQualityRunRef{ID: id, Model: m})
	}
	if err := tx.Commit(); err != nil {
		return 0, nil, err
	}
	return roundID, refs, nil
}

// otokQualityMarkRunning 只在第一次尝试时写入 started_at，耗时按整次（含重试）计算。
func otokQualityMarkRunning(ctx context.Context, db *sql.DB, runID int64, attempt int) error {
	_, err := db.ExecContext(ctx, `
		UPDATE otok_quality_runs
		SET status = 'running', attempt = $2, started_at = COALESCE(started_at, NOW())
		WHERE id = $1`, runID, attempt)
	return err
}

func otokQualityFinishRun(ctx context.Context, db *sql.DB, runID int64, attempt int, res otokQualityResult, html string) error {
	status := otokQualityStatusSucceeded
	if !res.ok() {
		status = otokQualityStatusFailed
	}
	var httpStatus *int
	if res.HTTPStatus > 0 {
		v := res.HTTPStatus
		httpStatus = &v
	}
	_, err := db.ExecContext(ctx, `
		UPDATE otok_quality_runs SET
			status = $2, attempt = $3, finished_at = NOW(),
			duration_ms = (EXTRACT(EPOCH FROM (NOW() - COALESCE(started_at, NOW()))) * 1000)::INTEGER,
			first_token_ms = $4, http_status = $5, response_model = $6,
			input_tokens = $7, output_tokens = $8, reasoning_tokens = $9,
			output_text = $10, output_html = $11, error_code = $12, error_message = $13
		WHERE id = $1`,
		runID, status, attempt, res.FirstTokenMs, httpStatus, truncateOtokQualityBytes(res.ResponseModel, 128),
		res.InputTokens, res.OutputTokens, res.ReasoningTokens,
		truncateOtokQualityBytes(res.Text, otokQualityMaxStoreBytes), truncateOtokQualityBytes(html, otokQualityMaxStoreBytes),
		truncateOtokQualityBytes(res.ErrCode, 64), res.ErrMessage)
	return err
}

// otokQualityFailRun 把未完成的记录标记为失败（停机、key 解密失败等）。
func otokQualityFailRun(ctx context.Context, db *sql.DB, runID int64, code, msg string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE otok_quality_runs
		SET status = 'failed', finished_at = NOW(), error_code = $2, error_message = $3,
			duration_ms = CASE WHEN started_at IS NULL THEN NULL
				ELSE (EXTRACT(EPOCH FROM (NOW() - started_at)) * 1000)::INTEGER END
		WHERE id = $1 AND status IN ('queued', 'running')`, runID, code, msg)
	return err
}

func otokQualityFinishRoundIfDone(ctx context.Context, db *sql.DB, roundID int64) error {
	_, err := db.ExecContext(ctx, `
		UPDATE otok_quality_rounds r SET finished_at = NOW()
		WHERE r.id = $1 AND r.finished_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM otok_quality_runs WHERE round_id = r.id AND status IN ('queued', 'running'))`,
		roundID)
	return err
}

// otokQualityPrune 按条目只保留最近 keep 个已结束的轮次，runs 随外键级联删除。
func otokQualityPrune(ctx context.Context, db *sql.DB, itemID int64, keep int) (int64, error) {
	res, err := db.ExecContext(ctx, `
		DELETE FROM otok_quality_rounds
		WHERE item_id = $1 AND finished_at IS NOT NULL
		  AND id NOT IN (
			SELECT id FROM otok_quality_rounds
			WHERE item_id = $1 AND finished_at IS NOT NULL
			ORDER BY started_at DESC, id DESC
			LIMIT $2)`, itemID, keep)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// otokQualityDeleteItemsExcept 删掉 keep 以外所有条目的结果（keep 为空时全删），runs 随外键级联删除。
func otokQualityDeleteItemsExcept(ctx context.Context, db *sql.DB, keep []int64) (int64, error) {
	if keep == nil {
		keep = []int64{}
	}
	res, err := db.ExecContext(ctx, `DELETE FROM otok_quality_rounds WHERE NOT (item_id = ANY($1))`, pq.Array(keep))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// otokQualityFailStale 清理进程崩溃留下的未完成记录：
// running 超过 runningAge、queued 超过 queuedAge 或早于 before 创建的都记为 interrupted。
func otokQualityFailStale(ctx context.Context, db *sql.DB, runningAge, queuedAge time.Duration, before time.Time) (int64, error) {
	res, err := db.ExecContext(ctx, `
		UPDATE otok_quality_runs
		SET status = 'failed', finished_at = NOW(), error_code = 'interrupted',
			error_message = 'run was interrupted (process restarted or stuck)'
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
		UPDATE otok_quality_rounds r SET finished_at = NOW()
		WHERE r.finished_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM otok_quality_runs WHERE round_id = r.id AND status IN ('queued', 'running'))`); err != nil {
		return n, err
	}
	return n, nil
}

// otokQualityListRounds 返回条目最近的轮次（新的在前），不含生成内容。
func otokQualityListRounds(ctx context.Context, db *sql.DB, itemID int64, limit int) ([]OtokQualityAdminRound, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, item_id, trigger, prompt, started_at, finished_at
		FROM otok_quality_rounds
		WHERE item_id = $1
		ORDER BY started_at DESC, id DESC
		LIMIT $2`, itemID, limit)
	if err != nil {
		return nil, err
	}
	rounds := []OtokQualityAdminRound{}
	index := map[int64]int{}
	ids := []int64{}
	for rows.Next() {
		var r OtokQualityAdminRound
		var finished sql.NullTime
		if err := rows.Scan(&r.ID, &r.ItemID, &r.Trigger, &r.Prompt, &r.StartedAt, &finished); err != nil {
			_ = rows.Close()
			return nil, err
		}
		r.FinishedAt = otokNullTime(finished)
		r.Runs = []OtokQualityAdminRunSummary{}
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
			output_html <> '', error_code, error_message, http_status, response_model, protocol, started_at, finished_at
		FROM otok_quality_runs
		WHERE round_id = ANY($1)
		ORDER BY id`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer func() { _ = runRows.Close() }()
	for runRows.Next() {
		var roundID int64
		var s OtokQualityAdminRunSummary
		var dur, ftm, out, reason, httpStatus sql.NullInt64
		var started, finished sql.NullTime
		if err := runRows.Scan(&roundID, &s.ID, &s.Model, &s.Status, &s.Attempt, &dur, &ftm, &out, &reason,
			&s.HasHTML, &s.ErrorCode, &s.ErrorMessage, &httpStatus, &s.ResponseModel, &s.Protocol, &started, &finished); err != nil {
			return nil, err
		}
		s.DurationMs, s.FirstTokenMs, s.OutputTokens, s.ReasoningTokens = otokNullInt(dur), otokNullInt(ftm), otokNullInt(out), otokNullInt(reason)
		s.HTTPStatus = otokNullInt(httpStatus)
		s.StartedAt, s.FinishedAt = otokNullTime(started), otokNullTime(finished)
		if i, ok := index[roundID]; ok {
			rounds[i].Runs = append(rounds[i].Runs, s)
		}
	}
	return rounds, runRows.Err()
}

func otokQualityGetRun(ctx context.Context, db *sql.DB, runID int64) (*OtokQualityAdminRunDetail, error) {
	var d OtokQualityAdminRunDetail
	var dur, ftm, out, reason, in, httpStatus sql.NullInt64
	var started, finished sql.NullTime
	err := db.QueryRowContext(ctx, `
		SELECT id, round_id, item_id, model, status, attempt, protocol, response_model,
			duration_ms, first_token_ms, input_tokens, output_tokens, reasoning_tokens, http_status,
			output_text, output_html, error_code, error_message, started_at, finished_at
		FROM otok_quality_runs WHERE id = $1`, runID).Scan(
		&d.ID, &d.RoundID, &d.ItemID, &d.Model, &d.Status, &d.Attempt, &d.Protocol, &d.ResponseModel,
		&dur, &ftm, &in, &out, &reason, &httpStatus,
		&d.OutputText, &d.OutputHTML, &d.ErrorCode, &d.ErrorMessage, &started, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrOtokQualityRunNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get quality run: %w", err)
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
