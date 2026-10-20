package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

// otok: 「降智检测」（鹈鹕测试）调度。每分钟检查一次：到期的启用分组新建一轮，每个模型一条请求，
// 进程内按并发上限执行，临时失败重试 1 次，结束后按分组只保留最近 N 轮。

const (
	otokPelicanTick          = time.Minute
	otokPelicanStartDelay    = time.Minute // wire 阶段 HTTP 还没监听，第一轮延后
	otokPelicanLeaderKey     = "otok:pelican:scheduler:leader"
	otokPelicanLeaderTTL     = 2 * time.Minute
	otokPelicanStaleRunning  = 2*otokPelicanMaxTimeout*time.Second + 3*time.Minute
	otokPelicanStaleQueued   = 24 * time.Hour
	otokPelicanConfigTTL     = 30 * time.Second
	otokPelicanListLimit     = otokPelicanMaxKeepRounds + 5
	otokPelicanRetryMinDelay = 5 * time.Second
	otokPelicanRetryJitter   = 5 * time.Second
)

type otokPelicanJob struct {
	RoundID int64
	GroupID int64
	RunID   int64
	Model   string
	APIKey  string
	Prompt  string
	Effort  string
	Timeout time.Duration
	Keep    int
}

// OtokPelicanProbeStatus 是后台展示的内部账号状态。
type OtokPelicanProbeStatus struct {
	UserID     int64                         `json:"user_id"`
	UserEmail  string                        `json:"user_email"`
	UserStatus string                        `json:"user_status"`
	Balance    float64                       `json:"balance"`
	BaseURL    string                        `json:"base_url"`
	Disabled   bool                          `json:"disabled"`
	Error      string                        `json:"error"`
	Groups     []OtokPelicanGroupProbeStatus `json:"groups"`
}

// OtokPelicanGroupProbeStatus 是单个分组的 key 准备情况与最近一轮。
type OtokPelicanGroupProbeStatus struct {
	GroupID       int64      `json:"group_id"`
	HasKey        bool       `json:"has_key"`
	LastStartedAt *time.Time `json:"last_started_at"`
	Running       bool       `json:"running"`
	LastError     string     `json:"last_error"`
}

// OtokPelicanService 负责配置、内部账号、调度与结果查询。
type OtokPelicanService struct {
	settingRepo  SettingRepository
	db           *sql.DB
	userRepo     UserRepository
	groupRepo    GroupRepository
	rateRepo     UserGroupRateRepository
	apiKeys      *APIKeyService
	billingCache *BillingCacheService
	lockCache    LeaderLockCache
	instanceID   string
	baseURL      string
	client       *http.Client
	disabled     bool
	startedAt    time.Time
	now          func() time.Time

	cfgMu     sync.Mutex
	cfgCache  *OtokPelicanConfig
	cfgLoaded time.Time

	provisionMu sync.Mutex
	errMu       sync.Mutex
	groupErrors map[int64]string

	queueMu     sync.Mutex
	queue       []otokPelicanJob
	active      int
	concurrency atomic.Int32

	runCtx    context.Context
	runCancel context.CancelFunc
	stopCh    chan struct{}
	stopOnce  sync.Once
	wg        sync.WaitGroup
}

// NewOtokPelicanService 创建服务；disabled 为 true 时不启动调度（查询和配置照常可用）。
func NewOtokPelicanService(
	settings *SettingService,
	db *sql.DB,
	cfg *config.Config,
	userRepo UserRepository,
	groupRepo GroupRepository,
	rateRepo UserGroupRateRepository,
	apiKeys *APIKeyService,
	billingCache *BillingCacheService,
	disabled bool,
) *OtokPelicanService {
	var repo SettingRepository
	if settings != nil {
		repo = settings.settingRepo
	}
	runCtx, runCancel := context.WithCancel(context.Background())
	s := &OtokPelicanService{
		settingRepo:  repo,
		db:           db,
		userRepo:     userRepo,
		groupRepo:    groupRepo,
		rateRepo:     rateRepo,
		apiKeys:      apiKeys,
		billingCache: billingCache,
		instanceID:   uuid.NewString(),
		baseURL:      otokPelicanBaseURL(cfg),
		client:       newOtokPelicanHTTPClient(),
		disabled:     disabled,
		startedAt:    time.Now(),
		now:          time.Now,
		groupErrors:  map[int64]string{},
		runCtx:       runCtx,
		runCancel:    runCancel,
		stopCh:       make(chan struct{}),
	}
	s.concurrency.Store(otokPelicanDefaultConcurrency)
	return s
}

