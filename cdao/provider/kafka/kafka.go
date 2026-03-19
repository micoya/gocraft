package kafka

import (
	"context"
	"fmt"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/micoya/gocraft/cdao"
	"github.com/micoya/gocraft/config"
)

const defaultDialTimeout = 10 * time.Second

func init() {
	cdao.Register("kafka", factory)
}

func factory(_ string, raw any) (cdao.Provider, error) {
	cfg, ok := raw.(config.KafkaConfig)
	if !ok {
		return nil, fmt.Errorf("dao/provider/kafka: expected config.KafkaConfig, got %T", raw)
	}
	if len(cfg.Brokers) == 0 {
		return nil, fmt.Errorf("dao/provider/kafka: brokers is required")
	}
	return &provider{cfg: cfg}, nil
}

type provider struct {
	cfg    config.KafkaConfig
	client *Client
}

func (p *provider) Init(ctx context.Context) error {
	dialTimeout := p.cfg.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = defaultDialTimeout
	}

	// 验证至少一个 broker 可达
	dialer := &kafkago.Dialer{Timeout: dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", p.cfg.Brokers[0])
	if err != nil {
		return fmt.Errorf("dao/provider/kafka: dial %s: %w", p.cfg.Brokers[0], err)
	}
	_ = conn.Close()

	p.client = &Client{
		brokers:     p.cfg.Brokers,
		dialTimeout: dialTimeout,
	}
	return nil
}

func (p *provider) Close(_ context.Context) error {
	return nil
}

// Health 重新拨号到第一个 broker 以验证当前连通性。
func (p *provider) Health(ctx context.Context) error {
	dialer := &kafkago.Dialer{Timeout: p.client.dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", p.cfg.Brokers[0])
	if err != nil {
		return fmt.Errorf("dao/provider/kafka: health: dial %s: %w", p.cfg.Brokers[0], err)
	}
	return conn.Close()
}

func (p *provider) Instance() any {
	return p.client
}

// Client 是 Kafka 客户端工厂，负责创建带 OTel 追踪的 Writer / Reader。
type Client struct {
	brokers     []string
	dialTimeout time.Duration
}

// Brokers 返回配置的 broker 地址列表。
func (c *Client) Brokers() []string {
	return c.brokers
}

// NewWriter 创建指向指定 topic 的带追踪 Writer，使用 LeastBytes 均衡策略。
func (c *Client) NewWriter(topic string) *TracedWriter {
	w := &kafkago.Writer{
		Addr:     kafkago.TCP(c.brokers...),
		Topic:    topic,
		Balancer: &kafkago.LeastBytes{},
	}
	return &TracedWriter{Writer: w}
}

// NewReader 创建带追踪的 Reader。cfg.Brokers 未指定时自动填充。
func (c *Client) NewReader(cfg kafkago.ReaderConfig) *TracedReader {
	if len(cfg.Brokers) == 0 {
		cfg.Brokers = c.brokers
	}
	return &TracedReader{Reader: kafkago.NewReader(cfg)}
}
