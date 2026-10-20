package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// otok: 「降智检测」（鹈鹕测试）的配置。整组以 JSON 存在 settings 表的 otok_pelican 一行里。

const (
	SettingKeyOtokPelican = "otok_pelican"

	otokPelicanDefaultInterval    = 10
	otokPelicanMinInterval        = 5
	otokPelicanMaxInterval        = 1440
	otokPelicanDefaultTimeout     = 480
	otokPelicanMinTimeout         = 60
	otokPelicanMaxTimeout         = 900
	otokPelicanDefaultConcurrency = 2
	otokPelicanDefaultKeepRounds  = 10
	otokPelicanMaxKeepRounds      = 50
	otokPelicanMaxPromptRunes     = 8000
	otokPelicanMaxModels          = 5
	otokPelicanDefaultEffort      = "medium"
	otokPelicanDefaultModel       = "gpt-6-astra"
)

// OtokPelicanDefaultPrompt 要求只用内联 SVG / CSS：预览 iframe 是 sandbox 且继承站点 CSP，脚本和外部资源都不会生效。
const OtokPelicanDefaultPrompt = `请创作一个「鹈鹕骑自行车」的单文件 HTML 页面：用内联 SVG 画一只鹈鹕骑在自行车上，画面完整、比例协调、细节丰富，可以加背景和 CSS 动画。
要求：
1. 只能使用内联 SVG 和 CSS，不要使用 JavaScript、外部图片、外部字体或任何外部资源。
2. 页面在 1200×800 左右的窗口里完整显示，不出现滚动条。
3. 只输出完整的 HTML 代码（从 <!DOCTYPE html> 开始），不要任何解释。`

var (
	otokPelicanModelRe   = regexp.MustCompile(`^[A-Za-z0-9._:/\-]{1,128}$`)
	otokPelicanEffortSet = map[string]bool{"none": true, "minimal": true, "low": true, "medium": true, "high": true, "xhigh": true}
)

// OtokPelicanGroupConfig 是单个分组的开关；Models 为空时使用全局 DefaultModels。
type OtokPelicanGroupConfig struct {
	GroupID int64    `json:"group_id"`
	Enabled bool     `json:"enabled"`
	Models  []string `json:"models"`
}

// OtokPelicanConfig 是管理员配置的完整内容。
type OtokPelicanConfig struct {
	Enabled         bool                     `json:"enabled"`
	IntervalMinutes int                      `json:"interval_minutes"`
	ReasoningEffort string                   `json:"reasoning_effort"`
	Prompt          string                   `json:"prompt"`
	TimeoutSeconds  int                      `json:"timeout_seconds"`
	Concurrency     int                      `json:"concurrency"`
	KeepRounds      int                      `json:"keep_rounds"`
	DefaultModels   []string                 `json:"default_models"`
	Groups          []OtokPelicanGroupConfig `json:"groups"`
}

// DefaultOtokPelicanConfig 返回未配置时的默认值（总开关关闭）。
func DefaultOtokPelicanConfig() OtokPelicanConfig {
	return OtokPelicanConfig{
		IntervalMinutes: otokPelicanDefaultInterval,
		ReasoningEffort: otokPelicanDefaultEffort,
		Prompt:          OtokPelicanDefaultPrompt,
		TimeoutSeconds:  otokPelicanDefaultTimeout,
		Concurrency:     otokPelicanDefaultConcurrency,
		KeepRounds:      otokPelicanDefaultKeepRounds,
		DefaultModels:   []string{otokPelicanDefaultModel},
		Groups:          []OtokPelicanGroupConfig{},
	}
}

// EnabledGroups 返回启用的分组；总开关关闭时为空。
func (c OtokPelicanConfig) EnabledGroups() []OtokPelicanGroupConfig {
	if !c.Enabled {
		return nil
	}
	out := make([]OtokPelicanGroupConfig, 0, len(c.Groups))
	for _, g := range c.Groups {
		if g.Enabled {
			out = append(out, g)
		}
	}
	return out
}

// ModelsFor 返回分组实际要测的模型。
func (c OtokPelicanConfig) ModelsFor(g OtokPelicanGroupConfig) []string {
	if len(g.Models) > 0 {
		return g.Models
	}
	return c.DefaultModels
}

func otokPelicanInvalid(code, msg string) error {
	return infraerrors.BadRequest("OTOK_PELICAN_"+code, msg)
}

