package oss

import (
	"context"
	"fmt"

	alioss "github.com/aliyun/aliyun-oss-go-sdk/oss"

	"github.com/micoya/gocraft/cdao"
	"github.com/micoya/gocraft/config"
)

func init() {
	cdao.Register("oss", factory)
}

func factory(_ string, raw any) (cdao.Provider, error) {
	cfg, ok := raw.(config.OSSConfig)
	if !ok {
		return nil, fmt.Errorf("dao/provider/oss: expected config.OSSConfig, got %T", raw)
	}
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("dao/provider/oss: endpoint is required")
	}
	if cfg.AccessKeyID == "" {
		return nil, fmt.Errorf("dao/provider/oss: access_key_id is required")
	}
	if cfg.AccessKeySecret == "" {
		return nil, fmt.Errorf("dao/provider/oss: access_key_secret is required")
	}
	return &provider{cfg: cfg}, nil
}

type provider struct {
	cfg    config.OSSConfig
	client *alioss.Client
}

func (p *provider) Init(_ context.Context) error {
	client, err := alioss.New(p.cfg.Endpoint, p.cfg.AccessKeyID, p.cfg.AccessKeySecret)
	if err != nil {
		return fmt.Errorf("dao/provider/oss: create client: %w", err)
	}
	p.client = client
	return nil
}

func (p *provider) Close(_ context.Context) error {
	p.client = nil
	return nil
}

// Health 通过 ListBuckets 验证鉴权及连通性。
func (p *provider) Health(_ context.Context) error {
	_, err := p.client.ListBuckets(alioss.MaxKeys(1))
	if err != nil {
		return fmt.Errorf("dao/provider/oss: health check: %w", err)
	}
	return nil
}

func (p *provider) Instance() any {
	return p.client
}
