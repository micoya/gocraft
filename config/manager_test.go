package config

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/spf13/viper"
)

// mockProvider 是测试用的 DynamicProvider 实现。
type mockProvider struct {
	name    string
	initial []byte
	updates [][]byte
	delay   time.Duration
}

func (m *mockProvider) Name() string { return m.name }
func (m *mockProvider) Load(_ context.Context) ([]byte, error) {
	return m.initial, nil
}
func (m *mockProvider) Watch(ctx context.Context, patches chan<- []byte) error {
	for _, data := range m.updates {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(m.delay):
			select {
			case patches <- data:
			case <-ctx.Done():
				return nil
			}
		}
	}
	<-ctx.Done()
	return nil
}
func (m *mockProvider) Close() error { return nil }

type testAppConfig struct {
	A      int    `mapstructure:"a"`
	B      string `mapstructure:"b"`
	Nested struct {
		C int `mapstructure:"c"`
		D int `mapstructure:"d"`
	} `mapstructure:"nested"`
}

// newTestConfig 构造仅含 app 字段的测试用 Config。
func newTestConfig(app testAppConfig) *Config[testAppConfig] {
	v := viper.New()
	v.Set("app.a", app.A)
	v.Set("app.b", app.B)
	v.Set("app.nested.c", app.Nested.C)
	v.Set("app.nested.d", app.Nested.D)
	return &Config[testAppConfig]{App: app, v: v}
}

func TestManager_InitialLoad(t *testing.T) {
	base := testAppConfig{A: 1, B: "hello"}
	cfg := newTestConfig(base)

	p := &mockProvider{
		name:    "mock",
		initial: []byte(`{"a": 99, "b": "world"}`),
	}

	mgr, err := NewManager(context.Background(), cfg, p)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	app := mgr.App()
	if app.A != 99 {
		t.Errorf("A: want 99, got %d", app.A)
	}
	if app.B != "world" {
		t.Errorf("B: want world, got %s", app.B)
	}
}

func TestManager_PartialPatch(t *testing.T) {
	base := testAppConfig{A: 1, B: "hello"}
	base.Nested.C = 10
	base.Nested.D = 20
	cfg := newTestConfig(base)

	// 只 patch nested.c，其余字段应保持不变
	p := &mockProvider{
		name:    "mock",
		initial: []byte(`{"nested": {"c": 99}}`),
	}

	mgr, err := NewManager(context.Background(), cfg, p)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	app := mgr.App()
	if app.Nested.C != 99 {
		t.Errorf("nested.c: want 99, got %d", app.Nested.C)
	}
	if app.B != "hello" {
		t.Errorf("B: should not be overwritten, want hello, got %s", app.B)
	}
	if app.Nested.D != 20 {
		t.Errorf("nested.d: should not be overwritten, want 20, got %d", app.Nested.D)
	}
}

func TestManager_ProviderPriority(t *testing.T) {
	base := testAppConfig{A: 1}
	cfg := newTestConfig(base)

	// p1 先加载，p2 后加载，p2 覆盖 p1
	p1 := &mockProvider{name: "p1", initial: []byte(`{"a": 10}`)}
	p2 := &mockProvider{name: "p2", initial: []byte(`{"a": 20}`)}

	mgr, err := NewManager(context.Background(), cfg, p1, p2)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	if mgr.App().A != 20 {
		t.Errorf("A: want 20 (p2 wins), got %d", mgr.App().A)
	}
}

func TestManager_HotUpdate(t *testing.T) {
	base := testAppConfig{A: 1, B: "hello"}
	cfg := newTestConfig(base)

	p := &mockProvider{
		name:    "mock",
		delay:   20 * time.Millisecond,
		updates: [][]byte{[]byte(`{"a": 42}`)},
	}

	mgr, err := NewManager(context.Background(), cfg, p)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if mgr.App().A == 42 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if mgr.App().A != 42 {
		t.Errorf("A: want 42 after hot-update, got %d", mgr.App().A)
	}
	if mgr.App().B != "hello" {
		t.Errorf("B: should not be overwritten, want hello, got %s", mgr.App().B)
	}
}

