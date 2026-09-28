package env

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/lock"
	"github.com/antifailure/antifailure/engine/internal/oracle"
	"github.com/antifailure/antifailure/engine/internal/replay"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/airgap"
	"github.com/antifailure/antifailure/engine/pkg/extension"
	"github.com/antifailure/antifailure/engine/pkg/livekey"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// ReplayStore is local to the application project, beside its resource journal.
func (o *Orchestrator) ReplayStore() replay.Store {
	return replay.Store{Root: filepath.Join(o.opts.Root, StateDir, "replay"), Project: o.opts.Manifest.Name}
}

func (o *Orchestrator) checkReplayGoldenPin(version string) error {
	entries, err := o.ReplayStore().List("scenarios")
	if err != nil {
		return aferrors.Coded(aferrors.AFRPL001, "detail", "replay golden references could not be read")
	}
	for _, entry := range entries {
		if entry.Error != "" {
			return aferrors.Coded(aferrors.AFRPL001, "detail", "repair the malformed scenario "+entry.ID+" before collecting goldens")
		}
		var scenario replay.Scenario
		if replay.Decode(entry.Value, &scenario) != nil {
			return aferrors.Coded(aferrors.AFRPL001, "detail", "scenario references could not be decoded")
		}
		if scenario.Golden == version {
			return aferrors.Coded(aferrors.AFRPL001, "detail", "golden is pinned by replay scenario "+entry.ID)
		}
	}
	return nil
}

// replayHarness refuses sources the first local protocol cannot reset or contain.
func replayHarness(m *schema.Manifest) error {
	encoded, encodeErr := json.Marshal(m)
	if encodeErr != nil || len(livekey.Scan(string(encoded), "replay harness")) > 0 {
		return fmt.Errorf("replay harness contains a credential")
	}
	if m.Database == nil || string(m.Database.Provider) != "docker" {
		return fmt.Errorf("replay requires a verified Docker Postgres golden")
	}
	if m.Runtime != nil && ((m.Runtime.Provider != "" && string(m.Runtime.Provider) != "local") || len(m.Runtime.Targets) > 0) {
		return fmt.Errorf("replay requires the local runtime without remote targets")
	}
	if m.Infrastructure != nil && len(m.Infrastructure.Stacks) > 0 {
		return fmt.Errorf("replay has no checkpoint adapter for additional datastores or infrastructure")
	}
	for _, store := range m.Datastores {
		if store.Name != schema.PrimaryDatastore || store.Engine != "postgres" {
			return fmt.Errorf("replay has no checkpoint adapter for datastore %s", store.Name)
		}
	}
	if m.Egress != nil {
		if m.Egress.Default != "" && string(m.Egress.Default) != "block" {
			return fmt.Errorf("strict replay requires default block egress")
		}
		for _, rule := range m.Egress.Rules {
			if string(rule.Mode) != "block" {
				return fmt.Errorf("strict replay requires all external boundaries to be served by the SDK; rule %s is not block", rule.Host)
			}
		}
	}
	if len(m.Services) == 0 {
		return fmt.Errorf("replay needs an application service")
	}
	for _, service := range m.Services {
		for _, v := range service.Env {
			if v.From != "" || v.Value == "" || v.Sandbox {
				return fmt.Errorf("replay service %s variable %s must be an explicit test value", service.Name, v.Name)
			}
			upper := strings.ToUpper(v.Name)
			if (strings.Contains(upper, "SECRET") || strings.Contains(upper, "TOKEN") || strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "KEY")) && !strings.HasPrefix(v.Value, "AF_FAKE_") {
				return fmt.Errorf("replay credential %s must be synthetic", v.Name)
			}
		}
	}
	return nil
}

func (o *Orchestrator) replayOrchestrator(m *schema.Manifest, branch, golden, tree string) (*Orchestrator, error) {
	if err := replayHarness(m); err != nil {
		return nil, err
	}
	opts := o.opts
	opts.Manifest = m
	opts.Branch = branch
	opts.PinGolden = golden
	opts.BuildRoot = tree
	opts.Secrets = secrets.NewChain()
	opts.Getenv = func(string) string { return "" }
	opts.ControlPlaneURL = ""
	opts.ControlPlaneToken = ""
	opts.Extensions = extension.NewRegistry()
	child, err := New(opts)
	if err == nil {
		child.MarkEphemeral(30 * time.Minute)
	}
	return child, err
}

