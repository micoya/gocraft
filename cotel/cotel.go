package cotel

import (
	"context"
	"errors"
	"net/http"

	"go.opentelemetry.io/otel"

	"github.com/micoya/gocraft/config"
)

// Provider 持有 OTel TracerProvider 和 MeterProvider 的生命周期。
type Provider struct {
	shutdowns   []func(context.Context) error
	promHandler http.Handler
}

// New 根据配置初始化 OTel SDK，设置全局 TracerProvider 和 MeterProvider。
// cfg 为 nil 时返回 no-op Provider（Shutdown 安全调用、PrometheusHandler 返回 nil）。
func New(ctx context.Context, cfg *config.OtelConfig, serviceName string) (*Provider, error) {
	p := &Provider{}
	if cfg == nil {
		return p, nil
	}

	tp, err := newTracerProvider(ctx, cfg.Trace, serviceName)
	if err != nil {
		return nil, err
	}
	otel.SetTracerProvider(tp)
	p.shutdowns = append(p.shutdowns, tp.Shutdown)

	mp, promHandler, err := newMeterProvider(ctx, cfg.Metric, serviceName)
	if err != nil {
		_ = tp.Shutdown(ctx)
		return nil, err
	}
	otel.SetMeterProvider(mp)
	p.shutdowns = append(p.shutdowns, mp.Shutdown)
	p.promHandler = promHandler

	return p, nil
}

// PrometheusHandler 返回 Prometheus metrics 的 HTTP handler。
// 若 OTel 未启用则返回 nil。
func (p *Provider) PrometheusHandler() http.Handler {
	return p.promHandler
}

// Shutdown 优雅关闭所有 provider，flush 未发送的 span/metric。
func (p *Provider) Shutdown(ctx context.Context) error {
	var errs []error
	for i := len(p.shutdowns) - 1; i >= 0; i-- {
		if err := p.shutdowns[i](ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
