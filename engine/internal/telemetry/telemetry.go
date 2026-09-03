// Package telemetry connects the engine's event stream to the things that
// consume it.
//
// The events package describes itself as "the engine's single observability
// stream", and says the terminal dashboard, the pull request comment, the JSON
// output, the local NDJSON log and the control plane are all views over it
// rather than five parallel reporting paths that drift apart. That was the
// design and it was correct. It was not, however, connected: no production code
// in the engine had ever called Bus.AddSink, no sink constructor had a caller
// outside a test, and controlplane.NewSink had none at all. Forty event types
// were declared, two were emitted, and none of them went anywhere.
//
// This package is the connection. It is deliberately the only place that
// decides what a bus is attached to, because the alternative is each command
// assembling its own set and the paths drifting apart exactly as the events
// package warned.
//
// Three rules hold here and are each enforced by a test.
//
// Redaction happens at the writer. Every sink this package builds is handed the
// redactor, and the one place that converts an event into a span attribute
// applies it to every string. A call site somebody forgot is how a secret
// reaches a log, so there are no call sites: an event goes to the bus and the
// bus goes to the sinks.
//
// Nothing here can fail an environment. A control plane that is down, a
// collector that is unreachable, a log directory that cannot be created: each
// is reported and then ignored. The observability of a thing is not the thing,
// and a preview environment that will not come up because a dashboard is down
// is a worse failure than a missing graph.
//
// What cannot be delivered is either kept or counted. Events that cannot reach
// the control plane go to a durable spool that outlives the process, because
// AF-CPL-003 promises they are sent when it returns and the three commands that
// make up one environment are three processes. What is genuinely lost is
// counted and reported, because a gap nobody can account for is worse than one
// that is explained.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/controlplane"
	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/internal/state"
)

// LogDir is where the NDJSON event log lives, under the state directory.
const LogDir = "logs"

// SpoolDir is where undelivered control plane events wait, under the state
// directory.
const SpoolDir = "spool"

// Options configures what a bus is attached to.
type Options struct {
	// StateDir is the environment's local state directory, normally
	// <repository>/.antifailure. The log and the spool live under it so that
	// they disappear with the environment.
	StateDir string
	// EnvID is the environment these events belong to.
	EnvID string
	// Redactor scrubs everything on its way to a sink. Required.
	Redactor *redact.Redactor
	// Clock is the time source for the control plane sink's flush timer.
	Clock clock.Clock
	// State is the local state database, used for the durable event sequence.
	// Nil disables it, which means sequences restart at zero per process and
	// the control plane's projection stops advancing after the first command.
	State *state.DB
	// Getenv reads configuration. Nil uses the process environment.
	Getenv func(string) string
	// Version is the engine version, reported as a trace attribute.
	Version string
	// ControlPlaneURL overrides AF_CONTROL_PLANE_URL, for tests and for a
	// caller that already knows which control plane the credential below
	// belongs to.
	ControlPlaneURL string
	// ControlPlaneToken is the credential af login stored on this machine, or
	// "" when nobody has signed this terminal in.
	//
	// PASSED IN RATHER THAN READ HERE, and that is the whole reason it is a
	// field. This package must not open a credential store: it is the package
	// that writes support bundles and event logs, and a token it could read is
	// a token it could spill into one. The CLI knows where the credential lives
	// and hands over the value, which keeps the rule that nothing under
	// telemetry or controlplane touches a secret on disk.
	ControlPlaneToken string
	// TraceExporter replaces the OTLP exporter, for tests.
	TraceExporter sdktrace.SpanExporter
	// OnWarning receives the reasons a consumer could not be attached. Nil
	// discards them, which is right for a command that has no way to show one.
	OnWarning func(string)
}

// Telemetry owns everything attached to one bus, so that one Close puts all of
// it away in the right order.
type Telemetry struct {
	bus      *events.Bus
	envID    string
	sequence *SequenceReserver
	tracing  *Tracing
	restore  func()
	red      *redact.Redactor

	file    *events.FileSink
	cpSink  *controlplane.Sink
	spool   *Spool
	spanEnd func()

	warn func(string)
}

