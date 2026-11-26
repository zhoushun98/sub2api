//go:build unit || integration

package service

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// otok: 降智检测单测与集成测试共用的桩。

type otokQualitySettingRepoStub struct {
	mu     sync.Mutex
	values map[string]string
	getErr error
}

func (r *otokQualitySettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.getErr != nil {
		return "", r.getErr
	}
	if v, ok := r.values[key]; ok {
		return v, nil
	}
	return "", ErrSettingNotFound
}
func (r *otokQualitySettingRepoStub) Get(context.Context, string) (*Setting, error) {
	return nil, errors.New("unused")
}
func (r *otokQualitySettingRepoStub) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[key] = value
	return nil
}
func (r *otokQualitySettingRepoStub) GetMultiple(context.Context, []string) (map[string]string, error) {
	return r.values, nil
}
func (r *otokQualitySettingRepoStub) SetMultiple(context.Context, map[string]string) error {
	return nil
}
func (r *otokQualitySettingRepoStub) GetAll(context.Context) (map[string]string, error) {
	return r.values, nil
}
func (r *otokQualitySettingRepoStub) Delete(context.Context, string) error { return nil }

// otokQualityEncryptorStub 是可逆的假加密：密文带上 keyID，换一把 keyID 就解不开。
type otokQualityEncryptorStub struct{ keyID string }

func (e otokQualityEncryptorStub) prefix() string { return "enc:" + e.keyID + ":" }

func (e otokQualityEncryptorStub) Encrypt(plain string) (string, error) {
	return e.prefix() + base64.StdEncoding.EncodeToString([]byte(plain)), nil
}

func (e otokQualityEncryptorStub) Decrypt(cipher string) (string, error) {
	if !strings.HasPrefix(cipher, e.prefix()) {
		return "", errors.New("cipher: message authentication failed")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(cipher, e.prefix()))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// otokQualityTestConfig 返回显式配置了加密密钥的 config。
func otokQualityTestConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Totp.EncryptionKeyConfigured = true
	return cfg
}
