package mns

import (
	"context"
	"fmt"

	ali_mns "github.com/aliyun/aliyun-mns-go-sdk"

	"github.com/micoya/gocraft/cdao"
	"github.com/micoya/gocraft/config"
)

func init() {
	cdao.Register("mns", factory)
}

func factory(_ string, raw any) (cdao.Provider, error) {
	cfg, ok := raw.(config.MNSConfig)
	if !ok {
		return nil, fmt.Errorf("dao/provider/mns: expected config.MNSConfig, got %T", raw)
	}
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("dao/provider/mns: endpoint is required")
	}
	if cfg.AccessKeyID == "" {
		return nil, fmt.Errorf("dao/provider/mns: access_key_id is required")
	}
	if cfg.AccessKeySecret == "" {
		return nil, fmt.Errorf("dao/provider/mns: access_key_secret is required")
	}
	return &provider{cfg: cfg}, nil
}

type provider struct {
	cfg    config.MNSConfig
	client ali_mns.MNSClient
}

func (p *provider) Init(_ context.Context) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("dao/provider/mns: create client: %v", r)
		}
	}()

	p.client = ali_mns.NewAliMNSClientWithConfig(ali_mns.AliMNSClientConfig{
		EndPoint:        p.cfg.Endpoint,
		AccessKeyId:     p.cfg.AccessKeyID,
		AccessKeySecret: p.cfg.AccessKeySecret,
	})
	return nil
}

func (p *provider) Close(_ context.Context) error {
	p.client = nil
	return nil
}

// Health 通过 ListQueue 验证鉴权及连通性。
func (p *provider) Health(_ context.Context) error {
	mgr := ali_mns.NewMNSQueueManager(p.client)
	_, err := mgr.ListQueue("", 1, "")
	if err != nil {
		return fmt.Errorf("dao/provider/mns: health check: %w", err)
	}
	return nil
}

func (p *provider) Instance() any {
	return p.client
}
