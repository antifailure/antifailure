package security

import "github.com/antifailure/antifailure/engine/internal/runtime/local"

// The per-run reader interface a family reads through, and the router writes
// through.
//
// Some families are active: authz and injection drive Env.BaseURL and read the
// responses they provoke, so they need nothing here. Others are readers: ssrf
// reads the egress decision log, side_effect reads the decision and message
// logs and diffs them against a base twin, canary_leak reads the browser
// evidence, and authz's increment reads structured per-persona observations.
// Those artifacts exist only after the run, so they cannot be handed to a
// family at registration (which is static); they arrive on the Input the router
// builds per run, through the accessors below.
//
// Every accessor distinguishes ABSENT from EMPTY, because for a security reader
// the two are opposite verdicts. An empty decision log is "the run made no
// outbound call"; an absent one is "we did not capture the log", and a reader
// that could not look must fail closed rather than report a clean pass. So the
// backing fields are unexported, the accessors report absence honestly, and a
// reader keys on it.

// Decisions returns the CANDIDATE run's egress decisions, shared by the ssrf
// family (which reads where the application reached) and the side_effect family
// (which counts outbound calls). Nil when the router did not attach a log,
// which a reader treats as "not measured", never as "no calls".
func (in Input) Decisions() []local.Decision { return in.decisions }

// Messages returns the CANDIDATE run's captured messages: the email, sms and
// webhook side effects the application emitted against the twin. The
// side_effect family counts them. Nil when the router did not attach them.
func (in Input) Messages() []local.Message { return in.messages }

// Observations returns the CANDIDATE run's structured per-persona observations,
// which the authz family reads to decide whether a persona reached content it
// should not have. Each is a bounded location and category, never a value.
// Nil until the runner emits them, which a reader fails closed on rather than
// reading as "no violation".
func (in Input) Observations() []RawObservation { return in.observations }

// Evidence returns the CANDIDATE run's browser evidence: the DOM the pages
// rendered and the response bodies the browser received, which the canary_leak
// family scans for a planted token. Empty is a family simply not handed that
// evidence, against the twin; the values stay inside the engine and never reach
// a finding.
func (in Input) Evidence() Evidence { return in.evidence }

// Routes returns the ingress routes the workflows actually reached during the
// candidate run, for the injection family to fuzz. Three states, and the
// injection family reads all three:
//
//   - a populated slice: fuzz these routes;
//   - an empty, non-nil slice: the run reached routes but none this change
//     touched are worth fuzzing, a quiet and legitimate pass;
//   - nil: no observed-route source was wired, which is UNAVAILABLE, and the
//     injection family reports blocked rather than a pass.
//
// The router returns nil until an ingress-route source exists, so an injection
// probe never reads a missing source as a clean bill of health.
func (in Input) Routes() []Route { return in.routes }

// Baseline returns the BASE twin's artifacts as one bundle, and ok reporting
// whether a base twin was actually built and run. side_effect reads
// base.Decisions and base.Messages to diff its candidate counts; authz reads
// base.Observations to tell a pre-existing reach from one this change opened.
//
// ok=false is the load-bearing state: it means no base twin was measured, and a
// reader MUST then skip its baseline comparison entirely rather than treat the
// absent baseline as a base of zero. A zero that means "did not measure" read as
// "the base did nothing" is exactly the banned defect this product keeps finding
// in its own instruments, so the absence is a bool and never a zero-valued
// struct. ok=true only when a real base twin produced the bundle.
func (in Input) Baseline() (Baseline, bool) {
	if in.baseline == nil {
		return Baseline{}, false
	}
	return *in.baseline, true
}

// Baseline is what the base twin emitted, so a reader can diff a candidate run
// against it. It exists only when a base twin was actually built and run;
// Input.Baseline returns ok=false otherwise, and the fields are meaningless in
// that case and must not be read. One bundle, one ok flag, so a base run is
// present or absent as a whole rather than field by field.
type Baseline struct {
	// Decisions is the base twin's egress decision log.
	Decisions []local.Decision
	// Messages is the base twin's captured message log.
	Messages []local.Message
	// Observations is the base twin's per-persona observations, for authz to
	// tell a reach the base already allowed from one this change opened.
	Observations []RawObservation
}

