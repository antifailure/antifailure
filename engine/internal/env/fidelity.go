package env

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/antifailure/antifailure/engine/internal/crossstore"
	"github.com/antifailure/antifailure/engine/internal/fidelity"
	"github.com/antifailure/antifailure/engine/internal/mockpack"
	"github.com/antifailure/antifailure/engine/internal/personas"
	"github.com/antifailure/antifailure/engine/internal/verify"
	"github.com/antifailure/antifailure/engine/internal/volume"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Taking the inventory: asking every part of the engine what it already knows
// about this environment, and writing down what none of them could answer.
//
// Nothing in this file measures anything new. The runtime is asked what is
// running, the database provider is asked which golden the branch came from,
// the branch is asked how much it holds and whether the personas exist in it,
// and the manifest is read for the hosts the policy names. Every one of those
// is a question something here already answers for another command. What is
// new is that the answers are collected in one place and the ones that could
// not be given are written down as such rather than left out.
//
// The failures are deliberately not fatal. An inventory that refuses to report
// anything because the runtime is unreachable tells the reader less than one
// that reports the database and says the runtime could not be asked, and the
// second is also the only one that is honest about which is which.

// Fidelity takes the inventory of what this environment reproduces.
func (o *Orchestrator) Fidelity(ctx context.Context) (fidelity.Inventory, error) {
	// An observation, so no lock. The runtime is opened by observeRuntime on
	// its own, and the database provider is all this session is for.
	s, err := o.openReading(ctx)
	if err != nil {
		return fidelity.Inventory{}, err
	}
	defer s.close()

	obs := fidelity.Observation{EnvID: o.envID, Manifest: o.opts.Manifest}
	o.observeRuntime(ctx, &obs)
	o.observeDatabase(ctx, s, &obs)
	o.observeDatastores(ctx, s, &obs)
	o.observeHosts(&obs)
	o.observeTraffic(&obs)
	o.observeCrossStore(ctx, s, &obs)
	return fidelity.Build(obs), nil
}

// observeCrossStore asks whether one identity masks to one person across every
// store, which is the question a twin with two stores has and a twin with one
// does not.
//
// Attempted only when a second datastore names a source_url_env, because
// reading a schema means opening a connection and a report must not start
// dialling anything a manifest did not point it at. When none does, the reason
// says so and the component is unmeasured, which keeps it out of the score in
// both directions: nothing here has shown the stores agree and nothing here
// has shown they do not.
//
// Its failure is not fatal, in keeping with every other observation in this
// file. A report that refuses to say anything because one store was
// unreachable tells the reader less than one that reports the rest and names
// what it could not ask.
func (o *Orchestrator) observeCrossStore(ctx context.Context, s *session, obs *fidelity.Observation) {
	if o.opts.Manifest == nil {
		obs.CrossStoreReason = "there is no manifest, so nothing here knows which stores to compare"
		return
	}
	withSource := 0
	for _, ds := range o.opts.Manifest.Datastores {
		if ds.SourceURLEnv != "" {
			withSource++
		}
	}
	if withSource < 2 {
		obs.CrossStoreReason = "fewer than two datastores name a source_url_env, so nothing " +
			"here could read a second schema; give each store the name of the variable " +
			"holding its connection string and check it with af mask crossstore"
		return
	}

	res, err := o.crossStoreCheck(ctx, s)
	if err != nil {
		obs.CrossStoreReason = "the cross store check could not run: " + oneLine(err)
		return
	}
	if len(res.Report.Read) < 2 {
		// One store read is not a comparison. Reported as the reason rather
		// than as a report with a zero in it, because a percentage over one
		// store is a number about nothing.
		obs.CrossStoreReason = strings.TrimSpace(strings.ReplaceAll(res.Report.Summary(), "\n", " "))
		return
	}
	obs.CrossStore = &fidelity.CrossStore{
		Stores:    res.Report.Read,
		Checked:   res.Report.Cross.Checked,
		Identical: res.Report.Cross.Identical,
		Detail:    crossStoreDetail(res.Report),
	}
}

