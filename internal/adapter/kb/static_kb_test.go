package kb_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ntttrang/ai-incident-triage/internal/adapter/kb"
	"github.com/ntttrang/ai-incident-triage/internal/domain"
)

func TestStaticKBCoversEveryCategory(t *testing.T) {
	kb := kb.NewStaticKB()
	for _, category := range domain.Categories {
		ref, err := kb.RunbookForCategory(context.Background(), category)
		require.NoError(t, err)
		require.NotNil(t, ref, "category %q must have a runbook", category)
		assert.NotEmpty(t, ref.URL)
		assert.NotEmpty(t, ref.Title)
	}
}

func TestStaticKBUnknownCategoryReturnsNil(t *testing.T) {
	kb := kb.NewStaticKB()
	ref, err := kb.RunbookForCategory(context.Background(), "definitely-not-a-category")
	require.NoError(t, err, "a KB miss degrades the suggestion, never the job")
	assert.Nil(t, ref)
}
