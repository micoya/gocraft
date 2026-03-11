package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad_Defaults(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load[struct{}](context.Background(), WithConfigPath(dir), WithEnvFile(""))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Name != "my_app" {
		t.Errorf("Name = %q, want %q", cfg.Name, "my_app")
	}
	if cfg.Env != "local" {
		t.Errorf("Env = %q, want %q", cfg.Env, "local")
	}
	if cfg.Log.Level != "INFO" {
		t.Errorf("Log.Level = %q, want %q", cfg.Log.Level, "INFO")
	}
	if cfg.Log.Format != "text" {
		t.Errorf("Log.Format = %q, want %q", cfg.Log.Format, "text")
	}
	if cfg.Log.Path != "stdout" {
		t.Errorf("Log.Path = %q, want %q", cfg.Log.Path, "stdout")
	}
	if !cfg.Log.WithTrace {
		t.Error("Log.WithTrace = false, want true")
	}
	if cfg.HTTPServer.ShutdownTimeout != 30*time.Second {
		t.Errorf("HTTPServer.ShutdownTimeout = %v, want %v", cfg.HTTPServer.ShutdownTimeout, 30*time.Second)
	}
	if cfg.HTTPServer.ReadTimeout != 30*time.Second {
		t.Errorf("HTTPServer.ReadTimeout = %v, want %v", cfg.HTTPServer.ReadTimeout, 30*time.Second)
	}
	if cfg.HTTPServer.WriteTimeout != 30*time.Second {
		t.Errorf("HTTPServer.WriteTimeout = %v, want %v", cfg.HTTPServer.WriteTimeout, 30*time.Second)
	}
	if cfg.HTTPServer.IdleTimeout != 60*time.Second {
		t.Errorf("HTTPServer.IdleTimeout = %v, want %v", cfg.HTTPServer.IdleTimeout, 60*time.Second)
	}
	if cfg.HTTPServer.HealthPath != "/healthz" {
		t.Errorf("HTTPServer.HealthPath = %q, want %q", cfg.HTTPServer.HealthPath, "/healthz")
	}
	if cfg.HTTPServer.MetricsPath != "/metrics" {
		t.Errorf("HTTPServer.MetricsPath = %q, want %q", cfg.HTTPServer.MetricsPath, "/metrics")
	}
	if !cfg.HTTPServer.AccessLog.Enabled {
		t.Error("HTTPServer.AccessLog.Enabled = false, want true")
	}
}

func TestLoad_FromConfigFile(t *testing.T) {
	dir := t.TempDir()
	content := `
name: testapp
env: production
log:
  level: DEBUG
  format: json
  path: /var/log/app.log
  with_trace: false
http_server:
  addr: ":8080"
  health_path: /health
  cors:
    allow_all_origins: true
    allow_methods:
      - GET
      - POST
  access_log:
    enabled: false
    format: json
otel:
  trace:
    endpoint: "http://otel-collector:4317"
    insecure: true
    sample_rate: 0.5
dao:
  database:
    primary:
      driver: mysql
      dsn: "root:pass@tcp(127.0.0.1:3306)/mydb?parseTime=true"
  redis:
    cache:
      addr: "127.0.0.1:6379"
      db: 1
      read_timeout: 3s
      write_timeout: 3s
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load[struct{}](context.Background(), WithConfigPath(dir), WithEnvFile(""))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Name != "testapp" {
		t.Errorf("Name = %q, want %q", cfg.Name, "testapp")
	}
	if cfg.Env != "production" {
		t.Errorf("Env = %q, want %q", cfg.Env, "production")
	}
	if cfg.Log.Level != "DEBUG" {
		t.Errorf("Log.Level = %q, want %q", cfg.Log.Level, "DEBUG")
	}
	if cfg.Log.Format != "json" {
		t.Errorf("Log.Format = %q, want %q", cfg.Log.Format, "json")
	}
	if cfg.Log.WithTrace {
		t.Error("Log.WithTrace = true, want false")
	}
	if cfg.HTTPServer.Addr != ":8080" {
		t.Errorf("HTTPServer.Addr = %q, want %q", cfg.HTTPServer.Addr, ":8080")
	}
	if cfg.HTTPServer.CORS == nil {
		t.Fatal("HTTPServer.CORS is nil")
	}
	if !cfg.HTTPServer.CORS.AllowAllOrigins {
		t.Error("HTTPServer.CORS.AllowAllOrigins = false, want true")
	}
	if len(cfg.HTTPServer.CORS.AllowMethods) != 2 {
		t.Errorf("HTTPServer.CORS.AllowMethods len = %d, want 2", len(cfg.HTTPServer.CORS.AllowMethods))
	}
	if cfg.HTTPServer.AccessLog.Enabled {
		t.Error("HTTPServer.AccessLog.Enabled = true, want false")
	}
	if cfg.Otel == nil || cfg.Otel.Trace.Endpoint != "http://otel-collector:4317" {
		t.Errorf("Otel.Trace.Endpoint unexpected: %v", cfg.Otel)
	}
	if cfg.Otel.Trace.SampleRate != 0.5 {
		t.Errorf("Otel.Trace.SampleRate = %v, want 0.5", cfg.Otel.Trace.SampleRate)
	}
	if !cfg.Otel.Trace.Insecure {
		t.Error("Otel.Trace.Insecure = false, want true")
	}
	if db := cfg.DAO.Database["primary"]; db.Driver != "mysql" || db.DSN != "root:pass@tcp(127.0.0.1:3306)/mydb?parseTime=true" {
		t.Errorf("DAO.Database[primary] unexpected: %+v", db)
	}
	if r := cfg.DAO.Redis["cache"]; r.Addr != "127.0.0.1:6379" || r.DB != 1 {
		t.Errorf("DAO.Redis[cache] unexpected: %+v", r)
	}
	if cfg.DAO.Redis["cache"].ReadTimeout != 3*time.Second {
		t.Errorf("DAO.Redis[cache].ReadTimeout = %v, want 3s", cfg.DAO.Redis["cache"].ReadTimeout)
	}
}

func TestLoad_EnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	content := `
name: fromfile
env: staging
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("NAME", "fromenv")
	t.Setenv("ENV", "production")

	cfg, err := Load[struct{}](context.Background(), WithConfigPath(dir), WithEnvFile(""))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Name != "fromenv" {
		t.Errorf("Name = %q, want %q", cfg.Name, "fromenv")
	}
	if cfg.Env != "production" {
		t.Errorf("Env = %q, want %q", cfg.Env, "production")
	}
}

