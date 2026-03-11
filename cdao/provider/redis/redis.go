package redis

import (
	"context"
	"fmt"

	goredis "github.com/redis/go-redis/v9"

	"github.com/micoya/gocraft/config"
	"github.com/micoya/gocraft/cdao"
)

func init() {
	cdao.Register("redis", factory)
}

func factory(_ string, raw any) (cdao.Provider, error) {
	cfg, ok := raw.(config.RedisConfig)
	if !ok {
		return nil, fmt.Errorf("dao/provider/redis: expected config.RedisConfig, got %T", raw)
	}
	return &provider{cfg: cfg}, nil
}

type provider struct {
	cfg    config.RedisConfig
	client *goredis.Client
}

func (p *provider) Init(ctx context.Context) error {
	p.client = goredis.NewClient(&goredis.Options{
		Addr:         p.cfg.Addr,
		Password:     p.cfg.Password,
		DB:           p.cfg.DB,
		ReadTimeout:  p.cfg.ReadTimeout,
		WriteTimeout: p.cfg.WriteTimeout,
	})
	return p.client.Ping(ctx).Err()
}

func (p *provider) Close(_ context.Context) error {
	if p.client == nil {
		return nil
	}
	return p.client.Close()
}

func (p *provider) Health(ctx context.Context) error {
	return p.client.Ping(ctx).Err()
}

func (p *provider) Instance() any {
	return p.client
}
