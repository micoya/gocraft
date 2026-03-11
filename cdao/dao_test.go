package cdao

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/micoya/gocraft/config"
)

// --- mock helpers ---

type mockProvider struct {
	initFn   func(ctx context.Context) error
	closeFn  func(ctx context.Context) error
	healthFn func(ctx context.Context) error
	inst     any
}

func (m *mockProvider) Init(ctx context.Context) error {
	if m.initFn != nil {
		return m.initFn(ctx)
	}
	return nil
}

func (m *mockProvider) Close(ctx context.Context) error {
	if m.closeFn != nil {
		return m.closeFn(ctx)
	}
	return nil
}

func (m *mockProvider) Health(ctx context.Context) error {
	if m.healthFn != nil {
		return m.healthFn(ctx)
	}
	return nil
}

func (m *mockProvider) Instance() any { return m.inst }

func mockFactory(inst any) Factory {
	return func(name string, raw any) (Provider, error) {
		return &mockProvider{inst: inst}, nil
	}
}

func registerMock(t *testing.T, kind string, f Factory) {
	t.Helper()
	factories[kind] = f
	t.Cleanup(func() { delete(factories, kind) })
}

// --- NewFromConfig ---

func TestNewFromConfig_EmptyConfig(t *testing.T) {
	d, err := NewFromConfig(&config.DAOConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(d.entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(d.entries))
	}
}

func TestNewFromConfig_MissingProvider(t *testing.T) {
	cfg := &config.DAOConfig{
		Database: map[string]config.DBConfig{
			"primary": {Driver: "mysql", DSN: "test"},
		},
	}
	_, err := NewFromConfig(cfg)
	if err == nil {
		t.Fatal("expected error for missing provider")
	}
	if !strings.Contains(err.Error(), "not registered") {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "import _") {
		t.Errorf("error should hint import path: %v", err)
	}
}

func TestNewFromConfig_Success(t *testing.T) {
	registerMock(t, "database", mockFactory("db-inst"))
	registerMock(t, "redis", func(name string, raw any) (Provider, error) {
		return &mockProvider{inst: "redis-inst"}, nil
	})

	cfg := &config.DAOConfig{
		Database: map[string]config.DBConfig{
			"primary": {Driver: "mysql", DSN: "root:pass@tcp(127.0.0.1)/db"},
		},
		Redis: map[string]config.RedisConfig{
			"cache": {Addr: "127.0.0.1:6379"},
		},
	}

	d, err := NewFromConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(d.entries["database"]) != 1 {
		t.Errorf("expected 1 database entry, got %d", len(d.entries["database"]))
	}
	if len(d.entries["redis"]) != 1 {
		t.Errorf("expected 1 redis entry, got %d", len(d.entries["redis"]))
	}
}

func TestNewFromConfig_FactoryError(t *testing.T) {
	registerMock(t, "database", func(name string, raw any) (Provider, error) {
		return nil, errors.New("bad dsn")
	})

	cfg := &config.DAOConfig{
		Database: map[string]config.DBConfig{
			"primary": {Driver: "mysql", DSN: "invalid"},
		},
	}
	_, err := NewFromConfig(cfg)
	if err == nil {
		t.Fatal("expected error from factory")
	}
	if !strings.Contains(err.Error(), "bad dsn") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- Init ---

func TestInit_Success(t *testing.T) {
	d := &DAO{entries: make(map[string]map[string]*entry)}
	d.entries["mock"] = map[string]*entry{
		"a": {provider: &mockProvider{inst: "a"}},
	}

	if err := d.Init(context.Background()); err != nil {
		t.Fatalf("Init() error: %v", err)
	}
	if !d.inited {
		t.Error("expected DAO to be marked as inited")
	}
	if !d.entries["mock"]["a"].inited {
		t.Error("expected entry to be marked as inited")
	}
}

func TestInit_Idempotent(t *testing.T) {
	calls := 0
	d := &DAO{entries: make(map[string]map[string]*entry)}
	d.entries["mock"] = map[string]*entry{
		"a": {provider: &mockProvider{
			initFn: func(ctx context.Context) error { calls++; return nil },
			inst:   "a",
		}},
	}

	_ = d.Init(context.Background())
	_ = d.Init(context.Background())

	if calls != 1 {
		t.Errorf("Init called provider %d times, want 1", calls)
	}
}

func TestInit_FailFastRollback(t *testing.T) {
	closed := make(map[string]bool)

	mkProvider := func(name string, fail bool) *mockProvider {
		return &mockProvider{
			initFn: func(ctx context.Context) error {
				if fail {
					return errors.New("init failed")
				}
				return nil
			},
			closeFn: func(ctx context.Context) error {
				closed[name] = true
				return nil
			},
			inst: name,
		}
	}

	d := &DAO{entries: make(map[string]map[string]*entry)}
	// 使用单个 kind 以保证 map 迭代顺序一致（单 key 无序问题）
	d.entries["db"] = map[string]*entry{
		"ok":   {provider: mkProvider("ok", false)},
		"fail": {provider: mkProvider("fail", true)},
	}

	err := d.Init(context.Background())
	if err == nil {
		t.Fatal("expected Init error")
	}
	if d.inited {
		t.Error("DAO should not be marked as inited after failure")
	}
}

// --- Close ---

func TestClose(t *testing.T) {
	closedCount := 0
	d := &DAO{
		entries: map[string]map[string]*entry{
			"mock": {
				"a": {
					provider: &mockProvider{
						closeFn: func(ctx context.Context) error { closedCount++; return nil },
					},
					inited: true,
				},
			},
		},
		inited: true,
	}

	if err := d.Close(context.Background()); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	if closedCount != 1 {
		t.Errorf("close called %d times, want 1", closedCount)
	}
	if d.inited {
		t.Error("DAO should not be marked inited after Close")
	}
}

func TestClose_SkipsUninitialized(t *testing.T) {
	closedCount := 0
	d := &DAO{
		entries: map[string]map[string]*entry{
			"mock": {
				"a": {
					provider: &mockProvider{
						closeFn: func(ctx context.Context) error { closedCount++; return nil },
					},
					inited: false,
				},
			},
		},
		inited: true,
	}

	_ = d.Close(context.Background())
	if closedCount != 0 {
		t.Errorf("close called on uninited entry")
	}
}

// --- HealthCheck ---

func TestHealthCheck_Success(t *testing.T) {
	d := &DAO{
		entries: map[string]map[string]*entry{
			"mock": {"a": {provider: &mockProvider{}, inited: true}},
		},
		inited: true,
	}
	if err := d.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck() error: %v", err)
	}
}

