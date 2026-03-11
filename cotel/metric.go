package cotel

import (
	"context"
	"fmt"
	"net/http"

	promclient "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	"github.com/micoya/gocraft/config"
)

func newMeterProvider(ctx context.Context, cfg config.OtelMetricConfig, serviceName string) (*sdkmetric.MeterProvider, http.Handler, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(serviceName)),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("cotel: build resource: %w", err)
	}

	registry := promclient.NewRegistry()
	promExporter, err := otelprom.New(otelprom.WithRegisterer(registry))
	if err != nil {
		return nil, nil, fmt.Errorf("cotel: create prometheus exporter: %w", err)
	}

	opts := []sdkmetric.Option{
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(promExporter),
	}

	if cfg.Endpoint != "" {
		expOpts := []otlpmetricgrpc.Option{
			otlpmetricgrpc.WithEndpoint(cfg.Endpoint),
		}
		if cfg.Insecure {
			expOpts = append(expOpts, otlpmetricgrpc.WithInsecure())
		}
		otlpExporter, err := otlpmetricgrpc.New(ctx, expOpts...)
		if err != nil {
			return nil, nil, fmt.Errorf("cotel: create metric exporter: %w", err)
		}
		opts = append(opts, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(otlpExporter)))
	}

	mp := sdkmetric.NewMeterProvider(opts...)
	handler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	return mp, handler, nil
}
