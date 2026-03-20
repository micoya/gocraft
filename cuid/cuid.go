// Package cuid 提供常见分布式 ID 生成能力：UUID v4、Twitter Snowflake（静态节点 /
// 基于 Redis 租约的动态节点）、以及 Sonyflake（默认取本机私网 IPv4 的后 16 位作为机器号）。
//
// 涉及租约与心跳的 Snowflake 构造与 Close 均应传入 context；生成 ID 的调用为本地内存操作，无需 context。
package cuid
