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
	AllowAllOrigins    bool          `mapstructure:"allow_all_origins"`
	AllowOrigins       []string      `mapstructure:"allow_origins"`
	AllowOriginDomains []string      `mapstructure:"allow_origin_domains"`
	AllowMethods       []string      `mapstructure:"allow_methods"`
	AllowHeaders       []string      `mapstructure:"allow_headers"`
	AllowCredentials   bool          `mapstructure:"allow_credentials"`
	MaxAge             time.Duration `mapstructure:"max_age"`
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
	Driver                   string `mapstructure:"driver"` // "mysql" 或 "postgres"
	DSN                      string `mapstructure:"dsn"`
	DisableMigrateForeignKey *bool  `mapstructure:"disable_migrate_foreign_key"` // AutoMigrate 不产生外键约束，nil 视为 true
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

// MCacheConfig 单个内存缓存配置
type MCacheConfig struct {
	NumCounters int64 `mapstructure:"num_counters"` // 频率计数器数量，建议为预期最大缓存条目数的 10 倍；默认 10_000_000
	MaxCost     int64 `mapstructure:"max_cost"`     // 缓存最大容量（字节）；默认 512MB
	BufferItems int64 `mapstructure:"buffer_items"` // 写入缓冲区大小；默认 64
}

// RabbitMQConfig 单个 RabbitMQ 连接配置
type RabbitMQConfig struct {
	URL string `mapstructure:"url"` // AMQP 连接地址，如 amqp://user:pass@host:5672/vhost
}

// KafkaConfig 单个 Kafka 集群配置
type KafkaConfig struct {
	Brokers     []string      `mapstructure:"brokers"`      // Broker 地址列表，如 ["kafka:9092"]
	DialTimeout time.Duration `mapstructure:"dial_timeout"` // 拨号超时，默认 10s
}

// ElasticsearchConfig 单个 Elasticsearch 集群配置
type ElasticsearchConfig struct {
	Addresses []string `mapstructure:"addresses"` // 节点地址列表，如 ["http://localhost:9200"]
	Username  string   `mapstructure:"username"`
	Password  string   `mapstructure:"password"`
	APIKey    string   `mapstructure:"api_key"`  // 优先级高于 Username/Password
	CloudID   string   `mapstructure:"cloud_id"` // Elastic Cloud 部署 ID
}

// MongoConfig 单个 MongoDB 连接配置
type MongoConfig struct {
	URI            string        `mapstructure:"uri"`             // MongoDB 连接字符串，如 mongodb://host:27017
	ConnectTimeout time.Duration `mapstructure:"connect_timeout"` // 连接超时，默认 10s
}

// TableStoreConfig 单个阿里云表格存储（Tablestore）连接配置
type TableStoreConfig struct {
	Endpoint        string `mapstructure:"endpoint"`          // 实例访问地址，如 https://instance-name.cn-hangzhou.ots.aliyuncs.com
	InstanceName    string `mapstructure:"instance_name"`     // 实例名称
	AccessKeyID     string `mapstructure:"access_key_id"`     // 阿里云 AccessKey ID
	AccessKeySecret string `mapstructure:"access_key_secret"` // 阿里云 AccessKey Secret
}

// MNSConfig 单个阿里云消息服务（MNS）连接配置
type MNSConfig struct {
	Endpoint        string `mapstructure:"endpoint"`          // MNS 接入地址，如 http://1234567890123456.mns.cn-hangzhou.aliyuncs.com
	AccessKeyID     string `mapstructure:"access_key_id"`     // 阿里云 AccessKey ID
	AccessKeySecret string `mapstructure:"access_key_secret"` // 阿里云 AccessKey Secret
}

// RetryConfig HTTP 客户端重试配置。
type RetryConfig struct {
	// MaxAttempts 最大重试次数（不含首次请求），默认 3。
	MaxAttempts int `mapstructure:"max_attempts"`
	// WaitMin 首次重试前的最小等待时间，默认 100ms。
	WaitMin time.Duration `mapstructure:"wait_min"`
	// WaitMax 重试等待时间上限（指数退避不超过此值），默认 2s。
	WaitMax time.Duration `mapstructure:"wait_max"`
}

// CircuitBreakerConfig HTTP 客户端熔断器配置。
type CircuitBreakerConfig struct {
	// MaxRequests 半开状态下允许通过的最大探测请求数，默认 1。
	MaxRequests uint32 `mapstructure:"max_requests"`
	// Interval 统计滑动窗口时长，默认 60s。窗口内连续失败达到阈值时熔断。
	Interval time.Duration `mapstructure:"interval"`
	// Timeout 熔断持续时间（Open → HalfOpen），默认 30s。
	Timeout time.Duration `mapstructure:"timeout"`
	// Threshold 连续失败次数阈值，达到后进入熔断状态，默认 5。
	Threshold uint32 `mapstructure:"threshold"`
}

