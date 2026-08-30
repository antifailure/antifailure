// Package events is the engine's single observability stream.
//
// Everything the engine does emits a typed event. The terminal dashboard, the
// pull request comment, the JSON output mode, the local NDJSON log, and the
// control plane are all views over the same stream rather than five parallel
// reporting paths that drift apart. If something is worth showing a user, it
// is an event, and every consumer gets it for free.
//
// Two properties are load bearing. Events carry a monotonic sequence number
// per environment, so a consumer can order them and detect a gap. And no sink
// can ever block the engine: a slow or dead sink drops events and increments a
// counter that the dashboard shows, because a preview environment that stalls
// because a log file is on a full disk is a worse failure than a missing log
// line.
package events

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Type identifies what happened. Types are stable strings: they appear in the
// NDJSON log, in the control plane, and in user written filters.
type Type string

// The event catalog. Adding a type here is the only way to add an event, and
// the reference page is generated from this list.
const (
	// Lifecycle
	EnvCreating   Type = "env.creating"
	EnvReady      Type = "env.ready"
	EnvFailed     Type = "env.failed"
	EnvSleeping   Type = "env.sleeping"
	EnvWaking     Type = "env.waking"
	EnvDestroying Type = "env.destroying"
	EnvDestroyed  Type = "env.destroyed"

	// Resources. Every create and delete is recorded so the leak detector has
	// a ledger to compare provider inventory against.
	ResourceCreated Type = "resource.created"
	ResourceDeleted Type = "resource.deleted"
	ResourceLeaked  Type = "resource.leaked"

	// Database and goldens
	GoldenRefreshing Type = "golden.refreshing"
	GoldenReady      Type = "golden.ready"
	GoldenFailed     Type = "golden.failed"
	GoldenCollected  Type = "golden.collected"
	DBBranching      Type = "db.branching"
	DBBranched       Type = "db.branched"
	DBReset          Type = "db.reset"
	DBDestroyed      Type = "db.destroyed"

	// Masking and verification
	MaskPlanned   Type = "mask.planned"
	MaskProgress  Type = "mask.progress"
	MaskApplied   Type = "mask.applied"
	MaskVerifying Type = "mask.verifying"
	MaskVerified  Type = "mask.verified"
	MaskFinding   Type = "mask.finding"

	// Build
	BuildStarted  Type = "build.started"
	BuildLog      Type = "build.log"
	BuildFinished Type = "build.finished"
	BuildFailed   Type = "build.failed"

	// Runtime
	ServiceStarting Type = "service.starting"
	ServiceReady    Type = "service.ready"
	ServiceLog      Type = "service.log"
	ServiceExited   Type = "service.exited"
	ServiceRestart  Type = "service.restarted"
	CronFired       Type = "cron.fired"

	// Egress
	EgressDecision   Type = "egress.decision"
	EgressTripwire   Type = "egress.tripwire"
	CaptureMessage   Type = "capture.message"
	WebhookQueued    Type = "webhook.queued"
	WebhookDelivered Type = "webhook.delivered"
	WebhookFailed    Type = "webhook.failed"

	// Agents
	AgentStarted  Type = "agent.started"
	AgentStep     Type = "agent.step"
	AgentVerdict  Type = "agent.verdict"
	AgentFinished Type = "agent.finished"

	// Insights and load
	InsightFinding Type = "insight.finding"
	LoadSample     Type = "load.sample"
	LoadFinished   Type = "load.finished"

	// Engine internals worth surfacing
	Progress    Type = "engine.progress"
	Warning     Type = "engine.warning"
	Error       Type = "engine.error"
	Retry       Type = "engine.retry"
	SinkDropped Type = "engine.sink_dropped"
)

