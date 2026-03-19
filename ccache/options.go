package ccache

import "time"

// DurationOpt 用于传递可选的 TTL 参数，避免 API 中出现魔法零值。
type DurationOpt = time.Duration

// TTL 是一个语义化辅助函数，使调用更具可读性：
//
//	cache.Set(ctx, key, val, ccache.TTL(time.Hour))
func TTL(d time.Duration) DurationOpt {
	return d
}

func resolveTTL(opts []DurationOpt, defaultTTL time.Duration) time.Duration {
	if len(opts) > 0 && opts[0] > 0 {
		return opts[0]
	}
	return defaultTTL
}
