package config

import (
	"errors"
	"os"

	"github.com/joho/godotenv"
)

// loadDotEnv 将 .env 文件中的键值对写入当前进程的环境变量，
// 由 viper AutomaticEnv 统一按前缀+替换规则完成映射，避免手动转换带来的歧义。
// 文件不存在时静默跳过。
func loadDotEnv(path string) error {
	err := godotenv.Overload(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
