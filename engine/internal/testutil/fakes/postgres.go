package fakes

// A provider.Database with real storage behind it, so the five conformance
// behaviours that read rows back can be proved able to fail.
//
// [InMemoryDatabase] answers twenty of the twenty six and cannot answer
// these, and the reason is not laziness: isolation, reset, and what a branch
// actually holds are claims about bytes. A fake with no bytes can only agree
// with whatever it was told, which is the same thing as not checking. So this
// keeps each golden and each branch in its own database on a real Postgres,
// makes a branch with CREATE DATABASE ... TEMPLATE, and hands out a connection
// string that reaches it. Everything the suite asserts about isolation is then
// a property of Postgres rather than of this file.
//
// It needs a server. That is a real cost and it is the smaller one: CI sets
// AF_REQUIRE_DATABASE precisely so a suite cannot report ok having skipped
// itself, and this run is one of the things that setting exists to protect.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// PostgresBranchLimit is what the fake declares and enforces.
//
// Concurrency_RespectsTheDeclaredLimit creates exactly this many branches
// before asking for one more, and every one of them is a CREATE DATABASE, so a
// larger number buys nothing and costs seconds on every run.
const PostgresBranchLimit = 3

// PostgresOptions configure the fake.
type PostgresOptions struct {
	// AdminURL is a connection string for a database that may create others.
	AdminURL string
	// Prefix names every database this provider makes, and must be unique per
	// run. Providers sharing a prefix are the SAME provider: the conformance
	// suite builds a fresh one per behaviour and expects the inventory of one
	// to see what another created, which is exactly what a leak detector
	// needs and what an in-process fake cannot give.
	Prefix string
	// SeedSQL fills a golden candidate. The suite's DefaultSeedSQL is what
	// every behaviour that counts rows is counting.
	SeedSQL string
	// PublishUnverified records a version whose verification failed rather
	// than withholding it. See InMemoryDatabase.publishUnverified: it is an
	// affordance rather than a fault, and it is what makes the branch side of
	// Branch_RefusesAnUnverifiedGolden reachable at all.
	PublishUnverified bool
	// FlatBranch makes branching cost the same whatever the golden holds,
	// which is what a copy on write provider does and what Postgres cannot.
	//
	// It is an affordance rather than a fault, and it is the only way
	// CopyOnWrite_BranchTimeMatchesTheDeclaration can be proved from both
	// ends. That behaviour needs four providers: one that copies and admits
	// it, one that copies and denies it, one that is flat and admits it, and
	// one that is flat and denies it. Postgres gives the first two for
	// nothing, because CREATE DATABASE ... TEMPLATE is a file copy. It gives
	// the other two only if something makes a branch constant time, and no
	// amount of SQL will share storage between two databases.
	//
	// So the copy is made at REFRESH, several times, and Branch hands one out
	// with ALTER DATABASE ... RENAME, which is a catalogue update and costs
	// the same for a megabyte and a gibibyte. That is not a simulation of copy
	// on write, it is the same observable contract: the expensive work happens
	// once when the golden is built, a branch carries ALL of the golden's data
	// including whatever ballast the suite put in it, and branch time does not
	// grow with the data. A provider whose branches are ZFS clones or Aurora
	// fast clones is doing the same trick with better storage.
	//
	// The pool is finite. When it runs out a branch falls back to copying,
	// which is correct rather than flat, and is why the pool is sized from
	// what the timing behaviour asks for rather than left at one.
	FlatBranch bool
}

// PostgresDatabase is the provider. Use [NewPostgresDatabase].
type PostgresDatabase struct {
	opts  PostgresOptions
	fault Fault
	state *pgState
}

// pgState is the provider's bookkeeping, shared by every instance built with
// one prefix.
//
// Which facts live here and which live on the server is the load bearing
// decision. Existence is read from pg_database, always, because a leak
// detector that trusted a provider's own memory would report exactly the
// resources the provider remembered to tell it about. Metadata that Postgres
// has nowhere to keep, such as whether a version verified, lives here.
type pgState struct {
	mu       sync.Mutex
	goldens  map[string]provider.GoldenVersion
	branches map[string]provider.Branch // env id -> branch
	firstOf  map[string]string          // golden id -> first branch database
	// pool holds the copies FlatBranch made at refresh, per golden, waiting to
	// be renamed into a branch.
	pool map[string][]string
	// pooled counts every pool database ever made, so two of them never share
	// a name. A counter rather than the golden's identifier plus an index,
	// because a golden destroyed and remade would then collide with its own
	// leftovers.
	pooled int
}

