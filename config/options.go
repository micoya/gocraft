package config

type options struct {
	configPath string
	envFile    string
}

func defaultOptions() *options {
	return &options{
		configPath: ".",
		envFile:    ".env",
	}
}

// Option 加载配置的可选项
type Option func(*options)

// WithConfigPath 指定配置文件搜索目录，默认为当前目录
func WithConfigPath(path string) Option {
	return func(o *options) {
		o.configPath = path
	}
}

// WithEnvFile 指定 .env 文件路径，默认为 .env；传空字符串表示不加载
func WithEnvFile(path string) Option {
	return func(o *options) {
		o.envFile = path
	}
}