// RawObservation is one per-persona observation the runner made against the
// candidate twin, for the authz family to decide access control by content
// presence rather than by status code.
//
// Every field is a bounded location, a category or a flag, and NONE is a raw
// value: Route is a template, ObjectClass is a category label such as "another
// tenant's invoice" and never an id, and content presence is decided against
// the golden's planted canaries rather than by returning the body. It crosses
// into a finding as a location and a category only, never as what was read.
type RawObservation struct {
	// Route is the location template the persona reached, never a value.
	Route string
	// Method is the HTTP method used.
	Method string
	// Anonymous reports that no session was carried: the no-session probe.
	Anonymous bool
	// ActorTenant, ActorUser and ActorRole identify who acted, by stable
	// identifier, never a credential.
	ActorTenant string
	ActorUser   string
	ActorRole   string
	// ObjectClass is the category of the thing reached, a label and never a
	// raw id, for example "another tenant's invoice".
	ObjectClass string
	// OwnerTenant, OwnerUser and OwnerRole identify who owns the object
	// reached, so an idor is the actor and owner differing on a present read.
	OwnerTenant string
	OwnerUser   string
	OwnerRole   string
	// Status is the HTTP status the reach returned.
	Status int
	// VictimContentPresent reports that content the persona should not see was
	// present, decided by a golden canary rather than by the status code.
	VictimContentPresent bool
	// SetupConfirmed reports that the object was seeded before the reach, so a
	// 404 proves a boundary held rather than that the id was invented.
	SetupConfirmed bool
}

// Evidence is the browser evidence a leak family scans: the DOM the pages
// rendered and the bodies of the responses the browser received, against the
// twin. The values stay inside the engine; a finding that reads them reports a
// location and never a body. Empty is "not handed evidence", not "found none".
type Evidence struct {
	// DOM is the rendered document for each observed page.
	DOM []string
	// Responses is the body of each response the browser received.
	Responses []string
}

// Route is one ingress route the workflows reached, for a fuzzer to vary. It is
// a location, never a value: the method and the path, and the names of the
// parameters a fuzzer changes, never a captured request body.
type Route struct {
	// Method is the HTTP method, for example GET or POST.
	Method string
	// Path is the route pattern, for example /api/orders/{id}.
	Path string
	// Params names the parameters a fuzzer varies, by name only.
	Params []string
}

// RunArtifacts are the per-run reader artifacts the router attaches to an Input
// before Probe. It is the one place the accessors are filled, so a caller sets
// them together and a reader reads them together. The candidate fields are
// flat; the base twin's artifacts, when one was built, arrive in Baseline.
type RunArtifacts struct {
	// Decisions is the candidate run's egress decision log.
	Decisions []local.Decision
	// Messages is the candidate run's captured message log.
	Messages []local.Message
	// Observations is the candidate run's per-persona observations, or nil
	// until the runner emits them.
	Observations []RawObservation
	// Evidence is the candidate run's browser evidence.
	Evidence Evidence
	// Routes is the observed ingress routes, or nil when none were sourced,
	// which Input.Routes surfaces as UNAVAILABLE.
	Routes []Route
	// Baseline is the base twin's bundle, or nil when no base twin was run,
	// which Input.Baseline surfaces as ok=false.
	Baseline *Baseline
}

// WithRunArtifacts returns a copy of the Input carrying the per-run reader
// artifacts. The router builds the static parts of an Input as a struct literal
// and then attaches these, so the unexported reader fields are filled in one
// place and never by a family. It is additive: an Input never passed through
// here exposes every reader as absent.
func (in Input) WithRunArtifacts(a RunArtifacts) Input {
	in.decisions = a.Decisions
	in.messages = a.Messages
	in.observations = a.Observations
	in.evidence = a.Evidence
	in.routes = a.Routes
	in.baseline = a.Baseline
	return in
}
