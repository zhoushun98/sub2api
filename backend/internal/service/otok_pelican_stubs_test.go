//go:build unit || integration

package service

import (
	"context"
	"errors"
)

// otok: 鹈鹕测试单测与集成测试共用的仓储桩。

type otokPelicanSettingRepoStub struct {
	values map[string]string
	getErr error
}

func (r *otokPelicanSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	if r.getErr != nil {
		return "", r.getErr
	}
	if v, ok := r.values[key]; ok {
		return v, nil
	}
	return "", ErrSettingNotFound
}
func (r *otokPelicanSettingRepoStub) Get(context.Context, string) (*Setting, error) {
	return nil, errors.New("unused")
}
func (r *otokPelicanSettingRepoStub) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}
func (r *otokPelicanSettingRepoStub) GetMultiple(context.Context, []string) (map[string]string, error) {
	return r.values, nil
}
func (r *otokPelicanSettingRepoStub) SetMultiple(context.Context, map[string]string) error {
	return nil
}
func (r *otokPelicanSettingRepoStub) GetAll(context.Context) (map[string]string, error) {
	return r.values, nil
}
func (r *otokPelicanSettingRepoStub) Delete(context.Context, string) error { return nil }

type otokPelicanUserRepoStub struct {
	UserRepository
	users      map[int64]*User
	byEmail    map[string]*User
	created    []*User
	adjusted   []float64
	allowed    []int64
	createdErr error
}

func (r *otokPelicanUserRepoStub) GetByID(_ context.Context, id int64) (*User, error) {
	if u, ok := r.users[id]; ok {
		cp := *u
		return &cp, nil
	}
	return nil, ErrUserNotFound
}
func (r *otokPelicanUserRepoStub) GetByEmail(_ context.Context, email string) (*User, error) {
	if u, ok := r.byEmail[email]; ok {
		cp := *u
		return &cp, nil
	}
	return nil, ErrUserNotFound
}
func (r *otokPelicanUserRepoStub) Create(_ context.Context, u *User) error {
	if r.createdErr != nil {
		return r.createdErr
	}
	u.ID = int64(1000 + len(r.created))
	r.created = append(r.created, u)
	r.users[u.ID] = u
	r.byEmail[u.Email] = u
	return nil
}
func (r *otokPelicanUserRepoStub) AdjustBalance(_ context.Context, id int64, delta float64) (BalanceChange, error) {
	r.adjusted = append(r.adjusted, delta)
	old := r.users[id].Balance
	r.users[id].Balance += delta
	return BalanceChange{Old: old, New: r.users[id].Balance}, nil
}
func (r *otokPelicanUserRepoStub) UpdateConcurrency(_ context.Context, id int64, amount int) error {
	r.users[id].Concurrency += amount
	return nil
}
func (r *otokPelicanUserRepoStub) AddGroupToAllowedGroups(_ context.Context, _ int64, groupID int64) error {
	r.allowed = append(r.allowed, groupID)
	return nil
}

type otokPelicanRateRepoStub struct {
	UserGroupRateRepository
	rates map[int64]float64
	syncs int
}

func (r *otokPelicanRateRepoStub) GetByUserAndGroup(_ context.Context, _, groupID int64) (*float64, error) {
	if v, ok := r.rates[groupID]; ok {
		return &v, nil
	}
	return nil, nil
}
func (r *otokPelicanRateRepoStub) SyncUserGroupRates(_ context.Context, _ int64, rates map[int64]*float64) error {
	r.syncs++
	for gid, v := range rates {
		r.rates[gid] = *v
	}
	return nil
}