// SetLeaderLock 注入多实例选主所需的锁。
func (s *OtokPelicanService) SetLeaderLock(lockCache LeaderLockCache) {
	if s != nil {
		s.lockCache = lockCache
	}
}

// Start 启动调度循环。
func (s *OtokPelicanService) Start() {
	if s == nil || s.disabled || s.db == nil {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		select {
		case <-time.After(otokPelicanStartDelay):
		case <-s.stopCh:
			return
		}
		ticker := time.NewTicker(otokPelicanTick)
		defer ticker.Stop()
		s.tick()
		for {
			select {
			case <-ticker.C:
				s.tick()
			case <-s.stopCh:
				return
			}
		}
	}()
}

// Stop 停止调度，取消进行中的请求并等待记录写完。
func (s *OtokPelicanService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		close(s.stopCh)
		s.runCancel()
	})
	s.queueMu.Lock()
	pending := s.queue
	s.queue = nil
	s.queueMu.Unlock()
	if len(pending) > 0 && s.db != nil {
		s.failJobs(pending, "shutdown", "service stopped before the probe started")
	}
	s.wg.Wait()
}

func (s *OtokPelicanService) stopped() bool {
	select {
	case <-s.stopCh:
		return true
	default:
		return false
	}
}

// ---------- 配置 ----------

// GetConfig 返回配置（带 30 秒缓存，保存时立即刷新）。
func (s *OtokPelicanService) GetConfig(ctx context.Context) (OtokPelicanConfig, error) {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	if s.cfgCache != nil && s.now().Sub(s.cfgLoaded) < otokPelicanConfigTTL {
		return *s.cfgCache, nil
	}
	cfg, err := loadOtokPelicanConfig(ctx, s.settingRepo)
	if err != nil {
		return OtokPelicanConfig{}, err
	}
	s.cfgCache, s.cfgLoaded = &cfg, s.now()
	return cfg, nil
}

func (s *OtokPelicanService) freshConfig(ctx context.Context) (OtokPelicanConfig, error) {
	s.cfgMu.Lock()
	s.cfgCache = nil
	s.cfgMu.Unlock()
	return s.GetConfig(ctx)
}

// SaveConfig 校验后整组替换。启用的分组必须是 OpenAI、启用中、非订阅型。
func (s *OtokPelicanService) SaveConfig(ctx context.Context, in OtokPelicanConfig) (OtokPelicanConfig, error) {
	cfg, err := NormalizeOtokPelicanConfig(in)
	if err != nil {
		return OtokPelicanConfig{}, err
	}
	for _, g := range cfg.Groups {
		if !g.Enabled {
			continue
		}
		if _, err := s.probeGroup(ctx, g.GroupID); err != nil {
			return OtokPelicanConfig{}, err
		}
	}
	if s.settingRepo == nil {
		return OtokPelicanConfig{}, infraerrors.ServiceUnavailable("OTOK_PELICAN_UNAVAILABLE", "setting storage unavailable")
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return OtokPelicanConfig{}, err
	}
	if err := s.settingRepo.Set(ctx, SettingKeyOtokPelican, string(b)); err != nil {
		return OtokPelicanConfig{}, err
	}
	s.cfgMu.Lock()
	s.cfgCache, s.cfgLoaded = &cfg, s.now()
	s.cfgMu.Unlock()
	return cfg, nil
}

// probeGroup 取分组并确认可以做鹈鹕测试。
func (s *OtokPelicanService) probeGroup(ctx context.Context, groupID int64) (*Group, error) {
	if s.groupRepo == nil {
		return nil, infraerrors.ServiceUnavailable("OTOK_PELICAN_UNAVAILABLE", "group repository unavailable")
	}
	g, err := s.groupRepo.GetByID(ctx, groupID)
	if err != nil || g == nil {
		return nil, otokPelicanInvalid("GROUP_NOT_FOUND", fmt.Sprintf("group %d not found", groupID))
	}
	if g.Platform != PlatformOpenAI {
		return nil, otokPelicanInvalid("GROUP_NOT_OPENAI", fmt.Sprintf("group %d is not an OpenAI group", groupID))
	}
	if !g.IsActive() {
		return nil, otokPelicanInvalid("GROUP_INACTIVE", fmt.Sprintf("group %d is not active", groupID))
	}
	if g.IsSubscriptionType() {
		return nil, otokPelicanInvalid("GROUP_SUBSCRIPTION", fmt.Sprintf("group %d is a subscription group", groupID))
	}
	return g, nil
}

