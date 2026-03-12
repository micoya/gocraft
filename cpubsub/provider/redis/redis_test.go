package redis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/micoya/gocraft/cpubsub"
)

func setup(t *testing.T, opts ...Option) (cpubsub.PubSub, *miniredis.Miniredis) {
	t.Helper()
	s := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: s.Addr()})
	t.Cleanup(func() { client.Close() })
	return New(client, opts...), s
}

func TestPublishSubscribe(t *testing.T) {
	ps, _ := setup(t, WithPrefix("test:"), WithTTL(time.Hour))
	ctx := context.Background()

	id, err := ps.Publish(ctx, "orders", `{"id":1}`)
	if err != nil {
		t.Fatalf("Publish() error: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty message ID")
	}

	var got cpubsub.Message
	subCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err = ps.Subscribe(subCtx, "orders", "grp", "w1", func(_ context.Context, msg cpubsub.Message) error {
		got = msg
		cancel()
		return nil
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Subscribe() error: %v", err)
	}

	if got.Body != `{"id":1}` {
		t.Errorf("body = %q, want %q", got.Body, `{"id":1}`)
	}
	if got.Topic != "orders" {
		t.Errorf("topic = %q, want %q", got.Topic, "orders")
	}
}

func TestPublishSubscribeWithCompression(t *testing.T) {
	ps, _ := setup(t, WithCompress(true))
	ctx := context.Background()

	body := strings.Repeat("hello world ", 100)
	if _, err := ps.Publish(ctx, "big", body); err != nil {
		t.Fatalf("Publish() error: %v", err)
	}

	var got string
	subCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_ = ps.Subscribe(subCtx, "big", "grp", "w1", func(_ context.Context, msg cpubsub.Message) error {
		got = msg.Body
		cancel()
		return nil
	})

	if got != body {
		t.Errorf("decompressed body mismatch: got len %d, want len %d", len(got), len(body))
	}
}

func TestMultipleMessages(t *testing.T) {
	ps, _ := setup(t)
	ctx := context.Background()

	const n = 5
	for i := range n {
		if _, err := ps.Publish(ctx, "multi", fmt.Sprintf("msg-%d", i)); err != nil {
			t.Fatalf("Publish(%d) error: %v", i, err)
		}
	}

	var bodies []string
	subCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_ = ps.Subscribe(subCtx, "multi", "grp", "w1", func(_ context.Context, msg cpubsub.Message) error {
		bodies = append(bodies, msg.Body)
		if len(bodies) == n {
			cancel()
		}
		return nil
	})

	if len(bodies) != n {
		t.Fatalf("got %d messages, want %d", len(bodies), n)
	}
	for i, b := range bodies {
		if want := fmt.Sprintf("msg-%d", i); b != want {
			t.Errorf("bodies[%d] = %q, want %q", i, b, want)
		}
	}
}

func TestHandlerErrorStopsSubscription(t *testing.T) {
	ps, _ := setup(t)
	ctx := context.Background()

	if _, err := ps.Publish(ctx, "err-test", "data"); err != nil {
		t.Fatal(err)
	}

	wantErr := errors.New("handler boom")
	subCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err := ps.Subscribe(subCtx, "err-test", "grp", "w1", func(_ context.Context, _ cpubsub.Message) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Errorf("Subscribe() error = %v, want %v", err, wantErr)
	}
}

func TestDefaultPrefix(t *testing.T) {
	p := &pubsub{opts: options{prefix: defaultPrefix}}
	if got := p.key("orders"); got != "channel:orders" {
		t.Errorf("key = %q, want %q", got, "channel:orders")
	}
}

func TestCustomPrefix(t *testing.T) {
	p := &pubsub{opts: options{prefix: "app:"}}
	if got := p.key("events"); got != "app:events" {
		t.Errorf("key = %q, want %q", got, "app:events")
	}
}

func TestTTLApplied(t *testing.T) {
	ps, s := setup(t, WithTTL(10*time.Minute))
	ctx := context.Background()

	if _, err := ps.Publish(ctx, "ttl-test", "data"); err != nil {
		t.Fatal(err)
	}

	ttl := s.TTL("channel:ttl-test")
	if ttl <= 0 {
		t.Error("expected positive TTL on stream key")
	}
}

func TestCompressRoundTrip(t *testing.T) {
	original := "the quick brown fox jumps over the lazy dog"
	compressed, err := deflateCompress([]byte(original))
	if err != nil {
		t.Fatal(err)
	}
	if len(compressed) == 0 {
		t.Fatal("compressed data is empty")
	}

	decompressed, err := deflateDecompress(compressed)
	if err != nil {
		t.Fatal(err)
	}
	if string(decompressed) != original {
		t.Errorf("round-trip failed: got %q", decompressed)
	}
}
