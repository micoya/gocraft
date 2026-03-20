package cuid

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bwmarrin/snowflake"
	goredis "github.com/redis/go-redis/v9"

	"github.com/micoya/gocraft/cdao"
	"github.com/micoya/gocraft/cdao/redisx"
)

var (
	// ErrRedisSnowflakeNoSlot 表示 0..1023 槽位均被占用，短期无法抢到 node。
	ErrRedisSnowflakeNoSlot = errors.New("cuid: redis snowflake: no free node slot")

	// ErrRedisSnowflakeLeaseLost 表示租约已丢失（心跳失败或已被其他实例抢占），不应再继续生成 ID。
	ErrRedisSnowflakeLeaseLost = errors.New("cuid: redis snowflake: lease lost or closed")
)

const (
	defaultRedisKeyPrefix   = "cuid:snowflake:"
	defaultHeartbeatEvery   = 5 * time.Second
	defaultLeaseTTL         = 20 * time.Second
)

var luaRefreshLease = goredis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("PEXPIRE", KEYS[1], ARGV[2])
end
return 0
`)

var luaReleaseLease = goredis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0
`)

// RedisSnowflake 在 Redis 上占用一个 Snowflake 节点槽位（默认 0..1023），通过心跳续期租约。
type RedisSnowflake struct {
	node   *snowflake.Node
	nodeID int64

	client    *goredis.Client
	leaseKey  string
	token     string
	leaseMS   int64
	stop      context.CancelFunc
	heartbeat *time.Ticker
	wg        sync.WaitGroup

	closed atomic.Bool
	dead   atomic.Bool
}

type redisSnowflakeOptions struct {
	keyPrefix        string
	heartbeatEvery   time.Duration
	leaseTTL         time.Duration
	maxNodeExclusive int
}

// RedisSnowflakeOption 配置基于 Redis 的 Snowflake。
type RedisSnowflakeOption func(*redisSnowflakeOptions)

// WithRedisKeyPrefix 设置 Redis 中占用槽位键的前缀，默认 "cuid:snowflake:"，
// 实际键为 prefix + "slot:" + nodeID。
func WithRedisKeyPrefix(prefix string) RedisSnowflakeOption {
	return func(o *redisSnowflakeOptions) { o.keyPrefix = prefix }
}

// WithHeartbeat 设置心跳间隔与租约 TTL。TTL 应明显大于心跳间隔（建议 ≥ 3×）。
func WithHeartbeat(every, leaseTTL time.Duration) RedisSnowflakeOption {
	return func(o *redisSnowflakeOptions) {
		o.heartbeatEvery = every
		o.leaseTTL = leaseTTL
	}
}

// WithMaxNode 设置抢占的 node 上界（开区间），默认 1024，即 id ∈ [0, 1023]。
// 若调整了 bwmarrin/snowflake 的全局 NodeBits，需与此保持一致。
func WithMaxNode(maxExclusive int) RedisSnowflakeOption {
	return func(o *redisSnowflakeOptions) { o.maxNodeExclusive = maxExclusive }
}

// NewRedisSnowflake 在 Redis 上抢占一个 node 槽位并创建 Snowflake 生成器。
// ctx 仅用于初始化阶段的 SET NX；成功后由后台定时器续期直到 Close。
func NewRedisSnowflake(ctx context.Context, client *goredis.Client, opts ...RedisSnowflakeOption) (*RedisSnowflake, error) {
	if client == nil {
		return nil, errors.New("cuid: redis snowflake: redis client is nil")
	}

	maxSlots := 1 << snowflake.NodeBits
	o := redisSnowflakeOptions{
		keyPrefix:        defaultRedisKeyPrefix,
		heartbeatEvery:   defaultHeartbeatEvery,
		leaseTTL:         defaultLeaseTTL,
		maxNodeExclusive: int(maxSlots),
	}
	for _, opt := range opts {
		opt(&o)
	}
	if o.keyPrefix == "" {
		o.keyPrefix = defaultRedisKeyPrefix
	}
	if o.heartbeatEvery <= 0 {
		o.heartbeatEvery = defaultHeartbeatEvery
	}
	if o.leaseTTL <= 0 {
		o.leaseTTL = defaultLeaseTTL
	}
	if o.leaseTTL < 3*o.heartbeatEvery {
		return nil, fmt.Errorf("cuid: redis snowflake: lease TTL %v should be >= 3× heartbeat %v", o.leaseTTL, o.heartbeatEvery)
	}
	if o.maxNodeExclusive <= 0 || int64(o.maxNodeExclusive) > int64(maxSlots) {
		return nil, fmt.Errorf("cuid: redis snowflake: invalid max node %d (snowflake.NodeBits=%d)", o.maxNodeExclusive, snowflake.NodeBits)
	}

	token, err := randomLeaseToken()
	if err != nil {
		return nil, err
	}

	nodeID, leaseKey, err := acquireSnowflakeSlot(ctx, client, o.keyPrefix, token, o.leaseTTL, o.maxNodeExclusive)
	if err != nil {
		return nil, err
	}

	sn, err := snowflake.NewNode(nodeID)
	if err != nil {
		_, _ = luaReleaseLease.Run(ctx, client, []string{leaseKey}, token).Result()
		return nil, fmt.Errorf("cuid: redis snowflake: %w", err)
	}

	hbCtx, cancel := context.WithCancel(context.Background())
	rs := &RedisSnowflake{
		node:      sn,
		nodeID:    nodeID,
		client:    client,
		leaseKey:  leaseKey,
		token:     token,
		leaseMS:   o.leaseTTL.Milliseconds(),
		stop:      cancel,
		heartbeat: time.NewTicker(o.heartbeatEvery),
	}

	rs.wg.Add(1)
	go rs.runHeartbeat(hbCtx)

	return rs, nil
}