// crossStoreDetail names the first pair that disagreed, and is empty when none
// did.
func crossStoreDetail(r crossstore.Report) string {
	for _, p := range r.Cross.Mismatches() {
		return p.A.String() + " and " + p.B.String() + ": " + p.Reason
	}
	return ""
}

// observeRuntime asks the runtime what is running.
func (o *Orchestrator) observeRuntime(ctx context.Context, obs *fidelity.Observation) {
	rt, err := o.newRuntime(ctx)
	if err != nil {
		obs.Runtime = "a runtime that could not be built"
		obs.ServicesReason = "the runtime could not be reached: " + oneLine(err)
		return
	}
	defer func() { _ = rt.Close() }()

	obs.Runtime = describeRuntime(rt.Name(), rt.Capabilities().Ingress)
	env, err := rt.Status(ctx, o.envID)
	if err != nil {
		obs.ServicesReason = "the runtime could not say what is running: " + oneLine(err)
		return
	}
	if len(env.Services) == 0 {
		// A stopped environment is not an environment that reproduces nothing.
		// Reporting every service absent here would put a real zero in a real
		// denominator for a run that never happened.
		obs.ServicesReason = "nothing is running for this environment; bring it up with af up first"
		return
	}
	obs.Running = env.Services
}

func describeRuntime(name string, ingress bool) string {
	if ingress {
		return "the " + name + " runtime, which publishes an address this machine can reach"
	}
	return "the " + name + " runtime, which publishes no address this machine can reach"
}

