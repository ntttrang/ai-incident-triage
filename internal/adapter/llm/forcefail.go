package llm

import (
	"context"
	"errors"
	"fmt"

	"github.com/ntttrang/ai-incident-triage/internal/domain"
)

// ForceFailMode selects which classifier(s) the CLASSIFY_FORCE_FAIL knob
// short-circuits. Test/demo only: it exists so degradation paths are
// demonstrable without recompiling or breaking real credentials.
type ForceFailMode string

// ForceFailNone disables the knob; the other values short-circuit that
// classifier for the process lifetime.
const (
	ForceFailNone      ForceFailMode = ""
	ForceFailLLM       ForceFailMode = "llm"
	ForceFailHeuristic ForceFailMode = "heuristic"
	ForceFailAll       ForceFailMode = "all"
)

// ErrForcedFailure is the sentinel injected by the knob.
var ErrForcedFailure = errors.New("classifier force-failed via CLASSIFY_FORCE_FAIL")

// ParseForceFailMode validates the knob value. Unknown values are an error at
// startup, not a silent no-op.
func ParseForceFailMode(s string) (ForceFailMode, error) {
	switch ForceFailMode(s) {
	case ForceFailNone, ForceFailLLM, ForceFailHeuristic, ForceFailAll:
		return ForceFailMode(s), nil
	default:
		return ForceFailNone, fmt.Errorf("invalid CLASSIFY_FORCE_FAIL %q (want llm, heuristic, all, or empty)", s)
	}
}

// ApplyForceFail wraps the named classifier(s) with always-fail shims.
// Unselected classifiers pass through untouched.
func ApplyForceFail(primary, fallback domain.Classifier, mode ForceFailMode) (domain.Classifier, domain.Classifier) {
	if mode == ForceFailLLM || mode == ForceFailAll {
		primary = failingClassifier{inner: primary, name: "llm"}
	}
	if mode == ForceFailHeuristic || mode == ForceFailAll {
		fallback = failingClassifier{inner: fallback, name: "heuristic"}
	}
	return primary, fallback
}

type failingClassifier struct {
	inner domain.Classifier
	name  string
}

// Classify always fails, simulating an outage of the wrapped classifier.
func (f failingClassifier) Classify(ctx context.Context, inc domain.Incident) (*domain.Classification, error) {
	if f.inner != nil {
		// Keep the inner call so a wrapper also sees realistic context
		// cancellation timing in tests; its result is discarded.
		_, _ = f.inner.Classify(ctx, inc)
	}
	return nil, fmt.Errorf("%s: %w", f.name, ErrForcedFailure)
}
