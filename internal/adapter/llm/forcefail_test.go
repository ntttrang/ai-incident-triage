package llm_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ntttrang/ai-incident-triage/internal/adapter/llm"
	"github.com/ntttrang/ai-incident-triage/internal/domain"
)

func TestParseForceFailMode(t *testing.T) {
	for _, valid := range []string{"", "llm", "heuristic", "all"} {
		mode, err := llm.ParseForceFailMode(valid)
		require.NoError(t, err, "value %q must parse", valid)
		assert.Equal(t, valid, string(mode))
	}

	for _, invalid := range []string{"LLM", "true", "both", " "} {
		_, err := llm.ParseForceFailMode(invalid)
		require.Error(t, err, "unknown value %q must fail startup-fast, not no-op", invalid)
	}
}

func TestApplyForceFailWrapsSelectedSides(t *testing.T) {
	ok := &stubClassifier{cls: llmVerdict()}
	inc := domain.Incident{Summary: "db down"}

	cases := []struct {
		mode          llm.ForceFailMode
		primaryFails  bool
		fallbackFails bool
	}{
		{llm.ForceFailNone, false, false},
		{llm.ForceFailLLM, true, false},
		{llm.ForceFailHeuristic, false, true},
		{llm.ForceFailAll, true, true},
	}
	for _, tc := range cases {
		t.Run(string(tc.mode), func(t *testing.T) {
			primary, fallback := llm.ApplyForceFail(ok, llm.NewHeuristicClassifier(), tc.mode)

			_, pErr := primary.Classify(context.Background(), inc)
			assert.Equal(t, tc.primaryFails, errors.Is(pErr, llm.ErrForcedFailure))

			_, fErr := fallback.Classify(context.Background(), inc)
			assert.Equal(t, tc.fallbackFails, errors.Is(fErr, llm.ErrForcedFailure))
		})
	}
}

func TestApplyForceFailNonePreservesVerdicts(t *testing.T) {
	primary, fallback := llm.ApplyForceFail(&stubClassifier{cls: llmVerdict()}, llm.NewHeuristicClassifier(), llm.ForceFailNone)

	cls, err := primary.Classify(context.Background(), domain.Incident{Summary: "s"})
	require.NoError(t, err)
	assert.Equal(t, domain.ClassifySourceLLM, cls.Source)

	cls, err = fallback.Classify(context.Background(), domain.Incident{Summary: "s"})
	require.NoError(t, err)
	assert.Equal(t, domain.ClassifySourceHeuristic, cls.Source)
}