var pgStates sync.Map // prefix -> *pgState

// NewPostgresDatabase returns a provider that keeps every guarantee, backed by
// databases on the server AdminURL names.
func NewPostgresDatabase(ctx context.Context, opts PostgresOptions) (*PostgresDatabase, error) {
	if opts.AdminURL == "" {
		return nil, fmt.Errorf("fakes: a Postgres backed provider needs an admin connection string")
	}
	if opts.Prefix == "" {
		return nil, fmt.Errorf("fakes: a Postgres backed provider needs a prefix unique to this run")
	}
	if !validIdentifier(opts.Prefix) {
		return nil, fmt.Errorf("fakes: %q is not usable as a database name prefix", opts.Prefix)
	}
	conn, err := pgx.Connect(ctx, opts.AdminURL)
	if err != nil {
		return nil, fmt.Errorf("fakes: connect to the Postgres this provider creates databases on: %w", err)
	}
	if err := conn.Close(ctx); err != nil {
		return nil, fmt.Errorf("fakes: close the probe connection: %w", err)
	}
	st, _ := pgStates.LoadOrStore(opts.Prefix, &pgState{
		goldens:  map[string]provider.GoldenVersion{},
		branches: map[string]provider.Branch{},
		firstOf:  map[string]string{},
		pool:     map[string][]string{},
	})
	return &PostgresDatabase{opts: opts, state: st.(*pgState)}, nil
}

// NewPrefix returns a database name prefix no other run will use.
//
// Random rather than derived, for the reason harness.rulesHash in the
// conformance suite records at length: every derivation tried so far collided
// along an axis nobody thought about, and the shared test cluster carries
// every branch's work at once.
func NewPrefix() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("fakes: no randomness for a database prefix: %w", err)
	}
	return "afcf" + hex.EncodeToString(b[:]), nil
}

func validIdentifier(s string) bool {
	if s == "" || len(s) > 24 {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9' && i > 0:
		case r == '_' && i > 0:
		default:
			return false
		}
	}
	return true
}

func (d *PostgresDatabase) Name() string { return "postgres-template" }

// Capabilities declares what this provider does, and its copy on write answer
// is derived from how it actually branches rather than written down beside it.
//
// That is the point of the two faults below. Honest is the default in both
// modes: a copying provider declares false, a FlatBranch one declares true.
// Each fault flips exactly that one bit and changes nothing else, so a red in
// CopyOnWrite_BranchTimeMatchesTheDeclaration is attributable to the
// declaration disagreeing with the stopwatch and to nothing else.
func (d *PostgresDatabase) Capabilities() provider.Caps {
	cow := d.opts.FlatBranch
	switch {
	case d.is(CopyOnWriteThatCopies):
		cow = true // and every branch is still a full CREATE DATABASE ... TEMPLATE
	case d.is(CopyOnWriteUnderstated):
		cow = false // and every branch is still a constant time rename
	}
	return provider.Caps{
		Branching:             true,
		Reset:                 true,
		CopyOnWrite:           cow,
		PooledEndpoints:       true,
		MaxConcurrentBranches: PostgresBranchLimit,
		ExpectedBranchLatency: 30 * time.Second,
		SupportedVersions:     []int{13, 14, 15, 16, 17},
	}
}

func (d *PostgresDatabase) injectFault(f Fault) bool {
	switch f {
	case BranchLosesTheGoldensRows, BranchSharesTheGoldensStorage,
		BranchesShareOneDatabase, RefreshRebuildsExistingBranches:
		d.fault = f
		return true
	case CopyOnWriteThatCopies:
		// Refused on a flat provider, and the refusal is the point. This fault
		// is "declares copy on write and copies anyway", so injecting it into
		// a provider that does not copy would produce a green run whose
		// greenness proved nothing, which is the exact confusion Uninjectable
		// exists to make impossible.
		if d.opts.FlatBranch {
			return false
		}
		d.fault = f
		return true
	case CopyOnWriteUnderstated:
		// The mirror image: "branches in constant time and denies it" needs a
		// provider that branches in constant time.
		if !d.opts.FlatBranch {
			return false
		}
		d.fault = f
		return true
	}
	return false
}

