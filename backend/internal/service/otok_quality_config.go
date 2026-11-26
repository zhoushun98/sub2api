package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// otok: 「降智检测」的配置。整组以 JSON 存在 settings 表的 otok_quality 一行里。

const (
	SettingKeyOtokQuality = "otok_quality"

	otokQualityDefaultInterval    = 60
	otokQualityMinInterval        = 5
	otokQualityMaxInterval        = 1440
	otokQualityDefaultTimeout     = 480
	otokQualityMinTimeout         = 60
	otokQualityMaxTimeout         = 900
	otokQualityDefaultConcurrency = 2
	otokQualityMaxConcurrency     = 16
	otokQualityDefaultKeepRounds  = 10
	otokQualityMaxKeepRounds      = 50
	otokQualityMaxItems           = 50
	otokQualityMaxNameRunes       = 64
	otokQualityMaxPromptRunes     = 8000
	otokQualityMaxModels          = 5
	otokQualityMaxExtraBodyBytes  = 4000
	otokQualityMaxAPIKeyLen       = 512
	otokQualityMaxURLLen          = 2048

	OtokQualityProtocolResponses = "responses"
	OtokQualityProtocolChat      = "chat"
	OtokQualityProtocolMessages  = "messages"

	otokQualityKeyMaskPrefix = 4
	otokQualityKeyMaskSuffix = "***"
)

var (
	otokQualityModelRe   = regexp.MustCompile(`^[A-Za-z0-9._:/\-]{1,128}$`)
	otokQualityEffortSet = map[string]bool{"none": true, "minimal": true, "low": true, "medium": true, "high": true, "xhigh": true}
)

// OtokQualityItem 是存库的条目。APIKeyEnc 是 SecretEncryptor 密文，不对外返回。
type OtokQualityItem struct {
	ID              int64    `json:"id"`
	Name            string   `json:"name"`
	Enabled         bool     `json:"enabled"`
	URL             string   `json:"url"`
	APIKeyEnc       string   `json:"api_key_enc"`
	Models          []string `json:"models"`
	ReasoningEffort string   `json:"reasoning_effort"`
	Prompt          string   `json:"prompt"`
	ExtraBody       string   `json:"extra_body"`
}

// Protocol 按 URL 路径识别协议。
func (it OtokQualityItem) Protocol() string { return otokQualityProtocolOf(it.URL) }

// OtokQualityConfig 是存库的完整配置。
type OtokQualityConfig struct {
	Version         int64             `json:"version"`
	Enabled         bool              `json:"enabled"`
	IntervalMinutes int               `json:"interval_minutes"`
	TimeoutSeconds  int               `json:"timeout_seconds"`
	Concurrency     int               `json:"concurrency"`
	KeepRounds      int               `json:"keep_rounds"`
	NextItemID      int64             `json:"next_item_id"`
	Items           []OtokQualityItem `json:"items"`
}

// OtokQualityItemInput 是 PUT /config 里的条目；APIKey 为空表示沿用已存的 key。
type OtokQualityItemInput struct {
	ID              int64    `json:"id"`
	Name            string   `json:"name"`
	Enabled         bool     `json:"enabled"`
	URL             string   `json:"url"`
	APIKey          string   `json:"api_key"`
	Models          []string `json:"models"`
	ReasoningEffort string   `json:"reasoning_effort"`
	Prompt          string   `json:"prompt"`
	ExtraBody       string   `json:"extra_body"`
}

// OtokQualityConfigInput 是 PUT /config 的请求体。
type OtokQualityConfigInput struct {
	Version         int64                  `json:"version"`
	Enabled         bool                   `json:"enabled"`
	IntervalMinutes int                    `json:"interval_minutes"`
	TimeoutSeconds  int                    `json:"timeout_seconds"`
	Concurrency     int                    `json:"concurrency"`
	KeepRounds      int                    `json:"keep_rounds"`
	Items           []OtokQualityItemInput `json:"items"`
}

// OtokQualityAdminItem 是后台回显的条目，不含 key 明文和密文。
type OtokQualityAdminItem struct {
	ID                  int64    `json:"id"`
	Name                string   `json:"name"`
	Enabled             bool     `json:"enabled"`
	URL                 string   `json:"url"`
	Protocol            string   `json:"protocol"`
	HasAPIKey           bool     `json:"has_api_key"`
	APIKeyMasked        string   `json:"api_key_masked"`
	APIKeyDecryptFailed bool     `json:"api_key_decrypt_failed"`
	Models              []string `json:"models"`
	ReasoningEffort     string   `json:"reasoning_effort"`
	Prompt              string   `json:"prompt"`
	ExtraBody           string   `json:"extra_body"`
}

// OtokQualityAdminConfig 是 GET / PUT /config 的返回体。
type OtokQualityAdminConfig struct {
	Version         int64                  `json:"version"`
	Invalid         bool                   `json:"invalid"`
	Enabled         bool                   `json:"enabled"`
	IntervalMinutes int                    `json:"interval_minutes"`
	TimeoutSeconds  int                    `json:"timeout_seconds"`
	Concurrency     int                    `json:"concurrency"`
	KeepRounds      int                    `json:"keep_rounds"`
	Items           []OtokQualityAdminItem `json:"items"`
}

