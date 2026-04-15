package tablestore

import (
	"context"
	"testing"

	alits "github.com/aliyun/aliyun-tablestore-go-sdk/tablestore"

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
	_, err := factory("test", config.TableStoreConfig{
		InstanceName:    "inst",
		AccessKeyID:     "ak",
		AccessKeySecret: "sk",
	})
	if err == nil {
		t.Fatal("expected error for missing endpoint")
	}
}

func TestFactory_MissingInstanceName(t *testing.T) {
	_, err := factory("test", config.TableStoreConfig{
		Endpoint:        "https://inst.cn-hangzhou.ots.aliyuncs.com",
		AccessKeyID:     "ak",
		AccessKeySecret: "sk",
	})
	if err == nil {
		t.Fatal("expected error for missing instance_name")
	}
}

func TestFactory_MissingAccessKeyID(t *testing.T) {
	_, err := factory("test", config.TableStoreConfig{
		Endpoint:        "https://inst.cn-hangzhou.ots.aliyuncs.com",
		InstanceName:    "inst",
		AccessKeySecret: "sk",
	})
	if err == nil {
		t.Fatal("expected error for missing access_key_id")
	}
}

func TestFactory_MissingAccessKeySecret(t *testing.T) {
	_, err := factory("test", config.TableStoreConfig{
		Endpoint:     "https://inst.cn-hangzhou.ots.aliyuncs.com",
		InstanceName: "inst",
		AccessKeyID:  "ak",
	})
	if err == nil {
		t.Fatal("expected error for missing access_key_secret")
	}
}

func TestFactory_ValidConfig(t *testing.T) {
	p, err := factory("test", config.TableStoreConfig{
		Endpoint:        "https://inst.cn-hangzhou.ots.aliyuncs.com",
		InstanceName:    "inst",
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
	p := &provider{cfg: config.TableStoreConfig{
		Endpoint:        "https://inst.cn-hangzhou.ots.aliyuncs.com",
		InstanceName:    "inst",
		AccessKeyID:     "ak",
		AccessKeySecret: "sk",
	}}
	if err := p.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if p.Instance() == nil {
		t.Fatal("Instance() should not be nil after Init")
	}
	if _, ok := p.Instance().(*alits.TableStoreClient); !ok {
		t.Errorf("Instance() = %T, want *tablestore.TableStoreClient", p.Instance())
	}
}

func TestClose_ClearsClient(t *testing.T) {
	p := &provider{cfg: config.TableStoreConfig{
		Endpoint:        "https://inst.cn-hangzhou.ots.aliyuncs.com",
		InstanceName:    "inst",
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
	p := &provider{cfg: config.TableStoreConfig{
		Endpoint:        "https://inst.cn-hangzhou.ots.aliyuncs.com",
		InstanceName:    "inst",
		AccessKeyID:     "ak",
		AccessKeySecret: "sk",
	}}
	if err := p.Close(context.Background()); err != nil {
		t.Errorf("Close on uninitialized provider: %v", err)
	}
}
