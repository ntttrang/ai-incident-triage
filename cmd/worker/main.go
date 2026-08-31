// Package main is the composition root for the ai-incident-triage worker: a
// River consumer that classifies incidents (LLM with heuristic fallback) and
// a small HTTP server exposing /metrics for the phase-4 scrape job.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	kbadapter "github.com/ntttrang/ai-incident-triage/internal/adapter/kb"
	llmadapter "github.com/ntttrang/ai-incident-triage/internal/adapter/llm"
	postgresadapter "github.com/ntttrang/ai-incident-triage/internal/adapter/postgres"
	queueadapter "github.com/ntttrang/ai-incident-triage/internal/adapter/queue"
	"github.com/ntttrang/ai-incident-triage/internal/platform/config"
	"github.com/ntttrang/ai-incident-triage/internal/platform/database"
	"github.com/ntttrang/ai-incident-triage/internal/platform/logger"
	"github.com/ntttrang/ai-incident-triage/internal/platform/metrics"
	"github.com/ntttrang/ai-incident-triage/internal/platform/tracing"
	"github.com/ntttrang/ai-incident-triage/internal/service"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("load .env: %w", err)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	bootCtx, bootCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer bootCancel()

	shutdownTracing, err := tracing.Init(bootCtx, tracing.Config{
		ServiceName:    serviceName(cfg),
		ServiceVersion: cfg.ServiceVersion,
		Environment:    cfg.Env,
		OTLPEndpoint:   cfg.OTLPEndpoint,
		SampleRatio:    cfg.TraceSampleRatio,
	})
	if err != nil {
		return fmt.Errorf("tracing: %w", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := shutdownTracing(shutdownCtx); err != nil {
			fmt.Fprintf(os.Stderr, "tracing shutdown: %v\n", err)
		}
	}()

	// river.* metrics ride the same OTLP endpoint; noop when unconfigured.
	shutdownMeter, err := tracing.InitMeter(bootCtx, tracing.Config{
		ServiceName:    serviceName(cfg),
		ServiceVersion: cfg.ServiceVersion,
		Environment:    cfg.Env,
		OTLPEndpoint:   cfg.OTLPEndpoint,
	})
	if err != nil {
		return fmt.Errorf("meter: %w", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := shutdownMeter(shutdownCtx); err != nil {
			fmt.Fprintf(os.Stderr, "meter shutdown: %v\n", err)
		}
	}()

	log := logger.NewWithOptions(logger.Options{
		Level:   cfg.LogLevel,
		Service: serviceName(cfg),
		Env:     cfg.Env,
	})

	forceFail, err := llmadapter.ParseForceFailMode(cfg.ClassifyForceFail)
	if err != nil {
		return err
	}
	if forceFail != llmadapter.ForceFailNone {
		log.Warn("CLASSIFY_FORCE_FAIL is set — this is a test/demo knob", "mode", forceFail)
	}
	if cfg.OpenAIAPIKey == "" {
		log.Warn("OPENAI_API_KEY empty — every incident classifies via the heuristic fallback")
	}
	// Fail fast when the LLM budget cannot degrade inside the job budget.
	if err := queueadapter.CheckLLMTimeoutMargin(time.Duration(cfg.OpenAITimeoutSecs) * time.Second); err != nil {
		return err
	}

	pool, err := database.NewPool(bootCtx, cfg)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer pool.Close()

	// The API applies migrations at boot; the worker re-applies for safety
	// when started standalone (golang-migrate is a no-op when current).
	if err := database.RunMigrations(cfg, log); err != nil {
		return fmt.Errorf("migrations: %w", err)
	}

	m := metrics.New()
	// The worker's /metrics is scraped (worker:8086); without this loop its
	// db_pool_* gauges would export misleading zeros on a live endpoint.
	go collectDBPoolMetrics(pool, m)

	// Classifier chain: OpenAI primary behind a circuit breaker, heuristic
	// fallback, with the optional force-fail knob for demos and tests.
	primary, fallback := llmadapter.ApplyForceFail(
		llmadapter.NewOpenAIClassifier(llmadapter.OpenAIConfig{
			APIKey:   cfg.OpenAIAPIKey,
			Model:    cfg.OpenAIModel,
			Timeout:  time.Duration(cfg.OpenAITimeoutSecs) * time.Second,
			Observer: m,
		}),
		llmadapter.NewHeuristicClassifier(),
		forceFail,
	)
	classifier := llmadapter.NewResilientClassifier(primary, fallback,
		llmadapter.NewCircuitBreaker(llmadapter.DefaultBreakerThreshold, llmadapter.DefaultBreakerCooldown), m)

	incidentRepo := postgresadapter.NewIncidentRepository(pool)
	classifySvc := service.NewClassifyService(classifier, kbadapter.NewStaticKB(), incidentRepo, log, m)

	workers := queueadapter.NewWorkers(classifySvc, log)
	riverClient, err := queueadapter.NewWorkerClient(pool, workers)
	if err != nil {
		return fmt.Errorf("queue client: %w", err)
	}

	// Worker-process metrics are invisible without their own endpoint; the
	// API's /metrics lives on the API port. Phase 4 scrapes this one.
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{}))
	metricsSrv := &http.Server{
		Addr:              ":" + cfg.WorkerMetricsPort,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
	if err := riverClient.Start(runCtx); err != nil {
		return fmt.Errorf("start river client: %w", err)
	}
	log.Info("worker started",
		"queue", queueadapter.QueueName,
		"max_workers", queueadapter.QueueMaxWorkers,
		"model", cfg.OpenAIModel,
		"metrics_addr", metricsSrv.Addr,
	)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		runCancel()
		return err
	case sig := <-stop:
		log.Info("shutdown signal received", "signal", sig.String())
	}

	// Graceful: stop accepting jobs, let in-flight Work calls finish inside
	// the JobTimeout budget, then close the metrics server.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), queueadapter.JobTimeout+30*time.Second)
	defer stopCancel()
	if err := riverClient.Stop(stopCtx); err != nil {
		log.Error("river stop", "error", err)
	}
	if err := metricsSrv.Shutdown(stopCtx); err != nil {
		log.Error("metrics shutdown", "error", err)
	}
	log.Info("worker stopped gracefully")
	return nil
}

// serviceName names the worker process distinctly from the API in traces and
// logs. Compose sets OTEL_SERVICE_NAME to ai-incident-triage-worker already;
// local runs inherit the API's name and get the suffix appended.
func serviceName(cfg *config.Config) string {
	if strings.HasSuffix(cfg.ServiceName, "-worker") {
		return cfg.ServiceName
	}
	return cfg.ServiceName + "-worker"
}

func collectDBPoolMetrics(pool *pgxpool.Pool, m *metrics.Metrics) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		m.ObserveDBPool(pool.Stat())
		<-ticker.C
	}
}
