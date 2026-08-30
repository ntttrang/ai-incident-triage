// Package kb provides the knowledge-base side of classification. This phase
// ships a static stub; the Notion RAG adapter lands in a later phase behind
// the same domain.KnowledgeBase contract.
package kb

import (
	"context"

	"github.com/ntttrang/ai-incident-triage/internal/domain"
)

// StaticKB maps categories to canned runbook references.
type StaticKB struct {
	runbooks map[string]domain.RunbookRef
}

// NewStaticKB returns the stub knowledge base.
func NewStaticKB() *StaticKB {
	return &StaticKB{
		runbooks: map[string]domain.RunbookRef{
			"database":    {Title: "Database incident runbook", URL: "https://runbooks.example.com/database"},
			"deployment":  {Title: "Deployment rollback runbook", URL: "https://runbooks.example.com/deployment"},
			"network":     {Title: "Network troubleshooting runbook", URL: "https://runbooks.example.com/network"},
			"security":    {Title: "Security incident response runbook", URL: "https://runbooks.example.com/security"},
			"performance": {Title: "Performance triage runbook", URL: "https://runbooks.example.com/performance"},
			"access":      {Title: "Access request runbook", URL: "https://runbooks.example.com/access"},
		},
	}
}

// RunbookForCategory returns the canned runbook for a classified category, or
// nil when the category has no entry (the incident keeps an empty
// suggested_runbook rather than failing the job).
func (k *StaticKB) RunbookForCategory(_ context.Context, category string) (*domain.RunbookRef, error) {
	if ref, ok := k.runbooks[category]; ok {
		return &ref, nil
	}
	return nil, nil
}
