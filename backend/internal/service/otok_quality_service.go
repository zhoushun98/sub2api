package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

// otok: 「降智检测」调度。每分钟检查一次：到期的启用条目新建一轮，每个模型一条请求，
// 进程内按并发上限执行，临时失败重试 1 次，结束后按条目只保留最近 N 轮。

const (
	otokQualityTick          = time.Minute
	otokQualityStartDelay    = time.Minute // wire 阶段 HTTP 还没监听，第一轮延后（测本站分组走的是环回）
	otokQualityLeaderKey     = "otok:quality:scheduler:leader"
	otokQualityLeaderTTL     = 2 * time.Minute
	otokQualityStaleRunning  = 2*otokQualityMaxTimeout*time.Second + 3*time.Minute
	otokQualityStaleQueued   = 24 * time.Hour
	otokQualityConfigTTL     = 30 * time.Second
	otokQualityListLimit     = otokQualityMaxKeepRounds + 5
	otokQualityRetryMinDelay = 5 * time.Second
	otokQualityRetryJitter   = 5 * time.Second
)

type otokQualityJob struct {
	RoundID   int64
	ItemID    int64
	RunID     int64
	Model     string
	URL       string
	Protocol  string
	APIKey    string
	Prompt    string
	Effort    string
	ExtraBody string
	Timeout   time.Duration
	Keep      int
}

// OtokQualityItemStatus 是条目最近一轮的情况。
type OtokQualityItemStatus struct {
	ItemID        int64      `json:"item_id"`
	LastStartedAt *time.Time `json:"last_started_at"`
	Running       bool       `json:"running"`
}

// OtokQualityStatus 是后台 GET /status 的返回体。
type OtokQualityStatus struct {
	Disabled bool                    `json:"disabled"`
	Items    []OtokQualityItemStatus `json:"items"`
}

// OtokQualityService 负责配置、调度与结果查询。
type OtokQualityService struct {
	settingRepo SettingRepository
	db          *sql.DB
	cfg         *config.Config
	encryptor   SecretEncryptor
	lockCache   LeaderLockCache
	instanceID  string
	client      *http.Client
	disabled    bool
	startedAt   time.Time
	now         func() time.Time

	cfgMu      sync.Mutex
	cfgCache   *OtokQualityConfig
	cfgInvalid bool
	cfgLoaded  time.Time

	saveMu sync.Mutex

	queueMu     sync.Mutex
	queue       []otokQualityJob
	active      int
	concurrency atomic.Int32

	runCtx    context.Context
	runCancel context.CancelFunc
	stopCh    chan struct{}
	stopOnce  sync.Once
	wg        sync.WaitGroup
}

// NewOtokQualityService 创建服务；disabled 为 true 时不启动调度（查询和配置照常可用）。
func NewOtokQualityService(settings *SettingService, db *sql.DB, cfg *config.Config, encryptor SecretEncryptor, disabled bool) *OtokQualityService {
	var repo SettingRepository
	if settings != nil {
		repo = settings.settingRepo
	}
	runCtx, runCancel := context.WithCancel(context.Background())
	s := &OtokQualityService{
		settingRepo: repo,
		db:          db,
		cfg:         cfg,
		encryptor:   encryptor,
		instanceID:  uuid.NewString(),
		client:      newOtokQualityHTTPClient(),
		disabled:    disabled,
		startedAt:   time.Now(),
		now:         time.Now,
		runCtx:      runCtx,
		runCancel:   runCancel,
		stopCh:      make(chan struct{}),
	}
	s.concurrency.Store(otokQualityDefaultConcurrency)
	return s
}

// SetLeaderLock 注入多实例选主所需的锁。
func (s *OtokQualityService) SetLeaderLock(lockCache LeaderLockCache) {
	if s != nil {
		s.lockCache = lockCache
	}
}

