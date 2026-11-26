package service

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// otok: 降智检测结果的预览地址。
//
// 模型写的 HTML 常用脚本做动画，嵌在页面里的 srcdoc 会继承站点 CSP（script-src 带 nonce），脚本一律被拦。
// 所以预览改成独立地址，由响应头给出只针对它的 CSP（含 sandbox，直接打开也是不透明来源）。
// iframe 发请求带不了 Authorization 头，地址不走登录鉴权，改用「run id + 过期时间」的 HMAC 签名：
// 签名只在用户端 / 管理端的结果详情接口里下发，用户端下发前已做过可见性检查。

const (
	OtokQualityPreviewPath = "/api/v1/otok/quality/preview/"
	otokQualityPreviewTTL  = 2 * time.Hour
)

// OtokQualityPreviewCSP 允许内联脚本和样式，禁止一切网络请求与外部资源；sandbox 让文档始终是不透明来源，读不到本站的登录信息。
const OtokQualityPreviewCSP = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; " +
	"img-src data: blob:; font-src data:; media-src data: blob:; connect-src 'none'; " +
	"base-uri 'none'; form-action 'none'; frame-ancestors 'self'; sandbox allow-scripts"

// newOtokQualityPreviewKey 从 JWT 密钥派生签名密钥（加用途前缀，不直接复用）；没有 JWT 密钥时用进程内随机密钥。
func newOtokQualityPreviewKey(cfg *config.Config) []byte {
	if cfg != nil && cfg.JWT.Secret != "" {
		m := hmac.New(sha256.New, []byte(cfg.JWT.Secret))
		_, _ = m.Write([]byte("otok-quality-preview-key"))
		return m.Sum(nil)
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	return key
}

func (s *OtokQualityService) previewSignature(runID, exp int64) string {
	m := hmac.New(sha256.New, s.previewKey)
	_, _ = m.Write([]byte(strconv.FormatInt(runID, 10) + ":" + strconv.FormatInt(exp, 10)))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// PreviewURL 返回某条结果的预览地址（相对路径，带过期时间和签名）。
func (s *OtokQualityService) PreviewURL(runID int64) string {
	exp := s.now().Add(otokQualityPreviewTTL).Unix()
	return OtokQualityPreviewPath + strconv.FormatInt(runID, 10) + "?token=" + strconv.FormatInt(exp, 10) + "." + s.previewSignature(runID, exp)
}

// VerifyPreviewToken 校验预览地址里的 token（过期时间 + 签名）。
func (s *OtokQualityService) VerifyPreviewToken(runID int64, token string) bool {
	expRaw, sig, ok := strings.Cut(token, ".")
	if !ok || sig == "" {
		return false
	}
	exp, err := strconv.ParseInt(expRaw, 10, 64)
	if err != nil || s.now().Unix() > exp {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(s.previewSignature(runID, exp)))
}

// OtokQualityPreviewDocument 返回预览响应体。没有 doctype 的输出（只有 <html>、片段、截断的文档）前面补一个，
// 按标准模式渲染，与改用独立地址之前（前端固定在前面拼 <!DOCTYPE html>）一致。
func OtokQualityPreviewDocument(html string) []byte {
	const doctype = "<!doctype"
	trimmed := strings.TrimLeft(html, " \t\r\n\ufeff")
	if len(trimmed) >= len(doctype) && strings.EqualFold(trimmed[:len(doctype)], doctype) {
		return []byte(html)
	}
	return []byte("<!DOCTYPE html>\n" + html)
}
