package cuid

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	"github.com/sony/sonyflake/v2"
)

func TestUUIDV4NoDash(t *testing.T) {
	s := UUIDV4NoDash()
	if len(s) != 32 {
		t.Fatalf("len = %d, want 32", len(s))
	}
	for _, c := range s {
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'f' {
			continue
		}
		t.Fatalf("non-hex in %q", s)
	}
	dashed := UUIDV4String()
	if strings.ReplaceAll(dashed, "-", "") == dashed {
		t.Fatal("dashed form should contain hyphens")
	}
}

func TestNewSnowflakeNode(t *testing.T) {
	_, err := NewSnowflakeNode(-1)
	if err == nil {
		t.Fatal("expected error for negative node")
	}
	n, err := NewSnowflakeNode(42)
	if err != nil {
		t.Fatal(err)
	}
	id := n.Generate()
	if id.Int64() == 0 {
		t.Fatal("unexpected zero id")
	}
}

func TestNewRedisSnowflake_AcquireAndHeartbeat(t *testing.T) {
	srv := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	rs, err := NewRedisSnowflake(ctx, client,
		WithRedisKeyPrefix("test:sf:"),
		WithHeartbeat(100*time.Millisecond, 500*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rs.Close(ctx) }()

	id1, err := rs.Generate()
	if err != nil {
		t.Fatal(err)
	}
	id2, err := rs.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if id1 == id2 {
		t.Fatal("ids should differ")
	}

	// 快进时间后租约仍应被心跳续上
	srv.FastForward(400 * time.Millisecond)
	time.Sleep(50 * time.Millisecond)

	_, err = rs.Generate()
	if err != nil {
		t.Fatalf("after fast-forward: %v", err)
	}
}

func TestNewRedisSnowflake_SecondInstanceGetsSlot(t *testing.T) {
	srv := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()

	a, err := NewRedisSnowflake(ctx, client,
		WithRedisKeyPrefix("t2:"),
		WithHeartbeat(50*time.Millisecond, 300*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close(ctx) }()

	b, err := NewRedisSnowflake(ctx, client,
		WithRedisKeyPrefix("t2:"),
		WithHeartbeat(50*time.Millisecond, 300*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close(ctx) }()

	if a.NodeID() == b.NodeID() {
		t.Fatalf("same node %d", a.NodeID())
	}
}

func TestNewRedisSnowflake_LeaseLost(t *testing.T) {
	srv := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()

	rs, err := NewRedisSnowflake(ctx, client,
		WithRedisKeyPrefix("t3:"),
		WithHeartbeat(200*time.Millisecond, 600*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rs.Close(ctx) }()

	key := "t3:slot:" + strconv.FormatInt(rs.NodeID(), 10)
	srv.Del(key)

	time.Sleep(250 * time.Millisecond)

	_, err = rs.Generate()
	if err != ErrRedisSnowflakeLeaseLost {
		t.Fatalf("want %v, got %v", ErrRedisSnowflakeLeaseLost, err)
	}
}

func TestNewSonyflakeWithSettings_FixedMachineID(t *testing.T) {
	sf, err := NewSonyflakeWithSettings(sonyflake.Settings{
		MachineID: func() (int, error) { return 0x1234, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	id, err := sf.NextID()
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("unexpected zero id")
	}
}
