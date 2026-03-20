package cuid

import (
	"github.com/sony/sonyflake/v2"
)

// NewSonyflake 创建 Sonyflake 实例：机器号为本机首个私网 IPv4 的后 16 位
//（与 sonyflake 在 MachineID 为 nil 时的默认实现一致，见官方 Settings 注释）。
func NewSonyflake() (*sonyflake.Sonyflake, error) {
	return sonyflake.New(sonyflake.Settings{})
}

// NewSonyflakeWithSettings 使用自定义 Settings 创建 Sonyflake（可覆盖时间起点、序列位宽等）。
func NewSonyflakeWithSettings(st sonyflake.Settings) (*sonyflake.Sonyflake, error) {
	return sonyflake.New(st)
}