// DefaultOtokQualityConfig 返回未配置时的默认值（总开关关闭、没有条目）。
func DefaultOtokQualityConfig() OtokQualityConfig {
	return OtokQualityConfig{
		IntervalMinutes: otokQualityDefaultInterval,
		TimeoutSeconds:  otokQualityDefaultTimeout,
		Concurrency:     otokQualityDefaultConcurrency,
		KeepRounds:      otokQualityDefaultKeepRounds,
		NextItemID:      1,
		Items:           []OtokQualityItem{},
	}
}

// EnabledItems 返回启用的条目；总开关关闭时为空。
func (c OtokQualityConfig) EnabledItems() []OtokQualityItem {
	if !c.Enabled {
		return nil
	}
	out := make([]OtokQualityItem, 0, len(c.Items))
	for _, it := range c.Items {
		if it.Enabled {
			out = append(out, it)
		}
	}
	return out
}

// ItemByID 按 id 取条目（不论是否启用）。
func (c OtokQualityConfig) ItemByID(id int64) (OtokQualityItem, bool) {
	for _, it := range c.Items {
		if it.ID == id {
			return it, true
		}
	}
	return OtokQualityItem{}, false
}

func otokQualityInvalid(code, msg string) error {
	return infraerrors.BadRequest("OTOK_QUALITY_"+code, msg)
}

// normalizeOtokQualityGlobals 补默认值并校验全局数值范围。
func normalizeOtokQualityGlobals(c *OtokQualityConfig) error {
	if c.IntervalMinutes == 0 {
		c.IntervalMinutes = otokQualityDefaultInterval
	}
	if c.IntervalMinutes < otokQualityMinInterval || c.IntervalMinutes > otokQualityMaxInterval {
		return otokQualityInvalid("INVALID_INTERVAL", "interval_minutes must be between 5 and 1440")
	}
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = otokQualityDefaultTimeout
	}
	if c.TimeoutSeconds < otokQualityMinTimeout || c.TimeoutSeconds > otokQualityMaxTimeout {
		return otokQualityInvalid("INVALID_TIMEOUT", "timeout_seconds must be between 60 and 900")
	}
	if c.Concurrency == 0 {
		c.Concurrency = otokQualityDefaultConcurrency
	}
	if c.Concurrency < 1 || c.Concurrency > otokQualityMaxConcurrency {
		return otokQualityInvalid("INVALID_CONCURRENCY", "concurrency must be between 1 and 16")
	}
	if c.KeepRounds == 0 {
		c.KeepRounds = otokQualityDefaultKeepRounds
	}
	if c.KeepRounds < 1 || c.KeepRounds > otokQualityMaxKeepRounds {
		return otokQualityInvalid("INVALID_KEEP_ROUNDS", "keep_rounds must be between 1 and 50")
	}
	return nil
}

// normalizeOtokQualityItem 校验条目里除 key 以外的字段，并就地规范化。
func normalizeOtokQualityItem(it *OtokQualityItem) error {
	it.Name = strings.TrimSpace(it.Name)
	if n := utf8.RuneCountInString(it.Name); n == 0 || n > otokQualityMaxNameRunes {
		return otokQualityInvalid("INVALID_NAME", "name must be 1-64 characters")
	}
	u, _, err := normalizeOtokQualityURL(it.URL)
	if err != nil {
		return err
	}
	it.URL = u
	models, err := normalizeOtokQualityModels(it.Models)
	if err != nil {
		return err
	}
	it.Models = models
	it.ReasoningEffort = strings.ToLower(strings.TrimSpace(it.ReasoningEffort))
	if it.ReasoningEffort != "" && !otokQualityEffortSet[it.ReasoningEffort] {
		return otokQualityInvalid("INVALID_EFFORT", "reasoning_effort must be empty or one of none/minimal/low/medium/high/xhigh")
	}
	it.Prompt = strings.TrimSpace(it.Prompt)
	if it.Prompt == "" {
		return otokQualityInvalid("PROMPT_REQUIRED", "prompt is required")
	}
	if utf8.RuneCountInString(it.Prompt) > otokQualityMaxPromptRunes {
		return otokQualityInvalid("PROMPT_TOO_LONG", "prompt must be at most 8000 characters")
	}
	it.ExtraBody = strings.TrimSpace(it.ExtraBody)
	if it.ExtraBody != "" {
		var obj map[string]any
		if len(it.ExtraBody) > otokQualityMaxExtraBodyBytes || json.Unmarshal([]byte(it.ExtraBody), &obj) != nil || obj == nil {
			return otokQualityInvalid("INVALID_EXTRA_BODY", "extra_body must be a JSON object of at most 4000 bytes")
		}
	}
	return nil
}

