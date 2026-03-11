package config

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// LogConfig 日志配置
type LogConfig struct {
	Level     string `mapstructure:"level"`
	Format    string `mapstructure:"format"`
	Path      string `mapstructure:"path"`
	WithTrace bool   `mapstructure:"with_trace"`
	AddSource bool   `mapstructure:"add_source"`
}

// CORSConfig CORS 跨域配置，零值代表不启用
type CORSConfig struct {
	AllowAllOrigins  bool          `mapstructure:"allow_all_origins"`
	AllowOrigins     []string      `mapstructure:"allow_origins"`
	AllowMethods     []string      `mapstructure:"allow_methods"`
	AllowHeaders     []string      `mapstructure:"allow_headers"`
	AllowCredentials bool          `mapstructure:"allow_credentials"`
	MaxAge           time.Duration `mapstructure:"max_age"`
}

// AccessLogConfig 访问日志配置
type AccessLogConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Format  string `mapstructure:"format"`
}

// PprofConfig pprof 性能分析配置
type PprofConfig struct {
	Enabled            bool   `mapstructure:"enabled"`
	AllowExternal      bool   `mapstructure:"allow_external"`
	AuthorizationToken string `mapstructure:"authorization_token"`
}

// HTTPServerConfig HTTP 服务配置
type HTTPServerConfig struct {
	Addr            string          `mapstructure:"addr"`
	ShutdownTimeout time.Duration   `mapstructure:"shutdown_timeout"`
	ReadTimeout     time.Duration   `mapstructure:"read_timeout"`
	WriteTimeout    time.Duration   `mapstructure:"write_timeout"`
	IdleTimeout     time.Duration   `mapstructure:"idle_timeout"`
	HealthPath      string          `mapstructure:"health_path"`
	MetricsPath     string          `mapstructure:"metrics_path"`
	CORS            *CORSConfig     `mapstructure:"cors"`
	AccessLog       AccessLogConfig `mapstructure:"access_log"`
	Pprof           PprofConfig     `mapstructure:"pprof"`
}

// OtelTraceConfig 链路追踪导出配置
type OtelTraceConfig struct {
	Endpoint   string  `mapstructure:"endpoint"`    // OTLP gRPC 地址，空=不导出 trace
	Insecure   bool    `mapstructure:"insecure"`    // 是否使用非 TLS 连接
	SampleRate float64 `mapstructure:"sample_rate"` // 采样率 0.0~1.0，默认 1.0
}

// OtelMetricConfig 指标导出配置
type OtelMetricConfig struct {
	Endpoint string `mapstructure:"endpoint"` // OTLP gRPC 地址，空=仅 Prometheus 导出
	Insecure bool   `mapstructure:"insecure"`
}

// OtelConfig OpenTelemetry 配置，nil 代表不启用
type OtelConfig struct {
	Trace  OtelTraceConfig  `mapstructure:"trace"`
	Metric OtelMetricConfig `mapstructure:"metric"`
}

// DBConfig 单个数据库连接配置
type DBConfig struct {
	Driver string `mapstructure:"driver"` // "mysql" 或 "postgres"
	DSN    string `mapstructure:"dsn"`
}

// RedisConfig 单个 Redis 连接配置
type RedisConfig struct {
	Addr         string        `mapstructure:"addr"`
	Password     string        `mapstructure:"password"`
	DB           int           `mapstructure:"db"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout"`
}

// OpenAIConfig 单个 OpenAI 客户端配置
type OpenAIConfig struct {
	APIKey  string `mapstructure:"api_key"`  // OpenAI API Key
	BaseURL string `mapstructure:"base_url"` // 自定义端点，空则使用官方默认地址；可用于兼容代理或其他 OpenAI 兼容服务
}

// OSSConfig 单个阿里云 OSS 连接配置
type OSSConfig struct {
	Endpoint        string `mapstructure:"endpoint"`         // OSS 服务端点，如 https://oss-cn-hangzhou.aliyuncs.com
	AccessKeyID     string `mapstructure:"access_key_id"`    // 阿里云 AccessKey ID
	AccessKeySecret string `mapstructure:"access_key_secret"` // 阿里云 AccessKey Secret
}

