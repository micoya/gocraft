package redis

import (
	"bytes"
	"compress/flate"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/micoya/gocraft/cpubsub"
)

const (
	defaultPrefix = "channel:"
	defaultTTL    = 7 * 24 * time.Hour

	fieldBody     = "b"
	fieldCompress = "c"
	compressFlag  = "1"
)

// Option 配置 Redis Stream PubSub 的可选项。
type Option func(*options)

type options struct {
	prefix   string
	ttl      time.Duration
	compress bool
}

// WithPrefix 设置 Stream KEY 前缀，默认 "channel:"。
func WithPrefix(prefix string) Option {
	return func(o *options) { o.prefix = prefix }
}

// WithTTL 设置 Stream KEY 的过期时间，默认 7 天。0 表示不过期。
func WithTTL(ttl time.Duration) Option {
	return func(o *options) { o.ttl = ttl }
}

// WithCompress 启用 deflate 压缩。适用于消息体较大的场景。
func WithCompress(on bool) Option {
	return func(o *options) { o.compress = on }
}

type pubsub struct {
	client *goredis.Client
	opts   options
}

// New 基于已有的 *redis.Client 创建 PubSub 实例。
// client 的生命周期由调用方管理，Close 不会关闭该连接。
func New(client *goredis.Client, opts ...Option) cpubsub.PubSub {
	o := options{
		prefix: defaultPrefix,
		ttl:    defaultTTL,
	}
	for _, opt := range opts {
		opt(&o)
	}
	return &pubsub{client: client, opts: o}
}

func (p *pubsub) key(topic string) string {
	return p.opts.prefix + topic
}

func (p *pubsub) Publish(ctx context.Context, topic string, body string) (string, error) {
	key := p.key(topic)
	values := map[string]any{fieldBody: body}

	if p.opts.compress {
		compressed, err := deflateCompress([]byte(body))
		if err != nil {
			return "", fmt.Errorf("cpubsub/redis: compress: %w", err)
		}
		values[fieldBody] = compressed
		values[fieldCompress] = compressFlag
	}

	pipe := p.client.Pipeline()
	xadd := pipe.XAdd(ctx, &goredis.XAddArgs{
		Stream: key,
		Values: values,
		ID:     "*",
	})
	if p.opts.ttl > 0 {
		pipe.Expire(ctx, key, p.opts.ttl)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return "", fmt.Errorf("cpubsub/redis: publish: %w", err)
	}
	return xadd.Val(), nil
}

func (p *pubsub) Subscribe(ctx context.Context, topic, group, consumer string, handler cpubsub.Handler) error {
	key := p.key(topic)

	if err := p.client.XGroupCreateMkStream(ctx, key, group, "0").Err(); err != nil {
		if !strings.Contains(err.Error(), "BUSYGROUP") {
			return fmt.Errorf("cpubsub/redis: xgroup create: %w", err)
		}
	}

	// 先消费该 consumer 的 pending 消息，全部处理完毕后切换到新消息
	startID := "0-0"

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		streams, err := p.client.XReadGroup(ctx, &goredis.XReadGroupArgs{
			Group:    group,
			Consumer: consumer,
			Streams:  []string{key, startID},
			Count:    10,
			Block:    2 * time.Second,
		}).Result()
		if err != nil {
			if errors.Is(err, goredis.Nil) {
				if startID != ">" {
					startID = ">"
				}
				continue
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("cpubsub/redis: xreadgroup: %w", err)
		}

		for _, stream := range streams {
			if len(stream.Messages) == 0 {
				if startID != ">" {
					startID = ">"
				}
				continue
			}
			for _, raw := range stream.Messages {
				msg, err := p.decodeMessage(topic, raw)
				if err != nil {
					return fmt.Errorf("cpubsub/redis: decode: %w", err)
				}
				if err := handler(ctx, msg); err != nil {
					return err
				}
				if err := p.client.XAck(ctx, key, group, raw.ID).Err(); err != nil {
					return fmt.Errorf("cpubsub/redis: xack: %w", err)
				}
			}
		}
	}
}

func (p *pubsub) decodeMessage(topic string, raw goredis.XMessage) (cpubsub.Message, error) {
	body, _ := raw.Values[fieldBody].(string)

	if flag, _ := raw.Values[fieldCompress].(string); flag == compressFlag {
		decompressed, err := deflateDecompress([]byte(body))
		if err != nil {
			return cpubsub.Message{}, fmt.Errorf("decompress: %w", err)
		}
		body = string(decompressed)
	}

	return cpubsub.Message{
		ID:    raw.ID,
		Topic: topic,
		Body:  body,
	}, nil
}

func (p *pubsub) Close(_ context.Context) error {
	return nil
}

// --- compression ---

func deflateCompress(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func deflateDecompress(data []byte) ([]byte, error) {
	return io.ReadAll(flate.NewReader(bytes.NewReader(data)))
}
