package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds application configuration loaded from the environment.
type Config struct {
	Env              string
	Port             string
	LogLevel         string
	ServiceName      string
	ServiceVersion   string
	OTLPEndpoint     string
	TraceSampleRatio float64
	DBHost           string
	DBPort           string
	DBUser           string
	DBPassword       string
	DBName           string
	DBSSLMode        string
	DBMaxConns       int32
	DBMinConns       int32
	MigrationsPath   string
	WebhookSecret    string

	// Classification (worker). An empty OpenAI API key is allowed: the
	// resilient classifier then degrades to the heuristic fallback on every
	// job, which is the documented no-credential demo mode.
	OpenAIAPIKey      string
	OpenAIModel       string
	OpenAITimeoutSecs int
	ClassifyForceFail string
	WorkerMetricsPort string
}

// TracingEnabled reports whether an OTLP endpoint is configured.
func (c *Config) TracingEnabled() bool {
	return c.OTLPEndpoint != ""
}

// Load reads configuration from environment variables with sensible defaults.
func Load() (*Config, error) {
	cfg := &Config{
		Env:            getEnv("APP_ENV", "development"),
		Port:           getEnv("APP_PORT", "8085"),
		LogLevel:       getEnv("LOG_LEVEL", "info"),
		ServiceName:    getEnv("OTEL_SERVICE_NAME", "ai-incident-triage"),
		ServiceVersion: getEnv("SERVICE_VERSION", "1.0.0"),
		OTLPEndpoint:   getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		DBHost:         getEnv("DB_HOST", "localhost"),
		DBPort:         getEnv("DB_PORT", "5432"),
		DBUser:         getEnv("DB_USER", "postgres"),
		DBPassword:     getEnv("DB_PASSWORD", "postgres"),
		DBName:         getEnv("DB_NAME", "ai_incident_triage"),
		DBSSLMode:      getEnv("DB_SSLMODE", "disable"),
		DBMaxConns:     int32(getEnvInt("DB_MAX_CONNS", 10)),
		DBMinConns:     int32(getEnvInt("DB_MIN_CONNS", 2)),
		MigrationsPath: getEnv("MIGRATIONS_PATH", "file://migrations"),
	}

	ratio, err := strconv.ParseFloat(getEnv("OTEL_TRACES_SAMPLER_ARG", "1.0"), 64)
	if err != nil {
		return nil, fmt.Errorf("invalid OTEL_TRACES_SAMPLER_ARG: %w", err)
	}
	if ratio < 0 || ratio > 1 {
		return nil, fmt.Errorf("OTEL_TRACES_SAMPLER_ARG must be between 0 and 1")
	}
	cfg.TraceSampleRatio = ratio

	// Fail-closed by design: the webhook endpoint must never boot with an
	// unknown secret (an open door to forged incidents). No skip path, no dev
	// bypass — seed and local compose both provide a secret.
	cfg.WebhookSecret = os.Getenv("WEBHOOK_SECRET")
	if cfg.WebhookSecret == "" {
		return nil, fmt.Errorf("WEBHOOK_SECRET must be set (see .env.example)")
	}

	cfg.OpenAIAPIKey = os.Getenv("OPENAI_API_KEY")
	cfg.OpenAIModel = getEnv("OPENAI_MODEL", "gpt-5-chat-latest")
	cfg.OpenAITimeoutSecs = getEnvInt("OPENAI_TIMEOUT_SECS", 60)
	if cfg.OpenAITimeoutSecs <= 0 {
		return nil, fmt.Errorf("OPENAI_TIMEOUT_SECS must be positive")
	}
	cfg.ClassifyForceFail = os.Getenv("CLASSIFY_FORCE_FAIL")
	switch cfg.ClassifyForceFail {
	case "", "llm", "heuristic", "all":
	default:
		return nil, fmt.Errorf("invalid CLASSIFY_FORCE_FAIL %q (want llm, heuristic, all, or empty)", cfg.ClassifyForceFail)
	}
	cfg.WorkerMetricsPort = getEnv("WORKER_METRICS_PORT", "8086")

	return cfg, nil
}

// DatabaseURL builds a PostgreSQL connection string.
func (c *Config) DatabaseURL() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=%s",
		c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName, c.DBSSLMode,
	)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
