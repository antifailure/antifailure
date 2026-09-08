package fidelity

import (
	"fmt"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/traffic"
	"github.com/antifailure/antifailure/engine/internal/volume"
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
	// Branch is what each table in the branch holds, which is the copy's side
	// of the volume comparison. Volume is production's side, read from the
	// committed profile, and VolumeReason says why there is none.
	//
	// A nil Volume with a reason is the case this exists for. The branch's own
	// row count was always readable and was always reported as a
	// reproduction; what was never available was the number to divide it by.
	Branch       []volume.TableRows
	Volume       *volume.Profile
	VolumeReason string
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
	// Sent is every route a load run would actually send at this environment,
	// which is the shape after the safe list has refused what it refuses. Not
	// the shape and not the safe list: a route the shape carries and the safe
	// list refuses is never sent, and a pattern in the safe list that no
	// source produces sends nothing.
	Sent []traffic.Endpoint
	// SentRate is how many requests a second the run would send, which is the
	// shape's rate times the manifest's scale.
	SentRate float64
	// TrafficProfile is what production actually served, read from the
	// committed profile, and TrafficProfileReason says why there is none.
	//
	// A nil profile with a reason is the case this exists for. Which routes a
	// run sends was always readable and was always reported as a reproduction
	// of production's traffic; what was never available was the list to
	// compare it against.
	TrafficProfile       *traffic.Profile
	TrafficProfileReason string

	// Stores describes each declared datastore this environment BRANCHED, as
	// that store's own provider answered for it.
	//
	// A store nothing branched is not in here at all, and the absence is load
	// bearing: it is what leaves the datastores dimension reporting a store
	// declared golden as absent, which is the answer for an environment that
	// has none. Only a store the environment actually holds appears, and only
	// then does the dimension report what is in it.
	Stores []Store

	// CrossStore is what the cross store masking check found, and
	// CrossStoreReason says why it could not be run. Nil with an empty reason
	// means nothing asked, which is what an environment with one store is.
	CrossStore       *CrossStore
	CrossStoreReason string
}