// HTTPClientConfig 单个外部 HTTP 服务客户端配置
type HTTPClientConfig struct {
	// BaseURL 外部服务的根地址，如 https://payment.internal。
	// 用于健康检查（HEAD 请求）和 OTel peer.service 属性；业务代码仍需自行拼接完整 URL。
	BaseURL string `mapstructure:"base_url"`
	// Timeout 单次请求的完整超时（含重定向），默认 30s。
	Timeout time.Duration `mapstructure:"timeout"`
	// MaxIdleConns 全局最大空闲连接数，默认 100。
	MaxIdleConns int `mapstructure:"max_idle_conns"`
	// MaxConnsPerHost 每个 host 的最大连接数（含活跃+空闲），0 = 不限。
	MaxConnsPerHost int `mapstructure:"max_conns_per_host"`
	// IdleConnTimeout 空闲连接保活超时，默认 90s。
	IdleConnTimeout time.Duration `mapstructure:"idle_conn_timeout"`
	// TLSSkipVerify 跳过 TLS 证书校验，仅用于开发/内网测试环境。
	TLSSkipVerify bool `mapstructure:"tls_skip_verify"`
	// Retry 重试配置。nil 或零值表示不重试。
	Retry *RetryConfig `mapstructure:"retry"`
	// CircuitBreaker 熔断器配置。nil 或零值表示不启用熔断。
	CircuitBreaker *CircuitBreakerConfig `mapstructure:"circuit_breaker"`
}

// TemporalTLSConfig Temporal 连接 TLS 证书配置。
type TemporalTLSConfig struct {
	// CertFile 客户端证书文件路径（PEM），mTLS 时需要。
	CertFile string `mapstructure:"cert_file"`
	// KeyFile 客户端私钥文件路径（PEM），mTLS 时需要。
	KeyFile string `mapstructure:"key_file"`
	// CAFile 自定义 CA 证书路径，空则使用系统 CA。
	CAFile string `mapstructure:"ca_file"`
	// ServerName TLS SNI，空则使用 host_port 中的 hostname。
	ServerName string `mapstructure:"server_name"`
	// InsecureSkipVerify 跳过服务端证书校验，仅限开发环境。
	InsecureSkipVerify bool `mapstructure:"insecure_skip_verify"`
}

// TemporalWorkerConfig 全局 Worker 并发控制默认值。
// 每个 NewWorker 调用可通过 WorkerOption 独立覆盖。
type TemporalWorkerConfig struct {
	// MaxConcurrentActivityExecutors 最大并发 Activity 执行数，默认 100。
	MaxConcurrentActivityExecutors int `mapstructure:"max_concurrent_activity_executors"`
	// MaxConcurrentWorkflowTaskExecutors 最大并发 Workflow Task 执行数，默认 100。
	MaxConcurrentWorkflowTaskExecutors int `mapstructure:"max_concurrent_workflow_task_executors"`
	// MaxConcurrentLocalActivityExecutors 最大并发 Local Activity 执行数，默认 100。
	MaxConcurrentLocalActivityExecutors int `mapstructure:"max_concurrent_local_activity_executors"`
}

// TemporalConfig Temporal 服务连接配置。
type TemporalConfig struct {
	// HostPort Temporal Server gRPC 地址。
	// 本地开发默认：localhost:7233
	// Temporal Cloud 格式：<namespace>.<accountId>.tmprl.cloud:7233
	HostPort string `mapstructure:"host_port"`

	// Namespace Temporal 命名空间，默认 "default"。
	// Temporal Cloud 格式：<namespace>.<accountId>
	Namespace string `mapstructure:"namespace"`

	// APIKey Temporal Cloud API Key 认证（推荐方式）。
	// 设置后自动启用 TLS，无需额外 tls 配置。
	// 从 Temporal Cloud 控制台生成：cloud.temporal.io → API Keys。
	APIKey string `mapstructure:"api_key"`

	// TLS mTLS 或自托管 TLS 配置。
	// 使用 Temporal Cloud API Key 时无需配置。
	TLS *TemporalTLSConfig `mapstructure:"tls"`

	// Worker 全局 Worker 并发默认配置，各 Worker 可独立覆盖。
	Worker *TemporalWorkerConfig `mapstructure:"worker"`
}

// UIDSnowflakeStaticConfig 静态 Snowflake 节点（配置文件中的 node_id）。
type UIDSnowflakeStaticConfig struct {
	NodeID int64 `mapstructure:"node_id"`
}