func TestManager_OnChange(t *testing.T) {
	base := testAppConfig{A: 1}
	cfg := newTestConfig(base)

	p := &mockProvider{
		name:    "mock",
		delay:   20 * time.Millisecond,
		updates: [][]byte{[]byte(`{"a": 55}`)},
	}

	mgr, err := NewManager(context.Background(), cfg, p)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	called := make(chan testAppConfig, 1)
	mgr.OnChange(func(app testAppConfig) {
		called <- app
	})

	select {
	case app := <-called:
		if app.A != 55 {
			t.Errorf("OnChange A: want 55, got %d", app.A)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("OnChange not triggered within timeout")
	}
}

func TestManager_InvalidJSON(t *testing.T) {
	base := testAppConfig{A: 1}
	cfg := newTestConfig(base)

	p := &mockProvider{
		name:    "mock",
		initial: []byte(`not-a-json`),
	}

	_, err := NewManager(context.Background(), cfg, p)
	if err == nil {
		t.Error("want error for invalid JSON, got nil")
	}
}

func TestManager_NoProviders(t *testing.T) {
	base := testAppConfig{A: 7, B: "base"}
	cfg := newTestConfig(base)

	mgr, err := NewManager(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewManager with no providers: %v", err)
	}
	defer mgr.Close()

	app := mgr.App()
	if app.A != 7 || app.B != "base" {
		t.Errorf("App should equal base config, got %+v", app)
	}
}

// mockClientGetter 实现 ClientGetter，供 NewManagerFromDAO 测试使用。
type mockClientGetter struct {
	clients map[string]any // "kind/name" → client
}

func (m *mockClientGetter) Get(kind, name string) (any, error) {
	key := kind + "/" + name
	c, ok := m.clients[key]
	if !ok {
		return nil, fmt.Errorf("mock: %s not found", key)
	}
	return c, nil
}

func TestNewManagerFromDAO_NilDynamic(t *testing.T) {
	cfg := newTestConfig(testAppConfig{A: 5})
	cfg.Dynamic = nil

	mgr, err := NewManagerFromDAO(context.Background(), cfg, &mockClientGetter{})
	if err != nil {
		t.Fatalf("NewManagerFromDAO with nil Dynamic: %v", err)
	}
	defer mgr.Close()

	if mgr.App().A != 5 {
		t.Errorf("A: want 5, got %d", mgr.App().A)
	}
}

func TestNewManagerFromDAO_UnregisteredType(t *testing.T) {
	cfg := newTestConfig(testAppConfig{A: 1})
	cfg.Dynamic = &DynConfig{
		Provider: &DynProviderConfig{Type: "nonexistent", Key: "k"},
	}

	_, err := NewManagerFromDAO(context.Background(), cfg, &mockClientGetter{})
	if err == nil {
		t.Error("want error for unregistered provider type, got nil")
	}
}

func TestNewManagerFromDAO_ClientNotFound(t *testing.T) {
	// 注册一个测试用 builder
	const testType = "testclient_missing"
	RegisterProviderBuilder(testType, func(pcfg DynProviderConfig, client any) (DynamicProvider, error) {
		return &mockProvider{name: testType}, nil
	})

	cfg := newTestConfig(testAppConfig{A: 1})
	cfg.Dynamic = &DynConfig{
		Provider: &DynProviderConfig{Type: testType, Name: "default"},
	}

	_, err := NewManagerFromDAO(context.Background(), cfg, &mockClientGetter{})
	if err == nil {
		t.Error("want error when dao client not found, got nil")
	}
}

func TestNewManagerFromDAO_Success(t *testing.T) {
	const testType = "testclient_ok"
	sentinelClient := struct{ id int }{id: 42}

	RegisterProviderBuilder(testType, func(pcfg DynProviderConfig, client any) (DynamicProvider, error) {
		return &mockProvider{
			name:    testType,
			initial: []byte(`{"a": 77}`),
		}, nil
	})

	cfg := newTestConfig(testAppConfig{A: 1})
	cfg.Dynamic = &DynConfig{
		Provider: &DynProviderConfig{Type: testType, Name: "default", Key: "mykey"},
	}

	dao := &mockClientGetter{
		clients: map[string]any{testType + "/default": sentinelClient},
	}

	mgr, err := NewManagerFromDAO(context.Background(), cfg, dao)
	if err != nil {
		t.Fatalf("NewManagerFromDAO: %v", err)
	}
	defer mgr.Close()

	if mgr.App().A != 77 {
		t.Errorf("A: want 77 from provider initial patch, got %d", mgr.App().A)
	}
}

func TestFlattenLeafs(t *testing.T) {
	cases := []struct {
		name   string
		input  map[string]any
		expect map[string]any
	}{
		{
			name:   "flat",
			input:  map[string]any{"a": 1, "b": "x"},
			expect: map[string]any{"a": 1, "b": "x"},
		},
		{
			name:   "nested one level",
			input:  map[string]any{"a": map[string]any{"b": 2, "c": 3}},
			expect: map[string]any{"a.b": 2, "a.c": 3},
		},
		{
			name:   "nested two levels",
			input:  map[string]any{"a": map[string]any{"b": map[string]any{"c": 4}}},
			expect: map[string]any{"a.b.c": 4},
		},
		{
			name:   "array is leaf",
			input:  map[string]any{"arr": []any{1, 2, 3}},
			expect: map[string]any{"arr": []any{1, 2, 3}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := make(map[string]any)
			flattenLeafs("", tc.input, out)
			for k, want := range tc.expect {
				got, ok := out[k]
				if !ok {
					t.Errorf("key %q missing", k)
					continue
				}
				if fmt.Sprintf("%v", want) != fmt.Sprintf("%v", got) {
					t.Errorf("key %q: want %v, got %v", k, want, got)
				}
			}
			if len(out) != len(tc.expect) {
				t.Errorf("extra keys: got %v, want %v", out, tc.expect)
			}
		})
	}
}
