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

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

// otok: 鹈鹕测试存储层与调度的 PostgreSQL 集成测试。网关用 httptest 假冒。

func otokPelicanDB(t *testing.T) *sql.DB {
	t.Helper()
	otokIntegrationClient(t)
	_, err := otokPGDB.Exec(`TRUNCATE otok_pelican_runs, otok_pelican_rounds RESTART IDENTITY CASCADE`)
	require.NoError(t, err)
	return otokPGDB
}

func TestOtokPelicanMigrationIdempotent(t *testing.T) {
	db := otokPelicanDB(t)
	raw, err := migrations.FS.ReadFile("otok_0003_pelican.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(raw))
	require.NoError(t, err)
}

func TestOtokPelicanStore_RoundLifecycleAndPrune(t *testing.T) {
	db := otokPelicanDB(t)
	ctx := context.Background()

	var roundIDs []int64
	for i := 0; i < 4; i++ {
		roundID, refs, err := otokPelicanCreateRound(ctx, db, 127, otokPelicanTriggerSchedule, "medium", []string{"gpt-6-astra", "gpt-6.1-sol"})
		require.NoError(t, err)
		require.Len(t, refs, 2)
		roundIDs = append(roundIDs, roundID)
		_, err = db.Exec(`UPDATE otok_pelican_rounds SET started_at = NOW() - make_interval(mins => $2) WHERE id = $1`, roundID, 40-i*10)
		require.NoError(t, err)

		states, err := otokPelicanGroupStates(ctx, db, []int64{127})
		require.NoError(t, err)
		require.True(t, states[127].Unfinished)

		for j, r := range refs {
			require.NoError(t, otokPelicanMarkRunning(ctx, db, r.ID, 1))
			res := otokPelicanProbeResult{HTTPStatus: 200, Text: "```html\n<html>" + r.Model + "</html>\n```", ResponseModel: r.Model}
			if j == 1 {
				res = otokPelicanProbeResult{HTTPStatus: 502, ErrCode: "http_502", ErrMessage: "bad gateway"}
			}
			require.NoError(t, otokPelicanFinishRun(ctx, db, r.ID, 1, res, extractOtokPelicanHTML(res.Text)))
			require.NoError(t, otokPelicanFinishRoundIfDone(ctx, db, roundID))
		}
	}

	states, err := otokPelicanGroupStates(ctx, db, []int64{127, 135})
	require.NoError(t, err)
	require.False(t, states[127].Unfinished)
	require.WithinDuration(t, time.Now().Add(-10*time.Minute), states[127].LastStartedAt, time.Minute)
	_, ok := states[135]
	require.False(t, ok)

	rounds, err := otokPelicanListRounds(ctx, db, 127, 10)
	require.NoError(t, err)
	require.Len(t, rounds, 4)
	require.Equal(t, roundIDs[3], rounds[0].ID, "新的在前")
	require.NotNil(t, rounds[0].FinishedAt)
	require.Len(t, rounds[0].Runs, 2)
	ok0, fail1 := rounds[0].Runs[0], rounds[0].Runs[1]
	require.Equal(t, otokPelicanStatusSucceeded, ok0.Status)
	require.True(t, ok0.HasHTML)
	require.NotNil(t, ok0.DurationMs)
	require.Equal(t, otokPelicanStatusFailed, fail1.Status)
	require.Equal(t, "http_502", fail1.ErrorCode)
	require.False(t, fail1.HasHTML)

	detail, err := otokPelicanGetRun(ctx, db, ok0.ID)
	require.NoError(t, err)
	require.Equal(t, "<html>gpt-6-astra</html>", detail.OutputHTML)
	require.Equal(t, int64(127), detail.GroupID)
	require.Equal(t, 200, *detail.HTTPStatus)
	_, err = otokPelicanGetRun(ctx, db, 999999)
	require.ErrorIs(t, err, ErrOtokPelicanRunNotFound)

	n, err := otokPelicanPrune(ctx, db, 127, 2)
	require.NoError(t, err)
	require.Equal(t, int64(2), n)
	rounds, err = otokPelicanListRounds(ctx, db, 127, 10)
	require.NoError(t, err)
	require.Len(t, rounds, 2)
	require.Equal(t, roundIDs[3], rounds[0].ID)
	require.Equal(t, roundIDs[2], rounds[1].ID)
	var orphanRuns int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM otok_pelican_runs`).Scan(&orphanRuns))
	require.Equal(t, 4, orphanRuns, "runs 随轮次级联删除")
}

func TestOtokPelicanStore_FailStale(t *testing.T) {
	db := otokPelicanDB(t)
	ctx := context.Background()

	// 进程启动前留下的 queued、卡住的 running、刚创建的 queued
	oldRound, oldRefs, err := otokPelicanCreateRound(ctx, db, 127, otokPelicanTriggerSchedule, "medium", []string{"a"})
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE otok_pelican_runs SET created_at = NOW() - INTERVAL '5 minutes' WHERE round_id = $1`, oldRound)
	require.NoError(t, err)

	stuckRound, stuckRefs, err := otokPelicanCreateRound(ctx, db, 135, otokPelicanTriggerSchedule, "medium", []string{"a"})
	require.NoError(t, err)
	require.NoError(t, otokPelicanMarkRunning(ctx, db, stuckRefs[0].ID, 1))
	_, err = db.Exec(`UPDATE otok_pelican_runs SET started_at = NOW() - INTERVAL '2 hours' WHERE id = $1`, stuckRefs[0].ID)
	require.NoError(t, err)

	freshRound, freshRefs, err := otokPelicanCreateRound(ctx, db, 240, otokPelicanTriggerSchedule, "medium", []string{"a"})
	require.NoError(t, err)

	n, err := otokPelicanFailStale(ctx, db, time.Hour, 24*time.Hour, time.Now().Add(-time.Minute))
	require.NoError(t, err)
	require.Equal(t, int64(2), n)

	for _, id := range []int64{oldRefs[0].ID, stuckRefs[0].ID} {
		d, err := otokPelicanGetRun(ctx, db, id)
		require.NoError(t, err)
		require.Equal(t, otokPelicanStatusFailed, d.Status)
		require.Equal(t, "interrupted", d.ErrorCode)
	}
	d, err := otokPelicanGetRun(ctx, db, freshRefs[0].ID)
	require.NoError(t, err)
	require.Equal(t, otokPelicanStatusQueued, d.Status)

	states, err := otokPelicanGroupStates(ctx, db, []int64{127, 135, 240})
	require.NoError(t, err)
	require.False(t, states[127].Unfinished)
	require.False(t, states[135].Unfinished)
	require.True(t, states[240].Unfinished)
	_ = oldRound
	_ = stuckRound
	_ = freshRound
}

