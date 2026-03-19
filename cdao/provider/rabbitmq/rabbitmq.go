package rabbitmq

import (
	"context"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/micoya/gocraft/cdao"
	"github.com/micoya/gocraft/config"
)

func init() {
	cdao.Register("rabbitmq", factory)
}

func factory(_ string, raw any) (cdao.Provider, error) {
	cfg, ok := raw.(config.RabbitMQConfig)
	if !ok {
		return nil, fmt.Errorf("dao/provider/rabbitmq: expected config.RabbitMQConfig, got %T", raw)
	}
	if cfg.URL == "" {
		return nil, fmt.Errorf("dao/provider/rabbitmq: url is required")
	}
	return &provider{cfg: cfg}, nil
}

type provider struct {
	cfg  config.RabbitMQConfig
	conn *amqp.Connection
}

func (p *provider) Init(_ context.Context) error {
	conn, err := amqp.Dial(p.cfg.URL)
	if err != nil {
		return fmt.Errorf("dao/provider/rabbitmq: dial: %w", err)
	}
	p.conn = conn
	return nil
}

func (p *provider) Close(_ context.Context) error {
	if p.conn == nil || p.conn.IsClosed() {
		return nil
	}
	return p.conn.Close()
}

// Health 通过创建并关闭一个临时 Channel 来验证连接可用性。
func (p *provider) Health(_ context.Context) error {
	if p.conn == nil || p.conn.IsClosed() {
		return fmt.Errorf("dao/provider/rabbitmq: connection is closed")
	}
	ch, err := p.conn.Channel()
	if err != nil {
		return fmt.Errorf("dao/provider/rabbitmq: health check channel: %w", err)
	}
	return ch.Close()
}

func (p *provider) Instance() any {
	return p.conn
}
