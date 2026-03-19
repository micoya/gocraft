package mongo

import (
	"context"
	"fmt"
	"time"

	mgo "go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"

	"github.com/micoya/gocraft/cdao"
	"github.com/micoya/gocraft/config"
)

const defaultConnectTimeout = 10 * time.Second

func init() {
	cdao.Register("mongo", factory)
}

func factory(_ string, raw any) (cdao.Provider, error) {
	cfg, ok := raw.(config.MongoConfig)
	if !ok {
		return nil, fmt.Errorf("dao/provider/mongo: expected config.MongoConfig, got %T", raw)
	}
	if cfg.URI == "" {
		return nil, fmt.Errorf("dao/provider/mongo: uri is required")
	}
	return &provider{cfg: cfg}, nil
}

type provider struct {
	cfg    config.MongoConfig
	client *mgo.Client
}

func (p *provider) Init(ctx context.Context) error {
	connectTimeout := p.cfg.ConnectTimeout
	if connectTimeout <= 0 {
		connectTimeout = defaultConnectTimeout
	}

	opts := options.Client().
		ApplyURI(p.cfg.URI).
		SetConnectTimeout(connectTimeout).
		SetMonitor(newMonitor())

	client, err := mgo.Connect(ctx, opts)
	if err != nil {
		return fmt.Errorf("dao/provider/mongo: connect: %w", err)
	}

	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		_ = client.Disconnect(ctx)
		return fmt.Errorf("dao/provider/mongo: ping: %w", err)
	}

	p.client = client
	return nil
}

func (p *provider) Close(ctx context.Context) error {
	if p.client == nil {
		return nil
	}
	return p.client.Disconnect(ctx)
}

func (p *provider) Health(ctx context.Context) error {
	return p.client.Ping(ctx, readpref.Primary())
}

func (p *provider) Instance() any {
	return p.client
}