// SaveIncident publishes only after all referenced artifacts are durable.
func (o *Orchestrator) SaveIncident(ctx context.Context, incident replay.Incident, id, golden, endpoint, owner string, assertion replay.Assertion, tables []string) (*replay.Scenario, error) {
	if err := incident.Validate(); err != nil {
		return nil, err
	}
	store := o.ReplayStore()
	if err := store.ImportIncident(ctx, incident); err != nil {
		return nil, err
	}
	unlock, lockErr := store.LockPublication(ctx)
	if lockErr != nil {
		return nil, lockErr
	}
	defer unlock()
	if store.IsRetired(id) {
		return nil, fmt.Errorf("scenario name was retired; use a new version name")
	}
	if err := store.CheckIncidentActive(incident.RunID); err != nil {
		return nil, err
	}
	if gaps := incident.Missing(); len(gaps) > 0 {
		return nil, fmt.Errorf("incident is not ready: %s", strings.Join(gaps, ", "))
	}
	if len(incident.Output) > 0 && !replay.Assert(incident.Output, assertion.Pointer, assertion.Baseline) {
		return nil, fmt.Errorf("the original failure assertion does not match the retained outcome")
	}
	if incident.Project != o.opts.Manifest.Name {
		return nil, fmt.Errorf("incident belongs to another project")
	}
	if golden == "" {
		golden = incident.Golden
	}
	if golden == "" {
		return nil, fmt.Errorf("pin a verified golden before saving")
	}
	if err := replayHarness(o.opts.Manifest); err != nil {
		return nil, err
	}
	safe, err := o.replayOrchestrator(o.opts.Manifest, "replay-save", golden, "")
	if err != nil {
		return nil, err
	}
	identity, err := safe.GoldenIdentity()
	if err != nil {
		return nil, err
	}
	goldens, err := safe.Goldens(ctx)
	if err != nil {
		return nil, err
	}
	var captured time.Time
	for _, g := range goldens {
		if g.ID == golden && g.Verified && g.Provenance == identity {
			captured = g.CreatedAt
		}
	}
	if captured.IsZero() {
		return nil, fmt.Errorf("golden is missing, unverified or belongs to another project")
	}
	b, err := json.Marshal(incident)
	if err != nil {
		return nil, err
	}
	ref, err := store.PutBlob(b)
	if err != nil {
		return nil, err
	}
	scenario := &replay.Scenario{SchemaVersion: 1, ID: id, IncidentRef: ref, Project: incident.Project, Golden: golden, GoldenIdentity: identity, GoldenAt: captured, Manifest: *o.opts.Manifest, Endpoint: endpoint, Assertion: assertion, Tables: tables, CreatedAt: o.opts.Clock.Now(), Owner: owner}
	if rules, readErr := os.ReadFile(o.MaskingRulesPath()); readErr == nil {
		scenario.MaskingRef, err = store.PutBlob(rules)
		if err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(readErr) {
		return nil, readErr
	}
	if err = scenario.Validate(); err != nil {
		return nil, err
	}
	if err = store.Put("scenarios", id, scenario); err != nil {
		return nil, err
	}
	return scenario, nil
}

func (o *Orchestrator) replayScenario(id string) (*replay.Scenario, *replay.Incident, error) {
	store := o.ReplayStore()
	b, err := store.Read("scenarios", id)
	if err != nil {
		return nil, nil, err
	}
	var scenario replay.Scenario
	if err = replay.Decode(b, &scenario); err != nil {
		return nil, nil, err
	}
	if err = scenario.Validate(); err != nil {
		return nil, nil, err
	}
	if scenario.Project != o.opts.Manifest.Name {
		return nil, nil, fmt.Errorf("scenario belongs to another project")
	}
	b, err = store.Blob(scenario.IncidentRef)
	if err != nil {
		return &scenario, nil, err
	}
	var incident replay.Incident
	if err = replay.Decode(b, &incident); err != nil {
		return &scenario, nil, err
	}
	if err = incident.Validate(); err != nil {
		return &scenario, nil, err
	}
	if incident.Project != scenario.Project {
		return &scenario, nil, fmt.Errorf("incident and scenario projects differ")
	}
	if gaps := incident.Missing(); len(gaps) > 0 {
		return &scenario, &incident, fmt.Errorf("incident is incomplete: %s", strings.Join(gaps, ", "))
	}
	if len(incident.Output) > 0 && !replay.Assert(incident.Output, scenario.Assertion.Pointer, scenario.Assertion.Baseline) {
		return &scenario, &incident, fmt.Errorf("the saved control contradicts the retained original outcome")
	}
	return &scenario, &incident, nil
}

// Replay proves the control before running the candidate. The report is durable
// before creation and never says PASS until both inventories are empty.
func (o *Orchestrator) Replay(ctx context.Context, id, candidate string) (report *replay.Report, err error) {
	ctx, cancelRun := context.WithTimeout(ctx, 20*time.Minute)
	defer cancelRun()
	var random [12]byte
	if _, err = rand.Read(random[:]); err != nil {
		return nil, err
	}
	report = &replay.Report{SchemaVersion: 1, ID: "rpl_" + hex.EncodeToString(random[:]), Scenario: id, Verdict: "INCONCLUSIVE", State: "preflight", Fidelity: "trace-only", StartedAt: o.opts.Clock.Now(), Issues: []string{}, Notes: []string{"Database state is a pinned masked golden, not the incident-time database.", "Clock control covers calls to the SDK clock only.", "Database comparison reports net writes to the declared tables."}}
	store := o.ReplayStore()
	releasePublication := func() {}
	finish := func() {
		defer func() { releasePublication() }()
		if recovered := recover(); recovered != nil {
			report.Verdict = "INCONCLUSIVE"
			report.Issues = append(report.Issues, "replay_internal_failure")
			err = fmt.Errorf("replay stopped on an internal failure")
		}
		if err != nil {
			report.Verdict = "INCONCLUSIVE"
			report.Issues = append(report.Issues, "replay error: "+o.opts.Redactor.String(err.Error()))
		}
		now := o.opts.Clock.Now()
		report.DurationMs = now.Sub(report.StartedAt).Milliseconds()
		report.CompletedAt = &now
		report.State = "complete"
		if writeErr := store.Put("attempts", report.ID, report); writeErr != nil {
			err = writeErr
			report.Verdict = "INCONCLUSIVE"
			report.Issues = append(report.Issues, "report_write_failed: restore access to the local artifact store and recover this attempt")
		}
	}
	held, lockErr := lock.Acquire(filepath.Join(store.Root, "locks", report.ID), o.opts.Clock, "af replay")
	if lockErr != nil {
		return report, lockErr
	}
	defer func() { _ = held.Release() }()
	defer finish()
	if ctx.Err() != nil {
		report.Issues = append(report.Issues, "replay_cancelled_or_budget_exhausted")
		return report, nil
	}
	unlock, publicationErr := store.LockPublication(ctx)
	if publicationErr != nil {
		return report, publicationErr
	}
	releasePublication = unlock
	if store.IsRetired(id) {
		report.Issues = append(report.Issues, "scenario_retired")
		return report, nil
	}
	scenario, incident, loadErr := o.replayScenario(id)
	if loadErr != nil {
		report.Fidelity = "trace-only"
		report.Issues = append(report.Issues, loadErr.Error())
		return report, nil
	}
	report.Golden = scenario.Golden
	report.GoldenAt = scenario.GoldenAt
	report.ObservedAt = incident.ObservedAt
	report.TraceID = incident.TraceID
	report.OriginalOutcome = incident.Output
	report.Assertion = &scenario.Assertion
	if len(incident.Output) == 0 {
		report.Notes = append(report.Notes, "Original outcome was not retained; the control uses the reviewer-supplied failure assertion.")
	}
	report.TeardownManifest = &scenario.Manifest
	report.Dependencies = map[string]string{"database": "pinned masked snapshot; historical state approximate", "clock": "SDK clock only", "identity": "synthetic", "external_network": "blocked"}
	for _, exchange := range incident.Exchanges {
		report.Dependencies[exchange.Kind+":"+exchange.Name] = "recorded"
	}
	if captured, captureErr := store.Blob(scenario.IncidentRef); captureErr == nil {
		report.CaptureBytes = len(captured)
	}
	m := scenario.Manifest
	if checkErr := replayHarness(&m); checkErr != nil {
		report.Issues = append(report.Issues, checkErr.Error())
		return report, nil
	}
	if scenario.MaskingRef != "" {
		expected, readErr := store.Blob(scenario.MaskingRef)
		if readErr != nil {
			report.Issues = append(report.Issues, readErr.Error())
			return report, nil
		}
		actual, readErr := os.ReadFile(o.MaskingRulesPath())
		if readErr != nil || !bytes.Equal(expected, actual) {
			report.Issues = append(report.Issues, "masking_rules_changed")
			return report, nil
		}
	}
	baseRef := gitOutput(o.opts.Root, "rev-parse", "--verify", "--end-of-options", incident.Commit+"^{commit}")
	candRef := gitOutput(o.opts.Root, "rev-parse", "--verify", "--end-of-options", candidate+"^{commit}")
	if baseRef == "" || candRef == "" {
		report.Issues = append(report.Issues, "code_revision_unavailable")
		return report, nil
	}
	report.Baseline = replay.Side{Commit: baseRef, Branch: report.ID + "-baseline"}
	report.Candidate = replay.Side{Commit: candRef, Branch: report.ID + "-candidate"}
	for index := range m.Services {
		m.Services[index].Env = append(m.Services[index].Env, schema.EnvVar{Name: "AF_REPLAY_ENABLED", Value: "true"})
		if m.Services[index].Resources == nil {
			m.Services[index].Resources = &schema.Resources{CPU: "1", Memory: "512Mi"}
		}
	}
	baseline, newErr := o.replayOrchestrator(&m, report.Baseline.Branch, scenario.Golden, "")
	if newErr != nil {
		report.Issues = append(report.Issues, newErr.Error())
		return report, nil
	}
	cand, newErr := o.replayOrchestrator(&m, report.Candidate.Branch, scenario.Golden, "")
	if newErr != nil {
		report.Issues = append(report.Issues, newErr.Error())
		return report, nil
	}
	report.Baseline.EnvID = baseline.EnvID()
	report.Candidate.EnvID = cand.EnvID()
	identity, identityErr := baseline.GoldenIdentity()
	if identityErr != nil || identity != scenario.GoldenIdentity {
		report.Issues = append(report.Issues, "golden_identity_changed")
		return report, nil
	}
	if err = store.Put("attempts", report.ID, report); err != nil {
		return report, err
	}
	reservations, reservationErr := store.List("reservations")
	if reservationErr != nil {
		return report, reservationErr
	}
	if len(reservations) >= 2 {
		report.Baseline.TornDown = true
		report.Candidate.TornDown = true
		report.Issues = append(report.Issues, "project_replay_budget_exhausted: wait for or recover the two reserved attempts")
		return report, nil
	}
	if err = store.Put("reservations", report.ID, map[string]string{"attemptId": report.ID}); err != nil {
		report.Baseline.TornDown = true
		report.Candidate.TornDown = true
		return report, err
	}
	unlock()
	// Registered before Up, including failed creation and cancelled callers.
	defer func() {
		for _, item := range []struct {
			name   string
			engine *Orchestrator
			side   *replay.Side
		}{{"baseline", baseline, &report.Baseline}, {"candidate", cand, &report.Candidate}} {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
			cleanErr := item.engine.replayDown(cleanup)
			cancel()
			item.side.TornDown = cleanErr == nil
			if cleanErr != nil {
				report.Issues = append(report.Issues, item.name+" cleanup: "+o.opts.Redactor.String(cleanErr.Error()))
			}
		}
		if !report.Baseline.TornDown || !report.Candidate.TornDown {
			report.Issues = append(report.Issues, "teardown_unconfirmed: run af replay recover "+report.ID)
		} else if releaseErr := store.ClearReservation(report.ID); releaseErr != nil {
			report.Issues = append(report.Issues, "reservation_release_failed: recover this attempt after restoring artifact-store access")
		}
		if len(report.Issues) > 0 {
			report.Verdict = "INCONCLUSIVE"
		} else if report.Baseline.Assertion && report.Candidate.Assertion && report.DatabaseUnchanged {
			report.Verdict = "PASS"
		} else if report.Baseline.Assertion {
			report.Verdict = "FAIL"
		}
	}()
	baseBefore, baseAfter, runErr := o.replaySide(ctx, baseline, scenario, incident, &report.Baseline, nil)
	if runErr != nil {
		report.Issues = append(report.Issues, "baseline: "+runErr.Error())
		return report, nil
	}
	report.Baseline.Assertion = replay.Assert(report.Baseline.Response.Output, scenario.Assertion.Pointer, scenario.Assertion.Baseline)
	report.Fidelity = "state-backed"
	if !report.Baseline.Assertion {
		report.Issues = append(report.Issues, "baseline_did_not_reproduce")
		return report, nil
	}
	report.State = "candidate"
	if err = store.Put("attempts", report.ID, report); err != nil {
		return report, err
	}
	candBefore, candAfter, runErr := o.replaySide(ctx, cand, scenario, incident, &report.Candidate, baseBefore)
	if runErr != nil {
		report.Issues = append(report.Issues, "candidate: "+runErr.Error())
		return report, nil
	}
	report.Candidate.Assertion = replay.Assert(report.Candidate.Response.Output, scenario.Assertion.Pointer, scenario.Assertion.Expected)
	report.DatabaseUnchanged = sameReplayDatabase(candBefore, candAfter)
	if !report.DatabaseUnchanged {
		report.Notes = append(report.Notes, "Candidate changed a declared database table; this scenario requires no net database writes.")
	}
	comparison := oracle.Compare(oracle.Input{BaselineBefore: baseBefore, BaselineAfter: baseAfter, CandidateBefore: candBefore, CandidateAfter: candAfter, Database: oracle.DatabaseOptions{Include: scenario.Tables}})
	report.Database, err = replayDatabaseEvidence(comparison)
	if err != nil {
		report.Issues = append(report.Issues, "database_comparator_failed")
	}
	return report, err
}

// The oracle's display strings contain row values, including primary keys.
// Replay records only finding metadata; a masked golden is not permission to
// copy a credential column into the attempt or its CLI response.
func replayDatabaseEvidence(comparison *oracle.Result) (json.RawMessage, error) {
	findings := make([]map[string]string, 0, len(comparison.Findings))
	for _, finding := range comparison.Findings {
		findings = append(findings, map[string]string{
			"kind": string(finding.Kind), "severity": finding.SeverityName,
			"table": finding.Where, "phase": string(finding.Phase),
		})
	}
	return json.Marshal(struct {
		Findings []map[string]string     `json:"findings"`
		Database *oracle.DatabaseSummary `json:"database,omitempty"`
	}{Findings: findings, Database: comparison.Database})
}

func (o *Orchestrator) replaySide(ctx context.Context, side *Orchestrator, scenario *replay.Scenario, incident *replay.Incident, result *replay.Side, expectedBefore *oracle.Snapshot) (before, after *oracle.Snapshot, err error) {
	tree, clean, err := o.baselineTree(ctx, result.Commit)
	if err != nil {
		return nil, nil, err
	}
	defer clean()
	side.opts.BuildRoot = tree
	// Archives exclude untracked files. Refuse tracked credential files too.
	err = filepath.WalkDir(tree, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := d.Name()
		if !d.IsDir() && (name == ".env" || strings.HasPrefix(name, ".env.") || strings.HasSuffix(name, ".pem") || name == "id_rsa") {
			return fmt.Errorf("build context contains credential file %s", name)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	up, err := side.Up(ctx)
	if err != nil {
		return nil, nil, err
	}
	if up.URL == "" {
		return nil, nil, fmt.Errorf("application has no replay endpoint")
	}
	opts := oracle.DatabaseOptions{Include: scenario.Tables}
	before, err = side.snapshotBranch(ctx, opts)
	if err != nil {
		return nil, nil, err
	}
	if err = completeSnapshot(before, scenario.Tables); err != nil {
		return before, nil, err
	}
	if expectedBefore != nil {
		initial := oracle.Compare(oracle.Input{Config: oracle.Config{KeepUUIDs: true, KeepTimestamps: true}, BaselineAfter: expectedBefore, CandidateAfter: before, Database: opts})
		if len(initial.Findings) > 0 || len(initial.Notes) > 0 {
			return before, nil, fmt.Errorf("initial_database_state_differs")
		}
	}
	body, err := json.Marshal(map[string]any{"schemaVersion": 1, "input": incident.Input, "clock": incident.ObservedAt, "exchanges": incident.Exchanges, "commit": result.Commit})
	if err != nil {
		return before, nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(up.URL, "/")+scenario.Endpoint, bytes.NewReader(body))
	if err != nil {
		return before, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("traceparent", "00-"+incident.TraceID+"-0000000000000001-01")
	client := airgap.Client(airgap.SiteOracle, 60*time.Second)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return before, nil, fmt.Errorf("replay endpoint failed")
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, replay.MaxBytes+1))
	if err != nil {
		return before, nil, err
	}
	if response.StatusCode != http.StatusOK {
		return before, nil, fmt.Errorf("replay endpoint returned %d", response.StatusCode)
	}
	var observed replay.Response
	if err = replay.Decode(data, &observed); err != nil {
		return before, nil, err
	}
	if err = replay.SafePayload(data); err != nil {
		return before, nil, fmt.Errorf("replay response contains unsafe evidence")
	}
	normalized, normalizeErr := json.Marshal(observed)
	if normalizeErr != nil || !bytes.Equal(side.opts.Redactor.Bytes(normalized), normalized) {
		return before, nil, fmt.Errorf("replay response was refused by the evidence redactor")
	}
	result.Response = &observed
	if observed.SchemaVersion != 1 || len(observed.Issues) > 0 || len(observed.Operations) != len(incident.Exchanges) {
		return before, nil, fmt.Errorf("incomplete SDK evidence: %s", strings.Join(observed.Issues, ", "))
	}
	expectedHits := 0
	expectedEffects := 0
	for n, op := range observed.Operations {
		expected := incident.Exchanges[n]
		if op.Seq != n || op.Key != expected.Key || op.Kind != expected.Kind || op.Name != expected.Name {
			return before, nil, fmt.Errorf("boundary diverged at %d", n)
		}
		if expected.Kind == "database" {
			if op.Source != "isolated_database" {
				return before, nil, fmt.Errorf("database boundary source is unconfirmed at %d", n)
			}
		} else {
			expectedHits++
			if op.Source != "recorded" {
				return before, nil, fmt.Errorf("boundary was not recorded at %d", n)
			}
		}
		if expected.Kind == "effect" {
			if expectedEffects >= len(observed.Effects) {
				return before, nil, fmt.Errorf("effect ledger is incomplete")
			}
			var identity struct {
				Input json.RawMessage `json:"input"`
			}
			if json.Unmarshal(expected.Request, &identity) != nil || observed.Effects[expectedEffects].Name != expected.Name || !replay.Equal(identity.Input, observed.Effects[expectedEffects].Request) {
				return before, nil, fmt.Errorf("effect ledger differs at %d", n)
			}
			expectedEffects++
		}
	}
	if observed.Hits != expectedHits || len(observed.Effects) != expectedEffects {
		return before, nil, fmt.Errorf("replay counters or effect ledger are incomplete")
	}
	rt, err := side.newRuntime(ctx)
	if err != nil {
		return before, nil, err
	}
	defer func() { _ = rt.Close() }()
	observer, ok := rt.(*local.Runtime)
	if !ok {
		return before, nil, fmt.Errorf("runtime cannot prove complete replay egress")
	}
	if err = observer.QuiesceReplay(ctx, side.EnvID()); err != nil {
		return before, nil, fmt.Errorf("application quiescence unconfirmed: %w", err)
	}
	conn, closeConn, connErr := side.connectBranch(ctx)
	if connErr != nil {
		return before, nil, connErr
	}
	var sessions int
	err = conn.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND backend_type='client backend' AND pid<>pg_backend_pid()").Scan(&sessions)
	closeConn()
	if err != nil || sessions != 0 {
		return before, nil, fmt.Errorf("application database sessions have not drained")
	}
	after, err = side.snapshotBranch(ctx, opts)
	if err != nil {
		return before, nil, err
	}
	if err = completeSnapshot(after, scenario.Tables); err != nil {
		return before, after, err
	}
	decisions, err := observer.ReplayDecisions(ctx, side.EnvID())
	if err != nil {
		return before, nil, err
	}
	for _, decision := range decisions {
		entry, _ := json.Marshal(decision)
		result.Egress = append(result.Egress, entry)
		if decision.Mode != "block" {
			return before, nil, fmt.Errorf("unexpected egress mode %s", decision.Mode)
		}
	}
	if len(decisions) > 0 {
		return before, nil, fmt.Errorf("unrecorded external request refused: %s", decisions[0].Host)
	}
	return before, after, nil
}

func completeSnapshot(snapshot *oracle.Snapshot, tables []string) error {
	if snapshot == nil || len(snapshot.Notes) > 0 {
		return fmt.Errorf("database snapshot is incomplete")
	}
	for _, table := range snapshot.Tables {
		if table.Truncated {
			return fmt.Errorf("database table %s exceeds comparison limit", table.Qualified())
		}
	}
	for _, wanted := range tables {
		found := false
		for _, table := range snapshot.Tables {
			if table.Qualified() == wanted || table.Name == wanted {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("declared database table %s is absent", wanted)
		}
	}
	return nil
}

func sameReplayDatabase(before, after *oracle.Snapshot) bool {
	if before == nil || after == nil || len(before.Tables) != len(after.Tables) {
		return false
	}
	for n, table := range before.Tables {
		other := after.Tables[n]
		if table.Qualified() != other.Qualified() || !reflect.DeepEqual(table.Columns, other.Columns) || !reflect.DeepEqual(table.Key, other.Key) || !reflect.DeepEqual(table.Rows, other.Rows) {
			return false
		}
	}
	return true
}

func (o *Orchestrator) replayDown(ctx context.Context) error {
	td, err := o.Down(ctx)
	if err != nil {
		return err
	}
	if td == nil {
		return fmt.Errorf("teardown returned no evidence")
	}
	if len(td.Pending) > 0 {
		return fmt.Errorf("teardown left %d pending resources", len(td.Pending))
	}
	rt, err := o.newRuntime(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = rt.Close() }()
	localRuntime, ok := rt.(*local.Runtime)
	if !ok {
		return fmt.Errorf("runtime cannot verify replay cleanup")
	}
	return localRuntime.ReplayAbsent(ctx, o.EnvID())
}

// RecoverReplay reconciles both recorded environments without rerunning the agent.
func (o *Orchestrator) RecoverReplay(ctx context.Context, id string) (*replay.Report, error) {
	store := o.ReplayStore()
	body, err := store.Read("attempts", id)
	if err != nil {
		return nil, err
	}
	var report replay.Report
	if err = replay.Decode(body, &report); err != nil {
		return nil, err
	}
	if report.ID != id || report.Baseline.Branch != id+"-baseline" || report.Candidate.Branch != id+"-candidate" {
		return nil, fmt.Errorf("attempt branch identities are invalid")
	}
	held, lockErr := lock.Acquire(filepath.Join(store.Root, "locks", id), o.opts.Clock, "af replay recover")
	if lockErr != nil {
		return nil, lockErr
	}
	defer func() { _ = held.Release() }()
	if report.TeardownManifest == nil || report.TeardownManifest.Name != o.opts.Manifest.Name || report.Golden == "" {
		return nil, fmt.Errorf("attempt has no valid teardown harness")
	}
	for _, side := range []*replay.Side{&report.Baseline, &report.Candidate} {
		if side.Branch == "" {
			continue
		}
		child, newErr := o.replayOrchestrator(report.TeardownManifest, side.Branch, report.Golden, "")
		if newErr != nil {
			return nil, newErr
		}
		if child.EnvID() != side.EnvID {
			return nil, fmt.Errorf("recorded environment identity mismatch")
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
		cleanErr := child.replayDown(cleanup)
		cancel()
		side.TornDown = cleanErr == nil
		if cleanErr != nil {
			report.Issues = append(report.Issues, "cleanup: "+o.opts.Redactor.String(cleanErr.Error()))
		}
	}
	report.Verdict = "INCONCLUSIVE"
	report.State = "recovered"
	report.Issues = append(report.Issues, "recovered_after_interruption: rerun the scenario for a new verdict")
	if report.Baseline.TornDown && report.Candidate.TornDown {
		if releaseErr := store.ClearReservation(id); releaseErr != nil {
			return &report, releaseErr
		}
	}
	err = store.Put("attempts", id, report)
	return &report, err
}
