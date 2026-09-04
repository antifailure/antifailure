package env

// The wire between what a run produced and anybody who is not watching the
// terminal it produced it in.
//
// Three of the eleven event types the control plane sink translates were
// emitted by nothing at all: agent.started, agent.finished and agent.verdict.
// The sink's own comment names them and says nobody could tell from the code
// whether they were reserved or abandoned. They were neither. `af test` runs in
// its own process, opens no session, and therefore has no bus, so there was
// nowhere for the events to go, and the runs and verdicts tables in the control
// plane had no writer outside the test harness and the staging seeder. The
// console's runs list and every verdict under it were empty for every customer
// and full in development.
//
// A fourth, egress.decision, was disconnected in a different way: the decisions
// are made by the sidecar, recorded, read back by af net and af ci, and never
// put on the bus. The console's network page groups them out of the events
// table, so nothing else had to change to make that page work; they only had to
// be sent.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/state"
	"github.com/antifailure/antifailure/engine/internal/telemetry"
)

// openReporting opens a session that can only report.
//
// A bus, the durable sequence behind it and whatever sinks the caller
// attached, and nothing else. No lock, because `af test` deliberately takes
// none: the moment somebody most wants to know what an environment reached is
// while it is doing something, and a reporting session that blocked `af down`
// would trade a working dashboard for a stuck teardown. No database provider
// and no runtime, because reporting touches neither.
//
// It returns nil rather than an error when it cannot open. A run must not fail
// because a dashboard could not be reached, which is the same rule the sink
// itself is built on, and every emitter here tolerates a nil session.
func (o *Orchestrator) openReporting(ctx context.Context) *session {
	stateDir := filepath.Join(o.opts.Root, StateDir)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		o.progress("this run is not being recorded: " + err.Error())
		return nil
	}
	s := &session{}
	db, err := state.Open(ctx, stateDir)
	if err != nil {
		o.progress("this run is not being recorded: " + err.Error())
		return nil
	}
	s.db = db
	s.bus = events.NewBus(o.opts.Clock)

	tel, terr := telemetry.Attach(ctx, s.bus, telemetry.Options{
		StateDir:          stateDir,
		EnvID:             o.envID,
		Redactor:          o.opts.Redactor,
		Clock:             o.opts.Clock,
		State:             s.db,
		Getenv:            o.opts.Getenv,
		Version:           o.opts.Version,
		ControlPlaneURL:   o.opts.ControlPlaneURL,
		ControlPlaneToken: o.opts.ControlPlaneToken,
		OnWarning: func(msg string) {
			o.progress(msg)
		},
	})
	if terr != nil {
		o.progress(fmt.Sprintf("this run is not being recorded: %v", terr))
	}
	s.tel = tel
	for _, sink := range o.sinks {
		if sink != nil {
			s.bus.AddSink(sink)
		}
	}
	return s
}

// closeSession closes a session that may not exist.
//
// session.close dereferences its receiver, so a deferred close on a session
// the caller was allowed to fail to open would panic on exactly the path that
// is meant to survive.
func closeSession(s *session) {
	if s != nil {
		s.close()
	}
}

// runID names one invocation of the agent runner.
//
// The same shape as a golden version, so that sorting by name sorts by age,
// and unique within an environment rather than globally: the control plane
// keys a run on the organization, the environment and this, so two machines
// running the same branch cannot collide unless they are also the same
// environment, in which case they are the same run being reported twice and
// collapsing them is correct.
func runID(at time.Time, envID string) string {
	sum := sha256.Sum256([]byte(envID + at.UTC().Format(time.RFC3339Nano)))
	return fmt.Sprintf("run_%s_%s", at.UTC().Format("20060102150405"), hex.EncodeToString(sum[:4]))
}

// runFields is what every event about one run carries.
//
// The identity is on all three, not only the first, for the reason
// identity.go states in full: the sink drops the OLDEST events of a failed
// batch, so an identity carried on only the first event is an identity that
// goes missing in precisely the case it is needed.
func (o *Orchestrator) runFields(id, kind string) []events.Field {
	return append(o.identity(), events.F("run_id", id), events.F("kind", kind))
}

// reportRunStarted says an agent run began.
func (o *Orchestrator) reportRunStarted(s *session, id, kind string, at time.Time, workflows int) {
	o.event(s, events.AgentStarted,
		fmt.Sprintf("running %d workflow(s)", workflows),
		append(o.runFields(id, kind),
			events.F("started_at", at.UTC().Format(time.RFC3339Nano)),
			events.F("workflows", workflows))...)
}