func (d *PostgresDatabase) is(f Fault) bool { return d.fault == f }

// sanitize turns an arbitrary identifier from the suite into one Postgres will
// accept, without ever producing the same name for two different inputs that
// matter: the suite's environment identifiers differ before the fortieth
// character and the prefix keeps runs apart.
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	out := b.String()
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}

func (d *PostgresDatabase) goldenDB(version string) string {
	return d.opts.Prefix + "_g_" + sanitize(version)
}

func (d *PostgresDatabase) branchDB(envID string) string {
	return d.opts.Prefix + "_b_" + sanitize(envID)
}

func (d *PostgresDatabase) urlFor(database string) string {
	return replaceDatabase(d.opts.AdminURL, database)
}

// replaceDatabase swaps the database out of a connection string, leaving the
// query string alone. A naive strings.Replace on the name would also rewrite a
// host or a user that happened to share it.
func replaceDatabase(url, database string) string {
	rest := url
	query := ""
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		query = rest[i:]
		rest = rest[:i]
	}
	if i := strings.LastIndexByte(rest, '/'); i >= 0 {
		rest = rest[:i+1]
	}
	return rest + database + query
}

func (d *PostgresDatabase) admin(ctx context.Context) (*pgx.Conn, error) {
	return pgx.Connect(ctx, d.opts.AdminURL)
}

// exec runs one statement against one database and closes the connection.
//
// Closing every time is deliberate rather than wasteful. CREATE DATABASE ...
// TEMPLATE refuses while anything else is connected to the template, so a
// provider that kept a pool open on a golden could never branch it, and the
// failure would arrive at the caller as a mystery about another session.
func exec(ctx context.Context, url, sql string) error {
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	_, err = conn.Exec(ctx, sql)
	return err
}

func quoted(name string) string { return pgx.Identifier{name}.Sanitize() }

func (d *PostgresDatabase) createDatabase(ctx context.Context, name string) error {
	return exec(ctx, d.opts.AdminURL, "CREATE DATABASE "+quoted(name))
}

func (d *PostgresDatabase) copyDatabase(ctx context.Context, from, to string) error {
	return exec(ctx, d.opts.AdminURL,
		"CREATE DATABASE "+quoted(to)+" TEMPLATE "+quoted(from))
}

func (d *PostgresDatabase) dropDatabase(ctx context.Context, name string) error {
	return exec(ctx, d.opts.AdminURL, "DROP DATABASE IF EXISTS "+quoted(name)+" WITH (FORCE)")
}

// renameDatabase is the constant time half of FlatBranch.
//
// A rename is a catalogue update: it moves no bytes, so it costs the same for
// a golden of eight mebibytes and one of a gibibyte, which is the whole
// property CopyOnWrite_BranchTimeMatchesTheDeclaration measures.
func (d *PostgresDatabase) renameDatabase(ctx context.Context, from, to string) error {
	return exec(ctx, d.opts.AdminURL,
		"ALTER DATABASE "+quoted(from)+" RENAME TO "+quoted(to))
}

// FlatBranchPoolSize is how many constant time branches one golden can serve.
//
// Three, because CopyOnWrite_BranchTimeMatchesTheDeclaration times three
// branches per golden by default and a fourth would be copies of a gibibyte
// nothing asks for. A run configured with more samples than this exhausts the
// pool, the extra branches fall back to copying, and the behaviour's minimum
// is taken over the fast ones, so the failure mode is a weaker measurement
// rather than a wrong one.
const FlatBranchPoolSize = 3

// fillPool makes the copies a flat branch hands out. Called once per refresh,
// where the time it costs is not what anything measures.
func (d *PostgresDatabase) fillPool(ctx context.Context, goldenID, goldenDB string) error {
	names := make([]string, 0, FlatBranchPoolSize)
	for i := 0; i < FlatBranchPoolSize; i++ {
		d.state.mu.Lock()
		d.state.pooled++
		name := fmt.Sprintf("%s_p%d", d.opts.Prefix, d.state.pooled)
		d.state.mu.Unlock()
		if err := d.copyDatabase(ctx, goldenDB, name); err != nil {
			return fmt.Errorf("fill the flat branch pool: %w", err)
		}
		names = append(names, name)
	}
	d.state.mu.Lock()
	d.state.pool[goldenID] = append(d.state.pool[goldenID], names...)
	d.state.mu.Unlock()
	return nil
}

