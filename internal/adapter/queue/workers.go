package queue

import (
	"log/slog"

	"github.com/riverqueue/river"

	"github.com/ntttrang/ai-incident-triage/internal/service"
)

// NewWorkers registers every worker this service runs. Registering the same
// kind twice panics, so this is the single registration point.
func NewWorkers(classifySvc *service.ClassifyService, log *slog.Logger) *river.Workers {
	workers := river.NewWorkers()
	river.AddWorker(workers, NewClassifyWorker(classifySvc, log))
	return workers
}