// NewRedisSnowflakeDAO 使用 cdao 中已初始化的 Redis 客户端（与 redisx.Must 相同解析规则）。
// redisName 为空时使用 "default"。
func NewRedisSnowflakeDAO(ctx context.Context, d *cdao.DAO, redisName string, opts ...RedisSnowflakeOption) (*RedisSnowflake, error) {
	var names []string
	if redisName != "" {
		names = append(names, redisName)
	}
	client := redisx.Must(d, names...)
	return NewRedisSnowflake(ctx, client, opts...)
}

// NodeID 返回当前占用的 Snowflake 节点号。
func (r *RedisSnowflake) NodeID() int64 {
	return r.nodeID
}

// Generate 在租约有效时生成 Snowflake ID；租约丢失或已 Close 后返回 ErrRedisSnowflakeLeaseLost。
func (r *RedisSnowflake) Generate() (snowflake.ID, error) {
	if r.closed.Load() || r.dead.Load() {
		return 0, ErrRedisSnowflakeLeaseLost
	}
	return r.node.Generate(), nil
}

// Close 停止心跳并尝试释放 Redis 槽位（仅当 token 仍匹配时删除）。
func (r *RedisSnowflake) Close(ctx context.Context) error {
	if !r.closed.CompareAndSwap(false, true) {
		return nil
	}
	r.stop()
	r.heartbeat.Stop()
	r.wg.Wait()

	_, err := luaReleaseLease.Run(ctx, r.client, []string{r.leaseKey}, r.token).Result()
	if err != nil {
		return fmt.Errorf("cuid: redis snowflake: release: %w", err)
	}
	return nil
}

func (r *RedisSnowflake) runHeartbeat(ctx context.Context) {
	defer r.wg.Done()
	defer func() {
		r.dead.Store(true)
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-r.heartbeat.C:
			if !ok {
				return
			}
			n, err := luaRefreshLease.Run(ctx, r.client, []string{r.leaseKey}, r.token, r.leaseMS).Int64()
			if err != nil || n == 0 {
				return
			}
		}
	}
}

func acquireSnowflakeSlot(ctx context.Context, client *goredis.Client, keyPrefix, token string, ttl time.Duration, maxExclusive int) (int64, string, error) {
	ids := shuffledInts(maxExclusive)
	for _, id := range ids {
		key := fmt.Sprintf("%sslot:%d", keyPrefix, id)
		ok, err := client.SetNX(ctx, key, token, ttl).Result()
		if err != nil {
			return 0, "", fmt.Errorf("cuid: redis snowflake: setnx %s: %w", key, err)
		}
		if ok {
			return int64(id), key, nil
		}
	}
	return 0, "", ErrRedisSnowflakeNoSlot
}

func shuffledInts(n int) []int {
	ids := make([]int, n)
	for i := range ids {
		ids[i] = i
	}
	for i := n - 1; i > 0; i-- {
		j := rand.IntN(i + 1)
		ids[i], ids[j] = ids[j], ids[i]
	}
	return ids
}

func randomLeaseToken() (string, error) {
	var b [16]byte
	if _, err := cryptorand.Read(b[:]); err != nil {
		return "", fmt.Errorf("cuid: redis snowflake: token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
