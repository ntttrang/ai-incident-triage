package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/ntttrang/ai-incident-triage/internal/domain"
)

// HeuristicVersion identifies the rule set that produced a fallback verdict.
// The phase-5 eval harness reports it alongside accuracy so baseline drift is
// detectable.
const HeuristicVersion = "heuristic-v1"

// categoryKeywords maps lowercase substrings to categories; the first hit in
// scan order wins (earlier rows are more specific).
var categoryKeywords = []struct {
	category string
	words    []string
}{
	{"database", []string{"database", "db ", "postgres", "mysql", "sql", "migration", "replication", "query timeout"}},
	{"security", []string{"security", "vulnerability", "cve", "breach", "unauthorized", "suspicious", "malware", "phishing", "cert"}},
	{"network", []string{"network", "latency", "packet", "dns", "vpn", "connectivity", "timeout"}},
	// No bare "ci" here: it substring-matches "incident", which would route
	// half this system's own traffic to deployment.
	{"deployment", []string{"deploy", "release", "rollback", "build", "pipeline", "k8s", "kubernetes", "pod"}},
	{"performance", []string{"memory", "cpu", "slow", "disk usage", "backlog", "queue", "throughput"}},
	{"access", []string{"access", "permission", "login", "credentials", "onboarding", "request"}},
}

// escalationWords bump severity when present.
var escalationWords = map[string][]string{
	"critical": {"outage", "down", "data loss", "production", "prod", "emergency", "all users", "breach"},
	"high":     {"degraded", "partial outage", "failover", "high cpu", "high memory", "disk full", "latency"},
}

// HeuristicClassifier is the deterministic fallback. It never touches the
// network and always succeeds, so it implements domain.Classifier with a nil
// error path only (its Classify can only fail when force-failed in tests).
type HeuristicClassifier struct{}

// NewHeuristicClassifier returns the stateless fallback classifier.
func NewHeuristicClassifier() *HeuristicClassifier { return &HeuristicClassifier{} }

// Classify maps keyword hits to a category/severity verdict with a fixed
// confidence: the rules are shallow, so their self-reported confidence is low
// by design.
func (h *HeuristicClassifier) Classify(_ context.Context, inc domain.Incident) (*domain.Classification, error) {
	text := strings.ToLower(inc.Summary + "\n" + inc.Description + "\n" + strings.Join(inc.Labels, "\n"))

	category := "access" // closed vocabulary default for unmatched text
	matched := ""
	for _, entry := range categoryKeywords {
		for _, w := range entry.words {
			if strings.Contains(text, w) {
				category, matched = entry.category, w
				break
			}
		}
		if matched != "" {
			break
		}
	}

	severity := "low"
	for _, sev := range []string{"critical", "high"} {
		for _, w := range escalationWords[sev] {
			if strings.Contains(text, w) {
				severity = sev
				break
			}
		}
		if severity == sev {
			break
		}
	}

	score := 20
	switch severity {
	case "critical":
		score = 85
	case "high":
		score = 60
	case "medium":
		score = 40
	}

	rationale := fmt.Sprintf("no keywords matched; category defaulted (%s)", HeuristicVersion)
	if matched != "" {
		rationale = fmt.Sprintf("matched keyword %q (%s)", matched, HeuristicVersion)
	}

	return &domain.Classification{
		Category:      category,
		Severity:      severity,
		PriorityScore: score,
		Confidence:    0.3,
		Rationale:     rationale,
		Source:        domain.ClassifySourceHeuristic,
	}, nil
}