// EnabledGroupIDs 返回启用了鹈鹕测试的分组；总开关关闭时为空。
func (s *OtokPelicanService) EnabledGroupIDs(ctx context.Context) ([]int64, OtokPelicanConfig, error) {
	cfg, err := s.GetConfig(ctx)
	if err != nil {
		return nil, OtokPelicanConfig{}, err
	}
	groups := cfg.EnabledGroups()
	ids := make([]int64, 0, len(groups))
	for _, g := range groups {
		ids = append(ids, g.GroupID)
	}
	return ids, cfg, nil
}

// ---------- 查询 ----------

// ListRounds 返回分组最近的轮次（不含生成内容）。
func (s *OtokPelicanService) ListRounds(ctx context.Context, groupID int64) ([]OtokPelicanRound, error) {
	if s.db == nil {
		return []OtokPelicanRound{}, nil
	}
	return otokPelicanListRounds(ctx, s.db, groupID, otokPelicanListLimit)
}

// GetRun 返回单条记录（含生成内容）。
func (s *OtokPelicanService) GetRun(ctx context.Context, runID int64) (*OtokPelicanRunDetail, error) {
	if s.db == nil {
		return nil, ErrOtokPelicanRunNotFound
	}
	return otokPelicanGetRun(ctx, s.db, runID)
}

// ProbeStatus 返回内部账号、余额和各启用分组的 key 情况。
func (s *OtokPelicanService) ProbeStatus(ctx context.Context) (OtokPelicanProbeStatus, error) {
	st := OtokPelicanProbeStatus{BaseURL: s.baseURL, Disabled: s.disabled, Groups: []OtokPelicanGroupProbeStatus{}}
	cfg, err := s.GetConfig(ctx)
	if err != nil {
		return st, err
	}
	var user *User
	if s.settingRepo != nil && s.userRepo != nil {
		if raw, err := s.settingRepo.GetValue(ctx, settingKeyOtokPelicanProbeUserID); err == nil {
			if id, perr := strconv.ParseInt(raw, 10, 64); perr == nil && id > 0 {
				user, _ = s.userRepo.GetByID(ctx, id)
			}
		}
	}
	if user != nil {
		st.UserID, st.UserEmail, st.UserStatus, st.Balance = user.ID, user.Email, user.Status, user.Balance
	}
	ids := make([]int64, 0, len(cfg.Groups))
	for _, g := range cfg.Groups {
		ids = append(ids, g.GroupID)
	}
	states := map[int64]otokPelicanGroupState{}
	if s.db != nil {
		states, _ = otokPelicanGroupStates(ctx, s.db, ids)
	}
	s.errMu.Lock()
	st.Error = s.groupErrors[0]
	s.errMu.Unlock()
	for _, gid := range ids {
		gs := OtokPelicanGroupProbeStatus{GroupID: gid}
		if user != nil {
			if key, err := s.lookupProbeKey(ctx, user.ID, gid); err == nil {
				gs.HasKey = key != ""
			}
		}
		if state, ok := states[gid]; ok {
			t := state.LastStartedAt
			gs.LastStartedAt, gs.Running = &t, state.Unfinished
		}
		s.errMu.Lock()
		gs.LastError = s.groupErrors[gid]
		s.errMu.Unlock()
		st.Groups = append(st.Groups, gs)
	}
	return st, nil
}

func (s *OtokPelicanService) setGroupError(groupID int64, msg string) {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	if msg == "" {
		delete(s.groupErrors, groupID)
	} else {
		s.groupErrors[groupID] = msg
	}
}

// ---------- 调度 ----------

