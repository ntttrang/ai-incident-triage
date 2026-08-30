package domain

// ClassifySource identifies which classifier produced a stored verdict.
const (
	ClassifySourceLLM       = "llm"
	ClassifySourceHeuristic = "heuristic"
)

// Categories is the closed category vocabulary. The LLM prompt constrains the
// model to these values and the heuristic fallback maps into them, so phase-5
// evals always compare like with like.
var Categories = []string{
	"database",
	"deployment",
	"network",
	"security",
	"performance",
	"access",
}

// Severities is the closed severity vocabulary, ordered most severe first.
var Severities = []string{
	"critical",
	"high",
	"medium",
	"low",
}

// KnownCategory reports whether c is one of the defined categories.
func KnownCategory(c string) bool {
	switch c {
	case "database", "deployment", "network", "security", "performance", "access":
		return true
	}
	return false
}

// KnownSeverity reports whether s is one of the defined severities.
func KnownSeverity(s string) bool {
	switch s {
	case "critical", "high", "medium", "low":
		return true
	}
	return false
}

// Classification is the stored verdict for one incident. Source records which
// classifier answered ("llm" or "heuristic") so degradation is observable.
type Classification struct {
	Category         string
	Severity         string
	PriorityScore    int
	Confidence       float64
	Rationale        string
	SuggestedRunbook string
	Source           string
}
