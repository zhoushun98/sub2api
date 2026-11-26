//go:build integration

package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

// otok: 降智检测存储层与调度的 PostgreSQL 集成测试。上游用 httptest 假冒。

func otokQualityDB(t *testing.T) *sql.DB {
	t.Helper()
	otokIntegrationClient(t)
	_, err := otokPGDB.Exec(`TRUNCATE otok_quality_runs, otok_quality_rounds RESTART IDENTITY CASCADE`)
	require.NoError(t, err)
	return otokPGDB
}

func TestOtokQualityMigrationIdempotent(t *testing.T) {
	db := otokQualityDB(t)
	raw, err := migrations.FS.ReadFile("otok_0006_quality.sql")
	require.NoError(t, err)
	require.NotContains(t, strings.ToUpper(string(raw)), "CONCURRENTLY")
	_, err = db.Exec(string(raw))
	require.NoError(t, err)
}

func TestOtokQualityStore_RoundLifecycleAndPrune(t *testing.T) {
	db := otokQualityDB(t)
	ctx := context.Background()

	var roundIDs []int64
	for i := 0; i < 4; i++ {
		roundID, refs, err := otokQualityCreateRound(ctx, db, 7, otokQualityTriggerSchedule, OtokQualityProtocolChat, fmt.Sprintf("prompt-%d", i), []string{"gpt-6-astra", "gpt-6.1-sol"})
		require.NoError(t, err)
		require.Len(t, refs, 2)
		roundIDs = append(roundIDs, roundID)
		_, err = db.Exec(`UPDATE otok_quality_rounds SET started_at = NOW() - make_interval(mins => $2) WHERE id = $1`, roundID, 40-i*10)
		require.NoError(t, err)

		states, err := otokQualityItemStates(ctx, db, []int64{7})
		require.NoError(t, err)
		require.True(t, states[7].Unfinished)

		for j, r := range refs {
			require.NoError(t, otokQualityMarkRunning(ctx, db, r.ID, 1))
			res := otokQualityResult{HTTPStatus: 200, Text: "```html\n<html>" + r.Model + "</html>\n```", ResponseModel: r.Model}
			if j == 1 {
				res = otokQualityResult{HTTPStatus: 502, ErrCode: "http_5xx", ErrMessage: "bad gateway"}
			}
			require.NoError(t, otokQualityFinishRun(ctx, db, r.ID, 1, res, extractOtokQualityHTML(res.Text)))
			require.NoError(t, otokQualityFinishRoundIfDone(ctx, db, roundID))
		}
	}

	states, err := otokQualityItemStates(ctx, db, []int64{7, 8})
	require.NoError(t, err)
	require.False(t, states[7].Unfinished)
	require.WithinDuration(t, time.Now().Add(-10*time.Minute), states[7].LastStartedAt, time.Minute)
	_, ok := states[8]
	require.False(t, ok)

	rounds, err := otokQualityListRounds(ctx, db, 7, 10)
	require.NoError(t, err)
	require.Len(t, rounds, 4)
	require.Equal(t, roundIDs[3], rounds[0].ID, "新的在前")
	require.Equal(t, "prompt-3", rounds[0].Prompt, "提示词快照")
	require.NotNil(t, rounds[0].FinishedAt)
	require.Len(t, rounds[0].Runs, 2)
	ok0, fail1 := rounds[0].Runs[0], rounds[0].Runs[1]
	require.Equal(t, otokQualityStatusSucceeded, ok0.Status)
	require.Equal(t, OtokQualityProtocolChat, ok0.Protocol)
	require.True(t, ok0.HasHTML)
	require.NotNil(t, ok0.DurationMs)
	require.Equal(t, otokQualityStatusFailed, fail1.Status)
	require.Equal(t, "http_5xx", fail1.ErrorCode)
	require.Equal(t, "bad gateway", fail1.ErrorMessage)
	require.Equal(t, 502, *fail1.HTTPStatus)
	require.False(t, fail1.HasHTML)

	detail, err := otokQualityGetRun(ctx, db, ok0.ID)
	require.NoError(t, err)
	require.Equal(t, "<html>gpt-6-astra</html>", detail.OutputHTML)
	require.Equal(t, int64(7), detail.ItemID)
	require.Equal(t, 200, *detail.HTTPStatus)
	require.Equal(t, OtokQualityProtocolChat, detail.Protocol)
	_, err = otokQualityGetRun(ctx, db, 999999)
	require.ErrorIs(t, err, ErrOtokQualityRunNotFound)

	n, err := otokQualityPrune(ctx, db, 7, 2)
	require.NoError(t, err)
	require.Equal(t, int64(2), n)
	rounds, err = otokQualityListRounds(ctx, db, 7, 10)
	require.NoError(t, err)
	require.Len(t, rounds, 2)
	require.Equal(t, roundIDs[3], rounds[0].ID)
	require.Equal(t, roundIDs[2], rounds[1].ID)
	var runs int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM otok_quality_runs`).Scan(&runs))
	require.Equal(t, 4, runs, "runs 随轮次级联删除")
}

func TestOtokQualityStore_FailStale(t *testing.T) {
	db := otokQualityDB(t)
	ctx := context.Background()

	oldRound, oldRefs, err := otokQualityCreateRound(ctx, db, 1, otokQualityTriggerSchedule, OtokQualityProtocolResponses, "p", []string{"a"})
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE otok_quality_runs SET created_at = NOW() - INTERVAL '5 minutes' WHERE round_id = $1`, oldRound)
	require.NoError(t, err)

	_, stuckRefs, err := otokQualityCreateRound(ctx, db, 2, otokQualityTriggerSchedule, OtokQualityProtocolResponses, "p", []string{"a"})
	require.NoError(t, err)
	require.NoError(t, otokQualityMarkRunning(ctx, db, stuckRefs[0].ID, 1))
	_, err = db.Exec(`UPDATE otok_quality_runs SET started_at = NOW() - INTERVAL '2 hours' WHERE id = $1`, stuckRefs[0].ID)
	require.NoError(t, err)

	_, freshRefs, err := otokQualityCreateRound(ctx, db, 3, otokQualityTriggerSchedule, OtokQualityProtocolResponses, "p", []string{"a"})
	require.NoError(t, err)

	n, err := otokQualityFailStale(ctx, db, time.Hour, 24*time.Hour, time.Now().Add(-time.Minute))
	require.NoError(t, err)
	require.Equal(t, int64(2), n)

	for _, id := range []int64{oldRefs[0].ID, stuckRefs[0].ID} {
		d, err := otokQualityGetRun(ctx, db, id)
		require.NoError(t, err)
		require.Equal(t, otokQualityStatusFailed, d.Status)
		require.Equal(t, "interrupted", d.ErrorCode)
	}
	d, err := otokQualityGetRun(ctx, db, freshRefs[0].ID)
	require.NoError(t, err)
	require.Equal(t, otokQualityStatusQueued, d.Status)

	states, err := otokQualityItemStates(ctx, db, []int64{1, 2, 3})
	require.NoError(t, err)
	require.False(t, states[1].Unfinished)
	require.False(t, states[2].Unfinished)
	require.True(t, states[3].Unfinished)
}

