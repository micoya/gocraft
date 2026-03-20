package cuid

import (
	"fmt"

	"github.com/bwmarrin/snowflake"
)

// NewSnowflakeNode 使用固定 nodeID 创建 Snowflake 节点（与配置文件中的节点号对应）。
// nodeID 有效范围取决于 bwmarrin/snowflake 的 NodeBits，默认 10 位时为 [0, 1023]。
func NewSnowflakeNode(nodeID int64) (*snowflake.Node, error) {
	n, err := snowflake.NewNode(nodeID)
	if err != nil {
		return nil, fmt.Errorf("cuid: snowflake static node %d: %w", nodeID, err)
	}
	return n, nil
}