// Start 启动调度循环。
func (s *OtokQualityService) Start() {
	if s == nil || s.disabled || s.db == nil {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		select {
		case <-time.After(otokQualityStartDelay):
		case <-s.stopCh:
			return
		}
		ticker := time.NewTicker(otokQualityTick)
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
func (s *OtokQualityService) Stop() {
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
		s.failJobs(pending, "shutdown", "service stopped before the request started")
	}
	s.wg.Wait()
}

func (s *OtokQualityService) stopped() bool {
	select {
	case <-s.stopCh:
		return true
	default:
		return false
	}
}

// ---------- 配置 ----------

// GetConfig 返回配置（带 30 秒缓存，保存时立即刷新）；第二个返回值表示已存内容无法读取、用的是默认值。
func (s *OtokQualityService) GetConfig(ctx context.Context) (OtokQualityConfig, bool, error) {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	if s.cfgCache != nil && s.now().Sub(s.cfgLoaded) < otokQualityConfigTTL {
		return *s.cfgCache, s.cfgInvalid, nil
	}
	cfg, invalid, err := loadOtokQualityConfig(ctx, s.settingRepo)
	if err != nil {
		return OtokQualityConfig{}, false, err
	}
	s.cfgCache, s.cfgInvalid, s.cfgLoaded = &cfg, invalid, s.now()
	return cfg, invalid, nil
}

func (s *OtokQualityService) freshConfig(ctx context.Context) (OtokQualityConfig, bool, error) {
	s.cfgMu.Lock()
	s.cfgCache = nil
	s.cfgMu.Unlock()
	return s.GetConfig(ctx)
}

// AdminConfig 返回后台回显用的配置。
func (s *OtokQualityService) AdminConfig(ctx context.Context) (OtokQualityAdminConfig, error) {
	cfg, invalid, err := s.freshConfig(ctx)
	if err != nil {
		return OtokQualityAdminConfig{}, err
	}
	return s.adminView(cfg, invalid), nil
}

func (s *OtokQualityService) adminView(cfg OtokQualityConfig, invalid bool) OtokQualityAdminConfig {
	out := OtokQualityAdminConfig{
		Version:         cfg.Version,
		Invalid:         invalid,
		Enabled:         cfg.Enabled,
		IntervalMinutes: cfg.IntervalMinutes,
		TimeoutSeconds:  cfg.TimeoutSeconds,
		Concurrency:     cfg.Concurrency,
		KeepRounds:      cfg.KeepRounds,
		Items:           make([]OtokQualityAdminItem, 0, len(cfg.Items)),
	}
	for _, it := range cfg.Items {
		ai := OtokQualityAdminItem{
			ID:              it.ID,
			Name:            it.Name,
			Enabled:         it.Enabled,
			URL:             it.URL,
			Protocol:        it.Protocol(),
			HasAPIKey:       it.APIKeyEnc != "",
			Models:          append([]string{}, it.Models...),
			ReasoningEffort: it.ReasoningEffort,
			Prompt:          it.Prompt,
			ExtraBody:       it.ExtraBody,
		}
		if ai.HasAPIKey {
			if plain, err := s.decryptKey(it.APIKeyEnc); err != nil {
				ai.APIKeyDecryptFailed = true
			} else {
				ai.APIKeyMasked = otokQualityMaskKey(plain)
			}
		}
		out.Items = append(out.Items, ai)
	}
	return out
}

func (s *OtokQualityService) decryptKey(enc string) (string, error) {
	if s.encryptor == nil {
		return "", infraerrors.ServiceUnavailable("OTOK_QUALITY_UNAVAILABLE", "secret encryptor unavailable")
	}
	return s.encryptor.Decrypt(enc)
}

func (s *OtokQualityService) encryptionKeyConfigured() bool {
	return s.cfg != nil && s.cfg.Totp.EncryptionKeyConfigured
}

// SaveConfig 校验后整组替换，并删掉不再保留的条目的结果。
func (s *OtokQualityService) SaveConfig(ctx context.Context, in OtokQualityConfigInput) (OtokQualityAdminConfig, error) {
	if s.settingRepo == nil {
		return OtokQualityAdminConfig{}, infraerrors.ServiceUnavailable("OTOK_QUALITY_UNAVAILABLE", "setting storage unavailable")
	}
	s.saveMu.Lock()
	defer s.saveMu.Unlock()

	old, _, err := loadOtokQualityConfig(ctx, s.settingRepo)
	if err != nil {
		return OtokQualityAdminConfig{}, err
	}
	if in.Version != old.Version {
		return OtokQualityAdminConfig{}, infraerrors.Conflict("OTOK_QUALITY_CONFIG_CONFLICT", "config was modified elsewhere, please reload")
	}

	next := OtokQualityConfig{
		Version:         old.Version + 1,
		Enabled:         in.Enabled,
		IntervalMinutes: in.IntervalMinutes,
		TimeoutSeconds:  in.TimeoutSeconds,
		Concurrency:     in.Concurrency,
		KeepRounds:      in.KeepRounds,
		NextItemID:      old.NextItemID,
	}
	if next.NextItemID < 1 {
		next.NextItemID = 1
	}
	if err := normalizeOtokQualityGlobals(&next); err != nil {
		return OtokQualityAdminConfig{}, err
	}
	if len(in.Items) > otokQualityMaxItems {
		return OtokQualityAdminConfig{}, otokQualityInvalid("TOO_MANY_ITEMS", "at most 50 items")
	}

	// 只保留「保存前就有、这次仍保留」的 id：配置被重置后新分配的 id 可能与旧结果撞号，不能接上旧结果。
	keep := make([]int64, 0, len(in.Items))
	seen := map[int64]bool{}
	next.Items = make([]OtokQualityItem, 0, len(in.Items))
	for _, ii := range in.Items {
		it := OtokQualityItem{
			ID:              ii.ID,
			Name:            ii.Name,
			Enabled:         ii.Enabled,
			URL:             ii.URL,
			Models:          ii.Models,
			ReasoningEffort: ii.ReasoningEffort,
			Prompt:          ii.Prompt,
			ExtraBody:       ii.ExtraBody,
		}
		var prev OtokQualityItem
		if ii.ID != 0 {
			p, ok := old.ItemByID(ii.ID)
			if !ok || seen[ii.ID] {
				return OtokQualityAdminConfig{}, otokQualityInvalid("ITEM_NOT_FOUND", "item not found or duplicated; reload and try again")
			}
			seen[ii.ID] = true
			prev = p
		}
		if err := normalizeOtokQualityItem(&it); err != nil {
			return OtokQualityAdminConfig{}, err
		}
		key := ii.APIKey
		switch {
		case key != "":
			if err := validateOtokQualityAPIKey(key); err != nil {
				return OtokQualityAdminConfig{}, err
			}
			if !s.encryptionKeyConfigured() {
				return OtokQualityAdminConfig{}, otokQualityInvalid("ENCRYPTION_KEY_REQUIRED", "TOTP_ENCRYPTION_KEY must be configured explicitly before saving api keys")
			}
			if s.encryptor == nil {
				return OtokQualityAdminConfig{}, infraerrors.ServiceUnavailable("OTOK_QUALITY_UNAVAILABLE", "secret encryptor unavailable")
			}
			enc, err := s.encryptor.Encrypt(key)
			if err != nil {
				return OtokQualityAdminConfig{}, infraerrors.InternalServer("OTOK_QUALITY_ENCRYPT_FAILED", "encrypt api key failed")
			}
			it.APIKeyEnc = enc
		case ii.ID == 0 || prev.APIKeyEnc == "":
			return OtokQualityAdminConfig{}, otokQualityInvalid("API_KEY_REQUIRED", "api_key is required for new items")
		default:
			it.APIKeyEnc = prev.APIKeyEnc
		}
		if ii.ID == 0 {
			it.ID = next.NextItemID
			next.NextItemID++
		} else {
			keep = append(keep, ii.ID)
		}
		next.Items = append(next.Items, it)
	}

	b, err := json.Marshal(next)
	if err != nil {
		return OtokQualityAdminConfig{}, err
	}
	if err := s.settingRepo.Set(ctx, SettingKeyOtokQuality, string(b)); err != nil {
		return OtokQualityAdminConfig{}, err
	}
	s.cfgMu.Lock()
	s.cfgCache, s.cfgInvalid, s.cfgLoaded = &next, false, s.now()
	s.cfgMu.Unlock()
	s.concurrency.Store(int32(next.Concurrency))
	s.dropQueuedJobs(keep)

	if s.db != nil {
		if n, err := otokQualityDeleteItemsExcept(ctx, s.db, keep); err != nil {
			slog.Warn("[OtokQuality] delete results of removed items failed", "error", err)
		} else if n > 0 {
			slog.Info("[OtokQuality] deleted results of removed items", "rounds", n)
		}
	}
	return s.adminView(next, false), nil
}

// dropQueuedJobs 丢掉已删除条目还没开始的请求（对应的记录随结果一起删除）。
func (s *OtokQualityService) dropQueuedJobs(keep []int64) {
	kept := make(map[int64]bool, len(keep))
	for _, id := range keep {
		kept[id] = true
	}
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	q := s.queue[:0]
	for _, j := range s.queue {
		if kept[j.ItemID] {
			q = append(q, j)
		}
	}
	s.queue = q
}

// EnabledItems 返回用户可见的条目（总开关开且条目启用）。
func (s *OtokQualityService) EnabledItems(ctx context.Context) ([]OtokQualityItem, OtokQualityConfig, error) {
	cfg, _, err := s.GetConfig(ctx)
	if err != nil {
		return nil, OtokQualityConfig{}, err
	}
	return cfg.EnabledItems(), cfg, nil
}

// ---------- 查询 ----------

// ListRounds 返回条目最近的轮次（不含生成内容）。
func (s *OtokQualityService) ListRounds(ctx context.Context, itemID int64) ([]OtokQualityAdminRound, error) {
	if s.db == nil {
		return []OtokQualityAdminRound{}, nil
	}
	return otokQualityListRounds(ctx, s.db, itemID, otokQualityListLimit)
}

// GetRun 返回单条记录（含生成内容）。
func (s *OtokQualityService) GetRun(ctx context.Context, runID int64) (*OtokQualityAdminRunDetail, error) {
	if s.db == nil {
		return nil, ErrOtokQualityRunNotFound
	}
	return otokQualityGetRun(ctx, s.db, runID)
}

// Status 返回各条目最近一轮的开始时间与是否进行中。
func (s *OtokQualityService) Status(ctx context.Context) (OtokQualityStatus, error) {
	st := OtokQualityStatus{Disabled: s.disabled, Items: []OtokQualityItemStatus{}}
	cfg, _, err := s.GetConfig(ctx)
	if err != nil {
		return st, err
	}
	ids := make([]int64, 0, len(cfg.Items))
	for _, it := range cfg.Items {
		ids = append(ids, it.ID)
	}
	states := map[int64]otokQualityItemState{}
	if s.db != nil {
		if states, err = otokQualityItemStates(ctx, s.db, ids); err != nil {
			return st, err
		}
	}
	for _, id := range ids {
		is := OtokQualityItemStatus{ItemID: id}
		if state, ok := states[id]; ok {
			t := state.LastStartedAt
			is.LastStartedAt, is.Running = &t, state.Unfinished
		}
		st.Items = append(st.Items, is)
	}
	return st, nil
}

// ---------- 调度 ----------

func (s *OtokQualityService) tick() {
	if s.stopped() {
		return
	}
	ctx, cancel := context.WithTimeout(s.runCtx, 30*time.Second)
	defer cancel()

	if n, err := otokQualityFailStale(ctx, s.db, otokQualityStaleRunning, otokQualityStaleQueued, s.startedAt); err != nil {
		slog.Warn("[OtokQuality] fail stale runs failed", "error", err)
	} else if n > 0 {
		slog.Info("[OtokQuality] marked stale runs as interrupted", "count", n)
	}

	cfg, _, err := s.freshConfig(ctx)
	if err != nil {
		slog.Warn("[OtokQuality] load config failed", "error", err)
		return
	}
	s.concurrency.Store(int32(cfg.Concurrency))
	items := cfg.EnabledItems()
	if len(items) == 0 {
		return
	}

	release, ok := tryAcquireSingletonLeaderLock(ctx, s.lockCache, s.db, otokQualityLeaderKey, s.instanceID, otokQualityLeaderTTL)
	if !ok {
		return
	}
	defer release()

	ids := make([]int64, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	states, err := otokQualityItemStates(ctx, s.db, ids)
	if err != nil {
		slog.Warn("[OtokQuality] load item states failed", "error", err)
		return
	}
	interval := time.Duration(cfg.IntervalMinutes) * time.Minute
	now := s.now()
	for _, it := range items {
		if st, ok := states[it.ID]; ok && (st.Unfinished || now.Sub(st.LastStartedAt) < interval) {
			continue
		}
		if _, err := s.startRound(ctx, cfg, it, otokQualityTriggerSchedule); err != nil {
			slog.Warn("[OtokQuality] start round failed", "item_id", it.ID, "error", err)
		}
	}
	s.dispatch()
}

// RunNow 立即为条目跑一轮（条目可以未启用、总开关可以关着，便于启用前先试）。已有进行中的轮次时拒绝。
func (s *OtokQualityService) RunNow(ctx context.Context, itemID int64) (int64, error) {
	if s.disabled || s.db == nil {
		return 0, infraerrors.ServiceUnavailable("OTOK_QUALITY_DISABLED", "quality check is disabled on this instance")
	}
	cfg, _, err := s.freshConfig(ctx)
	if err != nil {
		return 0, err
	}
	item, ok := cfg.ItemByID(itemID)
	if !ok {
		return 0, infraerrors.NotFound("OTOK_QUALITY_ITEM_NOT_FOUND", "item not found")
	}
	release, ok := tryAcquireSingletonLeaderLock(ctx, s.lockCache, s.db, otokQualityLeaderKey, s.instanceID, otokQualityLeaderTTL)
	if !ok {
		return 0, infraerrors.Conflict("OTOK_QUALITY_BUSY", "scheduler is busy, please retry in a moment")
	}
	defer release()
	states, err := otokQualityItemStates(ctx, s.db, []int64{itemID})
	if err != nil {
		return 0, err
	}
	if st, ok := states[itemID]; ok && st.Unfinished {
		return 0, infraerrors.Conflict("OTOK_QUALITY_RUNNING", "a round is already running for this item")
	}
	s.concurrency.Store(int32(cfg.Concurrency))
	roundID, err := s.startRound(ctx, cfg, item, otokQualityTriggerManual)
	if err != nil {
		return 0, err
	}
	s.dispatch()
	return roundID, nil
}

// startRound 解密 key、新建一轮并把请求放进队列。解密失败时也落一轮失败记录（key_error），用户侧可见、并按间隔重试。
func (s *OtokQualityService) startRound(ctx context.Context, cfg OtokQualityConfig, item OtokQualityItem, trigger string) (int64, error) {
	protocol := item.Protocol()
	key, keyErr := s.decryptKey(item.APIKeyEnc)
	if keyErr == nil && key == "" {
		keyErr = infraerrors.BadRequest("OTOK_QUALITY_API_KEY_REQUIRED", "api key is empty")
	}
	roundID, refs, err := otokQualityCreateRound(ctx, s.db, item.ID, trigger, protocol, item.Prompt, item.Models)
	if err != nil {
		return 0, err
	}
	if keyErr != nil {
		slog.Warn("[OtokQuality] decrypt api key failed", "item_id", item.ID, "error", keyErr)
		for _, r := range refs {
			_ = otokQualityFailRun(ctx, s.db, r.ID, "key_error", "api key could not be decrypted; re-enter it in the admin page")
		}
		_ = otokQualityFinishRoundIfDone(ctx, s.db, roundID)
		_, _ = otokQualityPrune(ctx, s.db, item.ID, cfg.KeepRounds)
		return roundID, nil
	}
	jobs := make([]otokQualityJob, 0, len(refs))
	for _, r := range refs {
		jobs = append(jobs, otokQualityJob{
			RoundID: roundID, ItemID: item.ID, RunID: r.ID, Model: r.Model,
			URL: item.URL, Protocol: protocol, APIKey: key,
			Prompt: item.Prompt, Effort: item.ReasoningEffort, ExtraBody: item.ExtraBody,
			Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second, Keep: cfg.KeepRounds,
		})
	}
	s.queueMu.Lock()
	s.queue = append(s.queue, jobs...)
	s.queueMu.Unlock()
	return roundID, nil
}

// dispatch 在并发上限内从队列取任务执行。
func (s *OtokQualityService) dispatch() {
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

func (s *OtokQualityService) failJobs(jobs []otokQualityJob, code, msg string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, j := range jobs {
		_ = otokQualityFailRun(ctx, s.db, j.RunID, code, msg)
		_ = otokQualityFinishRoundIfDone(ctx, s.db, j.RoundID)
	}
}

func (s *OtokQualityService) execute(job otokQualityJob) {
	// 写库用独立 ctx：停机时请求被取消，结果仍要落库。
	dbCtx := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), 10*time.Second)
	}
	var res otokQualityResult
	attempt, ran := 0, false
	for attempt < 2 {
		if s.runCtx.Err() != nil {
			break
		}
		attempt++
		ran = true
		c, cancel := dbCtx()
		if err := otokQualityMarkRunning(c, s.db, job.RunID, attempt); err != nil {
			slog.Warn("[OtokQuality] mark running failed", "run_id", job.RunID, "error", err)
		}
		cancel()
		reqCtx, reqCancel := context.WithTimeout(s.runCtx, job.Timeout)
		res = doOtokQualityRequest(reqCtx, s.client, otokQualityRequest{
			URL: job.URL, Protocol: job.Protocol, APIKey: job.APIKey, Model: job.Model,
			Prompt: job.Prompt, ReasoningEffort: job.Effort, ExtraBody: job.ExtraBody,
		})
		reqCancel()
		if res.ok() || !res.Retryable || attempt >= 2 {
			break
		}
		delay := otokQualityRetryMinDelay + rand.N(otokQualityRetryJitter)
		select {
		case <-time.After(delay):
		case <-s.runCtx.Done():
		}
	}

	c, cancel := dbCtx()
	defer cancel()
	if s.runCtx.Err() != nil && (!ran || !res.ok()) {
		_ = otokQualityFailRun(c, s.db, job.RunID, "shutdown", "service stopped while the request was running")
	} else if err := otokQualityFinishRun(c, s.db, job.RunID, attempt, res, extractOtokQualityHTML(res.Text)); err != nil {
		slog.Warn("[OtokQuality] save run failed", "run_id", job.RunID, "error", err)
		// 兜底标成失败，否则记录一直是 running、轮次收不了尾。
		code := res.ErrCode
		if code == "" {
			code = "interrupted"
		}
		fc, fcancel := dbCtx()
		if err := otokQualityFailRun(fc, s.db, job.RunID, code, "result could not be saved"); err != nil {
			slog.Warn("[OtokQuality] mark run failed after save error failed", "run_id", job.RunID, "error", err)
		}
		fcancel()
	}
	if !res.ok() {
		slog.Info("[OtokQuality] request failed", "item_id", job.ItemID, "model", job.Model, "attempt", attempt,
			"code", res.ErrCode, "message", truncateOtokQualityRunes(res.ErrMessage, 300))
	}
	if err := otokQualityFinishRoundIfDone(c, s.db, job.RoundID); err != nil {
		slog.Warn("[OtokQuality] finish round failed", "round_id", job.RoundID, "error", err)
	}
	if _, err := otokQualityPrune(c, s.db, job.ItemID, job.Keep); err != nil {
		slog.Warn("[OtokQuality] prune failed", "item_id", job.ItemID, "error", err)
	}
}