// normalizeOtokQualityURL 校验完整接口地址并去掉路径结尾的 /，返回规范化后的地址和协议。
func normalizeOtokQualityURL(raw string) (string, string, error) {
	bad := otokQualityInvalid("INVALID_URL", "url must be a http(s) address ending with /responses, /chat/completions or /messages")
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > otokQualityMaxURLLen || strings.Contains(raw, "#") {
		return "", "", bad
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", bad
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Hostname() == "" || u.User != nil || u.Opaque != "" {
		return "", "", bad
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if u.RawPath != "" {
		u.RawPath = strings.TrimRight(u.RawPath, "/")
	}
	proto := otokQualityProtocolOfPath(u.Path)
	if proto == "" {
		return "", "", bad
	}
	return u.String(), proto, nil
}

func otokQualityProtocolOfPath(p string) string {
	p = strings.TrimRight(p, "/")
	switch {
	case strings.HasSuffix(p, "/responses"):
		return OtokQualityProtocolResponses
	case strings.HasSuffix(p, "/chat/completions"):
		return OtokQualityProtocolChat
	case strings.HasSuffix(p, "/messages"):
		return OtokQualityProtocolMessages
	}
	return ""
}

// otokQualityProtocolOf 从已存的地址识别协议；地址无法解析时返回空串。
func otokQualityProtocolOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return otokQualityProtocolOfPath(u.Path)
}

func normalizeOtokQualityModels(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, m := range in {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue
		}
		if !otokQualityModelRe.MatchString(m) {
			return nil, otokQualityInvalid("INVALID_MODEL", "invalid model name: "+m)
		}
		seen[m] = true
		out = append(out, m)
	}
	if len(out) == 0 {
		return nil, otokQualityInvalid("NO_MODELS", "at least one model is required")
	}
	if len(out) > otokQualityMaxModels {
		return nil, otokQualityInvalid("TOO_MANY_MODELS", "at most 5 models per item")
	}
	return out, nil
}

// validateOtokQualityAPIKey 只校验格式；空串由调用方按「新条目必填 / 已有条目沿用」处理。
func validateOtokQualityAPIKey(key string) error {
	if len(key) > otokQualityMaxAPIKeyLen {
		return otokQualityInvalid("INVALID_API_KEY", "api_key must be at most 512 characters")
	}
	for _, r := range key {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return otokQualityInvalid("INVALID_API_KEY", "api_key must not contain whitespace or control characters")
		}
	}
	return nil
}

// validateStoredOtokQualityConfig 校验从库里读出的配置；条目 id 必须为正且不重复，next_item_id 不得落后于已有 id。
func validateStoredOtokQualityConfig(c *OtokQualityConfig) error {
	if err := normalizeOtokQualityGlobals(c); err != nil {
		return err
	}
	if len(c.Items) > otokQualityMaxItems {
		return otokQualityInvalid("TOO_MANY_ITEMS", "too many items")
	}
	if c.Items == nil {
		c.Items = []OtokQualityItem{}
	}
	seen := map[int64]bool{}
	var maxID int64
	for i := range c.Items {
		it := &c.Items[i]
		if it.ID <= 0 || seen[it.ID] {
			return fmt.Errorf("invalid or duplicate item id %d", it.ID)
		}
		seen[it.ID] = true
		if it.ID > maxID {
			maxID = it.ID
		}
		if err := normalizeOtokQualityItem(it); err != nil {
			return err
		}
	}
	if c.NextItemID <= maxID {
		c.NextItemID = maxID + 1
	}
	return nil
}

// loadOtokQualityConfig 读取配置。未配置时返回默认值；有内容但 JSON 损坏或校验不过时也返回默认值，并把 invalid 置为 true。
func loadOtokQualityConfig(ctx context.Context, repo SettingRepository) (OtokQualityConfig, bool, error) {
	if repo == nil {
		return DefaultOtokQualityConfig(), false, nil
	}
	raw, err := repo.GetValue(ctx, SettingKeyOtokQuality)
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return OtokQualityConfig{}, false, err
	}
	if strings.TrimSpace(raw) == "" {
		return DefaultOtokQualityConfig(), false, nil
	}
	var cfg OtokQualityConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		slog.Warn("[OtokQuality] stored config is not valid JSON, using defaults", "error", err)
		return DefaultOtokQualityConfig(), true, nil
	}
	if err := validateStoredOtokQualityConfig(&cfg); err != nil {
		slog.Warn("[OtokQuality] stored config is invalid, using defaults", "error", err)
		return DefaultOtokQualityConfig(), true, nil
	}
	return cfg, false, nil
}

// otokQualityMaskKey 前 4 位 + ***，与渠道监控的 key 回显一致。
func otokQualityMaskKey(plain string) string {
	if len(plain) <= otokQualityKeyMaskPrefix {
		return otokQualityKeyMaskSuffix
	}
	return plain[:otokQualityKeyMaskPrefix] + otokQualityKeyMaskSuffix
}