func TestLoad_DotEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	configContent := `
name: fromfile
log:
  level: INFO
http_server:
  addr: ":8080"
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// .env 使用 __ 表示层级分隔，单 _ 保留为字段名内的单词分隔符：
	// http_server.addr → HTTP_SERVER__ADDR，log.level → LOG__LEVEL
	envContent := "NAME=fromdotenv\nLOG__LEVEL=WARN\nHTTP_SERVER__ADDR=:9090\n"
	envFile := filepath.Join(dir, ".env")
	if err := os.WriteFile(envFile, []byte(envContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// godotenv.Overload 直接写入进程环境，测试结束后需手动还原，避免污染后续用例
	t.Cleanup(func() {
		os.Unsetenv("NAME")
		os.Unsetenv("LOG__LEVEL")
		os.Unsetenv("HTTP_SERVER__ADDR")
	})

	cfg, err := Load[struct{}](context.Background(), WithConfigPath(dir), WithEnvFile(envFile))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Name != "fromdotenv" {
		t.Errorf("Name = %q, want %q", cfg.Name, "fromdotenv")
	}
	if cfg.Log.Level != "WARN" {
		t.Errorf("Log.Level = %q, want %q", cfg.Log.Level, "WARN")
	}
	if cfg.HTTPServer.Addr != ":9090" {
		t.Errorf("HTTPServer.Addr = %q, want %q", cfg.HTTPServer.Addr, ":9090")
	}
}

func TestLoad_CustomAppConfig(t *testing.T) {
	type myApp struct {
		PaymentGateway string `mapstructure:"payment_gateway"`
		MaxRetry       int    `mapstructure:"max_retry"`
	}

	dir := t.TempDir()
	content := `
name: payment-svc
app:
  payment_gateway: stripe
  max_retry: 5
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load[myApp](context.Background(), WithConfigPath(dir), WithEnvFile(""))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Name != "payment-svc" {
		t.Errorf("Name = %q, want %q", cfg.Name, "payment-svc")
	}
	if cfg.App.PaymentGateway != "stripe" {
		t.Errorf("App.PaymentGateway = %q, want %q", cfg.App.PaymentGateway, "stripe")
	}
	if cfg.App.MaxRetry != 5 {
		t.Errorf("App.MaxRetry = %d, want %d", cfg.App.MaxRetry, 5)
	}
}

func TestLoad_InvalidConfigFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(":\tinvalid yaml{{{"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load[struct{}](context.Background(), WithConfigPath(dir), WithEnvFile(""))
	if err == nil {
		t.Error("Load() expected error for invalid yaml, got nil")
	}
}
