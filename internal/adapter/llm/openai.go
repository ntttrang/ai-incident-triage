// Package llm implements domain.Classifier: a primary OpenAI structured-output
// classifier, a deterministic heuristic fallback, and the circuit breaker +
// force-fail tooling that glues them.
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/invopop/jsonschema"
	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/ntttrang/ai-incident-triage/internal/domain"
)

// DefaultModel is the slug used when OPENAI_MODEL is unset. It accepts
// temperature 0 (reasoning-tier GPT-5 models reject it), which matters for
// reproducible classification.
const DefaultModel = "gpt-5-chat-latest"

// LLMTimeout is the per-request context budget derived from the Work context.
// It must stay strictly inside the queue JobTimeout (150s): when this
// deadline fires, the still-live Work context lets the heuristic fallback
// take over.
const LLMTimeout = 60 * time.Second

// completion is the strict-schema response contract. Fields carry no
// omitempty — the JSON-Schema reflector treats omitempty fields as optional,
// which breaks strict mode.
type completion struct {
	Category      string  `json:"category"`
	Severity      string  `json:"severity"`
	PriorityScore int     `json:"priority_score"`
	Confidence    float64 `json:"confidence"`
	Rationale     string  `json:"rationale"`
}

// completionSchema is the compiled JSON Schema for the structured output.
var completionSchema = func() map[string]any {
	reflector := &jsonschema.Reflector{
		AllowAdditionalProperties: false,
	}
	s := reflector.Reflect(completion{})
	raw, err := json.Marshal(s)
	if err != nil {
		// Reflect+Marshal on a static struct cannot fail; treat as programmer error.
		panic(fmt.Sprintf("llm: marshal classification schema: %v", err))
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		panic(fmt.Sprintf("llm: unmarshal classification schema: %v", err))
	}
	return m
}()

const systemPrompt = `You triage operations incidents reported as tickets.
Classify each incident into exactly one category and one severity.

Categories: database, deployment, network, security, performance, access.
Severity: critical, high, medium, low.

Scoring guidance:
- priority_score is 1-100; production down, data loss, or active security exposure score 80+.
- confidence is 0.0-1.0; low-information tickets score below 0.5.
- rationale is one or two short sentences citing the words that drove the verdict.`

// OpenAIClassifier classifies incidents via Chat Completions structured
// outputs. It implements domain.Classifier.
type OpenAIClassifier struct {
	client  openai.Client
	model   string
	timeout time.Duration
}

// OpenAIConfig configures the OpenAI classifier. BaseURL is empty in
// production; tests point it at an httptest server.
type OpenAIConfig struct {
	APIKey  string
	Model   string
	BaseURL string
	// Timeout overrides LLMTimeout; zero means LLMTimeout. Tests use short
	// values to exercise the timeout path quickly.
	Timeout time.Duration
}

// NewOpenAIClassifier builds a classifier with one automatic retry (the SDK's
// retry backoff shares the request context, so more retries eat the fallback
// budget instead of adding resilience).
func NewOpenAIClassifier(cfg OpenAIConfig) *OpenAIClassifier {
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = LLMTimeout
	}
	opts := []option.RequestOption{option.WithMaxRetries(1)}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	if cfg.APIKey != "" {
		opts = append(opts, option.WithAPIKey(cfg.APIKey))
	}
	return &OpenAIClassifier{
		client:  openai.NewClient(opts...),
		model:   cfg.Model,
		timeout: cfg.Timeout,
	}
}

// Classify calls the model with a strict JSON schema and temperature 0, then
// unmarshals the single choice's content. The request context is a bounded
// child of ctx so a hung API call cannot outlive the Work context budget.
func (c *OpenAIClassifier) Classify(ctx context.Context, inc domain.Incident) (*domain.Classification, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	resp, err := c.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: c.model,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(systemPrompt),
			openai.UserMessage(userPrompt(inc)),
		},
		Temperature: openai.Float(0),
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &openai.ResponseFormatJSONSchemaParam{
				JSONSchema: openai.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   "incident_classification",
					Schema: completionSchema,
					Strict: openai.Bool(true),
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("openai chat completion: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("openai chat completion: no choices returned")
	}

	var out completion
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &out); err != nil {
		return nil, fmt.Errorf("openai decode completion: %w", err)
	}
	// The strict schema constrains types, not ranges or vocabulary. Rejecting
	// here (rather than at the database CHECK) makes a bad reply fail the
	// primary classifier, so the resilient chain degrades to the heuristic
	// instead of burning all retries on verdicts that can never persist.
	if !domain.KnownCategory(out.Category) || !domain.KnownSeverity(out.Severity) {
		return nil, fmt.Errorf("openai completion: category/severity outside vocabulary (%q/%q)", out.Category, out.Severity)
	}
	if out.PriorityScore < 1 || out.PriorityScore > 100 {
		return nil, fmt.Errorf("openai completion: priority_score %d outside 1-100", out.PriorityScore)
	}
	if out.Confidence < 0 || out.Confidence > 1 {
		return nil, fmt.Errorf("openai completion: confidence %v outside 0.0-1.0", out.Confidence)
	}
	return &domain.Classification{
		Category:      out.Category,
		Severity:      out.Severity,
		PriorityScore: out.PriorityScore,
		Confidence:    out.Confidence,
		Rationale:     out.Rationale,
		Source:        domain.ClassifySourceLLM,
	}, nil
}

// userPrompt renders the incident projection the model sees. Raw webhook
// bodies stay in the database — the classifier gets the parsed projection.
func userPrompt(inc domain.Incident) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Summary: %s\n", inc.Summary)
	if inc.Description != "" {
		fmt.Fprintf(&b, "Description: %s\n", inc.Description)
	}
	if len(inc.Labels) > 0 {
		fmt.Fprintf(&b, "Labels: %s\n", strings.Join(inc.Labels, ", "))
	}
	if inc.IssueType != "" {
		fmt.Fprintf(&b, "Issue type: %s\n", inc.IssueType)
	}
	if inc.Priority != "" {
		fmt.Fprintf(&b, "Reported priority: %s\n", inc.Priority)
	}
	return b.String()
}
