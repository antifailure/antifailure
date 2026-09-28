package replay

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antifailure/antifailure/engine/pkg/schema"
	"github.com/stretchr/testify/require"
)

func validIncident() Incident {
	return Incident{SchemaVersion: 1, RunID: "one", TraceID: strings.Repeat("a", 32), Project: "billing", Service: "agent", Commit: strings.Repeat("b", 40), ObservedAt: "2026-09-27T00:00:00Z", PolicyVersion: "1", Input: json.RawMessage(`{"id":1}`), InputHash: strings.Repeat("c", 64), Status: "complete", Clock: "sdk", Identity: "synthetic", Issues: []string{}, Exchanges: []Exchange{}}
}

func TestImmutableArtifactsAreIdempotentAndDetectCorruption(t *testing.T) {
	s := Store{Root: t.TempDir()}
	body := []byte(`{"ready":true}`)
	id, err := s.PutBlob(body)
	require.NoError(t, err)
	again, err := s.PutBlob(body)
	require.NoError(t, err)
	require.Equal(t, id, again)
	restored, err := s.Blob(id)
	require.NoError(t, err)
	require.Equal(t, body, restored)
	require.NoError(t, s.Put("scenarios", "one", map[string]int{"version": 1}))
	require.ErrorContains(t, s.Put("scenarios", "one", map[string]int{"version": 2}), "immutable")
	path, err := s.path("blobs", id)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte(`{"ready":false}`), 0o600))
	_, err = s.Blob(id)
	require.ErrorContains(t, err, "hash mismatch")
}

func TestIncompletePublicationAndOneBadSiblingDoNotBlankTheList(t *testing.T) {
	s := Store{Root: t.TempDir()}
	incident := validIncident()
	require.NoError(t, s.Put("incidents", incident.RunID, incident))
	dir := filepath.Join(s.Root, "incidents")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.json"), []byte(`{"schemaVersion":`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "shape.json"), []byte(`[]`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".pending-crash"), []byte(`{"schemaVersion":`), 0o600))
	entries, err := s.List("incidents")
	require.NoError(t, err)
	require.Len(t, entries, 3)
	require.NotEmpty(t, entries[0].Error)
	require.Equal(t, "one", entries[1].ID)
	require.NotEmpty(t, entries[1].Value)
	require.NotEmpty(t, entries[2].Error)
}

func TestConcurrentIdenticalPublicationStoresOneArtifact(t *testing.T) {
	s := Store{Root: t.TempDir()}
	var wg sync.WaitGroup
	errors := make(chan error, 20)
	for n := 0; n < 20; n++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.PutBlob([]byte(`{"x":1}`)); errors <- err }()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	entries, err := s.List("blobs")
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestArtifactPathsAndSizeCannotEscapeTheStore(t *testing.T) {
	s := Store{Root: t.TempDir()}
	for _, id := range []string{"../escape", "/absolute", "x/y", "x\\y", "a:b", ""} {
		require.Error(t, s.Put("incidents", id, 1))
	}
	_, err := s.PutBlob(make([]byte, MaxBytes+1))
	require.Error(t, err)
	require.Error(t, s.Put("../escape", "one", 1))
	require.NoError(t, os.MkdirAll(filepath.Join(s.Root, "incidents"), 0o700))
	require.NoError(t, os.Symlink(filepath.Join(t.TempDir(), "secret"), filepath.Join(s.Root, "incidents", "link.json")))
	_, err = s.Read("incidents", "link")
	require.Error(t, err)
}

func TestRequiredEvidenceCannotBeMisreadAsAnEmptySuccessfulCapture(t *testing.T) {
	i := validIncident()
	require.NoError(t, i.Validate())
	require.Empty(t, i.Missing())
	i.Input = nil
	i.Identity = "unmapped"
	i.Status = "incomplete"
	i.Exchanges = []Exchange{{Seq: 0, Name: "search", Kind: "http", Version: "1", Key: strings.Repeat("d", 64), Provenance: "recorded"}}
	gaps := strings.Join(i.Missing(), ",")
	for _, gap := range []string{"input_not_captured", "identity_mapping_unavailable", "capture_incomplete", "request_not_captured:0", "response_not_captured:0"} {
		require.Contains(t, gaps, gap)
	}
}

