package tablestore

import (
	"context"
	"fmt"

	alits "github.com/aliyun/aliyun-tablestore-go-sdk/tablestore"

	"github.com/micoya/gocraft/cdao"
	"github.com/micoya/gocraft/config"
)

func init() {
	cdao.Register("tablestore", factory)
}

func factory(_ string, raw any) (cdao.Provider, error) {
	cfg, ok := raw.(config.TableStoreConfig)
	if !ok {
		return nil, fmt.Errorf("dao/provider/tablestore: expected config.TableStoreConfig, got %T", raw)
	}
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("dao/provider/tablestore: endpoint is required")
	}
	if cfg.InstanceName == "" {
		return nil, fmt.Errorf("dao/provider/tablestore: instance_name is required")
	}
	if cfg.AccessKeyID == "" {
		return nil, fmt.Errorf("dao/provider/tablestore: access_key_id is required")
	}
	if cfg.AccessKeySecret == "" {
		return nil, fmt.Errorf("dao/provider/tablestore: access_key_secret is required")
	}
	return &provider{cfg: cfg}, nil
}

type provider struct {
	cfg    config.TableStoreConfig
	client *alits.TableStoreClient
}

func (p *provider) Init(_ context.Context) error {
	tsCfg := alits.NewDefaultTableStoreConfig()
	tsCfg.Transport = &otelTransport{base: tsCfg.Transport}

	client := alits.NewClientWithConfig(
		p.cfg.Endpoint,
		p.cfg.InstanceName,
		p.cfg.AccessKeyID,
		p.cfg.AccessKeySecret,
		"",
		tsCfg,
	)
	p.client = client
	return nil
}

func (p *provider) Close(_ context.Context) error {
	p.client = nil
	return nil
}

func (p *provider) Health(_ context.Context) error {
	_, err := p.client.ListTable()
	if err != nil {
		return fmt.Errorf("dao/provider/tablestore: health check: %w", err)
	}
	return nil
}

func (p *provider) Instance() any {
	return p.client
}
