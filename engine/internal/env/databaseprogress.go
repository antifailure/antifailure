package env

import (
	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// attachDatabaseProgress hands a provider that can wait a long time the same
// progress sink the runtime's own steps use.
//
// engine.progress rather than a new event type, because it already means "a
// step in a long running operation, for work with no more specific event of its
// own", and it is not folded away by the non TTY fallback the way service output
// is. Without this, a managed database provider waiting on its cloud was a first
// af up that printed nothing for as long as the cloud took.
func (o *Orchestrator) attachDatabaseProgress(s *session) {
	reporting, ok := s.dbProv.(provider.ProgressReporting)
	if !ok {
		return
	}
	reporting.ReportProgressTo(func(line string) {
		o.event(s, events.Progress, line)
		if o.progress != nil {
			o.progress(line)
		}
	})
}
