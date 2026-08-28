package httpadapter

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ntttrang/ai-incident-triage/internal/domain"
)

// jiraIssueEvent is the projection of a Jira issue webhook this service cares
// about. Unknown fields are ignored; nested objects may be absent or null and
// still parse. Field shape follows Jira Cloud issue webhooks: issue.fields.*
// (summary, ADF description, issuetype, priority, status, labels, reporter)
// plus top-level timestamp/webhookEvent and the X-Atlassian-Webhook-Identifier
// delivery header.
type jiraIssueEvent struct {
	Timestamp    int64  `json:"timestamp"`
	WebhookEvent string `json:"webhookEvent"`
	Issue        struct {
		ID     int    `json:"id"`
		Key    string `json:"key"`
		Fields struct {
			Summary     string          `json:"summary"`
			Description json.RawMessage `json:"description"` // ADF object, plain string, or null
			IssueType   struct {
				Name string `json:"name"`
			} `json:"issuetype"`
			Priority struct {
				Name string `json:"name"`
			} `json:"priority"`
			Status struct {
				Name string `json:"name"`
			} `json:"status"`
			Labels   []string `json:"labels"`
			Reporter struct {
				DisplayName string `json:"displayName"`
			} `json:"reporter"`
			Created string `json:"created"`
		} `json:"fields"`
	} `json:"issue"`
}

// adfNode is one node of an Atlassian Document Format tree.
type adfNode struct {
	Type    string    `json:"type"`
	Text    string    `json:"text"`
	Content []adfNode `json:"content"`
}

// parseJiraEvent maps a Jira issue webhook body onto an Incident. Tolerant by
// design: unknown fields and missing optional fields are ignored. Required:
// issue.key and issue.fields.summary — anything else is ErrInvalidInput so the
// handler answers 400. An absent, null, or unparseable description projects to
// an empty string (real issues legitimately ship without one); the verbatim
// body is always kept in Raw.
func parseJiraEvent(body []byte) (*domain.Incident, error) {
	var event jiraIssueEvent
	if err := json.Unmarshal(body, &event); err != nil {
		return nil, fmt.Errorf("parse jira event: invalid json: %w", domain.ErrInvalidInput)
	}
	if event.Issue.Key == "" {
		return nil, fmt.Errorf("parse jira event: missing issue key: %w", domain.ErrInvalidInput)
	}
	if event.Issue.Fields.Summary == "" {
		return nil, fmt.Errorf("parse jira event: missing summary: %w", domain.ErrInvalidInput)
	}

	labels := event.Issue.Fields.Labels
	if labels == nil {
		labels = []string{}
	}

	return &domain.Incident{
		Source:         "jira",
		ExternalID:     event.Issue.Key,
		Summary:        event.Issue.Fields.Summary,
		Description:    descriptionText(event.Issue.Fields.Description),
		IssueType:      event.Issue.Fields.IssueType.Name,
		Priority:       event.Issue.Fields.Priority.Name,
		Labels:         labels,
		Reporter:       event.Issue.Fields.Reporter.DisplayName,
		Status:         domain.StatusReceived,
		Raw:            body,
		LastDeliveryID: nil, // set by the handler from the delivery header
	}, nil
}

// descriptionText projects a Jira description onto plain text. Jira Cloud sends
// ADF ({"type":"doc","content":[...]}); Jira Server may send a plain string;
// the field can also be null. Anything unrecognized projects to "".
func descriptionText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}

	var plain string
	if err := json.Unmarshal(raw, &plain); err == nil {
		return strings.TrimSpace(plain)
	}

	var doc adfNode
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ""
	}
	parts := collectADFText(doc, nil)
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// collectADFText walks an ADF tree depth-first gathering text node values.
// Inline runs inside one block join with a space; blocks join with newline.
func collectADFText(node adfNode, parts []string) []string {
	blockTypes := map[string]bool{"paragraph": true, "heading": true, "codeBlock": true, "listItem": true, "blockquote": true}
	var inline []string

	var walk func(n adfNode)
	walk = func(n adfNode) {
		if n.Text != "" {
			inline = append(inline, n.Text)
		}
		for _, child := range n.Content {
			walk(child)
			if blockTypes[child.Type] && len(inline) > 0 {
				parts = append(parts, strings.Join(inline, " "))
				inline = nil
			}
		}
	}
	walk(node)
	if len(inline) > 0 {
		parts = append(parts, strings.Join(inline, " "))
	}
	return parts
}