func TestOutcomeAssertionIsAnExactValueAtTheDeclaredPointer(t *testing.T) {
	body := json.RawMessage(`{"recommendation":"charge","nested":{"a/b":false},"balance":0}`)
	require.True(t, Assert(body, "/recommendation", json.RawMessage(`"charge"`)))
	require.False(t, Assert(body, "/recommendation", json.RawMessage(`"review"`)))
	require.False(t, Assert(body, "/absent", json.RawMessage(`null`)))
	require.True(t, Assert(body, "/nested/a~1b", json.RawMessage(`false`)))
	require.False(t, Assert(body, "/balance", json.RawMessage(`"0"`)))
}

func TestDecodeRejectsUnknownFieldsMultipleDocumentsAndWrongShapes(t *testing.T) {
	for _, body := range []string{`{"schemaVersion":1,"overrideSafety":true}`, `{} {}`, `[]`, `null`} {
		var incident Incident
		err := Decode([]byte(body), &incident)
		if err == nil {
			err = incident.Validate()
		}
		require.Error(t, err)
	}
}

func FuzzIncidentDecode(f *testing.F) {
	body, _ := json.Marshal(validIncident())
	f.Add(body)
	f.Add([]byte(`[]`))
	f.Add([]byte(`{"schemaVersion":2}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		var incident Incident
		if Decode(body, &incident) == nil {
			_ = incident.Validate()
			_ = incident.Missing()
		}
	})
}

func TestCheckpointAndTraceOrderingsReconcileWithoutChangingPublishedFacts(t *testing.T) {
	for _, ordering := range []string{"checkpoint-before-trace", "trace-before-checkpoint", "both-together", "checkpoint-only", "trace-only"} {
		t.Run(ordering, func(t *testing.T) {
			s := Store{Root: t.TempDir()}
			ctx := context.Background()
			complete := validIncident()
			complete.Golden = "gv-fixed"
			draft := complete
			draft.Status = "incomplete"
			draft.Issues = []string{"capture_pending"}
			switch ordering {
			case "checkpoint-before-trace", "checkpoint-only":
				draft.Input = nil
				draft.Output = nil
			case "trace-before-checkpoint", "trace-only":
				draft.Golden = ""
			case "both-together":
				draft = complete
			}
			require.NoError(t, s.ImportIncident(ctx, draft))
			require.NoError(t, s.ImportIncident(ctx, draft), "lost acknowledgment retry")
			if ordering == "checkpoint-only" || ordering == "trace-only" {
				require.NotEmpty(t, draft.Missing())
				return
			}
			require.NoError(t, s.ImportIncident(ctx, complete))
			require.NoError(t, s.ImportIncident(ctx, complete))
			body, err := s.Read("incidents", "one")
			require.NoError(t, err)
			var restored Incident
			require.NoError(t, Decode(body, &restored))
			require.Empty(t, restored.Missing())
			require.Equal(t, "gv-fixed", restored.Golden)
			changed := complete
			changed.Input = json.RawMessage(`{"id":2}`)
			require.Error(t, s.ImportIncident(ctx, changed))
		})
	}
}

func TestResponseBeforeSaveAfterSaveAndNoResponseRemainExplicit(t *testing.T) {
	for _, ordering := range []string{"response-before-save", "save-before-response", "response-never-arrives"} {
		t.Run(ordering, func(t *testing.T) {
			s := Store{Root: t.TempDir()}
			incident := validIncident()
			incident.Exchanges = []Exchange{{Seq: 0, Kind: "tool", Name: "search", Version: "1", Key: strings.Repeat("d", 64), Request: json.RawMessage(`{"q":"billing"}`), Provenance: "recorded"}}
			complete := incident
			complete.Exchanges = append([]Exchange{}, incident.Exchanges...)
			complete.Exchanges[0].Response = json.RawMessage(`"retained"`)
			incident.Status = "incomplete"
			incident.Issues = []string{"capture_pending"}
			if ordering == "response-before-save" {
				incident = complete
			}
			require.NoError(t, s.ImportIncident(context.Background(), incident))
			if ordering == "response-never-arrives" {
				require.Contains(t, incident.Missing(), "response_not_captured:0")
				return
			}
			require.NoError(t, s.ImportIncident(context.Background(), complete))
			body, err := s.Read("incidents", "one")
			require.NoError(t, err)
			var result Incident
			require.NoError(t, Decode(body, &result))
			require.Empty(t, result.Missing())
		})
	}
}

func TestRetirementRefusesActiveAttemptsThenDeletesOnlyUnreferencedContent(t *testing.T) {
	s := Store{Root: t.TempDir()}
	ctx := context.Background()
	incident := validIncident()
	body, err := json.Marshal(incident)
	require.NoError(t, err)
	ref, err := s.PutBlob(body)
	require.NoError(t, err)
	require.NoError(t, s.ImportIncident(ctx, incident))
	scenario := Scenario{SchemaVersion: 1, ID: "case-one", Project: "billing", IncidentRef: ref, Golden: "gv-one", GoldenIdentity: "gp1-one", GoldenAt: time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC), Manifest: schema.Manifest{Name: "billing"}, Endpoint: "/af-replay", Assertion: Assertion{Baseline: json.RawMessage(`false`), Expected: json.RawMessage(`true`)}, Tables: []string{"subscriptions"}}
	require.NoError(t, s.Put("scenarios", scenario.ID, scenario))
	other := scenario
	other.ID = "case-two"
	require.NoError(t, s.Put("scenarios", other.ID, other))
	attempt := Report{SchemaVersion: 1, ID: "attempt-one", Scenario: scenario.ID, Verdict: "INCONCLUSIVE", Baseline: Side{Branch: "baseline"}, Candidate: Side{Branch: "candidate"}}
	require.NoError(t, s.Put("attempts", attempt.ID, attempt))
	require.ErrorContains(t, s.Retire(ctx, scenario.ID, "replaced", scenario.GoldenAt), "recover attempt")
	attempt.Baseline.TornDown = true
	attempt.Candidate.TornDown = true
	require.NoError(t, s.Put("attempts", attempt.ID, attempt))
	require.NoError(t, s.Retire(ctx, scenario.ID, "replaced", scenario.GoldenAt))
	require.NoError(t, s.Retire(ctx, scenario.ID, "replaced", scenario.GoldenAt))
	require.True(t, s.IsRetired(scenario.ID))
	_, err = s.Blob(ref)
	require.NoError(t, err, "other case still references it")
	_, err = s.Read("attempts", attempt.ID)
	require.Error(t, err)
	require.NoError(t, s.Retire(ctx, other.ID, "retired too", scenario.GoldenAt))
	_, err = s.Blob(ref)
	require.Error(t, err)
	_, err = s.Read("incidents", incident.RunID)
	require.Error(t, err)
	audit, err := s.RetentionSummary(other.ID)
	require.NoError(t, err)
	require.NotContains(t, string(audit), "inputHash")
}

func TestUntrustedIncidentMetadataCannotImpersonateACompleteCapture(t *testing.T) {
	cases := []func(*Incident){
		func(i *Incident) { i.SchemaVersion = 2 }, func(i *Incident) { i.RunID = "../elsewhere" }, func(i *Incident) { i.PolicyVersion = "" },
		func(i *Incident) { i.TraceID = strings.Repeat("z", 32) }, func(i *Incident) { i.TraceID = strings.Repeat("0", 32) },
		func(i *Incident) { i.ObservedAt = "yesterday" }, func(i *Incident) { i.Status = "passed" }, func(i *Incident) { i.InputHash = "not-a-hash" },
		func(i *Incident) { i.Exchanges = make([]Exchange, 10001) },
		func(i *Incident) {
			i.Exchanges = []Exchange{{Seq: 0, Name: "x", Version: "1", Kind: "unknown", Key: strings.Repeat("a", 64), Provenance: "recorded"}}
		},
		func(i *Incident) {
			n := 1
			i.Exchanges = []Exchange{{Seq: 0, Parent: &n, Name: "x", Version: "1", Kind: "tool", Key: strings.Repeat("a", 64), Provenance: "recorded"}}
		},
		func(i *Incident) { i.Exchanges = []Exchange{{Seq: 2}} },
	}
	for _, change := range cases {
		i := validIncident()
		change(&i)
		require.Error(t, i.Validate())
	}
	i := validIncident()
	i.InputHash = ""
	i.Status = "incomplete"
	i.Issues = []string{"input_unhashable"}
	require.NoError(t, i.Validate())
	require.NotEmpty(t, i.Missing())
}

func TestScenarioRefusesUnsafeRoutesUnknownVersionsAndAmbiguousAssertions(t *testing.T) {
	base := Scenario{SchemaVersion: 1, ID: "one", IncidentRef: strings.Repeat("a", 64), Project: "billing", Golden: "gv-one", GoldenIdentity: "gp1-one", GoldenAt: time.Now(), Manifest: schema.Manifest{Name: "billing"}, Endpoint: "/af-replay", Assertion: Assertion{Baseline: json.RawMessage(`false`), Expected: json.RawMessage(`true`)}, Tables: []string{"subscriptions"}}
	require.NoError(t, base.Validate())
	cases := []func(*Scenario){func(s *Scenario) { s.SchemaVersion = 2 }, func(s *Scenario) { s.MaskingRef = "unknown" }, func(s *Scenario) { s.Endpoint = "//remote.example.test" }, func(s *Scenario) { s.Endpoint = "/path?override=true" }, func(s *Scenario) { s.Endpoint = "https://remote.example.test" }, func(s *Scenario) { s.Assertion.Expected = s.Assertion.Baseline }, func(s *Scenario) { s.Assertion.Pointer = "/bad~escape" }, func(s *Scenario) { s.Tables = nil }}
	for _, change := range cases {
		s := base
		change(&s)
		require.Error(t, s.Validate())
	}
}

func TestJSONPointerReadsArraysAndRefusesInvalidIndexesWithoutGuessing(t *testing.T) {
	body := json.RawMessage(`{"messages":[{"text":"correct"}],"~1":"escaped"}`)
	require.True(t, Assert(body, "/messages/0/text", json.RawMessage(`"correct"`)))
	require.True(t, Assert(body, "/~01", json.RawMessage(`"escaped"`)))
	for _, pointer := range []string{"messages", "/messages/01/text", "/messages/-1", "/messages/+0", "/messages/1", "/messages/0/text/extra", "/messages/~2"} {
		require.False(t, Assert(body, pointer, json.RawMessage(`"correct"`)))
	}
}

func TestLateCaptureCannotRewriteEarlierFactsOrEraseAnUnresolvedFailure(t *testing.T) {
	ctx := context.Background()
	base := validIncident()
	base.Status = "incomplete"
	base.Issues = []string{"capture_pending", "identity_transformed:input"}
	base.Golden = "gv-one"
	for _, change := range []func(*Incident){func(i *Incident) { i.Commit = strings.Repeat("c", 40) }, func(i *Incident) { i.Golden = "gv-two" }, func(i *Incident) { i.Input = json.RawMessage(`{"id":2}`) }, func(i *Incident) { i.Issues = []string{"capture_pending"} }} {
		s := Store{Root: t.TempDir()}
		require.NoError(t, s.ImportIncident(ctx, base))
		next := base
		change(&next)
		require.Error(t, s.ImportIncident(ctx, next))
	}
	s := Store{Root: t.TempDir(), Project: "other"}
	require.Error(t, s.ImportIncident(ctx, base))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Error(t, (Store{Root: t.TempDir()}).ImportIncident(ctx, base))
}
