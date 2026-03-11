package openai

import (
	"context"
	"fmt"

	gopenai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"github.com/micoya/gocraft/cdao"
	"github.com/micoya/gocraft/config"
)

func init() {
	cdao.Register("openai", factory)
}

func factory(_ string, raw any) (cdao.Provider, error) {
	cfg, ok := raw.(config.OpenAIConfig)
	if !ok {
		return nil, fmt.Errorf("dao/provider/openai: expected config.OpenAIConfig, got %T", raw)
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("dao/provider/openai: api_key is required")
	}
	return &provider{cfg: cfg}, nil
}

type provider struct {
	cfg    config.OpenAIConfig
	client *gopenai.Client
}

func (p *provider) Init(_ context.Context) error {
	opts := []option.RequestOption{option.WithAPIKey(p.cfg.APIKey)}
	if p.cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(p.cfg.BaseURL))
	}
	c := gopenai.NewClient(opts...)
	p.client = &c
	return nil
}

func (p *provider) Close(_ context.Context) error {
	p.client = nil
	return nil
}

// Health 通过列举 Models 验证 API Key 及连通性。
func (p *provider) Health(ctx context.Context) error {
	_, err := p.client.Models.List(ctx)
	if err != nil {
		return fmt.Errorf("dao/provider/openai: health check: %w", err)
	}
	return nil
}

func (p *provider) Instance() any {
	return p.client
}