// reportVerdicts says what each workflow decided.
//
// One event per workflow rather than one carrying all of them, because the
// control plane stores one row per workflow and a batch that overflows the
// sink's spool would otherwise lose every verdict of the run rather than the
// oldest few.
//
// The reproduction travels as the steps somebody would follow, which is what
// the console renders under a failing workflow. It is the runner's own list;
// nothing here interprets it.
func (o *Orchestrator) reportVerdicts(s *session, id string, results []WorkflowResult) {
	for _, r := range results {
		fields := append(o.runFields(id, "workflows"),
			events.F("workflow", r.Workflow),
			events.F("value", r.Outcome.Verdict),
			events.F("steps", len(r.Steps)),
			events.F("duration_ms", r.DurationMs),
		)
		if r.Outcome.Detail != "" {
			fields = append(fields, events.F("summary", r.Outcome.Detail))
		}
		if len(r.Outcome.Reproduction) > 0 {
			fields = append(fields, events.F("reproduction", jsonStrings(r.Outcome.Reproduction)))
		}
		o.event(s, events.AgentVerdict, r.Workflow+" is "+r.Outcome.Verdict, fields...)
	}
}

// reportRunFinished says how the run came out.
//
// The state is the run's own, not the application's verdict. A run whose
// workflows all failed still COMPLETED, and recording it as failed would make
// a working product indistinguishable from a runner that crashed. Only a run
// that could not be carried through is failed, which is what the caller passes
// when the runner itself returned an error.
func (o *Orchestrator) reportRunFinished(
	s *session, id string, at time.Time, report *TestReport, runState string,
) {
	fields := append(o.runFields(id, "workflows"),
		events.F("state", runState),
		events.F("started_at", at.UTC().Format(time.RFC3339Nano)),
	)
	if report != nil {
		fields = append(fields,
			events.F("passed", report.Passed), events.F("failed", report.Failed),
			events.F("flaky", report.Flaky), events.F("blocked", report.Blocked),
			events.F("unverified", report.Unverified))
	}
	o.event(s, events.AgentFinished, "the run finished", fields...)
}

// MaxReportedDecisions bounds how many egress decisions one teardown sends.
//
// A bound rather than everything, because a long lived environment can make
// hundreds of thousands of outbound requests and the sink's spool is not a
// database. The console groups them by host and mode to answer "what did this
// reach for", and the newest few hundred answer that; the complete log stays
// where it already is, in the sidecar, readable with af net.
const MaxReportedDecisions = 500

// ReportDecisions puts the sidecar's egress decisions on the bus.
//
// Called once per environment, at teardown, and that is the whole reason it is
// not called from anywhere else. The bus stamps a random identifier on each
// event, so the control plane cannot tell a resend from a second report, and
// the console's network page counts rows: emitting the same decision from two
// places would double every number on it.
//
// A failure to read them is reported through progress and does not fail the
// teardown. The environment is going away either way, and refusing to remove
// it because its egress log could not be read would leave a customer with
// containers they cannot get rid of.
func (o *Orchestrator) ReportDecisions(ctx context.Context, s *session) {
	if s == nil {
		return
	}
	// An environment with no sidecar is not an error here: Runtime.Decisions
	// answers with an empty list rather than a Docker error when the proxy
	// container does not exist, so the quiet common case stays quiet and a
	// real failure to read still gets said out loud.
	decisions, err := o.Decisions(ctx, MaxReportedDecisions)
	if err != nil {
		o.progress("the egress decisions could not be read, so the console will not show " +
			"what this environment reached for: " + err.Error())
		return
	}
	for _, d := range decisions {
		fields := append(o.identity(),
			events.F("host", d.Host),
			events.F("mode", d.Mode),
			events.F("allowed", d.Allowed),
			events.F("method", d.Method),
		)
		if d.Rule != "" {
			fields = append(fields, events.F("rule", d.Rule))
		}
		if d.Reason != "" {
			fields = append(fields, events.F("reason", d.Reason))
		}
		// The path is deliberately absent. A host and a mode answer what the
		// environment reached for and what policy said about it, and a path
		// carries query strings, identifiers and occasionally a token, which
		// is the boundary identity.go names as the one that must not be
		// crossed.
		o.event(s, events.EgressDecision,
			fmt.Sprintf("%s %s was %s", d.Mode, d.Host, allowedWord(d.Allowed)), fields...)
	}
}

func allowedWord(allowed bool) string {
	if allowed {
		return "allowed"
	}
	return "refused"
}

// jsonStrings renders a list of lines as a JSON array, for a jsonb column.
//
// Encoded here rather than left to the sink, because an event field is a
// scalar on the wire and the control plane stores this one as a document. A
// list joined with newlines would arrive as a string that every query written
// against the column would have to split.
func jsonStrings(lines []string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, line := range lines {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(quoteJSON(line))
	}
	b.WriteByte(']')
	return b.String()
}

func quoteJSON(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
