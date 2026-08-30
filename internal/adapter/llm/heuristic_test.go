package llm_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ntttrang/ai-incident-triage/internal/adapter/llm"
	"github.com/ntttrang/ai-incident-triage/internal/domain"
)

func TestHeuristicClassifiesByKeyword(t *testing.T) {
	cases := []struct {
		name        string
		summary     string
		description string
		labels      []string
		category    string
		severity    string
		score       int
	}{
		{
			name: "database outage is critical", summary: "Prod database down",
			category: "database", severity: "critical", score: 85,
		},
		{
			name: "security exposure is critical", summary: "Unauthorized access attempt", description: "possible breach of user data",
			category: "security", severity: "critical", score: 85,
		},
		{
			name: "degraded network is high", summary: "VPN connectivity degraded for EU office",
			category: "network", severity: "high", score: 60,
		},
		{
			name: "deployment keyword with low severity", summary: "Release pipeline question",
			category: "deployment", severity: "low", score: 20,
		},
		{
			name: "performance keywords", summary: "Slow checkout API, growing backlog",
			category: "performance", severity: "low", score: 20,
		},
		{
			name: "labels feed matching", summary: "Please help", labels: []string{"database"},
			category: "database", severity: "low", score: 20,
		},
		{
			name: "unmatched text defaults to access/low", summary: "Question about expense policy",
			category: "access", severity: "low", score: 20,
		},
	}

	h := llm.NewHeuristicClassifier()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cls, err := h.Classify(context.Background(), domain.Incident{
				Summary: tc.summary, Description: tc.description, Labels: tc.labels,
			})
			require.NoError(t, err)
			assert.Equal(t, tc.category, cls.Category)
			assert.Equal(t, tc.severity, cls.Severity)
			assert.Equal(t, tc.score, cls.PriorityScore)
			assert.Equal(t, domain.ClassifySourceHeuristic, cls.Source)
			assert.InEpsilon(t, 0.3, cls.Confidence, 0.001, "fallback confidence is fixed and low")
			assert.Contains(t, cls.Rationale, llm.HeuristicVersion)
		})
	}
}

func TestHeuristicVerdictStaysInVocabulary(t *testing.T) {
	// Whatever the text, the verdict must always be storable: SaveClassification
	// rejects unknown categories/severities, so the fallback may never emit one.
	h := llm.NewHeuristicClassifier()
	for _, summary := range []string{
		"db down", "cert expiring soon", "k8s pod crashloop", "weird ùñïçø∂é text", "",
	} {
		cls, err := h.Classify(context.Background(), domain.Incident{Summary: summary})
		require.NoError(t, err)
		assert.True(t, domain.KnownCategory(cls.Category), "category %q must be known (summary %q)", cls.Category, summary)
		assert.True(t, domain.KnownSeverity(cls.Severity), "severity %q must be known (summary %q)", cls.Severity, summary)
	}
}