// observeDatabase asks the provider and the branch about the data.
func (o *Orchestrator) observeDatabase(
	ctx context.Context, s *session, obs *fidelity.Observation,
) {
	if o.opts.Manifest.Database != nil {
		_, obs.Subset = o.subsetConfig()
		// A source that cannot be looked up is reported as no source rather
		// than failing the whole report, which is what this command is for:
		// saying what an environment reproduces, including the parts it does
		// not know about.
		source, _ := o.sourceURL(ctx)
		obs.Empty = source.IsZero()
	}

	version, why, err := o.branchGolden(ctx, s)
	switch {
	case err != nil:
		obs.GoldenReason = "the provider could not be asked which golden this branch came from: " +
			oneLine(err)
	case version == "":
		// The provider's own reason, which is a fact about the branch rather
		// than about what any one caller wanted it for.
		obs.GoldenReason = why
	default:
		obs.Golden = version
		o.observeAttestation(ctx, s, version, obs)
	}

	conn, err := connectSession(ctx, o, s)
	if err != nil {
		obs.BranchReason = "the branch could not be read: " + oneLine(err)
		obs.PersonasReason = obs.BranchReason
		return
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	if size, err := branchSize(ctx, conn); err != nil {
		obs.BranchReason = "the branch could not be counted: " + oneLine(err)
	} else {
		obs.Tables, obs.Rows, obs.RowsAreAFloor = size.tables, size.rows, size.atLeast
		obs.Branch = size.perTable
	}
	o.observeVolume(obs)
	o.observePersonas(ctx, conn, obs)
}

// observeAttestation reads back the golden's signed statement.
//
// The signature is checked here rather than trusted, which is the only reason
// the attestation is worth reporting at all: a claim the producing process can
// also forge says nothing to the reviewer reading it.
func (o *Orchestrator) observeAttestation(
	ctx context.Context, s *session, version string, obs *fidelity.Observation,
) {
	goldens, err := s.dbProv.ListGoldens(ctx)
	if err != nil {
		obs.GoldenReason = "the provider could not list its goldens: " + oneLine(err)
		return
	}
	obs.Attested, obs.Attestation = attestationOf(goldens, version)
}

// attestationOf reads back one golden's signed statement and says what it
// covered, or why it cannot be trusted.
//
// One function for the primary database and for every declared datastore,
// rather than the same six checks written twice. This is the product's central
// guarantee arriving in a report, and a second copy of it is a second thing to
// keep in step: the copy that forgets to call Verify still prints a confident
// sentence about a golden somebody edited after it was signed.
func attestationOf(goldens []provider.GoldenVersion, version string) (attested bool, detail string) {
	for _, g := range goldens {
		if g.ID != version {
			continue
		}
		if !g.Verified {
			// Absent rather than unknown, which is the reason this asks the
			// provider itself rather than going through the rehearsal's
			// lookup. A golden that lost its verification is the one case
			// somebody has to act on, and an unknown is the one result nobody
			// acts on.
			return false, "golden " + version + " is no longer marked verified"
		}
		if g.Attestation == "" {
			return false, "golden " + version + " carries no attestation, so nothing here can " +
				"check that its data was masked and read back"
		}
		var a verify.Attestation
		if err := json.Unmarshal([]byte(g.Attestation), &a); err != nil {
			return false, "the attestation on golden " + version + " could not be read: " + oneLine(err)
		}
		if !a.Verify() {
			return false, "the attestation on golden " + version +
				" does not match its own signature, so it was changed after it was signed"
		}
		out := fmt.Sprintf(
			"%d columns read back over %d rows sampled, signed and still matching its signature",
			a.Report.Columns, a.Report.RowsSampled)
		if n := len(a.Report.Skipped); n > 0 {
			out += fmt.Sprintf(", with %d columns the scan could not read", n)
		}
		return true, out
	}
	// Absent rather than unknown for the same reason: the provider was asked
	// and answered, and the answer is that the golden this branch came from is
	// gone, so nothing can check what was done to the data in it.
	return false, "golden " + version + " is the one this branch came from and the provider " +
		"no longer lists it"
}

// observeDatastores asks each store the manifest declares golden what its
// branch holds.
//
// The second store, arriving in the report. `af golden refresh` makes a golden
// of every declared store and `af up` branches each one, and until this the
// datastores dimension was built from the manifest alone: it read the stance,
// saw golden, and reported absent whether or not the environment held a masked
// copy. So an environment that genuinely held a masked, verified, branched
// ClickHouse was reported as not holding one. That understates rather than
// overstates, which is the better of the two directions and is still an
// instrument saying something untrue about what it can see.
//
// Only the golden stance is asked about, and that is deliberate rather than
// unfinished. Nothing here starts an empty store, runs a derived rebuild or
// creates a topic, so a store declared any of those three stays unmeasured:
// reporting one reproduced because somebody declared it empty would be the
// report believing a manifest instead of an environment.
func (o *Orchestrator) observeDatastores(
	ctx context.Context, s *session, obs *fidelity.Observation,
) {
	golden := make([]schema.Datastore, 0, len(o.opts.Manifest.Datastores))
	for _, ds := range datastoreDeclarations(o.opts.Manifest) {
		if ds.Stance == schema.StanceGolden {
			golden = append(golden, ds)
		}
	}
	if len(golden) == 0 {
		return
	}
	// mustExist, so that taking an inventory never STARTS a datastore. Opening
	// a ClickHouse provider brings the machine's server up when it is not
	// running, and a read only command that waits a minute on a container it
	// then asks nothing is the behaviour openDatastores' own comment warns
	// about. A store whose server is not there is one this environment does
	// not hold, which is what the dimension already reports.
	if err := o.openDatastores(ctx, s, true); err != nil {
		reason := "the datastore providers could not be opened: " + oneLine(err)
		for _, ds := range golden {
			obs.Stores = append(obs.Stores, fidelity.Store{
				Name: ds.Name, GoldenReason: reason, BranchReason: reason,
			})
		}
		return
	}
	for _, h := range s.stores {
		if h.decl.Stance != schema.StanceGolden {
			continue
		}
		if store, branched := o.observeDatastore(ctx, h); branched {
			obs.Stores = append(obs.Stores, store)
		}
	}
}

// observeDatastore asks one store's provider about this environment's branch,
// and reports false when there is no branch to ask about.
//
// False rather than a Store carrying nothing, because the two mean different
// things to the dimension and only one of them is this environment's answer. A
// store nothing branched is ABSENT with the four missing facts named, which is
// the sentence the manifest earns by declaring golden; a store that WAS
// branched is reported from what the branch holds. Returning an empty Store
// here would turn the first case into a report of a branch with no golden and
// no rows, which reads as a broken twin rather than as an absent one.
func (o *Orchestrator) observeDatastore(
	ctx context.Context, h *datastoreHandle,
) (fidelity.Store, bool) {
	store := fidelity.Store{Name: h.decl.Name}
	inventory, err := h.prov.Inventory(ctx)
	if err != nil {
		reason := "the " + h.decl.Name + " datastore could not be asked what it holds: " + oneLine(err)
		store.GoldenReason, store.BranchReason = reason, reason
		return store, true
	}

	var branch provider.Resource
	found := false
	for _, r := range inventory {
		if r.EnvID == o.envID {
			branch, found = r, true
			break
		}
	}
	if !found {
		return fidelity.Store{}, false
	}

	store.Golden = branch.Labels["golden"]
	if store.Golden == "" {
		store.GoldenReason = "this provider does not record which golden the " + h.decl.Name +
			" datastore's branch came from"
	} else {
		goldens, listErr := h.prov.ListGoldens(ctx)
		if listErr != nil {
			store.GoldenReason = "the " + h.decl.Name + " datastore could not list its goldens: " +
				oneLine(listErr)
		} else {
			store.Attested, store.Attestation = attestationOf(goldens, store.Golden)
		}
	}

	tables, rows, ok := branchContents(branch.Labels)
	if !ok {
		store.BranchReason = "this provider does not record what the " + h.decl.Name +
			" datastore's branch holds"
		return store, true
	}
	store.Tables, store.Rows = tables, rows
	return store, true
}

// branchContents reads what a provider says its branch holds.
//
// Both labels or neither. A count of tables with no count of rows would render
// as "12 tables over 0 rows", which is a store somebody would read as empty,
// and a report that turns a missing number into a zero is the failure this
// package exists to refuse. A provider that records neither gets the sentence
// saying so, in the exclusions list, rather than a figure.
func branchContents(labels map[string]string) (tables int, rows int64, ok bool) {
	t, hasTables := labels["tables"]
	r, hasRows := labels["rows"]
	if !hasTables || !hasRows {
		return 0, 0, false
	}
	tables, err := strconv.Atoi(t)
	if err != nil {
		return 0, 0, false
	}
	rows, err = strconv.ParseInt(r, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return tables, rows, true
}

// branchSize counts what the branch holds.
//
// The planner's own estimate, with a bounded live count for a table it has
// never analyzed, which is the same thing internal/subset does for the same
// reason: reading every row of a production sized database to print one number
// in a report is minutes of work for a figure an estimate gives in
// milliseconds. A table the planner has never seen reports minus one, which
// would render as a negative row count, so those are counted rather than
// reported.
//
// The count is a floor rather than a total, and the report says "at least" when
// any table reached the ceiling. A number somebody is going to read as
// production's row count must not be a number that quietly stopped early.
func branchSize(ctx context.Context, conn *pgx.Conn) (branchCount, error) {
	const query = `
SELECT n.nspname, c.relname, c.reltuples::bigint
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind = 'r'
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND n.nspname NOT LIKE 'pg_toast%'`

	var out branchCount
	result, err := conn.Query(ctx, query)
	if err != nil {
		return branchCount{}, err
	}
	// Qualified, because two schemas may hold a table of the same name and an
	// unqualified count would read whichever the search path found, twice.
	type pending struct{ qualified, name string }
	var unanalyzed []pending
	for result.Next() {
		var schemaName, name string
		var estimate int64
		if err := result.Scan(&schemaName, &name, &estimate); err != nil {
			result.Close()
			return branchCount{}, err
		}
		out.tables++
		if estimate < 0 {
			unanalyzed = append(unanalyzed, pending{
				qualified: pgx.Identifier{schemaName, name}.Sanitize(),
				name:      schemaName + "." + name,
			})
			continue
		}
		out.rows += estimate
		out.perTable = append(out.perTable,
			volume.TableRows{Name: schemaName + "." + name, Rows: estimate})
	}
	result.Close()
	if err := result.Err(); err != nil {
		return branchCount{}, err
	}
	sort.Slice(unanalyzed, func(i, j int) bool { return unanalyzed[i].name < unanalyzed[j].name })

	for _, u := range unanalyzed {
		var n int64
		q := fmt.Sprintf("SELECT count(*) FROM (SELECT 1 FROM %s LIMIT %d) s",
			u.qualified, countCeiling)
		if err := conn.QueryRow(ctx, q).Scan(&n); err != nil {
			// One table that will not answer must not discard the count of
			// every other table. It is reported as a floor either way, and it
			// is left out of the per table list rather than entered as zero:
			// a table nobody could count is not a table holding nothing, and
			// the volume comparison would read a zero as production data this
			// branch does not have.
			out.atLeast = true
			continue
		}
		if n == countCeiling {
			out.atLeast = true
		}
		out.rows += n
		out.perTable = append(out.perTable, volume.TableRows{Name: u.name, Rows: n})
	}
	sort.Slice(out.perTable, func(i, j int) bool { return out.perTable[i].Name < out.perTable[j].Name })
	return out, nil
}

// branchCount is what the branch holds, in total and per table.
//
// The per table list is the copy's side of the volume comparison, and it is
// produced by the same walk as the total so the two can never disagree. A
// second query for the same numbers is a second answer waiting to differ from
// the first, which is how a report ends up quoting a total that no row in its
// own table adds up to.
type branchCount struct {
	tables   int
	rows     int64
	atLeast  bool
	perTable []volume.TableRows
}

// observeVolume reads the committed profile of what production holds.
func (o *Orchestrator) observeVolume(obs *fidelity.Observation) {
	profile, why := o.volumeProfile()
	obs.Volume, obs.VolumeReason = profile, why
}

// countCeiling is where a live count of an unanalyzed table stops.
//
// The same figure internal/subset uses, for the same reason: the alternative is
// a report that takes minutes on a database nobody has run ANALYZE against yet,
// which is every freshly restored golden.
const countCeiling = 200000

// observePersonas asks the branch whether each declared account exists.
//
// The row rather than the manifest, because the manifest is an intention and a
// row is a fact. This is the difference between reporting that a project
// declares an administrator and reporting that an agent can sign in as one.
func (o *Orchestrator) observePersonas(
	ctx context.Context, conn *pgx.Conn, obs *fidelity.Observation,
) {
	list := o.opts.Manifest.Personas
	if len(list) == 0 {
		return
	}

	auth := o.opts.Manifest.Auth
	if auth == nil {
		auth = &schema.Auth{Adapter: schema.AuthAuto}
	}
	switch auth.Adapter {
	case schema.AuthSeed:
		obs.PersonasReason = "the seed adapter runs a command of the project's own, so this cannot " +
			"say where the accounts landed"
		return
	case schema.AuthSupabaseAPI, schema.AuthClerk, schema.AuthAuth0, schema.AuthWorkOS:
		obs.PersonasReason = "the " + string(auth.Adapter) + " adapter creates accounts through the " +
			"provider's own API, which this cannot read without calling it"
		return
	}

	scheme, err := o.personaScheme(ctx, conn, auth)
	if err != nil {
		obs.PersonasReason = "the accounts could not be looked for: " + oneLine(err)
		return
	}

	deliverable := capturesMessages(o.opts.Manifest.Egress)
	table := scheme.Users.Schema
	if table == "" {
		table = "public"
	}
	table += "." + scheme.Users.Name

	for _, p := range list {
		found := fidelity.Persona{
			Name: p.Name, Login: p.Login, MFA: p.MFA, Table: table,
			Factors: scheme.Factors != nil, Deliverable: deliverable,
		}
		present, err := accountExists(ctx, conn, scheme, p.Email)
		if err != nil {
			found.Reason = "the account could not be looked for in " + table + ": " + oneLine(err)
		} else {
			found.Present = present
		}
		obs.Personas = append(obs.Personas, found)
	}
}

// accountExists asks whether an address already has a row.
//
// Matched case insensitively on the address, which is what the SQL adapter
// matches on when it decides whether to reconcile or insert. Asking a
// different question here would report an account missing that provisioning
// would have found.
func accountExists(
	ctx context.Context, conn *pgx.Conn, scheme personas.Scheme, email string,
) (bool, error) {
	t := scheme.Users
	name := pgx.Identifier{t.Name}.Sanitize()
	if t.Schema != "" {
		name = pgx.Identifier{t.Schema, t.Name}.Sanitize()
	}
	q := fmt.Sprintf("SELECT EXISTS (SELECT 1 FROM %s WHERE lower(%s) = lower($1))",
		name, pgx.Identifier{t.Email}.Sanitize())

	var exists bool
	if err := conn.QueryRow(ctx, q, email).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

// capturesMessages reports whether anything in the policy records a message
// instead of sending it, which is what a magic link or a one time code needs
// in order to arrive anywhere an agent can read it.
func capturesMessages(e *schema.Egress) bool {
	if e == nil {
		return false
	}
	if e.Default == schema.ModeCapture {
		return true
	}
	for _, r := range e.Rules {
		if r.Mode == schema.ModeCapture {
			return true
		}
	}
	return false
}

// observeHosts reads the third party hosts the policy names, and which pack
// answers for each of the ones in mock mode.
func (o *Orchestrator) observeHosts(obs *fidelity.Observation) {
	if o.opts.Manifest.Egress == nil {
		return
	}

	packs, packErr := mockpack.Builtin()
	if extra, err := o.mockPacks(); err != nil {
		packErr = err
	} else {
		for _, body := range extra {
			p, parseErr := mockpack.Parse([]byte(body))
			if parseErr != nil {
				packErr = parseErr
				break
			}
			packs = append(packs, p)
		}
	}
	engine := mockpack.New(packs)

	for _, r := range o.opts.Manifest.Egress.Rules {
		h := fidelity.Host{Name: r.Host, Mode: r.Mode}
		if r.Mode == schema.ModeMock {
			switch {
			case packErr != nil:
				h.PackReason = "the packs could not be read, so which one answers for this host " +
					"is not known: " + oneLine(packErr)
			case strings.HasPrefix(r.Host, "*"):
				// A wildcard rule covers hosts nobody has enumerated, and a
				// pack answers for the ones it names. Reporting the first pack
				// that happens to fall inside the pattern would claim cover
				// for every other host the pattern also matches.
				h.PackReason = "the rule matches a pattern rather than one host, so which pack " +
					"answers depends on the host the application reaches"
			default:
				if p, ok := engine.PackFor(r.Host); ok {
					h.Pack, h.Stateful = p.Name, p.Stateful()
				}
			}
		}
		obs.Hosts = append(obs.Hosts, h)
	}
}

// observeTraffic asks where the endpoint mix would come from.
//
// Through the same function the load run uses, so the inventory cannot report
// a source the run would refuse. Reading the access log twice costs one file
// read and is worth more than a second implementation that could disagree.
func (o *Orchestrator) observeTraffic(obs *fidelity.Observation) {
	l := o.opts.Manifest.Load
	if l == nil || !l.Enabled {
		return
	}
	shape, err := o.trafficShape()
	if err != nil {
		obs.TrafficReason = oneLine(err)
		return
	}
	// Shape.Source rather than l.Source, because they answer different
	// questions: one is what the manifest asked for, the other is what the
	// read produced. trafficShape falls back to the engine's default whenever
	// the source is none or absent, and keying on the manifest would report
	// that fallback as production's traffic. It also means a source connected
	// later is covered here without this line being remembered, which is the
	// gap that made an otel run report the default shape.
	if shape.Source != "" && shape.Source != "default" {
		obs.Traffic = fmt.Sprintf("%d routes read from %s, at %.0f requests a second",
			len(shape.Routes), l.SourceConfig["path"], shape.RequestsPerSecond)
	}
}

// oneLine keeps a reason on one line of a report.
//
// Cut on a rune rather than on a byte. An error carrying a path with an accent
// in it truncated mid rune is invalid UTF-8, which JSON encoding silently
// replaces with a question mark, and the reason a component could not be
// measured is the last string in this package that should arrive corrupted.
func oneLine(err error) string {
	text := strings.TrimSpace(strings.ReplaceAll(err.Error(), "\n", " "))
	const max = 160
	if utf8.RuneCountInString(text) <= max {
		return text
	}
	cut := 0
	for i := range text {
		cut++
		if cut > max-1 {
			return text[:i] + "…"
		}
	}
	return text
}