// Attach connects a bus to the local log, the control plane, and tracing.
//
// It returns a Telemetry even when parts of it could not be built. A missing
// control plane token is the ordinary case rather than an error, and a log
// directory that cannot be created is reported and survived.
func Attach(ctx context.Context, bus *events.Bus, opts Options) (*Telemetry, error) {
	if bus == nil {
		return nil, errors.New("telemetry: no bus to attach to")
	}
	if opts.Redactor == nil {
		return nil, errors.New("telemetry: a redactor is required, because every sink writes through it")
	}
	warn := opts.OnWarning
	if warn == nil {
		warn = func(string) {}
	}
	c := opts.Clock
	if c == nil {
		c = clock.New()
	}
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}

	t := &Telemetry{bus: bus, envID: opts.EnvID, warn: warn, red: opts.Redactor}

	// The durable sequence comes first, before a single event is emitted.
	// Attaching a sink to a bus that has already issued numbers from zero would
	// send the control plane a batch it is bound to refuse.
	if opts.State != nil && opts.EnvID != "" {
		t.sequence = NewSequenceReserver(opts.State)
		base, err := t.sequence.Reserve(ctx, opts.EnvID)
		if err != nil {
			warn(fmt.Sprintf(
				"the durable event sequence could not be read, so the control plane may ignore this run's events: %v", err))
			t.sequence = nil
			// The local log is the second copy of the same fact, so a state
			// database that cannot be read does not have to mean starting the
			// counter over. Restarting it would make two different events in
			// one environment share a sequence number, which is the one thing
			// a consumer reading for gaps cannot recover from.
			if opts.StateDir != "" {
				bus.ResumeSequence(opts.EnvID, events.LastSequence(
					filepath.Join(opts.StateDir, LogDir), safeName(opts.EnvID), opts.EnvID))
			}
		} else {
			bus.ResumeSequence(opts.EnvID, base)
		}
	}

	if opts.StateDir != "" {
		file, err := events.NewFileSink(
			filepath.Join(opts.StateDir, LogDir), safeName(opts.EnvID), opts.Redactor,
			events.FileSinkOptions{},
		)
		if err != nil {
			warn(fmt.Sprintf("the event log could not be opened, so this run is not recorded locally: %v", err))
		} else {
			t.file = file
			bus.AddSink(file)
		}
	}

	if err := t.attachControlPlane(ctx, bus, opts, c, getenv); err != nil {
		warn(err.Error())
	}

	tracing, err := NewTracing(ctx, TracingOptions{
		Getenv: getenv, Version: opts.Version, Exporter: opts.TraceExporter,
	})
	if err != nil {
		warn(err.Error())
	}
	t.tracing = tracing
	t.restore = tracing.Install()

	return t, nil
}

// attachControlPlane builds the client, the spool, and the sink.
//
// No token is not a failure. Most runs are on a laptop with no control plane at
// all, and the engine is designed so that everything works without one.
//
// Where the token comes from is the whole reason this function has three
// sources. The environment is first, because a user who has set one has said
// what they want and a self hosted installation has no runner to ask. Then the
// credential af login stored, which is how a person who signed this terminal in
// gets their own runs into their own console without exporting anything. Then a
// job running in GitHub Actions, which can prove what it is and trade that proof
// for a short lived credential, and is what makes a hosted control plane work
// with no secret in the repository at all. Before that existed this function
// reached the empty token on every CI run in the world, returned here, and built
// no sink: forty event types went to the local log and nowhere else.
//
// The stored credential was missing for as long as af login existed. Somebody
// signed a terminal in, ran af up, and watched an empty environments list,
// because the only thing that ever read that credential was af env pull. A
// sign-in that connects nothing is the same shape of failure as the sink with
// no token: everything looks configured and no event arrives.
func (t *Telemetry) attachControlPlane(
	ctx context.Context, bus *events.Bus, opts Options, c clock.Clock, getenv func(string) string,
) error {
	lookup := func(k string) (string, bool) {
		v := getenv(k)
		return v, v != ""
	}

	baseURL := opts.ControlPlaneURL
	if baseURL == "" {
		baseURL = getenv("AF_CONTROL_PLANE_URL")
	}

	// mint is how a credential is obtained from this job's identity, and it is
	// held rather than called once because the credential is short lived and a
	// run outlives it. The client calls it again when the control plane refuses
	// a batch, which is the only notice an expiry ever gives.
	mint := func(ctx context.Context) (string, error) {
		return controlplane.TokenFromWorkflowIdentity(ctx, controlplane.WorkflowIdentityOptions{
			Lookup: lookup, BaseURL: baseURL,
		})
	}
	// renew stays nil for a token that came from the environment. That one does
	// not expire, and re-minting over the top of a credential the user chose
	// would quietly ignore their choice on the first 401.
	var renew func(context.Context) (string, error)

	token := controlplane.TokenFromEnvironment(lookup)
	if token == "" {
		// The credential af login stored, handed over by the CLI. Whoever sets
		// it sets ControlPlaneURL with it, to the origin the credential was
		// stored under, so a terminal signed in to a self hosted plane reports
		// there rather than to the hosted default an empty address resolves to.
		token = opts.ControlPlaneToken
	}
	// An address is what says this repository uses a control plane at all.
	//
	// Only for the minted path, and the asymmetry is the point. Setting a token
	// is itself an explicit choice, so that path may fall back to the hosted
	// address. Nobody chooses anything by running in GitHub Actions, and
	// "without a control plane" is a first class way to use this tool: without
	// this condition every such run would trade an identity against the hosted
	// instance, be refused because the repository is not connected to it, and
	// print a warning about a service the user has never heard of.
	if token == "" && baseURL != "" && controlplane.WorkflowIdentityAvailable(lookup) {
		minted, err := mint(ctx)
		if err != nil {
			// Reported and survived, like everything else here. The commonest
			// reason to land on this line is a pull request from a fork, where
			// GitHub declines to mint an identity on purpose.
			return fmt.Errorf("this run is not reported to the control plane: %w", err)
		}
		token = minted
		renew = mint
	}
	if token == "" {
		return nil
	}
	client, err := controlplane.New(controlplane.Options{
		BaseURL: baseURL, Token: token, Renew: renew, Clock: c, Redactor: opts.Redactor,
	})
	if err != nil {
		if errors.Is(err, controlplane.ErrNotConfigured) {
			return nil
		}
		return fmt.Errorf("the control plane is not configured, so this run is not reported: %w", err)
	}

	var overflow controlplane.Overflow
	if opts.StateDir != "" {
		spool, serr := NewSpool(SpoolOptions{
			Dir: filepath.Join(opts.StateDir, SpoolDir), Redactor: opts.Redactor,
		})
		if serr != nil {
			// Worth saying out loud rather than swallowing: without the spool
			// the sink is back to losing an outage's events at exit, which is
			// the behaviour AF-CPL-003 promises it does not have.
			t.warn(fmt.Sprintf(
				"events that cannot be delivered will be dropped rather than kept, because the spool could not be opened: %v", serr))
		} else {
			t.spool = spool
			overflow = spool
		}
	}

	t.cpSink = controlplane.NewSink(controlplane.SinkOptions{
		Client: client, Clock: c, Overflow: overflow,
		OnError: func(err error) { t.warn(err.Error()) },
	})
	bus.AddSink(t.cpSink)
	return nil
}

