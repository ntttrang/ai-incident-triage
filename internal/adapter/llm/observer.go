package llm

import "time"

// Observer receives LLM request telemetry. platform/metrics implements it; a
// nil observer is fine — every adapter treats it as optional.
type Observer interface {
	// ObserveLLMRequest reports one OpenAI request round trip by model,
	// failed requests included (the timeout story lives in the failing tail).
	ObserveLLMRequest(model string, duration time.Duration, failed bool)
	// ObserveLLMTokens reports token consumption by model and type
	// (prompt|completion).
	ObserveLLMTokens(model, tokenType string, count int64)
	// ObserveFallbackTrip reports one classification served by the heuristic
	// fallback after the LLM failed or the breaker was open.
	ObserveFallbackTrip()
}
