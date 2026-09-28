package replay

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/lock"
)

// ImportIncident reconciles a later bounded capture without changing facts
// already retained. Frozen scenario blobs are independent of this draft index.
func (s Store) ImportIncident(ctx context.Context, next Incident) error {
	if s.Project != "" && next.Project != s.Project {
		return fmt.Errorf("incident belongs to another project")
	}
	if err := next.Validate(); err != nil {
		return err
	}
	held, err := lock.Acquire(filepath.Join(s.Root, "locks", "incident-"+next.RunID), clock.New(), "af incident import")
	if err != nil {
		return err
	}
	defer func() { _ = held.Release() }()
	if err = ctx.Err(); err != nil {
		return err
	}
	path, err := s.path("incidents", next.RunID)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(path); os.IsNotExist(err) {
		return s.Put("incidents", next.RunID, next)
	} else if err != nil {
		return err
	}
	body, err := s.Read("incidents", next.RunID)
	if err != nil {
		return err
	}
	var previous Incident
	if err = Decode(body, &previous); err != nil {
		return err
	}
	nextBytes, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if Equal(body, nextBytes) {
		return nil
	}
	if previous.Status == "complete" {
		return fmt.Errorf("completed incident is immutable; capture a new run")
	}
	if previous.Project != next.Project || previous.Commit != next.Commit || previous.TraceID != next.TraceID || previous.InputHash != next.InputHash || previous.PolicyVersion != next.PolicyVersion || previous.ObservedAt != next.ObservedAt || previous.Identity != next.Identity || previous.Clock != next.Clock || previous.Service != next.Service {
		return fmt.Errorf("late capture changed the incident identity")
	}
	if previous.Golden != "" && previous.Golden != next.Golden {
		return fmt.Errorf("late capture changed the checkpoint")
	}
	if !fills(previous.Input, next.Input) || !fills(previous.Output, next.Output) || len(previous.Exchanges) > len(next.Exchanges) {
		return fmt.Errorf("late capture changed retained evidence")
	}
	for n, old := range previous.Exchanges {
		newer := next.Exchanges[n]
		if old.Key != newer.Key || old.Name != newer.Name || old.Kind != newer.Kind || old.Version != newer.Version || old.CapturedAt != newer.CapturedAt || !fills(old.Request, newer.Request) || !fills(old.Response, newer.Response) || old.Error != "" && old.Error != newer.Error {
			return fmt.Errorf("late capture changed exchange %d", n)
		}
	}
	for _, reason := range previous.Issues {
		if reason == "capture_pending" || reason == "unfinished_operations" {
			continue
		}
		if !contains(next.Issues, reason) {
			return fmt.Errorf("late capture removed an unresolved issue")
		}
	}
	if strings.TrimSpace(next.PolicyVersion) == "" {
		return fmt.Errorf("capture policy is required")
	}
	return s.write("incidents", next.RunID, nextBytes, true)
}
func fills(old, newer json.RawMessage) bool {
	return len(old) == 0 || string(old) == "null" || Equal(old, newer)
}
func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
