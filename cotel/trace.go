package cotel

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	"github.com/micoya/gocraft/config"
)

func newTracerProvider(ctx context.Context, cfg config.OtelTraceConfig, serviceName string) (*sdktrace.TracerProvider, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(serviceName)),
	)
	if err != nil {
		return nil, fmt.Errorf("cotel: build resource: %w", err)
	}

	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(buildSampler(cfg.SampleRate)),
	}

	if cfg.Endpoint != "" {
		expOpts := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpoint(cfg.Endpoint),
		}
		if cfg.Insecure {
			expOpts = append(expOpts, otlptracegrpc.WithInsecure())
		}
		exp, err := otlptracegrpc.New(ctx, expOpts...)
		if err != nil {
			return nil, fmt.Errorf("cotel: create trace exporter: %w", err)
		}
		opts = append(opts, sdktrace.WithBatcher(exp))
	}

	return sdktrace.NewTracerProvider(opts...), nil
}

func buildSampler(rate float64) sdktrace.Sampler {
	switch {
	case rate <= 0:
		return sdktrace.NeverSample()
	case rate >= 1:
		return sdktrace.AlwaysSample()
	default:
		return sdktrace.TraceIDRatioBased(rate)
	}
}
