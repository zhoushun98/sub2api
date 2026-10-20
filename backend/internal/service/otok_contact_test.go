//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type otokContactSettingRepoStub struct {
	values   map[string]string
	getCalls int
	getErr   error
}

func (r *otokContactSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	r.getCalls++
	if r.getErr != nil {
		return "", r.getErr
	}
	if v, ok := r.values[key]; ok {
		return v, nil
	}
	return "", ErrSettingNotFound
}
func (r *otokContactSettingRepoStub) Get(context.Context, string) (*Setting, error) {
	return nil, errors.New("unused")
}
func (r *otokContactSettingRepoStub) Set(_ context.Context, key, value string) error {
	if r.values == nil {
		r.values = map[string]string{}
	}
	r.values[key] = value
	return nil
}
func (r *otokContactSettingRepoStub) GetMultiple(context.Context, []string) (map[string]string, error) {
	return r.values, nil
}
func (r *otokContactSettingRepoStub) SetMultiple(context.Context, map[string]string) error {
	return nil
}
func (r *otokContactSettingRepoStub) GetAll(context.Context) (map[string]string, error) {
	return r.values, nil
}
func (r *otokContactSettingRepoStub) Delete(context.Context, string) error { return nil }

func otokContactTestImage() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.Black)
	return img
}

func otokContactPNG(t *testing.T) string {
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, otokContactTestImage()))
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func otokContactJPEG(t *testing.T) string {
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, otokContactTestImage(), nil))
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestNormalizeOtokContact(t *testing.T) {
	pngURL := otokContactPNG(t)
	jpegURL := otokContactJPEG(t)
	webp := append([]byte("RIFF\x24\x00\x00\x00WEBPVP8 "), make([]byte, 24)...)
	webpURL := "data:image/webp;base64," + base64.StdEncoding.EncodeToString(webp)
	gifURL := "data:image/gif;base64," + base64.StdEncoding.EncodeToString([]byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"))
	svgURL := "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`))
	fakePNG := "data:image/png;base64," + strings.TrimPrefix(jpegURL, "data:image/jpeg;base64,")
	huge := "data:image/png;base64," + base64.StdEncoding.EncodeToString(append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, otokContactMaxQRBytes)...))

	cases := []struct {
		name   string
		in     OtokContactConfig
		reason string
	}{
		{"全空", OtokContactConfig{}, ""},
		{"合法邮箱带空白", OtokContactConfig{Email: "  admin@otokapi.com "}, ""},
		{"邮箱带显示名", OtokContactConfig{Email: "Name <a@b.com>"}, "OTOK_CONTACT_INVALID_EMAIL"},
		{"邮箱无顶级域", OtokContactConfig{Email: "a@b"}, "OTOK_CONTACT_INVALID_EMAIL"},
		{"邮箱含空格", OtokContactConfig{Email: "a b@c.com"}, "OTOK_CONTACT_INVALID_EMAIL"},
		{"邮箱含问号", OtokContactConfig{Email: "a?x@b.com"}, "OTOK_CONTACT_INVALID_EMAIL"},
		{"邮箱超长", OtokContactConfig{Email: strings.Repeat("a", 250) + "@b.com"}, "OTOK_CONTACT_INVALID_EMAIL"},
		{"群号合法", OtokContactConfig{QQGroup: "12345"}, ""},
		{"群号前导 0", OtokContactConfig{QQGroup: "01234"}, "OTOK_CONTACT_INVALID_QQ_GROUP"},
		{"群号太短", OtokContactConfig{QQGroup: "1234"}, "OTOK_CONTACT_INVALID_QQ_GROUP"},
		{"群号非数字", OtokContactConfig{QQGroup: "abc12"}, "OTOK_CONTACT_INVALID_QQ_GROUP"},
		{"PNG", OtokContactConfig{WechatQR: pngURL}, ""},
		{"JPEG", OtokContactConfig{QQGroupQR: jpegURL}, ""},
		{"WebP", OtokContactConfig{WechatQR: webpURL}, ""},
		{"GIF", OtokContactConfig{WechatQR: gifURL}, "OTOK_CONTACT_INVALID_WECHAT_QR"},
		{"SVG", OtokContactConfig{QQGroupQR: svgURL}, "OTOK_CONTACT_INVALID_QQ_GROUP_QR"},
		{"声明 PNG 实为 JPEG", OtokContactConfig{WechatQR: fakePNG}, "OTOK_CONTACT_INVALID_WECHAT_QR"},
		{"base64 非法", OtokContactConfig{WechatQR: "data:image/png;base64,@@@"}, "OTOK_CONTACT_INVALID_WECHAT_QR"},
		{"超过 256KB", OtokContactConfig{WechatQR: huge}, "OTOK_CONTACT_INVALID_WECHAT_QR"},
		{"普通 URL", OtokContactConfig{WechatQR: "https://example.com/qr.png"}, "OTOK_CONTACT_INVALID_WECHAT_QR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := NormalizeOtokContact(tc.in)
			if tc.reason == "" {
				require.NoError(t, err)
				require.Equal(t, strings.TrimSpace(tc.in.Email), out.Email)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.reason)
		})
	}
}

