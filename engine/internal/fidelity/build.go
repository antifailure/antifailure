package fidelity

import (
	"fmt"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Observation is what the orchestrator saw, and it is the only input Build
// has.
//
// A struct rather than a set of live handles so that Build is a pure function:
// the same observation produces the same inventory, on any machine, in any
// order, which is what "reproducible" has to mean for a number somebody might
// gate on. It is also what lets every rule below be tested without a Docker
// daemon or a Postgres.
//
// Every field is filled from something that already knew the answer. The
// *Reason fields carry why a group could not be observed at all, and a
// non-empty one turns its whole dimension into a named unknown rather than
// into a silent absence.
type Observation struct {
	EnvID    string
	Manifest *schema.Manifest

	// Running is what the runtime reports, and ServicesReason says why it
	// could not be asked.
	Running        []provider.RunningService
	ServicesReason string

	// Runtime describes where the environment runs, in one line, for the
	// dimension that has nothing to compare itself against.
	Runtime string

	// Golden is the golden the branch came from, and GoldenReason says why
	// the provider could not say.
	Golden       string
	GoldenReason string
	// Attested reports whether that golden is verified and its attestation
	// parsed and matched its own signature. Attestation describes what the
	// attestation covered when it did, and says which of those failed when it
	// did not, so an absence always carries the reason for it.
	Attested    bool
	Attestation string
	// Tables and Rows are what the branch holds, read from the branch.
	// RowsAreAFloor reports that a live count stopped at its ceiling, so the
	// number is at least that many rather than exactly that many.
	// BranchReason says why it could not be read.
	Tables        int
	Rows          int64
	RowsAreAFloor bool
	BranchReason  string
	// Subset reports whether the golden was built as a production shaped
	// slice, and Empty whether it was built with no source database at all.
	Subset bool
	Empty  bool

	// Hosts describes each third party host the policy names.
	Hosts []Host

	// Personas describes each declared account, as the branch answered for
	// it. PersonasReason says why they could not be looked for.
	Personas       []Persona
	PersonasReason string

	// Traffic describes where the endpoint mix came from, and TrafficReason
	// says why the configured source produced nothing.
	Traffic       string
	TrafficReason string
}

// Host is one third party host the egress policy names.
type Host struct {
	Name string
	Mode schema.Mode
	// Pack is the mock pack that answers for this host, empty when none
	// does. Stateful reports whether that pack remembers what was created,
	// which is the difference between a mock of a provider and a list of
	// canned answers.
	Pack     string
	Stateful bool
	// PackReason says why the answering pack could not be determined, which
	// happens for a rule matching a pattern rather than one host.
	PackReason string
}

// Persona is one declared account, as the branch answered for it.
type Persona struct {
	Name  string
	Login schema.LoginStrategy
	MFA   bool
	// Present reports whether an account with this address exists in the
	// branch. Reason says why it could not be looked for, and a non-empty
	// Reason makes Present meaningless.
	Present bool
	Reason  string
	// Table is where it was looked for.
	Table string
	// Factors reports whether the scheme has somewhere to enrol a second
	// factor, which a totp or mfa persona needs.
	Factors bool
	// Deliverable reports whether the policy captures messages, which is what
	// a magic link or a one time code needs to arrive.
	Deliverable bool
}

// Build turns an observation into an inventory.
//
// Every dimension is present in the result whether or not it had anything to
// measure, in schema.AllFidelityDimensions order, so that the document shape
// does not change with the environment and a dimension that measured nothing
// is visibly there rather than missing.
func Build(obs Observation) Inventory {
	if obs.Manifest == nil {
		// A manifest is guaranteed by every caller, and an empty one here
		// makes every dimension report that it had nothing to measure rather
		// than making a report panic. A report that cannot be produced is the
		// one moment somebody most needs it.
		obs.Manifest = &schema.Manifest{}
	}
	return Inventory{
		EnvID: obs.EnvID,
		Dimensions: []Dimension{
			services(obs),
			database(obs),
			thirdParty(obs),
			auth(obs),
			runtime(obs),
			traffic(obs),
		},
	}
}

func services(obs Observation) Dimension {
	d := Dimension{Name: schema.FidelityServices}
	declared := obs.Manifest.Services
	if len(declared) == 0 {
		d.NotApplicable = "the manifest declares no services"
		return d
	}
	if obs.ServicesReason != "" {
		for _, s := range declared {
			d.Components = append(d.Components, Component{
				Name: s.Name, State: Unmeasured, Detail: obs.ServicesReason,
			})
		}
		return d
	}

	running := map[string]provider.RunningService{}
	for _, r := range obs.Running {
		running[r.Name] = r
	}
	for _, s := range declared {
		r, up := running[s.Name]
		switch {
		case !up:
			d.Components = append(d.Components, Component{
				Name: s.Name, State: Absent,
				Detail: "declared and not running",
			})
		case r.Ready:
			d.Components = append(d.Components, Component{
				Name: s.Name, State: Reproduced,
				Detail: describeService(s, r),
			})
		default:
			// Present and not answering. Absent rather than substituted: an
			// application nobody can reach is not standing in for anything,
			// and the runtime's own words say more than a verdict does.
			d.Components = append(d.Components, Component{
				Name: s.Name, State: Absent,
				Detail: strings.TrimSpace(r.State + " " + r.Detail),
			})
		}
	}
	return d
}

// describeService says what is running, in the words the runtime used.
func describeService(s schema.Service, r provider.RunningService) string {
	kind := string(s.Kind)
	if kind == "" {
		kind = r.Kind
	}
	if r.URL != "" {
		return kind + " at " + r.URL
	}
	return kind + ", running"
}

func database(obs Observation) Dimension {
	d := Dimension{Name: schema.FidelityDatabase}
	if obs.Manifest.Database == nil {
		d.NotApplicable = "the manifest declares no database"
		return d
	}
	d.Components = append(d.Components, dataComponent(obs), provenanceComponent(obs))
	return d
}

// dataComponent answers whether the branch holds production's data.
func dataComponent(obs Observation) Component {
	c := Component{Name: "data"}
	switch {
	case obs.BranchReason != "":
		c.State, c.Detail = Unmeasured, obs.BranchReason
	case obs.Empty:
		// A golden built with no source database has production's schema and
		// none of its rows, which is worth saying rather than counting as a
		// copy of production.
		c.State = Substituted
		c.Detail = fmt.Sprintf(
			"%s, and no source database is configured, so this is production's schema with none of its rows",
			describeSize(obs))
	case obs.Subset:
		c.State = Substituted
		c.Detail = fmt.Sprintf(
			"%s, taken as a production shaped slice, so the row counts are not production's",
			describeSize(obs))
	default:
		c.State = Reproduced
		c.Detail = describeSize(obs) + ", branched from " + orUnknown(obs.Golden)
	}
	return c
}

// describeSize renders what the branch holds.
//
// "at least" when a live count stopped at its ceiling, because a floor
// presented as a total is a number somebody would quote.
func describeSize(obs Observation) string {
	count := plural(obs.Rows, "row", "rows")
	if obs.RowsAreAFloor {
		count = "at least " + count
	}
	return fmt.Sprintf("%s over %s", plural(int64(obs.Tables), "table", "tables"), count)
}

// provenanceComponent answers whether the branch can be shown to have come
// from a golden that was masked and verified.
//
// A separate component from the data because it is a separate question and a
// reviewer asks it separately. A branch full of production's shape whose
// provenance nothing can check is not the same result as one whose attestation
// verifies, and a single verdict over both would hide whichever failed.
func provenanceComponent(obs Observation) Component {
	c := Component{Name: "provenance"}
	switch {
	case obs.GoldenReason != "":
		// The provider could not say where the branch came from, which is a
		// gap in what can be seen rather than a fact about the branch.
		c.State, c.Detail = Unmeasured, obs.GoldenReason
	case !obs.Attested:
		// The provider was asked and answered, and the answer does not add up
		// to a golden that was masked and read back. That is a fact about the
		// environment, so it is an absence rather than an unknown.
		c.State = Absent
		c.Detail = orUnknown(obs.Attestation)
	default:
		c.State = Reproduced
		c.Detail = "golden " + obs.Golden + ", " + obs.Attestation
	}
	return c
}

func thirdParty(obs Observation) Dimension {
	d := Dimension{Name: schema.FidelityThirdParty}
	if len(obs.Hosts) == 0 {
		mode := schema.ModeBlock
		if obs.Manifest.Egress != nil && obs.Manifest.Egress.Default != "" {
			mode = obs.Manifest.Egress.Default
		}
		// Not scored as a failure and not as a pass. The environment reaches
		// nothing by default, and nothing in the repository says which hosts
		// production reaches, so there is no inventory to take.
		d.NotApplicable = "the manifest names no third party hosts, and everything else is in " +
			string(mode) + " mode"
		return d
	}

	hosts := append([]Host(nil), obs.Hosts...)
	sort.Slice(hosts, func(i, j int) bool { return hosts[i].Name < hosts[j].Name })
	for _, h := range hosts {
		d.Components = append(d.Components, hostComponent(h))
	}
	return d
}

func hostComponent(h Host) Component {
	c := Component{Name: h.Name}
	switch h.Mode {
	case schema.ModeAllow:
		c.State, c.Detail = Reproduced, "reached for real"
	case schema.ModeSandbox:
		c.State = Substituted
		c.Detail = "the provider's own sandbox, with test credentials substituted at the sidecar"
	case schema.ModeCapture:
		c.State = Substituted
		c.Detail = "recorded into the inbox and answered with the provider's documented success shape"
	case schema.ModeMock:
		switch {
		case h.PackReason != "":
			c.State, c.Detail = Unmeasured, h.PackReason
		case h.Pack == "":
			c.State = Absent
			c.Detail = "in mock mode and no pack answers for it, so every request to it is refused with a 404"
		case h.Stateful:
			c.State = Substituted
			c.Detail = "answered offline by the " + h.Pack +
				" pack, which keeps what was created, so a read after a write returns it"
		default:
			c.State = Substituted
			c.Detail = "answered offline by the " + h.Pack +
				" pack, which keeps no state, so a read after a write returns nothing"
		}
	case schema.ModeSynth:
		// Unmeasured rather than substituted, and this is the product's own
		// position rather than a judgement made here: a synthesized response
		// marks everything that touched it unverified rather than passed, so
		// counting it as a reproduction would contradict the verdict.
		c.State = Unmeasured
		c.Detail = "a model invents the response, and anything that touched it is marked unverified rather than passed"
	default:
		c.State = Refused
		c.Detail = "blocked by the policy, so nothing stands in for it"
	}
	return c
}

func auth(obs Observation) Dimension {
	d := Dimension{Name: schema.FidelityAuth}
	if len(obs.Manifest.Personas) == 0 {
		d.NotApplicable = "the manifest declares no personas"
		return d
	}
	if obs.PersonasReason != "" {
		for _, p := range obs.Manifest.Personas {
			d.Components = append(d.Components, Component{
				Name: p.Name, State: Unmeasured, Detail: obs.PersonasReason,
			})
		}
		return d
	}
	for _, p := range obs.Personas {
		d.Components = append(d.Components, personaComponent(p))
	}
	return d
}

func personaComponent(p Persona) Component {
	c := Component{Name: p.Name}
	switch {
	case p.Reason != "":
		c.State, c.Detail = Unmeasured, p.Reason
	case !p.Present:
		c.State = Absent
		c.Detail = "no account with this address exists in " + p.Table
	case needsDelivery(p.Login) && !p.Deliverable:
		// The account exists and cannot be signed in as, which is a different
		// failure from a missing account and reads differently to whoever has
		// to fix it.
		c.State = Absent
		c.Detail = "the account exists in " + p.Table + " and its " + string(p.Login) +
			" cannot arrive: no rule captures messages"
	case (p.MFA || p.Login == schema.LoginTOTP) && !p.Factors:
		c.State = Absent
		c.Detail = "the account exists in " + p.Table +
			" and there is no table to enrol a second factor in"
	default:
		c.State = Reproduced
		c.Detail = "signs in with " + loginWord(p.Login) + ", in " + p.Table
	}
	return c
}

// needsDelivery reports whether a strategy waits for a message to arrive.
func needsDelivery(s schema.LoginStrategy) bool {
	switch s {
	case schema.LoginMagicLink, schema.LoginEmailCode, schema.LoginSMSCode:
		return true
	default:
		return false
	}
}

func loginWord(s schema.LoginStrategy) string {
	if s == "" {
		return string(schema.LoginPassword)
	}
	return strings.ReplaceAll(string(s), "_", " ")
}

func runtime(obs Observation) Dimension {
	// Reported and never scored. The manifest says where the copy runs and
	// says nothing at all about where production runs, so there is no
	// comparison to make, and scoring it would mean inventing the other side
	// of it. Naming that is the honest result; averaging a made up value into
	// a percentage is the thing this package exists to refuse.
	where := obs.Runtime
	if where == "" {
		where = "unknown"
	}
	return Dimension{
		Name: schema.FidelityRuntime,
		NotApplicable: "the environment runs on " + where +
			", and nothing in the manifest says what production runs on, so there is nothing to compare it against",
	}
}

func traffic(obs Observation) Dimension {
	d := Dimension{Name: schema.FidelityTraffic}
	l := obs.Manifest.Load
	if l == nil || !l.Enabled {
		d.NotApplicable = "the manifest does not ask for traffic, so there is none to reproduce"
		return d
	}
	c := Component{Name: "endpoint mix"}
	switch {
	case obs.TrafficReason != "":
		c.State, c.Detail = Absent, obs.TrafficReason
	// Keyed on whether a shape was actually read, not on which source the
	// manifest named. This arm used to test l.Source == LoadAccessLog, which
	// was every connected source at the time it was written. OpenTelemetry
	// became a real source afterwards, and an otel run would have fallen to
	// the default arm below and been reported as the engine's own shape while
	// carrying production's routes and production's rate. A report that calls
	// real traffic a default is worse than one that says nothing.
	case obs.Traffic != "":
		c.State, c.Detail = Reproduced, obs.Traffic
	default:
		c.State = Absent
		c.Detail = "the traffic is the engine's own default shape, not production's"
	}
	d.Components = append(d.Components, c)
	return d
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func plural(n int64, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
