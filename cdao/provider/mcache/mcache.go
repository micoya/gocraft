package mcache

import (
	"context"
	"fmt"

	"github.com/dgraph-io/ristretto/v2"

	"github.com/micoya/gocraft/cdao"
	"github.com/micoya/gocraft/config"
)

const (
	defaultNumCounters = 10_000_000
	defaultMaxCost     = 512 << 20 // 512 MB
	defaultBufferItems = 64
)

func init() {
	cdao.Register("mcache", factory)
}

func factory(_ string, raw any) (cdao.Provider, error) {
	cfg, ok := raw.(config.MCacheConfig)
	if !ok {
		return nil, fmt.Errorf("dao/provider/mcache: expected config.MCacheConfig, got %T", raw)
	}
	return &provider{cfg: cfg}, nil
}

type provider struct {
	cfg   config.MCacheConfig
	cache *ristretto.Cache[string, any]
}

func (p *provider) Init(_ context.Context) error {
	numCounters := p.cfg.NumCounters
	if numCounters <= 0 {
		numCounters = defaultNumCounters
	}
	maxCost := p.cfg.MaxCost
	if maxCost <= 0 {
		maxCost = defaultMaxCost
	}
	bufferItems := p.cfg.BufferItems
	if bufferItems <= 0 {
		bufferItems = defaultBufferItems
	}

	cache, err := ristretto.NewCache(&ristretto.Config[string, any]{
		NumCounters: numCounters,
		MaxCost:     maxCost,
		BufferItems: bufferItems,
	})
	if err != nil {
		return fmt.Errorf("dao/provider/mcache: create cache: %w", err)
	}
	p.cache = cache
	return nil
}

func (p *provider) Close(_ context.Context) error {
	if p.cache != nil {
		p.cache.Close()
	}
	return nil
}

func (p *provider) Health(_ context.Context) error {
	if p.cache == nil {
		return fmt.Errorf("dao/provider/mcache: cache not initialized")
	}
	return nil
}

func (p *provider) Instance() any {
	return p.cache
}