// DAOConfig 数据访问层配置
type DAOConfig struct {
	Database map[string]DBConfig      `mapstructure:"database"`
	Redis    map[string]RedisConfig   `mapstructure:"redis"`
	OSS      map[string]OSSConfig     `mapstructure:"oss"`
	OpenAI   map[string]OpenAIConfig  `mapstructure:"openai"`
}

// Config 应用总配置。T 为各业务自定义的扩展配置，对应配置文件中的 app 块。
// 不需要扩展配置时使用 Config[struct{}] 或直接调用 Load[struct{}]。
type Config[T any] struct {
	Name       string            `mapstructure:"name"`
	Env        string            `mapstructure:"env"`
	Log        *LogConfig        `mapstructure:"log"`
	HTTPServer *HTTPServerConfig `mapstructure:"http_server"`
	Otel       *OtelConfig       `mapstructure:"otel"`
	DAO        *DAOConfig        `mapstructure:"dao"`
	App        T                 `mapstructure:"app"`
}

// Load 从配置文件和环境变量加载配置，优先级：默认值 < 配置文件 < 环境变量。
// T 为业务自定义扩展配置类型，对应 yaml 中的 app 块；不需要扩展时传 struct{}。
// ctx 用于未来扩展（如远程配置中心），当前不做网络 IO。
func Load[T any](_ context.Context, opts ...Option) (*Config[T], error) {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}

	v := viper.New()
	setDefaults(v)

	v.SetConfigName("config")
	v.AddConfigPath(o.configPath)
	v.SetConfigType("yaml")

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("config: read config file: %w", err)
		}
	}

	// .env 须在 AutomaticEnv 之前写入进程环境，viper 才能感知
	if o.envFile != "" {
		if err := loadDotEnv(o.envFile); err != nil {
			return nil, fmt.Errorf("config: load env file: %w", err)
		}
	}

	// 用 __ 作为层级分隔符，避免字段名中的单 _ 与层级符号产生歧义：
	// http_server.addr → HTTP_SERVER__ADDR，顶层 http_server_addr → HTTP_SERVER_ADDR，互不冲突。
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "__"))
	v.AutomaticEnv()

	cfg := new(Config[T])
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("config: unmarshal: %w", err)
	}

	return cfg, nil
}

// setDefaults 注册所有字段的默认值。
// viper 的 Unmarshal + AutomaticEnv 只对"已知 key"生效，因此每个字段都必须有
// 显式默认（即使是零值），否则纯靠环境变量赋值的字段会被忽略。
func setDefaults(v *viper.Viper) {
	v.SetDefault("name", "my_app")
	v.SetDefault("env", "local")

	v.SetDefault("log.level", "INFO")
	v.SetDefault("log.format", "text")
	v.SetDefault("log.path", "stdout")
	v.SetDefault("log.with_trace", true)
	v.SetDefault("log.add_source", true)

	v.SetDefault("http_server.addr", ":8080")
	v.SetDefault("http_server.shutdown_timeout", 30*time.Second)
	v.SetDefault("http_server.read_timeout", 30*time.Second)
	v.SetDefault("http_server.write_timeout", 30*time.Second)
	v.SetDefault("http_server.idle_timeout", 60*time.Second)
	v.SetDefault("http_server.health_path", "/healthz")
	v.SetDefault("http_server.metrics_path", "/metrics")
	v.SetDefault("http_server.access_log.enabled", true)
	v.SetDefault("http_server.access_log.format", "")

	v.SetDefault("http_server.pprof.enabled", true)
	v.SetDefault("http_server.pprof.allow_external", false)
	v.SetDefault("http_server.pprof.authorization_token", "")

	v.SetDefault("otel.trace.endpoint", "")
	v.SetDefault("otel.trace.insecure", false)
	v.SetDefault("otel.trace.sample_rate", 1.0)
	v.SetDefault("otel.metric.endpoint", "")
	v.SetDefault("otel.metric.insecure", false)
}
