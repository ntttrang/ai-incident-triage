package tracing

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// MetricReaderInterval matches Prometheus' 15s scrape interval closely enough
// that a scrape between exports still sees fresh values.
const MetricReaderInterval = 15 * time.Second

// InitMeter configures the global MeterProvider for OTLP metric export (the
// river.* tier emitted by the otelriver middleware). When OTLPEndpoint is
// empty, the noop provider stays installed and Shutdown is a no-op.
func InitMeter(ctx context.Context, cfg Config) (ShutdownFunc, error) {
	if cfg.OTLPEndpoint == "" {
		return func(context.Context) error { return nil }, nil
	}

	exporter, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpoint(stripScheme(cfg.OTLPEndpoint)),
		otlpmetricgrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("create otlp metric exporter: %w", err)
	}

	res, err := newResource(ctx, cfg)
	if err != nil {
		return nil, err
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter,
			sdkmetric.WithInterval(MetricReaderInterval))),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	return mp.Shutdown, nil
}
