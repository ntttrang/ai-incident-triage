package httpadapter

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ntttrang/ai-incident-triage/internal/domain"
)

// canonicalEvent returns a valid Jira issue event. A non-empty description is
// substituted verbatim (JSON value or null); an empty one omits the field.
func canonicalEvent(description string) []byte {
	desc := ""
	if description != "" {
		desc = `"description":` + description + ","
	}
	return []byte(fmt.Sprintf(`{"timestamp":1785000000000,"webhookEvent":"jira:issue_created","issue":{"id":40001,"key":"OPS-42","fields":{"summary":"Prod db down",%s"issuetype":{"name":"Bug"},"priority":{"name":"Highest"},"status":{"name":"Open"},"labels":["prod","db"],"reporter":{"displayName":"Grace Hopper"},"created":"2026-08-27T08:30:00.000+0700"}}}`, desc))
}

func TestParseJiraEventFullProjection(t *testing.T) {
	body := canonicalEvent(`{"type":"doc","version":1,"content":[
		{"type":"paragraph","content":[{"type":"text","text":"Primary failed over."}]},
		{"type":"paragraph","content":[{"type":"text","text":"Payments recovering."}]}
	]}`)

	inc, err := parseJiraEvent(body)
	require.NoError(t, err)

	assert.Equal(t, "jira", inc.Source)
	assert.Equal(t, "OPS-42", inc.ExternalID)
	assert.Equal(t, "Prod db down", inc.Summary)
	assert.Equal(t, "Primary failed over.\nPayments recovering.", inc.Description)
	assert.Equal(t, "Bug", inc.IssueType)
	assert.Equal(t, "Highest", inc.Priority)
	assert.Equal(t, []string{"prod", "db"}, inc.Labels)
	assert.Equal(t, "Grace Hopper", inc.Reporter)
	assert.Empty(t, inc.Status, "initial status is the ingest transaction's decision, not the parser's")
	assert.Equal(t, body, inc.Raw, "verbatim body must be kept in Raw")
	assert.Nil(t, inc.LastDeliveryID, "delivery id comes from the header, not the body")
}

func TestParseJiraEventInlineTextJoinsWithSpace(t *testing.T) {
	body := canonicalEvent(`{"type":"doc","version":1,"content":[
		{"type":"paragraph","content":[{"type":"text","text":"foo"},{"type":"text","text":"bar"}]}
	]}`)

	inc, err := parseJiraEvent(body)
	require.NoError(t, err)
	assert.Equal(t, "foo bar", inc.Description)
}

func TestParseJiraEventDescriptionVariants(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"plain string", `"plain text here"`, "plain text here"},
		{"null", `null`, ""},
		{"absent", ``, ""},
		{"unrecognized shape", `[1,2,3]`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inc, err := parseJiraEvent(canonicalEvent(tc.raw))
			require.NoError(t, err)
			assert.Equal(t, tc.want, inc.Description)
		})
	}
}

func TestParseJiraEventMissingRequiredFields(t *testing.T) {
	noKey := `{"timestamp":1,"webhookEvent":"jira:issue_created","issue":{"id":1,"key":"","fields":{"summary":"s"}}}`
	noSummary := `{"timestamp":1,"webhookEvent":"jira:issue_created","issue":{"id":1,"key":"OPS-1","fields":{"summary":""}}}`

	for name, body := range map[string]string{
		"missing issue key":  noKey,
		"missing summary":    noSummary,
		"invalid json":       `{"issue":`,
		"not an issue event": `{"webhookEvent":"comment_created"}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseJiraEvent([]byte(body))
			require.Error(t, err)
			assert.ErrorIs(t, err, domain.ErrInvalidInput)
		})
	}
}

func TestParseJiraEventToleratesUnknownShape(t *testing.T) {
	// Extra top-level fields, missing optional nested objects: still an issue.
	body := []byte(`{"timestamp":1785000000000,"webhookEvent":"jira:issue_updated","changelist":[],"issue":{"id":9,"key":"OPS-9","fields":{"summary":"Only summary"},"extra":true}}`)

	inc, err := parseJiraEvent(body)
	require.NoError(t, err)
	assert.Equal(t, "OPS-9", inc.ExternalID)
	assert.Equal(t, "", inc.Priority)
	assert.Equal(t, []string{}, inc.Labels)
	assert.Equal(t, "", inc.Description)
	assert.False(t, errors.Is(err, domain.ErrInvalidInput))
}
