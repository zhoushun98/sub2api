package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// otok: 侧边栏「联系我们」弹窗的内容（邮箱、QQ 群号与群二维码、微信客服二维码）。
// 整组以 JSON 存在 settings 表的 otok_contact 一行里，不进 public settings，按需单独拉取。

const (
	SettingKeyOtokContact = "otok_contact"

	otokContactCacheTTL   = 60 * time.Second
	otokContactMaxQRBytes = 256 * 1024
	otokContactMaxEmail   = 254
)

var (
	// 只放行常见邮箱字符，避免 mailto 链接被拼进 ?、& 等参数。
	otokContactEmailRe   = regexp.MustCompile(`^[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}$`)
	otokContactQQGroupRe = regexp.MustCompile(`^[1-9][0-9]{4,11}$`)
	otokContactQRPrefix  = regexp.MustCompile(`^data:(image/(?:png|jpeg|webp));base64,`)
)

// OtokContactConfig 是管理员配置的完整内容；空串表示未配置。
type OtokContactConfig struct {
	Email     string `json:"email"`
	QQGroup   string `json:"qq_group"`
	QQGroupQR string `json:"qq_group_qr"`
	WechatQR  string `json:"wechat_qr"`
}

// OtokContactSummary 是侧边栏判断显隐用的轻量信息，不含二维码图片。
type OtokContactSummary struct {
	Enabled      bool   `json:"enabled"`
	Email        string `json:"email"`
	QQGroup      string `json:"qq_group"`
	HasWechatQR  bool   `json:"has_wechat_qr"`
	HasQQGroupQR bool   `json:"has_qq_group_qr"`
	Version      string `json:"version"`
}

// OtokContactQRCodes 是打开弹窗时才拉取的二维码图片。
type OtokContactQRCodes struct {
	WechatQR  string `json:"wechat_qr"`
	QQGroupQR string `json:"qq_group_qr"`
	Version   string `json:"version"`
}

type otokContactEntry struct {
	cfg      OtokContactConfig
	version  string
	loadedAt time.Time
}

// OtokContactService 读写联系方式，进程内缓存 60 秒（多实例靠 TTL 最终一致）。
type OtokContactService struct {
	repo SettingRepository
	now  func() time.Time

	mu     sync.Mutex
	cached *otokContactEntry
}

// NewOtokContactService 复用 SettingService 的仓储，不需要单独注入。
func NewOtokContactService(settings *SettingService) *OtokContactService {
	var repo SettingRepository
	if settings != nil {
		repo = settings.settingRepo
	}
	return &OtokContactService{repo: repo, now: time.Now}
}

func (s *OtokContactService) load(ctx context.Context) (*otokContactEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached != nil && s.now().Sub(s.cached.loadedAt) < otokContactCacheTTL {
		return s.cached, nil
	}
	if s.repo == nil {
		return &otokContactEntry{}, nil
	}
	raw, err := s.repo.GetValue(ctx, SettingKeyOtokContact)
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return nil, err
	}
	var cfg OtokContactConfig
	if raw != "" {
		if jsonErr := json.Unmarshal([]byte(raw), &cfg); jsonErr != nil {
			slog.Warn("[OtokContact] stored config is not valid JSON, treating as empty", "error", jsonErr)
			cfg = OtokContactConfig{}
		}
	}
	s.cached = newOtokContactEntry(cfg, s.now())
	return s.cached, nil
}

func newOtokContactEntry(cfg OtokContactConfig, at time.Time) *otokContactEntry {
	return &otokContactEntry{cfg: cfg, version: otokContactVersion(cfg), loadedAt: at}
}

func otokContactVersion(cfg OtokContactConfig) string {
	if cfg == (OtokContactConfig{}) {
		return ""
	}
	b, _ := json.Marshal(cfg)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:12]
}

// Get 返回完整配置（管理端使用）。
func (s *OtokContactService) Get(ctx context.Context) (OtokContactConfig, error) {
	e, err := s.load(ctx)
	if err != nil {
		return OtokContactConfig{}, err
	}
	return e.cfg, nil
}

// Summary 返回不含图片的摘要。
func (s *OtokContactService) Summary(ctx context.Context) (OtokContactSummary, error) {
	e, err := s.load(ctx)
	if err != nil {
		return OtokContactSummary{}, err
	}
	return otokContactSummaryOf(e), nil
}

