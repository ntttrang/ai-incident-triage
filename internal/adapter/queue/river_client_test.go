package queue_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ntttrang/ai-incident-triage/internal/adapter/queue"
)

// The LLM budget must degrade inside the Work budget: 120s leaves exactly the
// 30s write margin, anything larger (or non-positive) must fail startup.
func TestCheckLLMTimeoutMargin(t *testing.T) {
	require.NoError(t, queue.CheckLLMTimeoutMargin(60*time.Second))
	require.NoError(t, queue.CheckLLMTimeoutMargin(120*time.Second))

	for _, bad := range []time.Duration{0, -time.Second, 121 * time.Second, 150 * time.Second} {
		assert.Error(t, queue.CheckLLMTimeoutMargin(bad), "%s must be rejected", bad)
	}
}