// StartCommand opens the root span for one `af` invocation.
//
// Events emitted while it is open are recorded on it, so a trace shows the
// shape of the command rather than a thousand one-microsecond spans. Calling it
// when tracing is off costs one no-op span and nothing else.
func (t *Telemetry) StartCommand(ctx context.Context, name string) context.Context {
	if t == nil {
		return ctx
	}
	ctx, span := t.tracing.Tracer().Start(ctx, name, trace.WithSpanKind(trace.SpanKindInternal))
	t.spanEnd = func() { span.End() }
	if t.tracing.Enabled() && t.bus != nil {
		sink := newSpanSink(span, t.red)
		t.bus.AddSink(sink)
	}
	return ctx
}

// Spool exposes the durable buffer.
//
// Exported for the tests, which is all that reads it. It claimed `af doctor`
// as a caller until a sweep found there was none; what an operator actually
// gets is the line Close reports when a command ends owing events, which is
// below and does have one.
func (t *Telemetry) Spool() *Spool { return t.spool }

// Close puts everything away in the order that loses least.
//
// The bus closes first, which drains every sink queue, so that an event emitted
// a microsecond before the command ended still reaches the control plane sink
// before that sink is asked to flush. Then the sequence is settled, so the next
// command continues from the right number. Then tracing is flushed. Each step
// runs even if an earlier one failed, and the first error is reported.
func (t *Telemetry) Close(ctx context.Context) error {
	if t == nil {
		return nil
	}
	var first error
	keep := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}

	if t.spanEnd != nil {
		t.spanEnd()
	}
	if t.bus != nil {
		keep(t.bus.Close())
	}
	if t.cpSink != nil {
		keep(t.cpSink.Close())
	}
	if t.file != nil {
		keep(t.file.Close())
	}
	if t.sequence != nil && t.bus != nil && t.envID != "" {
		keep(t.sequence.Settle(ctx, t.envID, t.bus.Seq(t.envID)))
	}
	// Said out loud, because this is the moment the promise either held or did
	// not. A command that ends owing events is fine and expected during a
	// control plane outage, and a user who is told so can tell it apart from a
	// dashboard that has silently stopped updating. Events genuinely lost are
	// reported by the sink's own error.
	if t.spool != nil {
		if owed := t.spool.Pending(); owed > 0 {
			t.warn(fmt.Sprintf(
				"%d batches of events are waiting for the control plane and will be sent by "+
					"the next command that reaches it", owed))
		}
		if dropped := t.spool.Dropped(); dropped > 0 {
			t.warn(fmt.Sprintf(
				"%d events were discarded because the spool is full; the dashboard will have a gap",
				dropped))
		}
	}

	if t.restore != nil {
		t.restore()
	}
	keep(t.tracing.Shutdown(ctx))
	return first
}

// safeName keeps an environment identifier usable as a filename. Identifiers
// are already lowercase and hyphenated by EnvID, so this is a guard rather than
// a transformation.
func safeName(env string) string {
	if env == "" {
		return "engine"
	}
	out := make([]rune, 0, len(env))
	for _, r := range env {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			out = append(out, r)
		default:
			out = append(out, '-')
		}
	}
	return string(out)
}