// CrossStore is the answer to the one question a twin with two stores has that
// a twin with one does not: is the person masked in the first store masked into
// the SAME person in the second.
//
// It is here because it was nowhere. The check existed, was well tested, and
// had zero production callers, so the clause a customer was told about their
// own environment was the one clause of seven with no command behind it.
type CrossStore struct {
	// Stores are the stores that were compared.
	Stores []string
	// Checked and Identical are the denominator and the numerator.
	Checked   int
	Identical int
	// Detail is the check's own sentence, which names the first pair that
	// disagreed when one did.
	Detail string
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

// Store is one declared datastore's branch, as its provider answered for it.
//
// The same two questions the database dimension asks about the primary, asked
// of the second store: which golden this branch came from and whether that
// golden's attestation still checks out, and what the branch holds. A separate
// type rather than a second set of fields on Observation, because a datastore
// has no pooled endpoint, no subset, no personas and no source of traffic, and
// a struct carrying fields nothing ever fills is one a reader has to check
// against the code before believing.
type Store struct {
	// Name is the store's name in the manifest, which is what the manifest
	// declared and what its components are named after.
	Name string
	// Golden is the golden this branch came from, and GoldenReason says why
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
	// BranchReason says why they could not be read.
	//
	// There is no floor here, unlike the primary database's count. That one
	// stops a live count at a ceiling because counting every row of a
	// production sized Postgres to print one number would take minutes; a
	// store that reports what it holds from its own metadata answers exactly
	// or does not answer at all, and a provider that cannot answer sets
	// BranchReason rather than a number somebody would quote.
	Tables       int
	Rows         int64
	BranchReason string
}

// There is deliberately no Empty field here, unlike the primary database's.
// Whether a store declares a source is in its own manifest entry, the manifest
// is part of this observation, and the dimension reads it from there. A copy
// of a fact the report already holds is a second thing to keep in step, and
// this is the file whose whole claim is that the inventory is a pure function
// of what was observed.

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
			trafficDimension(obs),
			datastores(obs),
			topology(obs),
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
//
// The instance count is part of that sentence whenever the manifest asked for
// more than one, and it names both numbers. A twin whose manifest asks for
// three and whose runtime is running one is not the topology somebody
// declared, and the fidelity report is the one place whose whole job is to say
// so. This line DESCRIBES the difference and the topology dimension SCORES it,
// which is the separate piece of work this comment used to say was still
// outstanding. Both are kept: somebody reading the services table sees the
// count beside the service it belongs to, and the score changes in one place.
func describeService(s schema.Service, r provider.RunningService) string {
	kind := string(s.Kind)
	if kind == "" {
		kind = r.Kind
	}
	out := kind + ", running"
	if r.URL != "" {
		out = kind + " at " + r.URL
	}
	if s.Replicas > 1 {
		running := r.Instances
		if running < 1 {
			running = 1
		}
		out = fmt.Sprintf("%s, %d of %d instances", out, running, s.Replicas)
	}
	return out
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
//
// It used to answer that question without ever asking production. The default
// arm said Reproduced and described the branch, so a golden built from a
// staging database holding two hundred rows in events was reported as
// reproducing a production holding four billion, in the same words and with
// the same verdict as a full copy. Everything the arm printed was true and the
// verdict was not, because there was no denominator anywhere in the engine.
//
// So the branch's own size is now measured against the committed volume
// profile, and the three outcomes are kept separate on purpose:
//
//   - no profile, or one too old to quote, is UNMEASURED. Not a smaller pass.
//     Nothing has been shown about whether this branch reproduces production,
//     and the reason names how to find out.
//   - a branch holding materially less than production is SUBSTITUTED, with
//     the fraction, and with the sentence that stops somebody quoting a
//     timing taken against it as a prediction.
//   - a branch holding production's rows is REPRODUCED, and now says what it
//     was measured against rather than asserting it.
func dataComponent(obs Observation) Component {
	c := Component{Name: "data"}
	switch {
	case obs.BranchReason != "":
		// The branch could not be counted, so there is nothing to compare
		// against the profile either. One unknown, reported once.
		c.State, c.Detail = Unmeasured, obs.BranchReason
		return c
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
	return againstProduction(c, obs)
}

// againstProduction folds the volume profile into the data component.
//
// It never improves a verdict. A subset measured against production is still a
// subset and an empty golden is still empty; what the profile adds to those is
// the fraction, which is the thing somebody actually wanted when they asked
// how production shaped the slice was. The only verdict it changes is the
// default arm's, which is the one that was never checked against anything.
func againstProduction(c Component, obs Observation) Component {
	if obs.Volume == nil {
		reason := obs.VolumeReason
		if reason == "" {
			reason = "no volume profile says what production holds, so whether this branch " +
				"reproduces it is unknown. Declare one under database.volume and record it " +
				"with af volume record"
		}
		if c.State == Reproduced {
			// The whole defect, in one line. A branch nothing was compared
			// against has not been shown to reproduce anything, and calling
			// that a reproduction is what let two hundred rows stand in for
			// four billion.
			c.State = Unmeasured
		}
		c.Detail += ", and " + reason
		return c
	}

	cmp := volume.Compare(obs.Branch, *obs.Volume)
	c.Detail += fmt.Sprintf(". Measured against the volume profile collected on %s, %s",
		obs.Volume.CollectedAt.UTC().Format("2006-01-02"), cmp.Describe())
	if _, ok := cmp.Share(); !ok {
		// A profile that names nothing this branch also has answers no
		// question about it, and it is not evidence either way.
		if c.State == Reproduced {
			c.State = Unmeasured
		}
		return c
	}
	if !cmp.Reproduces() {
		if c.State == Reproduced {
			c.State = Substituted
		}
		c.Detail += ". A timing measured against this branch is a lower bound and not a prediction"
	}
	return c
}

// describeSize renders what the branch holds.
//
// "at least" when a live count stopped at its ceiling, because a floor
// presented as a total is a number somebody would quote.
//
// Separated, because production's side of the same sentence is separated and
// a line reading "184000 rows against production's 4,200,000,000 rows" makes
// the reader do the digit counting the separators exist to save them.
func describeSize(obs Observation) string {
	count := volume.Rows(obs.Rows)
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
		if strings.HasPrefix(h.Name, "*") {
			// A rule covering a domain rather than naming a host cannot be
			// classified from the rule, and saying either thing would be
			// wrong for half the hosts it matches.
			//
			// A LEADING star, which is the same predicate the mock branch
			// below uses and the same one the policy uses to decide
			// Decision.NamesHost. An interior star, as in
			// email.*.amazonaws.com, pins the service label and the label
			// count, so it can only ever reach one service, the policy counts
			// it as naming the host, and the sidecar captures it. Widening
			// this to every pattern would report those as unknown when they
			// are not.
			//
			// The sidecar captures such a request only when this build has a
			// handler for whatever host actually arrives, and refuses it with
			// a 403 otherwise: *.resend.com is captured because there is a
			// Resend handler, and *.zapier.com is refused because there is
			// not. This ran as Substituted with the sentence about the
			// provider's documented success shape, for both, so a delivery
			// path written as *.zapier.com read here as recorded into the
			// inbox and was refused at run time having recorded nothing.
			//
			// Unmeasured rather than a guess in either direction, which is
			// what the mock branch below already does with the same rule shape
			// through PackReason. It is excluded from the score and named,
			// rather than counted as an answer nobody checked.
			c.State = Unmeasured
			c.Detail = "a rule covering a domain rather than naming a host, so whether the " +
				"sidecar captures a request or refuses it depends on which host arrives and " +
				"whether this build has a handler for that provider, which the rule does not say"
			break
		}
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
