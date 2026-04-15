package mns

import (
	"context"
	"testing"

	ali_mns "github.com/aliyun/aliyun-mns-go-sdk"

	"github.com/micoya/gocraft/config"
)

// ---- factory 验证 ----

func TestFactory_WrongType(t *testing.T) {
	_, err := factory("test", "not a config")
	if err == nil {
		t.Fatal("expected error for wrong config type")
	}
}

func TestFactory_MissingEndpoint(t *testing.T) {
	_, err := factory("test", config.MNSConfig{
		AccessKeyID:     "ak",
		AccessKeySecret: "sk",
	})
	if err == nil {
		t.Fatal("expected error for missing endpoint")
	}
}

func TestFactory_MissingAccessKeyID(t *testing.T) {
	_, err := factory("test", config.MNSConfig{
		Endpoint:        "http://1234567890.mns.cn-hangzhou.aliyuncs.com",
		AccessKeySecret: "sk",
	})
	if err == nil {
		t.Fatal("expected error for missing access_key_id")
	}
}

func TestFactory_MissingAccessKeySecret(t *testing.T) {
	_, err := factory("test", config.MNSConfig{
		Endpoint:    "http://1234567890.mns.cn-hangzhou.aliyuncs.com",
		AccessKeyID: "ak",
	})
	if err == nil {
		t.Fatal("expected error for missing access_key_secret")
	}
}

func TestFactory_ValidConfig(t *testing.T) {
	p, err := factory("test", config.MNSConfig{
		Endpoint:        "http://1234567890.mns.cn-hangzhou.aliyuncs.com",
		AccessKeyID:     "ak",
		AccessKeySecret: "sk",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

// ---- Init / Instance / Close 测试 ----

func TestInit_CreatesClient(t *testing.T) {
	p := &provider{cfg: config.MNSConfig{
		Endpoint:        "http://1234567890.mns.cn-hangzhou.aliyuncs.com",
		AccessKeyID:     "ak",
		AccessKeySecret: "sk",
	}}
	if err := p.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if p.Instance() == nil {
		t.Fatal("Instance() should not be nil after Init")
	}
	if _, ok := p.Instance().(ali_mns.MNSClient); !ok {
		t.Errorf("Instance() = %T, want ali_mns.MNSClient", p.Instance())
	}
}

func TestInit_InvalidEndpoint(t *testing.T) {
	p := &provider{cfg: config.MNSConfig{
		Endpoint:        "http://bad-endpoint",
		AccessKeyID:     "ak",
		AccessKeySecret: "sk",
	}}
	err := p.Init(context.Background())
	if err == nil {
		t.Fatal("expected error for invalid endpoint format")
	}
}

func TestClose_ClearsClient(t *testing.T) {
	p := &provider{cfg: config.MNSConfig{
		Endpoint:        "http://1234567890.mns.cn-hangzhou.aliyuncs.com",
		AccessKeyID:     "ak",
		AccessKeySecret: "sk",
	}}
	_ = p.Init(context.Background())
	if err := p.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if p.client != nil {
		t.Error("client should be nil after Close")
	}
}

func TestClose_NilSafe(t *testing.T) {
	p := &provider{cfg: config.MNSConfig{
		Endpoint:        "http://1234567890.mns.cn-hangzhou.aliyuncs.com",
		AccessKeyID:     "ak",
		AccessKeySecret: "sk",
	}}
	if err := p.Close(context.Background()); err != nil {
		t.Errorf("Close on uninitialized provider: %v", err)
	}
}
