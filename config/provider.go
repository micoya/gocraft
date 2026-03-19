package config

import (
	"context"
	"encoding/json"
)

// DynamicProvider 是动态配置提供者接口。
//
// 实现者负责从外部存储（Redis、数据库、etcd 等）读取 app 配置块的 JSON 补丁，
// 并在变更时向 Manager 推送新补丁。
//
// 补丁语义：深度合并叶子字段。
// 补丁 JSON 中出现的字段覆盖当前值，未出现的字段保持不变。
// 数组视为叶子节点整体替换，不做元素级合并。
//
// 示例：当前 app.a=1, app.b=2；推送 {"a":10} 后结果为 app.a=10, app.b=2。
type DynamicProvider interface {
	// Name 返回 provider 的名称，用于日志和错误信息。
	Name() string

	// Load 在进程启动时调用一次，返回初始 JSON 补丁。
	// 返回 nil/空切片表示当前无覆盖值。
	Load(ctx context.Context) ([]byte, error)

	// Watch 启动后台监听，有变更时向 patches 写入新补丁。
	// Watch 应阻塞直到 ctx 取消，退出后不再向 patches 写入。
	Watch(ctx context.Context, patches chan<- []byte) error

	// Close 释放 provider 持有的资源（连接、ticker 等）。
	Close() error
}

// flattenLeafs 将嵌套 map 递归展平为点路径叶子键值对，写入 out。
// map 类型的值递归展开，其他类型（数字、字符串、bool、数组、nil）视为叶子。
//
//	{"a": {"b": 1}, "c": 2} → {"a.b": 1, "c": 2}
func flattenLeafs(prefix string, m map[string]any, out map[string]any) {
	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		if child, ok := v.(map[string]any); ok {
			flattenLeafs(key, child, out)
		} else {
			out[key] = v
		}
	}
}

// parseJSONPatch 解析 JSON 补丁字节，返回展平后的叶子键值映射。
func parseJSONPatch(data []byte) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	out := make(map[string]any, len(m))
	flattenLeafs("", m, out)
	return out, nil
}
