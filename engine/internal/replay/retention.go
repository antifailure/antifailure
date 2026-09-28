package replay

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/lock"
	"github.com/antifailure/antifailure/engine/pkg/livekey"
)

// LockPublication serializes short reference changes, not running experiments.
func (s Store) LockPublication(ctx context.Context) (func(), error) {
	held, err := lock.AcquireWithin(ctx, filepath.Join(s.Root, "locks", "publication"), clock.New(), "agent replay references", 5*time.Second)
	if err != nil {
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { _ = held.Release() }) }, nil
}

type retirement struct {
	ID        string    `json:"id"`
	Reason    string    `json:"reason"`
	At        time.Time `json:"at"`
	Blobs     []string  `json:"blobs"`
	Attempts  []string  `json:"attempts"`
	Incidents []string  `json:"incidents"`
}

// Retire removes scenario content and unused references, retaining only a
// small reason record. A crash can resume from that record without the payload.
func (s Store) Retire(ctx context.Context, id, reason string, at time.Time) error {
	if strings.TrimSpace(reason) == "" || len(reason) > 500 || len(livekey.Scan(reason, "retirement reason")) > 0 {
		return fmt.Errorf("supply a bounded retirement reason without credentials")
	}
	unlock, err := s.LockPublication(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	var record retirement
	marker, markerErr := s.Read("retired", id)
	if markerErr == nil {
		if err = Decode(marker, &record); err != nil {
			return err
		}
	} else {
		body, readErr := s.Read("scenarios", id)
		if readErr != nil {
			return readErr
		}
		var scenario Scenario
		if err = Decode(body, &scenario); err != nil {
			return err
		}
		record = retirement{ID: id, Reason: reason, At: at, Blobs: []string{scenario.IncidentRef}, Attempts: []string{}, Incidents: []string{}}
		// Remember the identity even if the draft index is already absent. The
		// marker is the durable refusal for late imports, not only a GC list.
		captured, blobErr := s.Blob(scenario.IncidentRef)
		if blobErr != nil {
			return blobErr
		}
		var incident Incident
		if err = Decode(captured, &incident); err != nil {
			return err
		}
		record.Incidents = append(record.Incidents, incident.RunID)
		if scenario.MaskingRef != "" {
			record.Blobs = append(record.Blobs, scenario.MaskingRef)
		}
		attempts, listErr := s.List("attempts")
		if listErr != nil {
			return listErr
		}
		for _, entry := range attempts {
			if entry.Error != "" {
				return fmt.Errorf("repair attempt %s before retiring evidence", entry.ID)
			}
			var attempt Report
			if err = Decode(entry.Value, &attempt); err != nil {
				return err
			}
			if attempt.Scenario != id {
				continue
			}
			if attempt.Baseline.Branch != "" && (!attempt.Baseline.TornDown || !attempt.Candidate.TornDown) {
				return fmt.Errorf("recover attempt %s before retiring its evidence", attempt.ID)
			}
			record.Attempts = append(record.Attempts, entry.ID)
		}
		incidents, listErr := s.List("incidents")
		if listErr != nil {
			return listErr
		}
		for _, entry := range incidents {
			if entry.Error == "" && Digest(entry.Value) == scenario.IncidentRef && !contains(record.Incidents, entry.ID) {
				record.Incidents = append(record.Incidents, entry.ID)
			}
		}
		if err = s.Put("retired", id, record); err != nil {
			return err
		}
	}
	if record.ID != id {
		return fmt.Errorf("retirement identity mismatch")
	}
	others, err := s.List("scenarios")
	if err != nil {
		return err
	}
	referenced := map[string]bool{}
	for _, entry := range others {
		if entry.ID == id {
			continue
		}
		if entry.Error != "" {
			return fmt.Errorf("repair scenario %s before collecting evidence", entry.ID)
		}
		var scenario Scenario
		if err = Decode(entry.Value, &scenario); err != nil {
			return err
		}
		referenced[scenario.IncidentRef] = true
		referenced[scenario.MaskingRef] = true
	}
	remove := func(kind, name string) error {
		path, pathErr := s.path(kind, name)
		if pathErr != nil {
			return pathErr
		}
		removeErr := os.Remove(path)
		if os.IsNotExist(removeErr) {
			return nil
		}
		return removeErr
	}
	for _, attempt := range record.Attempts {
		if err = remove("attempts", attempt); err != nil {
			return err
		}
		if err = s.ClearReservation(attempt); err != nil {
			return err
		}
	}
	for _, blob := range record.Blobs {
		if referenced[blob] {
			continue
		}
		if !digestPattern.MatchString(blob) {
			return fmt.Errorf("retirement contains an invalid blob reference")
		}
		if err = remove("blobs", blob); err != nil {
			return err
		}
	}
	for _, incident := range record.Incidents {
		body, readErr := s.Read("incidents", incident)
		if readErr != nil {
			continue
		}
		if !referenced[Digest(body)] {
			if err = remove("incidents", incident); err != nil {
				return err
			}
		}
	}
	return remove("scenarios", id)
}

func (s Store) IsRetired(id string) bool {
	path, err := s.path("retired", id)
	if err != nil {
		return false
	}
	_, err = os.Lstat(path)
	return err == nil
}

// RetentionSummary withholds original bodies when listing retirement records.
func (s Store) RetentionSummary(id string) (json.RawMessage, error) { return s.Read("retired", id) }

// ClearReservation releases capacity only after the caller has proved cleanup.
func (s Store) ClearReservation(id string) error {
	path, err := s.path("reservations", id)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
