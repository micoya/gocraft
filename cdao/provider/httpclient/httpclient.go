package httpclient

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/sony/gobreaker"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/micoya/gocraft/cdao"
	"github.com/micoya/gocraft/config"
)

const (
	defaultTimeout         = 30 * time.Second
	defaultMaxIdleConns    = 100
	defaultIdleConnTimeout = 90 * time.Second
)

func init() {
	cdao.Register("httpclient", factory)
}

func factory(name string, raw any) (cdao.Provider, error) {
	cfg, ok := raw.(config.HTTPClientConfig)
	if !ok {
		return nil, fmt.Errorf("dao/provider/httpclient: expected config.HTTPClientConfig, got %T", raw)
	}
	return &provider{name: name, cfg: cfg}, nil
}

type provider struct {
	name   string
	cfg    config.HTTPClientConfig
	client *http.Client
}

func (p *provider) Init(_ context.Context) error {
	timeout := p.cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	maxIdleConns := p.cfg.MaxIdleConns
	if maxIdleConns <= 0 {
		maxIdleConns = defaultMaxIdleConns
	}
	idleConnTimeout := p.cfg.IdleConnTimeout
	if idleConnTimeout <= 0 {
		idleConnTimeout = defaultIdleConnTimeout
	}

	base := &http.Transport{
		MaxIdleConns:        maxIdleConns,
		MaxConnsPerHost:     p.cfg.MaxConnsPerHost,
		IdleConnTimeout:     idleConnTimeout,
		TLSHandshakeTimeout: 10 * time.Second,
		DisableCompression:  false,
	}
	if p.cfg.TLSSkipVerify {
		base.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
	}

	serverAddr := p.cfg.BaseURL
	if serverAddr == "" {
		serverAddr = "unknown"
	}

	// 传输链（由内向外）：base → otelhttp → retry → circuitbreaker
	var transport http.RoundTripper = otelhttp.NewTransport(
		base,
		otelhttp.WithTracerProvider(otel.GetTracerProvider()),
		otelhttp.WithSpanOptions(
			trace.WithAttributes(
				semconv.PeerService(p.name),
				attribute.String("server.address", serverAddr),
			),
		),
	)

	if p.cfg.Retry != nil {
		transport = newRetryTransport(transport, p.cfg.Retry)
	}

	if p.cfg.CircuitBreaker != nil {
		transport = newCBTransport(transport, p.name, p.cfg.CircuitBreaker)
	}

	p.client = &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
	return nil
}

func (p *provider) Close(_ context.Context) error {
	if p.client != nil {
		p.client.CloseIdleConnections()
		p.client = nil
	}
	return nil
}

// Health 通过向 BaseURL 发送 HEAD 请求验证连通性。
func (p *provider) Health(ctx context.Context) error {
	if p.cfg.BaseURL == "" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, p.cfg.BaseURL, nil)
	if err != nil {
		return fmt.Errorf("dao/provider/httpclient: health build request: %w", err)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("dao/provider/httpclient: health check: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("dao/provider/httpclient: health check: upstream returned %d", resp.StatusCode)
	}
	return nil
}

func (p *provider) Instance() any {
	return p.client
}

// --- retryTransport ---

// retryTransport 对幂等请求（GET/HEAD/PUT/DELETE/OPTIONS）以及网络错误自动重试。
// POST 等非幂等方法仅在纯网络错误时重试（如连接拒绝），5xx 不重试。
type retryTransport struct {
	base http.RoundTripper
	cfg  *config.RetryConfig
}

func newRetryTransport(base http.RoundTripper, cfg *config.RetryConfig) *retryTransport {
	return &retryTransport{base: base, cfg: cfg}
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	maxAttempts := t.cfg.MaxAttempts + 1
	if maxAttempts < 1 {
		maxAttempts = 4
	}
	waitMin := t.cfg.WaitMin
	if waitMin <= 0 {
		waitMin = 100 * time.Millisecond
	}
	waitMax := t.cfg.WaitMax
	if waitMax <= 0 {
		waitMax = 2 * time.Second
	}

	var (
		resp     *http.Response
		lastErr  error
	)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			wait := exponentialBackoff(attempt-1, waitMin, waitMax)
			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-time.After(wait):
			}

			// 重置 request body
			if req.GetBody != nil {
				body, err := req.GetBody()
				if err != nil {
					return nil, fmt.Errorf("httpclient: reset body: %w", err)
				}
				req.Body = body
			}
		}

		resp, lastErr = t.base.RoundTrip(req)

		if lastErr == nil {
			if resp.StatusCode < 500 {
				return resp, nil
			}
			// 5xx：消耗响应体，释放连接，然后决定是否重试
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()

			if !isIdempotent(req.Method) {
				// 非幂等方法不重试 5xx
				return resp, nil
			}
			continue
		}

		// 网络错误：所有 HTTP 方法均可重试（请求未到达服务端）
		if req.GetBody == nil && req.Body != nil {
			// 有 body 但不可重放，停止重试
			return nil, lastErr
		}
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return resp, nil
}

func isIdempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions,
		http.MethodDelete, http.MethodPut:
		return true
	}
	return false
}

// exponentialBackoff 指数退避 + ±20% 随机抖动，防止多实例同时重试形成惊群。
func exponentialBackoff(attempt int, min, max time.Duration) time.Duration {
	d := min * (1 << uint(attempt))
	if d > max {
		d = max
	}
	// ±20% jitter
	jitter := time.Duration(rand.Int64N(int64(d/5)*2+1)) - d/5
	d += jitter
	if d < min {
		d = min
	}
	return d
}

// --- circuitBreakerTransport ---

type cbTransport struct {
	base http.RoundTripper
	cb   *gobreaker.CircuitBreaker
}

func newCBTransport(base http.RoundTripper, name string, cfg *config.CircuitBreakerConfig) *cbTransport {
	maxRequests := cfg.MaxRequests
	if maxRequests == 0 {
		maxRequests = 1
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = 60 * time.Second
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	threshold := cfg.Threshold
	if threshold == 0 {
		threshold = 5
	}

	cb := gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        name,
		MaxRequests: maxRequests,
		Interval:    interval,
		Timeout:     timeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= threshold
		},
	})

	return &cbTransport{base: base, cb: cb}
}

func (t *cbTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var savedResp *http.Response
	_, err := t.cb.Execute(func() (interface{}, error) {
		resp, err := t.base.RoundTrip(req)
		if err != nil {
			return nil, err
		}
		savedResp = resp
		// 5xx 视为失败，通知熔断器计数；响应体留给调用方处理
		if resp.StatusCode >= 500 {
			return nil, fmt.Errorf("httpclient: upstream %d", resp.StatusCode)
		}
		return resp, nil
	})

	if err != nil {
		if savedResp != nil {
			// 5xx：有响应，直接返回让上层处理
			return savedResp, nil
		}
		return nil, fmt.Errorf("httpclient: circuit breaker: %w", err)
	}
	return savedResp, nil
}