func (s *OtokPelicanService) tick() {
	if s.stopped() {
		return
	}
	ctx, cancel := context.WithTimeout(s.runCtx, 30*time.Second)
	defer cancel()

	if n, err := otokPelicanFailStale(ctx, s.db, otokPelicanStaleRunning, otokPelicanStaleQueued, s.startedAt); err != nil {
		slog.Warn("[OtokPelican] fail stale runs failed", "error", err)
	} else if n > 0 {
		slog.Info("[OtokPelican] marked stale runs as interrupted", "count", n)
	}

	cfg, err := s.freshConfig(ctx)
	if err != nil {
		slog.Warn("[OtokPelican] load config failed", "error", err)
		return
	}
	s.concurrency.Store(int32(cfg.Concurrency))
	groups := cfg.EnabledGroups()
	if len(groups) == 0 {
		return
	}

	release, ok := tryAcquireSingletonLeaderLock(ctx, s.lockCache, s.db, otokPelicanLeaderKey, s.instanceID, otokPelicanLeaderTTL)
	if !ok {
		return
	}
	defer release()

	ids := make([]int64, 0, len(groups))
	for _, g := range groups {
		ids = append(ids, g.GroupID)
	}
	states, err := otokPelicanGroupStates(ctx, s.db, ids)
	if err != nil {
		slog.Warn("[OtokPelican] load group states failed", "error", err)
		return
	}
	interval := time.Duration(cfg.IntervalMinutes) * time.Minute
	now := s.now()
	for _, g := range groups {
		if st, ok := states[g.GroupID]; ok && (st.Unfinished || now.Sub(st.LastStartedAt) < interval) {
			continue
		}
		if _, err := s.startRound(ctx, cfg, g, otokPelicanTriggerSchedule); err != nil {
			slog.Warn("[OtokPelican] start round failed", "group_id", g.GroupID, "error", err)
		}
	}
	s.dispatch()
}

// RunNow 立即为分组跑一轮（分组可以未启用，便于启用前先试）。已有进行中的轮次时拒绝。
func (s *OtokPelicanService) RunNow(ctx context.Context, groupID int64) (int64, error) {
	if s.disabled || s.db == nil {
		return 0, infraerrors.ServiceUnavailable("OTOK_PELICAN_DISABLED", "pelican probe is disabled on this instance")
	}
	cfg, err := s.freshConfig(ctx)
	if err != nil {
		return 0, err
	}
	gc := OtokPelicanGroupConfig{GroupID: groupID, Enabled: true}
	for _, g := range cfg.Groups {
		if g.GroupID == groupID {
			gc.Models = g.Models
		}
	}
	release, ok := tryAcquireSingletonLeaderLock(ctx, s.lockCache, s.db, otokPelicanLeaderKey, s.instanceID, otokPelicanLeaderTTL)
	if !ok {
		return 0, infraerrors.Conflict("OTOK_PELICAN_BUSY", "scheduler is busy, please retry in a moment")
	}
	defer release()
	states, err := otokPelicanGroupStates(ctx, s.db, []int64{groupID})
	if err != nil {
		return 0, err
	}
	if st, ok := states[groupID]; ok && st.Unfinished {
		return 0, infraerrors.Conflict("OTOK_PELICAN_RUNNING", "a round is already running for this group")
	}
	s.concurrency.Store(int32(cfg.Concurrency))
	roundID, err := s.startRound(ctx, cfg, gc, otokPelicanTriggerManual)
	if err != nil {
		return 0, err
	}
	s.dispatch()
	return roundID, nil
}

// startRound 准备内部账号与 key，新建一轮并把请求放进队列。准备失败时也落一轮失败记录，用户侧可见、并按间隔重试。
func (s *OtokPelicanService) startRound(ctx context.Context, cfg OtokPelicanConfig, g OtokPelicanGroupConfig, trigger string) (int64, error) {
	models := cfg.ModelsFor(g)
	key, provisionErr := s.provision(ctx, g.GroupID)
	roundID, refs, err := otokPelicanCreateRound(ctx, s.db, g.GroupID, trigger, cfg.ReasoningEffort, models)
	if err != nil {
		return 0, err
	}
	if provisionErr != nil {
		slog.Warn("[OtokPelican] provision failed", "group_id", g.GroupID, "error", provisionErr)
		for _, r := range refs {
			_ = otokPelicanFailRun(ctx, s.db, r.ID, "provision_failed", truncateOtokPelicanRunes(provisionErr.Error(), otokPelicanMaxErrorRunes))
		}
		_ = otokPelicanFinishRoundIfDone(ctx, s.db, roundID)
		_, _ = otokPelicanPrune(ctx, s.db, g.GroupID, cfg.KeepRounds)
		return roundID, nil
	}
	jobs := make([]otokPelicanJob, 0, len(refs))
	for _, r := range refs {
		jobs = append(jobs, otokPelicanJob{
			RoundID: roundID, GroupID: g.GroupID, RunID: r.ID, Model: r.Model, APIKey: key,
			Prompt: cfg.Prompt, Effort: cfg.ReasoningEffort,
			Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second, Keep: cfg.KeepRounds,
		})
	}
	s.queueMu.Lock()
	s.queue = append(s.queue, jobs...)
	s.queueMu.Unlock()
	return roundID, nil
}

