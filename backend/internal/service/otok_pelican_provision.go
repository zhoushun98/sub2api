package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// otok: 鹈鹕测试的内部账号。每个启用分组一把 API key，请求走网关环回，和真实用户一样调度、计费。
// 该用户在这些分组的专属倍率是 0.0001，实际扣费可忽略（U 不变）。

const (
	settingKeyOtokPelicanProbeUserID = "otok_pelican_probe_user_id"

	otokPelicanProbeEmail       = "pelican-probe@otok.internal"
	otokPelicanProbeUsername    = "pelican-probe"
	otokPelicanProbeNotes       = "otok 降智检测（鹈鹕测试）内部账号，自动创建，勿删改。专属倍率 0.0001，余额低于 $1 自动补到 $100。"
	otokPelicanProbeRate        = 0.0001
	otokPelicanProbeBalance     = 100.0
	otokPelicanProbeMinBalance  = 1.0
	otokPelicanProbeConcurrency = 8
	otokPelicanKeyNamePrefix    = "otok-pelican-"
)

// ensureProbeUser 取得（必要时创建）内部用户，保证余额充足，且用户级并发不低于 minConcurrency
// （网关按用户限制同时在途的请求数，低于配置的并发会让多出的测试请求被拒或排队）。
func (s *OtokPelicanService) ensureProbeUser(ctx context.Context, minConcurrency int) (*User, error) {
	wantConcurrency := max(otokPelicanProbeConcurrency, minConcurrency)
	if s.userRepo == nil {
		return nil, errors.New("user repository unavailable")
	}
	var user *User
	if s.settingRepo != nil {
		if raw, err := s.settingRepo.GetValue(ctx, settingKeyOtokPelicanProbeUserID); err == nil {
			if id, perr := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); perr == nil && id > 0 {
				if u, gerr := s.userRepo.GetByID(ctx, id); gerr == nil && u != nil {
					user = u
				}
			}
		}
	}
	if user == nil {
		if u, err := s.userRepo.GetByEmail(ctx, otokPelicanProbeEmail); err == nil && u != nil {
			user = u
		}
	}
	if user == nil {
		created, err := s.createProbeUser(ctx, wantConcurrency)
		if err != nil {
			return nil, err
		}
		user = created
	}
	if s.settingRepo != nil {
		_ = s.settingRepo.Set(ctx, settingKeyOtokPelicanProbeUserID, strconv.FormatInt(user.ID, 10))
	}
	if !user.IsActive() {
		// 管理员手动禁用了内部账号，视为有意停用，不自动恢复。
		return nil, fmt.Errorf("probe user %d is %s", user.ID, user.Status)
	}
	if user.Concurrency < wantConcurrency {
		if err := s.userRepo.UpdateConcurrency(ctx, user.ID, wantConcurrency-user.Concurrency); err != nil {
			return nil, fmt.Errorf("raise probe user concurrency: %w", err)
		}
		slog.Info("[OtokPelican] raised probe user concurrency", "user_id", user.ID, "from", user.Concurrency, "to", wantConcurrency)
		user.Concurrency = wantConcurrency
		s.invalidateProbeUserCaches(ctx, user.ID)
	}
	if user.Balance < otokPelicanProbeMinBalance {
		change, err := s.userRepo.AdjustBalance(ctx, user.ID, otokPelicanProbeBalance-user.Balance)
		if err != nil {
			return nil, fmt.Errorf("top up probe user: %w", err)
		}
		user.Balance = change.New
		s.invalidateProbeUserCaches(ctx, user.ID)
		slog.Info("[OtokPelican] topped up probe user balance", "user_id", user.ID, "balance", user.Balance)
	}
	return user, nil
}

func (s *OtokPelicanService) createProbeUser(ctx context.Context, concurrency int) (*User, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	user := &User{
		Email:       otokPelicanProbeEmail,
		Username:    otokPelicanProbeUsername,
		Notes:       otokPelicanProbeNotes,
		Role:        RoleUser,
		Balance:     otokPelicanProbeBalance,
		Concurrency: concurrency,
		Status:      StatusActive,
	}
	// 随机密码不落地，没人能登录这个账号。
	if err := user.SetPassword(hex.EncodeToString(buf)); err != nil {
		return nil, err
	}
	if err := s.userRepo.Create(ctx, user); err != nil {
		return nil, fmt.Errorf("create probe user: %w", err)
	}
	slog.Info("[OtokPelican] created probe user", "user_id", user.ID)
	return user, nil
}

func (s *OtokPelicanService) invalidateProbeUserCaches(ctx context.Context, userID int64) {
	if s.apiKeys != nil {
		s.apiKeys.InvalidateAuthCacheByUserID(ctx, userID)
	}
	if s.billingCache != nil {
		cctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.billingCache.InvalidateUserBalance(cctx, userID)
	}
}

// ensureProbeKey 保证内部用户能用该分组，并返回绑定该分组的 API key 明文。
// 顺序：专属分组授权 → 专属倍率 → 建 key。倍率先于第一次请求写入，避免倍率缓存先读到分组默认值。
func (s *OtokPelicanService) ensureProbeKey(ctx context.Context, user *User, group *Group) (string, error) {
	if group.IsExclusive && !otokContainsInt64(user.AllowedGroups, group.ID) {
		if err := s.userRepo.AddGroupToAllowedGroups(ctx, user.ID, group.ID); err != nil {
			return "", fmt.Errorf("allow exclusive group: %w", err)
		}
		user.AllowedGroups = append(user.AllowedGroups, group.ID)
		if s.apiKeys != nil {
			s.apiKeys.InvalidateAuthCacheByUserID(ctx, user.ID)
		}
	}
	if s.rateRepo != nil {
		cur, err := s.rateRepo.GetByUserAndGroup(ctx, user.ID, group.ID)
		if err != nil {
			return "", fmt.Errorf("get probe rate: %w", err)
		}
		if cur == nil || *cur != otokPelicanProbeRate {
			rate := otokPelicanProbeRate
			if err := s.rateRepo.SyncUserGroupRates(ctx, user.ID, map[int64]*float64{group.ID: &rate}); err != nil {
				return "", fmt.Errorf("set probe rate: %w", err)
			}
		}
	}
	key, err := s.lookupProbeKey(ctx, user.ID, group.ID)
	if err != nil {
		return "", err
	}
	if key != "" {
		return key, nil
	}
	if s.apiKeys == nil {
		return "", errors.New("api key service unavailable")
	}
	gid := group.ID
	created, err := s.apiKeys.Create(ctx, user.ID, CreateAPIKeyRequest{
		Name:    otokPelicanKeyNamePrefix + strconv.FormatInt(gid, 10),
		GroupID: &gid,
	})
	if err != nil {
		return "", fmt.Errorf("create probe api key: %w", err)
	}
	slog.Info("[OtokPelican] created probe api key", "user_id", user.ID, "group_id", gid, "key_id", created.ID)
	return created.Key, nil
}

func (s *OtokPelicanService) lookupProbeKey(ctx context.Context, userID, groupID int64) (string, error) {
	if s.db == nil {
		return "", errors.New("database unavailable")
	}
	var key string
	err := s.db.QueryRowContext(ctx, `
		SELECT key FROM api_keys
		WHERE user_id = $1 AND group_id = $2 AND status = 'active' AND deleted_at IS NULL
		  AND (expires_at IS NULL OR expires_at > NOW())
		ORDER BY id LIMIT 1`, userID, groupID).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("lookup probe api key: %w", err)
	}
	return key, nil
}

func otokContainsInt64(list []int64, v int64) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
