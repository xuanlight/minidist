package service_test

import (
	"errors"
	"strings"
	"testing"

	"minidist/internal/kv"
	"minidist/internal/service"
)

// 编译期断言：kv.Store 满足 service.Store，kv 里一行声明都不需要。
var _ service.Store = (*kv.Store)(nil)

// fakeStore 证明 service 不依赖 kv——只要有这四个方法就算实现了接口。
type fakeStore struct{ m map[string]string }

func newFake() *fakeStore { return &fakeStore{m: map[string]string{}} }

func (f *fakeStore) Put(k, v string)             { f.m[k] = v }
func (f *fakeStore) Get(k string) (string, bool) { v, ok := f.m[k]; return v, ok }
func (f *fakeStore) Delete(k string) bool        { _, ok := f.m[k]; delete(f.m, k); return ok }
func (f *fakeStore) Len() int                    { return len(f.m) }

func TestPutValidation(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		value   string
		wantErr error
	}{
		{"ok", "name", "alice", nil},
		{"empty key", "", "v", service.ErrInvalidKey},
		{"key too long", strings.Repeat("k", service.MaxKeyLen+1), "v", service.ErrInvalidKey},
		{"space in key", "a b", "v", service.ErrInvalidKey},
		{"value too large", "k", strings.Repeat("v", service.MaxValueLen+1), service.ErrTooLarge},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := service.New(newFake()) // 每个用例一个干净实例
			err := svc.Put(tt.key, tt.value)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Put(%q, ...) error = %v, want %v", tt.key, err, tt.wantErr)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	svc := service.New(newFake())

	if err := svc.Put("name", "alice"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	v, err := svc.Get("name")
	if err != nil || v != "alice" {
		t.Fatalf("Get = %q, %v; want %q, nil", v, err, "alice")
	}

	if err := svc.Delete("name"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := svc.Get("name"); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("Get after delete = %v, want ErrNotFound", err)
	}
}

func TestDeleteMissing(t *testing.T) {
	svc := service.New(newFake())
	if err := svc.Delete("nope"); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("Delete missing = %v, want ErrNotFound", err)
	}
}