// UIDRedisSnowflakeConfig 基于 Redis 租约的动态 Snowflake。
type UIDRedisSnowflakeConfig struct {
	// Redis 为 cdao 中 Redis 实例名，空则使用 "default"。
	Redis string `mapstructure:"redis"`
	// KeyPrefix 对应 cuid.WithRedisKeyPrefix，空则使用 cuid 包默认值。
	KeyPrefix string `mapstructure:"key_prefix"`
	// HeartbeatEvery、LeaseTTL 需同时大于 0 才会传给 cuid.WithHeartbeat；否则用 cuid 默认心跳与租约。
	HeartbeatEvery time.Duration `mapstructure:"heartbeat_every"`
	LeaseTTL       time.Duration `mapstructure:"lease_ttl"`
	// MaxNodeExclusive 大于 0 时传给 cuid.WithMaxNode。
	MaxNodeExclusive int `mapstructure:"max_node_exclusive"`
}

// UIDConfig 分布式 ID 配置，配合 cfx.ProvideUID*FromConfig；nil 表示不在配置中声明。
type UIDConfig struct {
	SnowflakeStatic *UIDSnowflakeStaticConfig `mapstructure:"snowflake_static"`
	RedisSnowflake  *UIDRedisSnowflakeConfig  `mapstructure:"redis_snowflake"`
}

// CronConfig 定时任务调度器配置。
type CronConfig struct {
	// Timezone 任务调度时区，默认 "Asia/Shanghai"。
	Timezone string `mapstructure:"timezone"`
	// Distributed 是否启用分布式防重复执行（多实例部署时同一时刻只有一个节点执行任务）。
	// 启用时需通过 ccron.WithLocker 注入分布式锁实现。默认 false。
	Distributed bool `mapstructure:"distributed"`
	// LockRedis 分布式模式下使用的 Redis 实例名（对应 dao.redis 中的 key），默认 "default"。
	LockRedis string `mapstructure:"lock_redis"`
	// LockTTL 每个任务分布式锁的持有时长，应略大于任务预期最长执行时间，默认 5m。
	LockTTL time.Duration `mapstructure:"lock_ttl"`
}

// LimiterItemConfig 单个限流器实例配置。
type LimiterItemConfig struct {
	// Algo 限流算法，可选值：
	//   - sliding_window（推荐）：分布式滑动窗口，边界平滑，无需分布式锁
	//   - fixed_window：分布式固定窗口，资源消耗最低
	//   - token_bucket：分布式令牌桶，支持突发（配合 Burst 使用）
	//   - local：单机内存滑动窗口，无需 Redis，适合单实例或测试环境
	Algo string `mapstructure:"algo"`
	// Rate 每 Window 时间内允许的最大请求数。
	Rate int64 `mapstructure:"rate"`
	// Window 时间窗口大小，如 1s、1m。
	Window time.Duration `mapstructure:"window"`
	// Burst 令牌桶突发容量，仅 token_bucket 算法生效，默认 1（严格按速率限流）。
	Burst int64 `mapstructure:"burst"`
	// KeyPrefix Redis key 前缀，默认 "climiter:"。
	KeyPrefix string `mapstructure:"key_prefix"`
	// Redis cdao 中 Redis 实例名，local 算法无需填写，其余算法默认 "default"。
	Redis string `mapstructure:"redis"`
}

// DAOConfig 数据访问层配置
type DAOConfig struct {
	Database      map[string]DBConfig            `mapstructure:"database"`
	Redis         map[string]RedisConfig         `mapstructure:"redis"`
	OSS           map[string]OSSConfig           `mapstructure:"oss"`
	OpenAI        map[string]OpenAIConfig        `mapstructure:"openai"`
	MCache        map[string]MCacheConfig        `mapstructure:"mcache"`
	HTTPClient    map[string]HTTPClientConfig    `mapstructure:"http_client"`
	RabbitMQ      map[string]RabbitMQConfig      `mapstructure:"rabbitmq"`
	Kafka         map[string]KafkaConfig         `mapstructure:"kafka"`
	Elasticsearch map[string]ElasticsearchConfig `mapstructure:"elasticsearch"`
	Mongo         map[string]MongoConfig         `mapstructure:"mongo"`
	TableStore    map[string]TableStoreConfig    `mapstructure:"tablestore"`
	MNS           map[string]MNSConfig           `mapstructure:"mns"`
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
	Cron       *CronConfig                  `mapstructure:"cron"`
	UID        *UIDConfig                   `mapstructure:"uid"`
	Limiter    map[string]LimiterItemConfig `mapstructure:"limiter"`
	Temporal   *TemporalConfig              `mapstructure:"temporal"`
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

	v.SetDefault("cron.timezone", "Asia/Shanghai")
	v.SetDefault("cron.distributed", false)
	v.SetDefault("cron.lock_redis", "default")
	v.SetDefault("cron.lock_ttl", 5*time.Minute)

	v.SetDefault("temporal.host_port", "localhost:7233")
	v.SetDefault("temporal.namespace", "default")
	v.SetDefault("temporal.worker.max_concurrent_activity_executors", 100)
	v.SetDefault("temporal.worker.max_concurrent_workflow_task_executors", 100)
	v.SetDefault("temporal.worker.max_concurrent_local_activity_executors", 100)
}