// takeFromPool pops a prepared copy, or reports that there is none left.
func (d *PostgresDatabase) takeFromPool(goldenID string) (string, bool) {
	d.state.mu.Lock()
	defer d.state.mu.Unlock()
	free := d.state.pool[goldenID]
	if len(free) == 0 {
		return "", false
	}
	name := free[len(free)-1]
	d.state.pool[goldenID] = free[:len(free)-1]
	return name, true
}

// drainPool drops whatever the pool still holds for a golden.
//
// Without it a flat provider leaves three copies of every golden behind for
// ever, and a fake that leaks is a fake that teaches the suite to ignore its
// own leak check.
func (d *PostgresDatabase) drainPool(ctx context.Context, goldenID string) {
	d.state.mu.Lock()
	free := d.state.pool[goldenID]
	delete(d.state.pool, goldenID)
	d.state.mu.Unlock()
	for _, name := range free {
		_ = d.dropDatabase(ctx, name)
	}
}

// emptyEveryTable is what BranchLosesTheGoldensRows does. It leaves the schema
// exactly as the golden had it, which is what makes the bug survive a look at
// the environment: the tables are all there.
const emptyEveryTable = `
DO $$
DECLARE r record;
BEGIN
    FOR r IN SELECT tablename FROM pg_tables WHERE schemaname = 'public' LOOP
        EXECUTE 'TRUNCATE TABLE public.' || quote_ident(r.tablename) || ' CASCADE';
    END LOOP;
END $$;`

func (d *PostgresDatabase) RefreshGolden(ctx context.Context, spec provider.GoldenSpec) (provider.GoldenVersion, error) {
	hash := spec.RulesHash
	if hash == "" {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			return provider.GoldenVersion{}, err
		}
		hash = hex.EncodeToString(b[:])
	}
	now := time.Now().UTC()
	id := provider.NewGoldenVersionID(now, hash)
	db := d.goldenDB(id)

	if err := d.createDatabase(ctx, db); err != nil {
		return provider.GoldenVersion{}, fmt.Errorf("create the golden candidate: %w", err)
	}
	url := d.urlFor(db)
	if d.opts.SeedSQL != "" {
		if err := exec(ctx, url, d.opts.SeedSQL); err != nil {
			_ = d.dropDatabase(context.WithoutCancel(ctx), db)
			return provider.GoldenVersion{}, fmt.Errorf("seed the golden candidate: %w", err)
		}
	}

	candidate := secrets.New(url)
	if spec.Mask != nil {
		if err := spec.Mask(ctx, candidate); err != nil {
			_ = d.dropDatabase(context.WithoutCancel(ctx), db)
			return provider.GoldenVersion{}, err
		}
	}
	att, verified := "", true
	if spec.Verify != nil {
		a, err := spec.Verify(ctx, candidate)
		switch {
		case err != nil && !d.opts.PublishUnverified:
			// The guarantee: a failed scan publishes nothing, and leaves
			// nothing behind for something else to find and branch.
			_ = d.dropDatabase(context.WithoutCancel(ctx), db)
			return provider.GoldenVersion{}, err
		case err != nil:
			verified = false
		default:
			att = a
		}
	}

	v := provider.GoldenVersion{
		ID:          id,
		CreatedAt:   now,
		RulesHash:   spec.RulesHash,
		Provenance:  spec.Provenance,
		Verified:    verified,
		Attestation: att,
		ProviderRef: db,
	}

	d.state.mu.Lock()
	d.state.goldens[v.ID] = v
	live := make([]provider.Branch, 0, len(d.state.branches))
	for _, b := range d.state.branches {
		live = append(live, b)
	}
	d.state.mu.Unlock()

	if d.opts.FlatBranch {
		if err := d.fillPool(ctx, v.ID, db); err != nil {
			return v, err
		}
	}

	if d.is(RefreshRebuildsExistingBranches) {
		// Every environment that branched an hour ago is quietly remade from
		// the new version, losing everything it has done since.
		for _, b := range live {
			target := d.branchDB(b.EnvID)
			if err := d.dropDatabase(ctx, target); err != nil {
				return v, err
			}
			if err := d.copyDatabase(ctx, db, target); err != nil {
				return v, err
			}
		}
	}
	return v, nil
}

