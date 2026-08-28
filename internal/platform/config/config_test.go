package config

import (
	"strings"
	"testing"
)

// The webhook endpoint is fail-closed: no secret, no boot. This is the config
// half of that guarantee; handler tests cover the 401 path.
func TestLoadRejectsMissingWebhookSecret(t *testing.T) {
	t.Setenv("WEBHOOK_SECRET", "")

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "WEBHOOK_SECRET") {
		t.Fatalf("expected WEBHOOK_SECRET validation error, got %v", err)
	}
}

func TestLoadAcceptsWebhookSecret(t *testing.T) {
	t.Setenv("WEBHOOK_SECRET", "test-secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.WebhookSecret != "test-secret" {
		t.Fatalf("WebhookSecret = %q", cfg.WebhookSecret)
	}
}
