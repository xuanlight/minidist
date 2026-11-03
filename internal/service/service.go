// Package service 承载业务规则：key/value 的合法性、错误的语义。
// 它不认识 HTTP，也不知道数据存在内存还是磁盘。
package service

import (
	"errors"
	"fmt"
)

// Store 是 service 需要的存储能力。
// 接口定义在【使用方】：kv 包不需要 import service，
// 只要方法签名对得上，就自动满足这个接口（隐式实现）。
type Store interface {
	Put(key, value string)
	Get(key string) (string, bool)
	Delete(key string) bool
	Len() int
}

const (
	MaxKeyLen   = 128
	MaxValueLen = 1 << 20 // 1 MB
)

// 哨兵错误：调用方用 errors.Is 判断类别，而不是比较字符串。
var (
	ErrNotFound   = errors.New("key not found")
	ErrInvalidKey = errors.New("invalid key")
	ErrTooLarge   = errors.New("value too large")
)

type Service struct {
	store Store
}

func New(store Store) *Service { return &Service{store: store} }

func (s *Service) Put(key, value string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if len(value) > MaxValueLen {
		return fmt.Errorf("%w: %d > %d bytes", ErrTooLarge, len(value), MaxValueLen)
	}
	s.store.Put(key, value)
	return nil
}

func (s *Service) Get(key string) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}
	v, ok := s.store.Get(key)
	if !ok {
		return "", fmt.Errorf("get %q: %w", key, ErrNotFound)
	}
	return v, nil
}

func (s *Service) Delete(key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if !s.store.Delete(key) {
		return fmt.Errorf("delete %q: %w", key, ErrNotFound)
	}
	return nil
}

// Len 透传给 /stats 用。
func (s *Service) Len() int { return s.store.Len() }

func validateKey(key string) error {
	if key == "" || len(key) > MaxKeyLen {
		return fmt.Errorf("%w: length %d", ErrInvalidKey, len(key))
	}
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			return fmt.Errorf("%w: invalid char %q", ErrInvalidKey, r)
		}
	}
	return nil
}