func otokQualityCountRounds(t *testing.T, db *sql.DB, itemID int64) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM otok_quality_rounds WHERE item_id = $1`, itemID).Scan(&n))
	return n
}

func otokQualityItemInput(name, url string) OtokQualityItemInput {
	return OtokQualityItemInput{Name: name, Enabled: true, URL: url, APIKey: "sk-quality-integration", Models: []string{"m"}, Prompt: "p"}
}

func newOtokQualityIntegrationService(t *testing.T, db *sql.DB, settings *otokQualitySettingRepoStub, disabled bool) *OtokQualityService {
	t.Helper()
	svc := NewOtokQualityService(NewSettingService(settings, nil), db, otokQualityTestConfig(), otokQualityEncryptorStub{keyID: "k1"}, disabled)
	t.Cleanup(svc.Stop)
	return svc
}

func TestOtokQualitySaveConfig_DeletesResultsOfRemovedItems(t *testing.T) {
	db := otokQualityDB(t)
	ctx := context.Background()
	settings := &otokQualitySettingRepoStub{values: map[string]string{}}
	svc := newOtokQualityIntegrationService(t, db, settings, true)

	out, err := svc.SaveConfig(ctx, OtokQualityConfigInput{Items: []OtokQualityItemInput{
		otokQualityItemInput("a", "http://127.0.0.1:1/v1/responses"),
		otokQualityItemInput("b", "http://127.0.0.1:1/v1/messages"),
	}})
	require.NoError(t, err)
	for _, id := range []int64{1, 2, 99} {
		_, _, err := otokQualityCreateRound(ctx, db, id, otokQualityTriggerManual, OtokQualityProtocolResponses, "p", []string{"m"})
		require.NoError(t, err)
	}

	keep := otokQualityItemInput("b", "http://127.0.0.1:1/v1/messages")
	keep.ID, keep.APIKey = 2, ""
	_, err = svc.SaveConfig(ctx, OtokQualityConfigInput{Version: out.Version, Items: []OtokQualityItemInput{keep}})
	require.NoError(t, err)
	require.Zero(t, otokQualityCountRounds(t, db, 1), "删除条目后结果被清掉")
	require.Zero(t, otokQualityCountRounds(t, db, 99), "不在配置里的残留也清掉")
	require.Equal(t, 1, otokQualityCountRounds(t, db, 2))
	var runs int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM otok_quality_runs`).Scan(&runs))
	require.Equal(t, 1, runs, "runs 随轮次级联删除")
}