func otokContactSummaryOf(e *otokContactEntry) OtokContactSummary {
	c := e.cfg
	return OtokContactSummary{
		Enabled:      c.Email != "" || c.QQGroup != "" || c.QQGroupQR != "" || c.WechatQR != "",
		Email:        c.Email,
		QQGroup:      c.QQGroup,
		HasWechatQR:  c.WechatQR != "",
		HasQQGroupQR: c.QQGroupQR != "",
		Version:      e.version,
	}
}

// QRCodes 返回两张二维码。
func (s *OtokContactService) QRCodes(ctx context.Context) (OtokContactQRCodes, error) {
	e, err := s.load(ctx)
	if err != nil {
		return OtokContactQRCodes{}, err
	}
	return OtokContactQRCodes{WechatQR: e.cfg.WechatQR, QQGroupQR: e.cfg.QQGroupQR, Version: e.version}, nil
}

// Save 校验后整组替换，返回规范化后的配置与摘要。
func (s *OtokContactService) Save(ctx context.Context, in OtokContactConfig) (OtokContactConfig, OtokContactSummary, error) {
	cfg, err := NormalizeOtokContact(in)
	if err != nil {
		return OtokContactConfig{}, OtokContactSummary{}, err
	}
	if s.repo == nil {
		return OtokContactConfig{}, OtokContactSummary{}, infraerrors.ServiceUnavailable("OTOK_CONTACT_UNAVAILABLE", "setting storage unavailable")
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return OtokContactConfig{}, OtokContactSummary{}, err
	}
	if err := s.repo.Set(ctx, SettingKeyOtokContact, string(b)); err != nil {
		return OtokContactConfig{}, OtokContactSummary{}, err
	}
	s.mu.Lock()
	s.cached = newOtokContactEntry(cfg, s.now())
	e := s.cached
	s.mu.Unlock()
	return cfg, otokContactSummaryOf(e), nil
}

// NormalizeOtokContact 去掉首尾空白并校验各字段；空串表示不配置该项。
func NormalizeOtokContact(in OtokContactConfig) (OtokContactConfig, error) {
	out := OtokContactConfig{
		Email:     strings.TrimSpace(in.Email),
		QQGroup:   strings.TrimSpace(in.QQGroup),
		QQGroupQR: strings.TrimSpace(in.QQGroupQR),
		WechatQR:  strings.TrimSpace(in.WechatQR),
	}
	if out.Email != "" && (len(out.Email) > otokContactMaxEmail || !otokContactEmailRe.MatchString(out.Email)) {
		return OtokContactConfig{}, infraerrors.BadRequest("OTOK_CONTACT_INVALID_EMAIL", "invalid email address")
	}
	if out.QQGroup != "" && !otokContactQQGroupRe.MatchString(out.QQGroup) {
		return OtokContactConfig{}, infraerrors.BadRequest("OTOK_CONTACT_INVALID_QQ_GROUP", "QQ group number must be 5-12 digits")
	}
	if out.WechatQR != "" && !validOtokContactQR(out.WechatQR) {
		return OtokContactConfig{}, infraerrors.BadRequest("OTOK_CONTACT_INVALID_WECHAT_QR", "WeChat QR code must be a PNG/JPEG/WebP image no larger than 256KB")
	}
	if out.QQGroupQR != "" && !validOtokContactQR(out.QQGroupQR) {
		return OtokContactConfig{}, infraerrors.BadRequest("OTOK_CONTACT_INVALID_QQ_GROUP_QR", "QQ group QR code must be a PNG/JPEG/WebP image no larger than 256KB")
	}
	return out, nil
}

// validOtokContactQR 要求 data URL 声明的类型与实际内容一致，拦截 SVG / GIF 及伪造的 mime。
func validOtokContactQR(dataURL string) bool {
	m := otokContactQRPrefix.FindStringSubmatch(dataURL)
	if m == nil {
		return false
	}
	payload := dataURL[len(m[0]):]
	if base64.StdEncoding.DecodedLen(len(payload)) > otokContactMaxQRBytes+3 {
		return false
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil || len(data) == 0 || len(data) > otokContactMaxQRBytes {
		return false
	}
	return http.DetectContentType(data) == m[1]
}