func TestOtokContactServiceEmptyAndCorrupted(t *testing.T) {
	repo := &otokContactSettingRepoStub{}
	svc := NewOtokContactService(NewSettingService(repo, nil))
	s, err := svc.Summary(context.Background())
	require.NoError(t, err)
	require.False(t, s.Enabled)
	require.Empty(t, s.Version)

	repo2 := &otokContactSettingRepoStub{values: map[string]string{SettingKeyOtokContact: "{not json"}}
	s2, err := NewOtokContactService(NewSettingService(repo2, nil)).Summary(context.Background())
	require.NoError(t, err)
	require.False(t, s2.Enabled)
}

func TestOtokContactServiceSaveAndCache(t *testing.T) {
	ctx := context.Background()
	repo := &otokContactSettingRepoStub{}
	svc := NewOtokContactService(NewSettingService(repo, nil))
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	_, err := svc.Summary(ctx)
	require.NoError(t, err)
	_, err = svc.Summary(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, repo.getCalls, "TTL 内只读一次库")

	qr := otokContactPNG(t)
	cfg, summary, err := svc.Save(ctx, OtokContactConfig{Email: " admin@otokapi.com", QQGroup: "123456", WechatQR: qr})
	require.NoError(t, err)
	require.Equal(t, "admin@otokapi.com", cfg.Email)
	require.True(t, summary.Enabled)
	require.True(t, summary.HasWechatQR)
	require.False(t, summary.HasQQGroupQR)
	require.Len(t, summary.Version, 12)

	// Save 后直接命中缓存
	codes, err := svc.QRCodes(ctx)
	require.NoError(t, err)
	require.Equal(t, qr, codes.WechatQR)
	require.Equal(t, summary.Version, codes.Version)
	require.Equal(t, 1, repo.getCalls)

	// 过期后重新读库，内容与写入一致
	now = now.Add(otokContactCacheTTL + time.Second)
	got, err := svc.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, cfg, got)
	require.Equal(t, 2, repo.getCalls)

	// 非法输入不落库
	_, _, err = svc.Save(ctx, OtokContactConfig{QQGroup: "x"})
	require.Error(t, err)
	got, err = svc.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, "123456", got.QQGroup)
}

func TestOtokContactServiceDBErrorNotCached(t *testing.T) {
	ctx := context.Background()
	repo := &otokContactSettingRepoStub{getErr: errors.New("db down")}
	svc := NewOtokContactService(NewSettingService(repo, nil))
	_, err := svc.Summary(ctx)
	require.Error(t, err)

	repo.getErr = nil
	repo.values = map[string]string{SettingKeyOtokContact: `{"email":"a@b.com"}`}
	s, err := svc.Summary(ctx)
	require.NoError(t, err)
	require.True(t, s.Enabled)
	require.Equal(t, "a@b.com", s.Email)
	require.Equal(t, 2, repo.getCalls)
}