func TestHealthCheck_Failure(t *testing.T) {
	d := &DAO{
		entries: map[string]map[string]*entry{
			"mock": {"a": {
				provider: &mockProvider{
					healthFn: func(ctx context.Context) error { return errors.New("ping timeout") },
				},
				inited: true,
			}},
		},
		inited: true,
	}
	err := d.HealthCheck(context.Background())
	if err == nil {
		t.Fatal("expected health check error")
	}
	if !strings.Contains(err.Error(), "ping timeout") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- Get ---

func TestGet_BeforeInit(t *testing.T) {
	d := &DAO{entries: make(map[string]map[string]*entry)}
	_, err := d.Get("mock", "a")
	if err == nil {
		t.Fatal("expected error before init")
	}
	if !strings.Contains(err.Error(), "not initialized") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGet_KindNotFound(t *testing.T) {
	d := &DAO{entries: make(map[string]map[string]*entry), inited: true}
	_, err := d.Get("nosuch", "a")
	if err == nil {
		t.Fatal("expected error for missing kind")
	}
}

func TestGet_NameNotFound(t *testing.T) {
	d := &DAO{
		entries: map[string]map[string]*entry{
			"mock": {"a": {provider: &mockProvider{inst: "val"}, inited: true}},
		},
		inited: true,
	}
	_, err := d.Get("mock", "b")
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestGet_Success(t *testing.T) {
	d := &DAO{
		entries: map[string]map[string]*entry{
			"mock": {"a": {provider: &mockProvider{inst: "hello"}, inited: true}},
		},
		inited: true,
	}
	v, err := d.Get("mock", "a")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if v != "hello" {
		t.Errorf("Get() = %v, want %q", v, "hello")
	}
}

// --- Must ---

func TestMust_Success(t *testing.T) {
	d := &DAO{
		entries: map[string]map[string]*entry{
			"mock": {"a": {provider: &mockProvider{inst: "typed"}, inited: true}},
		},
		inited: true,
	}
	v := Must[string](d, "mock", "a")
	if v != "typed" {
		t.Errorf("Must() = %q, want %q", v, "typed")
	}
}

func TestMust_PanicOnMissing(t *testing.T) {
	d := &DAO{entries: make(map[string]map[string]*entry), inited: true}

	defer func() {
		if r := recover(); r == nil {
			t.Error("Must() should panic on missing entry")
		}
	}()
	Must[string](d, "mock", "a")
}

func TestMust_PanicOnTypeMismatch(t *testing.T) {
	d := &DAO{
		entries: map[string]map[string]*entry{
			"mock": {"a": {provider: &mockProvider{inst: 42}, inited: true}},
		},
		inited: true,
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Error("Must() should panic on type mismatch")
		}
		msg, ok := r.(string)
		if !ok {
			t.Errorf("expected string panic, got %T", r)
		}
		if !strings.Contains(msg, "want") || !strings.Contains(msg, "got") {
			t.Errorf("panic message should describe mismatch: %s", msg)
		}
	}()
	Must[string](d, "mock", "a")
}

// --- Register ---

func TestRegister_DuplicatePanics(t *testing.T) {
	registerMock(t, "dup_test", mockFactory(nil))

	defer func() {
		if r := recover(); r == nil {
			t.Error("Register should panic on duplicate")
		}
	}()
	Register("dup_test", mockFactory(nil))
}