type otokPelicanGroupRepoStub struct {
	GroupRepository
	groups map[int64]*Group
}

func (r *otokPelicanGroupRepoStub) GetByID(_ context.Context, id int64) (*Group, error) {
	if g, ok := r.groups[id]; ok {
		cp := *g
		return &cp, nil
	}
	return nil, ErrGroupNotFound
}

// TestOtokPelicanService_RunNowEndToEnd 走完整流程：准备 key → 建轮次 → 环回请求（首次 502 重试）→ 落库 → 保留轮数。
func TestOtokPelicanService_RunNowEndToEnd(t *testing.T) {
	db := otokPelicanDB(t)
	client := otokIntegrationClient(t)
	ctx := context.Background()

	dbUser := otokCreateTestUser(t, ctx, client)
	group, err := client.Group.Create().SetName(fmt.Sprintf("pelican-%d", time.Now().UnixNano())).SetPlatform(PlatformOpenAI).Save(ctx)
	require.NoError(t, err)
	apiKey := fmt.Sprintf("sk-pelican-%d", time.Now().UnixNano())
	_, err = client.APIKey.Create().SetUserID(dbUser.ID).SetKey(apiKey).SetName("otok-pelican").SetGroupID(group.ID).Save(ctx)
	require.NoError(t, err)

	var calls atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer "+apiKey, r.Header.Get("Authorization"))
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		require.NoError(t, json.Unmarshal(raw, &body))
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, `{"error":{"message":"upstream busy"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, otokPelicanSSEBody(fmt.Sprint(body["model"])))
	}))
	defer gateway.Close()

	users := &otokPelicanUserRepoStub{users: map[int64]*User{
		dbUser.ID: {ID: dbUser.ID, Email: dbUser.Email, Status: StatusActive, Balance: 100},
	}, byEmail: map[string]*User{}}
	settings := &otokPelicanSettingRepoStub{values: map[string]string{
		settingKeyOtokPelicanProbeUserID: fmt.Sprint(dbUser.ID),
		SettingKeyOtokPelican:            `{"enabled":true,"default_models":["gpt-6-astra"],"keep_rounds":1,"groups":[{"group_id":` + fmt.Sprint(group.ID) + `,"enabled":true,"models":["gpt-6-astra"]}]}`,
	}}
	groups := &otokPelicanGroupRepoStub{groups: map[int64]*Group{group.ID: {ID: group.ID, Platform: PlatformOpenAI, Status: StatusActive}}}
	rates := &otokPelicanRateRepoStub{rates: map[int64]float64{}}
	svc := NewOtokPelicanService(NewSettingService(settings, nil), db, nil, users, groups, rates, nil, nil, false)
	svc.baseURL = gateway.URL
	defer svc.Stop()

	waitDone := func() []OtokPelicanRound {
		var rounds []OtokPelicanRound
		require.Eventually(t, func() bool {
			rounds, err = svc.ListRounds(ctx, group.ID)
			require.NoError(t, err)
			return len(rounds) > 0 && rounds[0].FinishedAt != nil
		}, 30*time.Second, 100*time.Millisecond)
		return rounds
	}

	_, err = svc.RunNow(ctx, group.ID)
	require.NoError(t, err)
	rounds := waitDone()
	require.Len(t, rounds, 1)
	run := rounds[0].Runs[0]
	require.Equal(t, otokPelicanStatusSucceeded, run.Status, run.ErrorMessage)
	require.Equal(t, 2, run.Attempt)
	require.Equal(t, otokPelicanProbeRate, rates.rates[group.ID])

	detail, err := svc.GetRun(ctx, run.ID)
	require.NoError(t, err)
	require.Contains(t, detail.OutputHTML, "gpt-6-astra")
	require.True(t, strings.HasPrefix(detail.OutputHTML, "<!DOCTYPE html>"))

	// 第二轮成功后，keep_rounds=1 只留最新一轮
	_, err = svc.RunNow(ctx, group.ID)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		rs, err := svc.ListRounds(ctx, group.ID)
		require.NoError(t, err)
		return len(rs) == 1 && rs[0].ID != rounds[0].ID && rs[0].FinishedAt != nil
	}, 30*time.Second, 100*time.Millisecond)

	// 非 OpenAI 分组拒绝
	groups.groups[group.ID].Platform = PlatformAnthropic
	_, err = svc.RunNow(ctx, group.ID)
	require.NoError(t, err, "准备失败也落一轮失败记录")
	rs := waitDone()
	require.Equal(t, "provision_failed", rs[0].Runs[0].ErrorCode)
}

func otokPelicanSSEBody(model string) string {
	html := `<!DOCTYPE html><html><body><svg><text>` + model + `</text></svg></body></html>`
	delta, _ := json.Marshal(map[string]string{"type": "response.output_text.delta", "delta": "```html\n" + html + "\n```"})
	return "data: " + string(delta) + "\n\n" +
		`data: {"type":"response.completed","response":{"model":"` + model + `","usage":{"input_tokens":10,"output_tokens":20}}}` + "\n\n"
}