// NormalizeOtokPelicanConfig 补默认值并校验数值范围、模型名与 prompt；分组的平台和状态由服务层另行校验。
func NormalizeOtokPelicanConfig(in OtokPelicanConfig) (OtokPelicanConfig, error) {
	out := in
	if out.IntervalMinutes == 0 {
		out.IntervalMinutes = otokPelicanDefaultInterval
	}
	if out.IntervalMinutes < otokPelicanMinInterval || out.IntervalMinutes > otokPelicanMaxInterval {
		return OtokPelicanConfig{}, otokPelicanInvalid("INVALID_INTERVAL", "interval_minutes must be between 5 and 1440")
	}
	out.ReasoningEffort = strings.ToLower(strings.TrimSpace(out.ReasoningEffort))
	if out.ReasoningEffort == "" {
		out.ReasoningEffort = otokPelicanDefaultEffort
	}
	if !otokPelicanEffortSet[out.ReasoningEffort] {
		return OtokPelicanConfig{}, otokPelicanInvalid("INVALID_EFFORT", "reasoning_effort must be one of none/minimal/low/medium/high/xhigh")
	}
	out.Prompt = strings.TrimSpace(out.Prompt)
	if out.Prompt == "" {
		out.Prompt = OtokPelicanDefaultPrompt
	}
	if utf8.RuneCountInString(out.Prompt) > otokPelicanMaxPromptRunes {
		return OtokPelicanConfig{}, otokPelicanInvalid("PROMPT_TOO_LONG", "prompt must be at most 8000 characters")
	}
	if out.TimeoutSeconds == 0 {
		out.TimeoutSeconds = otokPelicanDefaultTimeout
	}
	if out.TimeoutSeconds < otokPelicanMinTimeout || out.TimeoutSeconds > otokPelicanMaxTimeout {
		return OtokPelicanConfig{}, otokPelicanInvalid("INVALID_TIMEOUT", "timeout_seconds must be between 60 and 900")
	}
	if out.Concurrency == 0 {
		out.Concurrency = otokPelicanDefaultConcurrency
	}
	// 并发不设上限；内部账号的用户级并发会跟着调高（见 ensureProbeUser）。
	if out.Concurrency < 1 {
		return OtokPelicanConfig{}, otokPelicanInvalid("INVALID_CONCURRENCY", "concurrency must be at least 1")
	}
	if out.KeepRounds == 0 {
		out.KeepRounds = otokPelicanDefaultKeepRounds
	}
	if out.KeepRounds < 1 || out.KeepRounds > otokPelicanMaxKeepRounds {
		return OtokPelicanConfig{}, otokPelicanInvalid("INVALID_KEEP_ROUNDS", "keep_rounds must be between 1 and 50")
	}

	models, err := normalizeOtokPelicanModels(out.DefaultModels)
	if err != nil {
		return OtokPelicanConfig{}, err
	}
	if len(models) == 0 {
		return OtokPelicanConfig{}, otokPelicanInvalid("NO_MODELS", "default_models must not be empty")
	}
	out.DefaultModels = models

	seen := map[int64]bool{}
	groups := make([]OtokPelicanGroupConfig, 0, len(out.Groups))
	for _, g := range out.Groups {
		if g.GroupID <= 0 {
			return OtokPelicanConfig{}, otokPelicanInvalid("INVALID_GROUP", "group_id must be positive")
		}
		if seen[g.GroupID] {
			continue
		}
		seen[g.GroupID] = true
		gm, err := normalizeOtokPelicanModels(g.Models)
		if err != nil {
			return OtokPelicanConfig{}, err
		}
		groups = append(groups, OtokPelicanGroupConfig{GroupID: g.GroupID, Enabled: g.Enabled, Models: gm})
	}
	out.Groups = groups
	return out, nil
}

func normalizeOtokPelicanModels(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, m := range in {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue
		}
		if !otokPelicanModelRe.MatchString(m) {
			return nil, otokPelicanInvalid("INVALID_MODEL", "invalid model name: "+m)
		}
		seen[m] = true
		out = append(out, m)
	}
	if len(out) > otokPelicanMaxModels {
		return nil, otokPelicanInvalid("TOO_MANY_MODELS", "at most 5 models per group")
	}
	return out, nil
}

// loadOtokPelicanConfig 读取配置；未配置或 JSON 损坏时返回默认值（总开关关闭）。
func loadOtokPelicanConfig(ctx context.Context, repo SettingRepository) (OtokPelicanConfig, error) {
	if repo == nil {
		return DefaultOtokPelicanConfig(), nil
	}
	raw, err := repo.GetValue(ctx, SettingKeyOtokPelican)
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return OtokPelicanConfig{}, err
	}
	if strings.TrimSpace(raw) == "" {
		return DefaultOtokPelicanConfig(), nil
	}
	var cfg OtokPelicanConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		slog.Warn("[OtokPelican] stored config is not valid JSON, using defaults", "error", err)
		return DefaultOtokPelicanConfig(), nil
	}
	norm, err := NormalizeOtokPelicanConfig(cfg)
	if err != nil {
		slog.Warn("[OtokPelican] stored config is invalid, using defaults", "error", err)
		return DefaultOtokPelicanConfig(), nil
	}
	return norm, nil
}