func TestOtokQualitySaveConfig_NewItemDoesNotInheritStaleResults(t *testing.T) {
	db := otokQualityDB(t)
	ctx := context.Background()
	// 配置被重置（settings 为空）后，id 1 仍有旧结果
	_, _, err := otokQualityCreateRound(ctx, db, 1, otokQualityTriggerSchedule, OtokQualityProtocolResponses, "old", []string{"m"})
	require.NoError(t, err)

	settings := &otokQualitySettingRepoStub{values: map[string]string{}}
	svc := newOtokQualityIntegrationService(t, db, settings, true)
	out, err := svc.SaveConfig(ctx, OtokQualityConfigInput{Items: []OtokQualityItemInput{otokQualityItemInput("new", "http://127.0.0.1:1/v1/responses")}})
	require.NoError(t, err)
	require.Equal(t, int64(1), out.Items[0].ID)
	require.Zero(t, otokQualityCountRounds(t, db, 1), "新条目不接旧 id 的残留结果")
}

// TestOtokQualityService_RunNowEndToEnd 走完整流程：建轮次 → 请求（首次 502 重试）→ 落库 → 保留轮数 → key 打码 → key 解密失败。
func TestOtokQualityService_RunNowEndToEnd(t *testing.T) {
	db := otokQualityDB(t)
	ctx := context.Background()
	const apiKey = "sk-quality-integration"

	var calls atomic.Int32
	var fail401 atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer "+apiKey, r.Header.Get("Authorization"))
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		require.NoError(t, json.Unmarshal(raw, &body))
		if fail401.Load() {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":{"message":"invalid key `+apiKey+`"}}`)
			return
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, `{"error":{"message":"upstream busy"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, otokQualitySSEBody(fmt.Sprint(body["model"])))
	}))
	defer upstream.Close()

	settings := &otokQualitySettingRepoStub{values: map[string]string{}}
	svc := newOtokQualityIntegrationService(t, db, settings, false)
	item := otokQualityItemInput("本站", upstream.URL+"/v1/responses")
	item.Enabled = false // 未启用、总开关关着也能手动跑
	out, err := svc.SaveConfig(ctx, OtokQualityConfigInput{KeepRounds: 1, Items: []OtokQualityItemInput{item}})
	require.NoError(t, err)
	itemID := out.Items[0].ID

	waitDone := func(prev int64) []OtokQualityAdminRound {
		var rounds []OtokQualityAdminRound
		require.Eventually(t, func() bool {
			rounds, err = svc.ListRounds(ctx, itemID)
			require.NoError(t, err)
			return len(rounds) > 0 && rounds[0].ID != prev && rounds[0].FinishedAt != nil
		}, 30*time.Second, 100*time.Millisecond)
		return rounds
	}

	_, err = svc.RunNow(ctx, 404)
	requireOtokQualityIntegrationReason(t, err, "OTOK_QUALITY_ITEM_NOT_FOUND")

	_, err = svc.RunNow(ctx, itemID)
	require.NoError(t, err)
	rounds := waitDone(0)
	require.Len(t, rounds, 1)
	require.Equal(t, "p", rounds[0].Prompt)
	run := rounds[0].Runs[0]
	require.Equal(t, otokQualityStatusSucceeded, run.Status, run.ErrorMessage)
	require.Equal(t, 2, run.Attempt)
	require.Equal(t, OtokQualityProtocolResponses, run.Protocol)

	detail, err := svc.GetRun(ctx, run.ID)
	require.NoError(t, err)
	require.Contains(t, detail.OutputHTML, "m")
	require.True(t, strings.HasPrefix(detail.OutputHTML, "<!DOCTYPE html>"))

	st, err := svc.Status(ctx)
	require.NoError(t, err)
	require.Len(t, st.Items, 1)
	require.NotNil(t, st.Items[0].LastStartedAt)
	require.False(t, st.Items[0].Running)

	// 401 回显 key：落库的错误原文不含 key；keep_rounds=1 只留最新一轮
	fail401.Store(true)
	_, err = svc.RunNow(ctx, itemID)
	require.NoError(t, err)
	rounds = waitDone(rounds[0].ID)
	require.Len(t, rounds, 1)
	failed := rounds[0].Runs[0]
	require.Equal(t, "http_4xx", failed.ErrorCode)
	require.Equal(t, 401, *failed.HTTPStatus)
	require.Equal(t, 1, failed.Attempt, "4xx 不重试")
	require.NotContains(t, failed.ErrorMessage, apiKey)
	require.Contains(t, failed.ErrorMessage, "***")

	// 换了加密密钥：照样落一轮，标成 key_error
	svc.encryptor = otokQualityEncryptorStub{keyID: "k2"}
	_, err = svc.RunNow(ctx, itemID)
	require.NoError(t, err)
	rounds = waitDone(rounds[0].ID)
	require.Equal(t, "key_error", rounds[0].Runs[0].ErrorCode)
}

func requireOtokQualityIntegrationReason(t *testing.T, err error, reason string) {
	t.Helper()
	require.Error(t, err)
	require.Equal(t, reason, infraerrors.Reason(err))
}

func otokQualitySSEBody(model string) string {
	html := `<!DOCTYPE html><html><body><svg><text>` + model + `</text></svg></body></html>`
	delta, _ := json.Marshal(map[string]string{"type": "response.output_text.delta", "delta": "```html\n" + html + "\n```"})
	return "data: " + string(delta) + "\n\n" +
		`data: {"type":"response.completed","response":{"model":"` + model + `","usage":{"input_tokens":10,"output_tokens":20}}}` + "\n\n"
}

// TestOtokQualityService_DirtyBytesAreStored 结果里有 NUL、非法 UTF-8 时照样落库，轮次正常收尾。
func TestOtokQualityService_DirtyBytesAreStored(t *testing.T) {
	db := otokQualityDB(t)
	ctx := context.Background()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		require.NoError(t, json.Unmarshal(raw, &body))
		if body["model"] == "bad" {
			w.Header().Set("Content-Type", "text/html; charset=gbk")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, "<html>\xc4\xe3\xba\xc3\x00</html>")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"<html><body>a\\u0000b</body></html>\"}\n\n"+
			"data: {\"type\":\"response.completed\",\"response\":{\"model\":\"good\\u0000x\",\"status\":\"completed\"}}\n\n")
	}))
	defer upstream.Close()

	settings := &otokQualitySettingRepoStub{values: map[string]string{}}
	svc := newOtokQualityIntegrationService(t, db, settings, false)
	item := otokQualityItemInput("dirty", upstream.URL+"/v1/responses")
	item.Models = []string{"good", "bad"}
	out, err := svc.SaveConfig(ctx, OtokQualityConfigInput{Items: []OtokQualityItemInput{item}})
	require.NoError(t, err)
	itemID := out.Items[0].ID

	_, err = svc.RunNow(ctx, itemID)
	require.NoError(t, err)
	var rounds []OtokQualityAdminRound
	require.Eventually(t, func() bool {
		rounds, err = svc.ListRounds(ctx, itemID)
		require.NoError(t, err)
		return len(rounds) == 1 && rounds[0].FinishedAt != nil
	}, 30*time.Second, 100*time.Millisecond, "轮次应正常收尾")

	byModel := map[string]OtokQualityAdminRunSummary{}
	for _, r := range rounds[0].Runs {
		byModel[r.Model] = r
	}
	good := byModel["good"]
	require.Equal(t, otokQualityStatusSucceeded, good.Status, good.ErrorMessage)
	require.Equal(t, "goodx", good.ResponseModel)
	detail, err := svc.GetRun(ctx, good.ID)
	require.NoError(t, err)
	require.Equal(t, "<html><body>ab</body></html>", detail.OutputText)
	require.Equal(t, "<html><body>ab</body></html>", detail.OutputHTML)

	bad := byModel["bad"]
	require.Equal(t, otokQualityStatusFailed, bad.Status)
	require.Equal(t, "http_4xx", bad.ErrorCode)
	require.Contains(t, bad.ErrorMessage, "�")
}