func (s *OtokPelicanService) provision(ctx context.Context, groupID int64) (string, error) {
	s.provisionMu.Lock()
	defer s.provisionMu.Unlock()
	group, err := s.probeGroup(ctx, groupID)
	if err != nil {
		s.setGroupError(groupID, err.Error())
		return "", err
	}
	user, err := s.ensureProbeUser(ctx)
	if err != nil {
		s.setGroupError(0, err.Error())
		return "", err
	}
	s.setGroupError(0, "")
	key, err := s.ensureProbeKey(ctx, user, group)
	if err != nil {
		s.setGroupError(groupID, err.Error())
		return "", err
	}
	s.setGroupError(groupID, "")
	return key, nil
}

// dispatch 在并发上限内从队列取任务执行。
func (s *OtokPelicanService) dispatch() {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	limit := int(s.concurrency.Load())
	if limit < 1 {
		limit = 1
	}
	for s.active < limit && len(s.queue) > 0 && !s.stopped() {
		job := s.queue[0]
		s.queue = s.queue[1:]
		s.active++
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.execute(job)
			s.queueMu.Lock()
			s.active--
			s.queueMu.Unlock()
			s.dispatch()
		}()
	}
}

func (s *OtokPelicanService) failJobs(jobs []otokPelicanJob, code, msg string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, j := range jobs {
		_ = otokPelicanFailRun(ctx, s.db, j.RunID, code, msg)
		_ = otokPelicanFinishRoundIfDone(ctx, s.db, j.RoundID)
	}
}

func (s *OtokPelicanService) execute(job otokPelicanJob) {
	// 写库用独立 ctx：停机时请求被取消，结果仍要落库。
	dbCtx := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), 10*time.Second)
	}
	var res otokPelicanProbeResult
	attempt, ran := 0, false
	for attempt < 2 {
		if s.runCtx.Err() != nil {
			break
		}
		attempt++
		ran = true
		c, cancel := dbCtx()
		if err := otokPelicanMarkRunning(c, s.db, job.RunID, attempt); err != nil {
			slog.Warn("[OtokPelican] mark running failed", "run_id", job.RunID, "error", err)
		}
		cancel()
		reqCtx, reqCancel := context.WithTimeout(s.runCtx, job.Timeout)
		res = doOtokPelicanProbe(reqCtx, s.client, s.baseURL, job.APIKey, otokPelicanProbeRequest{
			Model: job.Model, Prompt: job.Prompt, ReasoningEffort: job.Effort,
			RequestID: fmt.Sprintf("otok-pelican-%d-%d", job.RunID, attempt),
		})
		reqCancel()
		if res.ok() || !res.Retryable || attempt >= 2 {
			break
		}
		delay := otokPelicanRetryMinDelay + rand.N(otokPelicanRetryJitter)
		select {
		case <-time.After(delay):
		case <-s.runCtx.Done():
		}
	}

	c, cancel := dbCtx()
	defer cancel()
	if s.runCtx.Err() != nil && (!ran || !res.ok()) {
		_ = otokPelicanFailRun(c, s.db, job.RunID, "shutdown", "service stopped while the probe was running")
	} else if err := otokPelicanFinishRun(c, s.db, job.RunID, attempt, res, extractOtokPelicanHTML(res.Text)); err != nil {
		slog.Warn("[OtokPelican] save run failed", "run_id", job.RunID, "error", err)
	}
	if !res.ok() {
		slog.Info("[OtokPelican] probe failed", "group_id", job.GroupID, "model", job.Model, "attempt", attempt,
			"code", res.ErrCode, "message", truncateOtokPelicanRunes(res.ErrMessage, 300))
	}
	if err := otokPelicanFinishRoundIfDone(c, s.db, job.RoundID); err != nil {
		slog.Warn("[OtokPelican] finish round failed", "round_id", job.RoundID, "error", err)
	}
	if _, err := otokPelicanPrune(c, s.db, job.GroupID, job.Keep); err != nil {
		slog.Warn("[OtokPelican] prune failed", "group_id", job.GroupID, "error", err)
	}
}