// AllTypes returns every event type, sorted. The reference generator and the
// completeness test use it.
func AllTypes() []Type {
	out := make([]Type, 0, len(typeDocs))
	for t := range typeDocs {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Describe returns the one sentence description of an event type.
func Describe(t Type) string { return typeDocs[t] }

// typeDocs is the source for the generated events reference page. A type with
// no description fails the completeness test, so the reference cannot rot.
var typeDocs = map[Type]string{
	EnvCreating:      "An environment has started being created.",
	EnvReady:         "An environment is fully built, running, and reachable.",
	EnvFailed:        "An environment could not be created. The data carries the error code.",
	EnvSleeping:      "An idle environment has been scaled to zero.",
	EnvWaking:        "A sleeping environment is being woken by a request.",
	EnvDestroying:    "Teardown has started.",
	EnvDestroyed:     "Teardown finished and every recorded resource is gone.",
	ResourceCreated:  "An external resource was created and committed to the journal.",
	ResourceDeleted:  "An external resource was deleted and its journal entry compensated.",
	ResourceLeaked:   "The leak detector found a resource the journal does not know about.",
	GoldenRefreshing: "A golden refresh has started.",
	GoldenReady:      "A golden version is masked, verified, and available to branch from.",
	GoldenFailed:     "A golden refresh failed. No version was published.",
	GoldenCollected:  "An unreferenced golden version was garbage collected.",
	DBBranching:      "A database branch is being created from a golden version.",
	DBBranched:       "A database branch is ready.",
	DBReset:          "A branch was reset to its golden state.",
	DBDestroyed:      "A branch was destroyed.",
	MaskPlanned:      "Masking produced a plan. The data carries affected tables and row counts.",
	MaskProgress:     "A masking chunk finished. The data carries the fraction complete.",
	MaskApplied:      "Masking finished on a golden candidate.",
	MaskVerifying:    "The verification scanner has started reading back the golden.",
	MaskVerified:     "Verification passed and an attestation was signed.",
	MaskFinding:      "Verification found data matching a detector. The value is never included.",
	BuildStarted:     "A service build has started.",
	BuildLog:         "A line of build output, redacted.",
	BuildFinished:    "A service build succeeded. The data carries the image digest.",
	BuildFailed:      "A service build failed.",
	ServiceStarting:  "A service container or pod is starting.",
	ServiceReady:     "A service passed its readiness check.",
	ServiceLog:       "A line of service output, redacted.",
	ServiceExited:    "A service exited. The data carries the exit code.",
	ServiceRestart:   "A service was restarted after a crash or an eviction.",
	CronFired:        "A scheduled job fired.",
	EgressDecision:   "The proxy decided what to do with an outbound request.",
	EgressTripwire:   "A request carrying a live credential was blocked.",
	CaptureMessage:   "An outbound email or message was captured into the inbox.",
	WebhookQueued:    "An inbound webhook was queued for delivery.",
	WebhookDelivered: "An inbound webhook was delivered and acknowledged.",
	WebhookFailed:    "An inbound webhook could not be delivered after its retries.",
	AgentStarted:     "An agent workflow has started.",
	AgentStep:        "An agent took one action. The data carries its stated intent.",
	AgentVerdict:     "A workflow reached a verdict.",
	AgentFinished:    "An agent run finished. The data carries the verdict counts.",
	InsightFinding:   "A database insight was found: a lock, a regression, or a plan change.",
	LoadSample:       "A load test metric sample.",
	LoadFinished:     "A load run finished. The data carries the comparison against main.",
	Progress:         "A step in a long running operation, for work with no more specific event of its own.",
	Warning:          "Something is not right but the operation continues.",
	Error:            "An operation failed. The data carries the error code.",
	Retry:            "A provider call is being retried after a transient failure.",
	SinkDropped:      "A sink fell behind and dropped events. The data carries the count.",
}

// Level classifies an event for display and filtering.
type Level string

const (
	LevelDebug Level = "debug"
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// Event is one thing that happened.
//
// The envelope is fixed by schemas/events.v1.json, which is generated from this
// struct and diffed by the gate, and Data is type specific and always a JSON
// object.
//
// It is NOT the envelope the control plane receives, and this comment used to
// say it was identical across the engine, the runner and the control plane.
// Four of the eight names differ: ts against occurredAt, seq against sequence,
// env against envId, data against payload, and level and msg have no
// counterpart at all. internal/controlplane/client.go declares that shape and
// sink.go translates into it. The runner emits no such envelope in either form.
// Nothing outside Go reads this schema, so a false claim of a shared envelope
// invites somebody to build against one that does not exist.
type Event struct {
	// ID is unique for this event.
	ID string `json:"id"`
	// TS is when it happened, from the injected clock.
	TS time.Time `json:"ts"`
	// Env is the environment identifier, or empty for engine wide events.
	Env string `json:"env,omitempty"`
	// Seq is a monotonic counter per environment, so a consumer can order
	// events and notice a gap. Engine wide events share the empty environment's
	// sequence.
	Seq uint64 `json:"seq"`
	// Type is what happened.
	Type Type `json:"type"`
	// Level classifies the event for display and filtering.
	Level Level `json:"level"`
	// Msg is a short human readable summary. It is redacted before it is
	// written, like everything else.
	Msg string `json:"msg,omitempty"`
	// Data carries the type specific payload.
	Data map[string]any `json:"data,omitempty"`
}

// String renders a compact single line form, used by the non-interactive
// output mode and by tests.
func (e Event) String() string {
	var b strings.Builder
	b.WriteString(string(e.Type))
	if e.Env != "" {
		b.WriteString(" env=")
		b.WriteString(e.Env)
	}
	if e.Msg != "" {
		b.WriteString(" ")
		b.WriteString(e.Msg)
	}
	if len(e.Data) > 0 {
		keys := make([]string, 0, len(e.Data))
		for k := range e.Data {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, " %s=%v", k, e.Data[k])
		}
	}
	return b.String()
}

// MarshalJSON is the wire form. It is defined explicitly so that a change to
// the struct cannot silently change the schema.
func (e Event) MarshalJSON() ([]byte, error) {
	type alias Event
	return json.Marshal(alias(e))
}

// Field is a convenience for building the data map at a call site.
type Field struct {
	Key   string
	Value any
}

// F returns a field.
func F(key string, value any) Field { return Field{key, value} }

func fieldsToMap(fs []Field) map[string]any {
	if len(fs) == 0 {
		return nil
	}
	m := make(map[string]any, len(fs))
	for _, f := range fs {
		m[f.Key] = f.Value
	}
	return m
}
