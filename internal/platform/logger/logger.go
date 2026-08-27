package logger

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

// Options configures the application logger.
type Options struct {
	Level   string
	Service string
	Env     string
}

// New creates a structured JSON slog logger at the given level.
func New(level string) *slog.Logger {
	return NewWithOptions(Options{Level: level})
}

// NewWithOptions creates a JSON slog logger with service attrs and trace correlation.
func NewWithOptions(opts Options) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(opts.Level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	base := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})

	var attrs []slog.Attr
	if opts.Service != "" {
		attrs = append(attrs, slog.String("service", opts.Service))
	}
	if opts.Env != "" {
		attrs = append(attrs, slog.String("env", opts.Env))
	}

	handler := &traceHandler{Handler: base.WithAttrs(attrs)}
	return slog.New(handler)
}

// traceHandler injects trace_id and span_id from the context when present.
type traceHandler struct {
	slog.Handler
}

func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	span := trace.SpanFromContext(ctx)
	sc := span.SpanContext()
	if sc.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.Handler.Handle(ctx, r)
}

func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{Handler: h.Handler.WithGroup(name)}
}
