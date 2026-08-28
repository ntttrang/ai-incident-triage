package httpadapter

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ntttrang/ai-incident-triage/internal/domain"
	"github.com/ntttrang/ai-incident-triage/internal/service"
)

// deliveryHeader carries Jira's event id. Jira redelivers the same event (up to
// 5 times) with the same identifier, which drives delivery-level dedup.
const deliveryHeader = "X-Atlassian-Webhook-Identifier"

// signatureHeader carries the HMAC-SHA256 of the raw body, "sha256=<hex>".
// This is Jira Cloud's native webhook auth scheme (secret set at webhook
// registration).
const signatureHeader = "X-Hub-Signature"

// maxWebhookBodyBytes bounds the request body before it is buffered for HMAC
// verification. Jira payloads are small; without a cap an unauthenticated
// caller could force unbounded allocations ahead of signature checking.
const maxWebhookBodyBytes = 1 << 20 // 1 MiB

// WebhookHandler receives issue-tracker webhooks.
type WebhookHandler struct {
	svc    *service.IncidentService
	secret []byte
	log    *slog.Logger
}

// NewWebhookHandler returns a handler verifying bodies with the shared secret.
func NewWebhookHandler(svc *service.IncidentService, secret string, log *slog.Logger) *WebhookHandler {
	return &WebhookHandler{svc: svc, secret: []byte(secret), log: log}
}

// ReceiveJira verifies and stores one Jira webhook delivery.
//
// Responses: 202 created, 200 updated, 200 duplicate (no-op), 413 body over
// 1 MiB, 401 bad signature, 400 unparseable or non-issue-shaped body, 409
// delivery identifier collision, 500 unexpected failure.
func (h *WebhookHandler) ReceiveJira(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxWebhookBodyBytes)
	body, err := c.GetRawData()
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			c.JSON(http.StatusRequestEntityTooLarge, ErrorResponse{Error: "body exceeds 1 MiB limit"})
			return
		}
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "cannot read body"})
		return
	}

	if err := verifySignature(body, c.GetHeader(signatureHeader), h.secret); err != nil {
		h.log.WarnContext(c.Request.Context(), "webhook signature rejected",
			"error", err.Error(),
			"request_id", c.GetString("request_id"),
			"remote", c.ClientIP(),
		)
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	inc, err := parseJiraEvent(body)
	if err != nil {
		mapError(c, h.log, err)
		return
	}

	if deliveryID := c.GetHeader(deliveryHeader); deliveryID != "" {
		inc.LastDeliveryID = &deliveryID
	}

	outcome, err := h.svc.ReceiveIncident(c.Request.Context(), inc)
	if err != nil {
		mapError(c, h.log, err)
		return
	}

	switch outcome {
	case domain.UpsertCreated:
		c.JSON(http.StatusAccepted, WebhookResponse{Outcome: string(outcome), ID: &inc.ID, Status: string(domain.StatusReceived)})
	case domain.UpsertUpdated:
		c.JSON(http.StatusOK, WebhookResponse{Outcome: string(outcome), ID: &inc.ID, Status: string(domain.StatusReceived)})
	default:
		c.JSON(http.StatusOK, WebhookResponse{Outcome: string(domain.UpsertDuplicate)})
	}
}

// verifySignature checks "sha256=<hex>" HMAC-SHA256 of the raw body against the
// shared secret using constant-time comparison (crypto/hmac.Equal only; an
// early-exit string compare would leak prefix matches).
func verifySignature(body []byte, header string, secret []byte) error {
	const prefix = "sha256="
	if header == "" {
		return errors.New("missing " + signatureHeader + " header")
	}
	if !strings.HasPrefix(header, prefix) {
		return errors.New(signatureHeader + " must start with " + prefix)
	}
	sig, err := hex.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return errors.New(signatureHeader + " is not valid hex")
	}

	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	if !hmac.Equal(mac.Sum(nil), sig) {
		return errors.New("signature mismatch")
	}
	return nil
}