func (d *PostgresDatabase) ListGoldens(context.Context) ([]provider.GoldenVersion, error) {
	d.state.mu.Lock()
	defer d.state.mu.Unlock()
	out := make([]provider.GoldenVersion, 0, len(d.state.goldens))
	for _, v := range d.state.goldens {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

func (d *PostgresDatabase) DestroyGolden(ctx context.Context, version string) error {
	d.state.mu.Lock()
	for _, b := range d.state.branches {
		if b.From == version {
			d.state.mu.Unlock()
			return aferrors.Coded(aferrors.AFDB010, "version", version)
		}
	}
	delete(d.state.goldens, version)
	d.state.mu.Unlock()
	d.drainPool(ctx, version)
	return d.dropDatabase(ctx, d.goldenDB(version))
}

func (d *PostgresDatabase) Branch(ctx context.Context, version, envID string) (provider.Branch, error) {
	// A cancelled create must leave either no resource or one the journal
	// knows about, and creating nothing is the easier half to get right.
	if err := ctx.Err(); err != nil {
		return provider.Branch{}, err
	}

	d.state.mu.Lock()
	if b, ok := d.state.branches[envID]; ok {
		d.state.mu.Unlock()
		return b, nil
	}
	v, ok := d.state.goldens[version]
	if !ok {
		d.state.mu.Unlock()
		return provider.Branch{}, aferrors.Coded(aferrors.AFDB004, "version", version)
	}
	if !v.Verified {
		d.state.mu.Unlock()
		return provider.Branch{}, aferrors.Coded(aferrors.AFMSK001, "version", version)
	}
	if len(d.state.branches) >= PostgresBranchLimit {
		d.state.mu.Unlock()
		return provider.Branch{}, aferrors.Coded(aferrors.AFDB006,
			"limit", fmt.Sprintf("%d", PostgresBranchLimit))
	}
	d.state.mu.Unlock()

	// Which database this branch actually reaches, which is the whole of what
	// isolation means. The two sharing faults are injected here rather than in
	// ConnString for a reason worth recording: a branch that HANDS OUT the
	// golden's address still copies the golden first, and the suite then holds
	// a connection to it, and CREATE DATABASE ... TEMPLATE refuses while
	// anything is connected to the template. The behaviour went red for the
	// copy failing instead of for the isolation being gone, which is a red
	// that proves nothing. Not copying at all is also the more faithful bug:
	// a provider whose branches share storage is one that never made a copy.
	target := d.branchDB(envID)
	copyFrom := d.goldenDB(version)
	switch {
	case d.is(BranchSharesTheGoldensStorage):
		target, copyFrom = d.goldenDB(version), ""
	case d.is(BranchesShareOneDatabase):
		d.state.mu.Lock()
		first, ok := d.state.firstOf[version]
		d.state.mu.Unlock()
		if ok {
			target, copyFrom = first, ""
		}
	}
	if copyFrom != "" {
		// The flat path, which moves no bytes. Falling back to a copy when the
		// pool is empty rather than failing, because a provider that refused
		// its fourth branch would fail behaviours that have nothing to do with
		// timing and the red would point at the wrong thing.
		pooled, ok := "", false
		if d.opts.FlatBranch {
			pooled, ok = d.takeFromPool(version)
		}
		if ok {
			if err := d.renameDatabase(ctx, pooled, target); err != nil {
				return provider.Branch{}, fmt.Errorf("hand out a prepared branch: %w", err)
			}
		} else if err := d.copyDatabase(ctx, copyFrom, target); err != nil {
			return provider.Branch{}, fmt.Errorf("branch the golden: %w", err)
		}
	}
	if d.is(BranchLosesTheGoldensRows) {
		if err := exec(ctx, d.urlFor(target), emptyEveryTable); err != nil {
			return provider.Branch{}, err
		}
	}

	b := provider.Branch{
		EnvID:       envID,
		From:        version,
		ProviderRef: target,
		CreatedAt:   time.Now().UTC(),
	}
	d.state.mu.Lock()
	d.state.branches[envID] = b
	if _, seen := d.state.firstOf[version]; !seen {
		d.state.firstOf[version] = target
	}
	d.state.mu.Unlock()
	return b, nil
}

func (d *PostgresDatabase) Reset(ctx context.Context, b provider.Branch) error {
	if err := d.dropDatabase(ctx, b.ProviderRef); err != nil {
		return err
	}
	return d.copyDatabase(ctx, d.goldenDB(b.From), b.ProviderRef)
}

func (d *PostgresDatabase) Destroy(ctx context.Context, b provider.Branch) error {
	d.state.mu.Lock()
	delete(d.state.branches, b.EnvID)
	d.state.mu.Unlock()
	// Destroying something already gone succeeds, because teardown retries.
	return d.dropDatabase(ctx, b.ProviderRef)
}

func (d *PostgresDatabase) ConnString(_ context.Context, b provider.Branch, mode provider.ConnMode) (secrets.Value, error) {
	url := d.urlFor(b.ProviderRef)
	if mode == provider.ConnPooled {
		// A different string that reaches the same place, which is what a
		// pooler in front of one database is.
		sep := "?"
		if strings.Contains(url, "?") {
			sep = "&"
		}
		url += sep + "application_name=af-conformance-pooled"
	}
	return secrets.New(url), nil
}

// Inventory reads pg_database rather than this provider's own bookkeeping.
//
// A leak detector that trusted the provider's memory would report exactly the
// resources the provider remembered to tell it about, which is the one thing a
// leak detector must not do.
func (d *PostgresDatabase) Inventory(ctx context.Context) ([]provider.Resource, error) {
	conn, err := d.admin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	rows, err := conn.Query(ctx,
		`SELECT datname FROM pg_database WHERE datname LIKE $1 ORDER BY datname`,
		d.opts.Prefix+"\\_%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	d.state.mu.Lock()
	envOf := make(map[string]string, len(d.state.branches))
	for env, b := range d.state.branches {
		envOf[b.ProviderRef] = env
	}
	d.state.mu.Unlock()

	out := make([]provider.Resource, 0, len(names))
	for _, n := range names {
		kind := "branch"
		if strings.HasPrefix(n, d.opts.Prefix+"_g_") {
			kind = "golden"
		}
		out = append(out, provider.Resource{Kind: kind, ID: n, EnvID: envOf[n]})
	}
	return out, nil
}

// Health connects, because the question is whether the environment can.
//
// Gone is an answer rather than a fault: teardown asks for health, so a
// provider that errors on a branch it has just removed makes a successful
// teardown look like a failure.
func (d *PostgresDatabase) Health(ctx context.Context, b provider.Branch) (provider.Health, error) {
	started := time.Now()
	conn, err := pgx.Connect(ctx, d.urlFor(b.ProviderRef))
	if err != nil {
		return provider.Health{Reachable: false, Detail: err.Error()}, nil
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	if err := conn.Ping(ctx); err != nil {
		return provider.Health{Reachable: false, Detail: err.Error()}, nil
	}
	return provider.Health{Reachable: true, Latency: time.Since(started)}, nil
}

func (d *PostgresDatabase) Close() error { return nil }

// DropEverything removes every database this prefix owns.
//
// A behaviour that fails legitimately leaves its resources behind for
// inspection, and a fault whose whole content is "destroy removes nothing"
// leaves them by construction. Both run in a child process, so the parent is
// the only thing that can sweep, and the cluster it runs against is shared
// with every other branch's work.
func DropEverything(ctx context.Context, adminURL, prefix string) error {
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	rows, err := conn.Query(ctx,
		`SELECT datname FROM pg_database WHERE datname LIKE $1`, prefix+"\\_%")
	if err != nil {
		return err
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		names = append(names, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, n := range names {
		if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+quoted(n)+" WITH (FORCE)"); err != nil {
			return err
		}
	}

	// Ask again rather than trusting the loop. A sweep that reports success
	// because it ran is the shape of check this repository keeps finding in
	// its own instruments: it can only ever say yes.
	var left []string
	rows, err = conn.Query(ctx,
		`SELECT datname FROM pg_database WHERE datname LIKE $1 ORDER BY datname`, prefix+"\\_%")
	if err != nil {
		return err
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		left = append(left, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(left) > 0 {
		return fmt.Errorf("fakes: %d databases named %s_* survived the sweep: %s",
			len(left), prefix, strings.Join(left, ", "))
	}
	return nil
}
