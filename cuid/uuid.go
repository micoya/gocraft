package cuid

import (
	"encoding/hex"

	"github.com/google/uuid"
)

// UUIDV4 生成 RFC 4122 版本 4 的 UUID。
func UUIDV4() uuid.UUID {
	return uuid.New()
}

// UUIDV4String 返回带连字符的标准字符串形式，例如 "550e8400-e29b-41d4-a716-446655440000"。
func UUIDV4String() string {
	return uuid.New().String()
}

// UUIDV4NoDash 返回 32 位小写十六进制、无连字符，例如 "550e8400e29b41d4a716446655440000"。
func UUIDV4NoDash() string {
	u := uuid.New()
	return hex.EncodeToString(u[:])
}
