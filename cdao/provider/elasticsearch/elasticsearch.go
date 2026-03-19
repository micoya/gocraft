package elasticsearch

import (
	"context"
	"fmt"
	"net/http"

	es "github.com/elastic/go-elasticsearch/v8"

	"github.com/micoya/gocraft/cdao"
	"github.com/micoya/gocraft/config"
)

func init() {
	cdao.Register("elasticsearch", factory)
}

func factory(_ string, raw any) (cdao.Provider, error) {
	cfg, ok := raw.(config.ElasticsearchConfig)
	if !ok {
		return nil, fmt.Errorf("dao/provider/elasticsearch: expected config.ElasticsearchConfig, got %T", raw)
	}
	if len(cfg.Addresses) == 0 {
		return nil, fmt.Errorf("dao/provider/elasticsearch: addresses is required")
	}
	return &provider{cfg: cfg}, nil
}

type provider struct {
	cfg    config.ElasticsearchConfig
	client *es.Client
}

func (p *provider) Init(_ context.Context) error {
	client, err := es.NewClient(es.Config{
		Addresses: p.cfg.Addresses,
		Username:  p.cfg.Username,
		Password:  p.cfg.Password,
		APIKey:    p.cfg.APIKey,
		CloudID:   p.cfg.CloudID,
		Transport: newOtelTransport(http.DefaultTransport),
	})
	if err != nil {
		return fmt.Errorf("dao/provider/elasticsearch: create client: %w", err)
	}
	p.client = client
	return nil
}

func (p *provider) Close(_ context.Context) error {
	return nil
}

// Health 通过 Ping 接口验证集群可达性。
func (p *provider) Health(ctx context.Context) error {
	res, err := p.client.Ping(p.client.Ping.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("dao/provider/elasticsearch: ping: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("dao/provider/elasticsearch: ping: status %s", res.Status())
	}
	return nil
}

func (p *provider) Instance() any {
	return p.client
}
