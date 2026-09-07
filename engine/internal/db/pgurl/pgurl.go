// Package pgurl is the database provider for a Postgres nobody wrote a
// provider for.
//
// Every other provider in this repository is a provider for one product:
// Neon's branches, Supabase's branches, the Database Lab Engine's clones, a
// Docker daemon's images. That leaves out most of the Postgres in the world.
// A self hosted cluster, a machine at Hetzner or Scaleway or OVH, a managed
// Postgres whose vendor has no provider here, an internal server behind a
// bastion: none of them can be a source today, and all of them are reachable
// by URL.
//
// So this provider knows nothing about any product. It needs two connection
// strings and a role that may create databases:
//
//   - the SOURCE, which the engine already resolves from
//     database.source_url_env, read once per refresh and never stored.
//   - the HOST SERVER, named by database.api_key_env, which is where the
//     goldens and the branches live. It is not the source, and it should not
//     be the production server: this provider creates a database per golden
//     and a database per environment on it.
//
// The model is the one Postgres itself provides. A golden is a database,
// filled from the source through pgcopy, masked, verified, and then marked
// IS_TEMPLATE. A branch is CREATE DATABASE ... TEMPLATE, which is a server
// side file copy, so a branch is a real, separate database that shares nothing
// with the golden. Two properties follow and both are declared rather than
// implied: branching is NOT copy on write, so branch time grows with the
// database, and a template cannot be dropped while it is a template, so a
// golden cannot be removed by accident by anything that does not first ask
// this provider to unmark it.
//
// Nothing here is dropped or renamed on the strength of its name alone. Every
// database this provider creates carries a JSON marker in its comment, and
// every destructive path reads that marker first. The failure that rule exists
// for is a user whose own database happens to be called af_g_something: a
// provider that trusted the prefix would drop it during a routine garbage
// collection and the person would have no way to know what had done it.
package pgurl

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the pgx driver

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/db/pgcopy"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// The database name prefixes this provider owns, one per kind of thing it
// makes.
//
// Short on purpose. A Postgres identifier is 63 bytes and an environment
// identifier is not this package's to shorten, so every byte spent on a prefix
// is a byte an environment name cannot have.
const (
	goldenPrefix    = "af_g_"
	branchPrefix    = "af_b_"
	candidatePrefix = "af_c_"
)

// marker is the value of the "antifailure" key in the comment JSON. It is what
// separates a database this provider made from one that merely shares a name.
const marker = "pgurl"

// candidateAge is how old a candidate has to be before it can only be an
// orphan. A refresh that is still running holds one for as long as the copy
// takes, which on a large database is far more than a few minutes, so this is
// deliberately generous: removing a candidate another process is filling would
// turn one slow refresh into a corrupt one.
const candidateAge = 6 * time.Hour

// readyTimeout bounds the wait for a database to answer.
const readyTimeout = 2 * time.Minute

// meta is what this provider records in a database's comment.
//
// The comment is the only place a provider like this one can keep metadata: it
// survives the process, it is shared catalog state so every connection sees
// it, and it costs no table in anybody's schema. The Docker provider uses
// image labels for exactly the same reason and the fields are the same fields.
type meta struct {
	// Antifailure carries the marker. A comment without it is somebody else's.
	Antifailure string `json:"antifailure"`
	// Kind is golden, branch or candidate.
	Kind string `json:"kind"`
	// Version is the golden version identifier: its own for a golden, the one
	// it came from for a branch.
	Version string `json:"version,omitempty"`
	// EnvID is the environment a branch belongs to.
	EnvID string `json:"env,omitempty"`
	// Rules identifies the masking rules that produced a golden.
	Rules string `json:"rules,omitempty"`
	// Provenance identifies the project the golden was made for.
	Provenance string `json:"provenance,omitempty"`
	// Attestation is the signed statement the verifier returned. Its presence
	// is what Verified means, for the reason the Docker provider records in
	// ListGoldens: a refresh with no verifier publishes honestly with
	// Verified false, and a provider that recomputed the flag from "it exists,
	// so it must have passed" would hand back true for a golden nothing ever
	// scanned.
	Attestation string `json:"attestation,omitempty"`
	// CreatedAt is when the resource was published.
	CreatedAt time.Time `json:"created"`
}

// Provider is the database provider for any reachable Postgres.
type Provider struct {
	// admin is the connection string of the host server, pointing at the
	// database CREATE DATABASE and DROP DATABASE are issued from.
	admin secrets.Value
	// variable is the name of the manifest named variable admin came from, so
	// that a refusal can say which one to fix rather than describing it.
	variable string
	// hostport is the host and port, with no credential in it, for messages.
	hostport string
	// major is the host server's Postgres major, read from the server rather
	// than taken from the manifest. This provider cannot choose a version: a
	// golden here is a database on that server, so the server's version is the
	// only version there is.
	major int
	// seedSQL fills a candidate when there is no source, which is a project
	// that has not connected production yet.
	seedSQL string
	// maxBranches is the ceiling the manifest declared. Zero means the
	// provider imposes none, which is honest: the limits on this server are
	// disk and max_connections, and both surface as the server's own errors.
	maxBranches int
	clock       clock.Clock
}

// Options configure the provider.
type Options struct {
	// AdminURL is the host server's connection string. Required.
	AdminURL secrets.Value
	// Variable names the environment variable AdminURL was read from.
	Variable string
	// SeedSQL initialises a golden candidate when no source is configured.
	SeedSQL string
	// MaxBranches is the concurrent branch ceiling, or zero for none.
	MaxBranches int
	// Clock is the time source.
	Clock clock.Clock
}

// New returns a provider for the server AdminURL names.
//
// It connects before it returns, and that is deliberate. Everything this
// provider does needs a reachable server and a role that may create databases,
// and both of those are decidable in one round trip. Discovering them at the
// first refresh instead means the failure arrives after a golden refresh has
// already read production, which is the most expensive possible moment to
// learn that the destination was never going to work.
func New(ctx context.Context, opts Options) (*Provider, error) {
	if opts.Clock == nil {
		opts.Clock = clock.New()
	}
	variable := opts.Variable
	if variable == "" {
		variable = DefaultVariable
	}
	if opts.AdminURL.IsZero() {
		return nil, aferrors.Coded(aferrors.AFSEC001, "names", variable, "sources", "the manifest")
	}
	admin, hostport, err := normalize(opts.AdminURL, variable)
	if err != nil {
		return nil, err
	}

	p := &Provider{
		admin: admin, variable: variable, hostport: hostport,
		seedSQL: opts.SeedSQL, maxBranches: opts.MaxBranches, clock: opts.Clock,
	}

	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	db, err := p.open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	var versionNum int
	var role string
	var mayCreate bool
	err = db.QueryRowContext(probeCtx, `
		SELECT current_setting('server_version_num')::int,
		       current_user,
		       (SELECT rolcreatedb OR rolsuper FROM pg_catalog.pg_roles WHERE rolname = current_user)`,
	).Scan(&versionNum, &role, &mayCreate)
	if err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFDB034,
			"variable", variable, "host", hostport, "detail", shortError(err))
	}
	if !mayCreate {
		// Refused here rather than at the first CREATE DATABASE. This provider
		// makes one database per golden and one per environment, so a role
		// without CREATEDB cannot do any part of its job, and saying so before
		// anything reads production is the difference between a sentence and a
		// wasted dump.
		return nil, aferrors.Coded(aferrors.AFDB035,
			"role", role, "host", hostport, "variable", variable)
	}
	p.major = versionNum / 10000
	return p, nil
}

// DefaultVariable is the variable this provider reads when the manifest names
// none. It holds the host server's connection string, not an API key: for this
// provider the URL is the credential, which is why it is named rather than
// written into a manifest that gets committed.
const DefaultVariable = "PGURL_ADMIN_URL"

// Name identifies the provider, and is what appears in a manifest.
func (p *Provider) Name() string { return "pgurl" }

// Capabilities describes what this provider can do.
//
// Two of these are the honest answers rather than the flattering ones.
//
// CopyOnWrite is FALSE. CREATE DATABASE ... TEMPLATE copies the files, so
// branch time grows with the database: seconds for a small one and minutes for
// a large one. Declaring it true would make the conformance suite skip nothing
// and would make every published branch time a lie by a factor that grows with
// the customer.
//
// SupportedVersions is the ONE version the host server runs, read from the
// server. A provider that listed every major it had heard of would accept a
// manifest asking for Postgres 18, build the golden on a Postgres 16 server,
// and hand the application a database whose behaviour differs from production
// in ways that only appear under load.
func (p *Provider) Capabilities() provider.Caps {
	return provider.Caps{
		Branching: true,
		// Reset drops the branch and recreates it from the same template, so
		// it is exact rather than approximate: sequences, statistics and the
		// visibility map all come back, because they are file copies of the
		// golden's.
		Reset:           true,
		CopyOnWrite:     false,
		ProviderMasking: false,
		PooledEndpoints: false,
		// A candidate here is an empty database this provider creates, so a
		// subset can be loaded into it instead of the whole source.
		Subsetting:            true,
		MaxConcurrentBranches: p.maxBranches,
		// What branching the conformance dataset costs, which is the dataset
		// the field is defined against. It is not a claim about a customer's
		// database: that number is proportional to size and is published per
		// gigabyte in the benchmark report instead.
		ExpectedBranchLatency: 10 * time.Second,
		SupportedVersions:     []int{p.major},
	}
}

// Close releases nothing, because this provider holds no long lived
// connection: every operation opens a connection, uses it, and closes it.
//
// That is a deliberate trade. A pool held open would be faster and would keep
// a connection on the customer's server for the whole life of the process,
// which for a provider whose entire job is to be pointed at somebody else's
// database is the wrong default.
func (p *Provider) Close() error { return nil }

// RefreshGolden builds a new masked, verified golden version.
//
// The order is the same one every provider in this repository follows and it
// is not negotiable: create an empty candidate, load it, mask it, verify it,
// and only then publish. Publishing here is a rename plus IS_TEMPLATE, so
// there is no window in which a half built golden is visible as a golden: the
// listing reads the marker's kind, and the kind only becomes golden once the
// verifier has returned.
func (p *Provider) RefreshGolden(ctx context.Context, spec provider.GoldenSpec) (provider.GoldenVersion, error) {
	if !p.Capabilities().Supports(spec.Version) {
		return provider.GoldenVersion{}, aferrors.Coded(aferrors.AFDB003,
			"found", strconv.Itoa(spec.Version), "supported", strconv.Itoa(p.major))
	}

	p.sweepCandidates(ctx)

	candidate := fmt.Sprintf("%s%d", candidatePrefix, p.clock.Now().UnixNano())
	if err := p.createDatabase(ctx, candidate, ""); err != nil {
		return provider.GoldenVersion{}, err
	}
	if err := p.comment(ctx, candidate, meta{
		Antifailure: marker, Kind: "candidate", CreatedAt: p.clock.Now().UTC(),
	}); err != nil {
		return provider.GoldenVersion{}, err
	}
	// The candidate goes whatever happens. A failed refresh that leaves a
	// database holding a copy of production on somebody's server is worse than
	// any error this function can return.
	published := false
	defer func() {
		if !published {
			_ = p.dropDatabase(context.WithoutCancel(ctx), candidate)
		}
	}()

	conn := p.urlFor(candidate)
	if err := p.waitReady(ctx, conn); err != nil {
		return provider.GoldenVersion{}, err
	}
	if err := p.loadSource(ctx, conn, spec); err != nil {
		return provider.GoldenVersion{}, err
	}

	if spec.Mask != nil {
		if err := spec.Mask(ctx, conn); err != nil {
			return provider.GoldenVersion{}, fmt.Errorf("db.pgurl: mask the golden candidate: %w", err)
		}
	}
	attestation := ""
	if spec.Verify != nil {
		var err error
		attestation, err = spec.Verify(ctx, conn)
		if err != nil {
			// Nothing is published. The deferred drop removes the candidate,
			// so a failed verification leaves nothing anybody could branch.
			return provider.GoldenVersion{}, fmt.Errorf("db.pgurl: verify the golden candidate: %w", err)
		}
	}

	version := provider.NewGoldenVersionID(p.clock.Now(), spec.RulesHash)
	created := p.clock.Now().UTC()
	// The comment is written BEFORE the rename, so that the crash window holds
	// a candidate carrying a golden's metadata rather than a golden carrying a
	// candidate's. The first is swept; the second would be invisible to the
	// listing and invisible to the sweep, which is a leaked copy of production
	// that nothing ever removes.
	if err := p.comment(ctx, candidate, meta{
		Antifailure: marker, Kind: "golden", Version: version,
		Rules: spec.RulesHash, Provenance: spec.Provenance,
		Attestation: attestation, CreatedAt: created,
	}); err != nil {
		return provider.GoldenVersion{}, err
	}
	name := goldenPrefix + version
	if err := p.terminate(ctx, candidate); err != nil {
		return provider.GoldenVersion{}, err
	}
	if err := p.exec(ctx, fmt.Sprintf("ALTER DATABASE %s RENAME TO %s",
		quoteIdent(candidate), quoteIdent(name))); err != nil {
		return provider.GoldenVersion{}, fmt.Errorf("db.pgurl: publish the golden: %w", err)
	}
	published = true
	// IS_TEMPLATE is what makes the golden branchable by a role that does not
	// own it, and it is also the accidental drop guard: Postgres refuses to
	// drop a template database, so DestroyGolden has to unmark one on purpose
	// before it can remove it. ALLOW_CONNECTIONS false is the other half: a
	// golden nobody can connect to cannot drift from what was verified, and
	// CREATE DATABASE ... TEMPLATE refuses while any session is connected to
	// the template, so a stray psql would otherwise break every branch on the
	// machine until somebody found it.
	for _, stmt := range []string{
		fmt.Sprintf("ALTER DATABASE %s ALLOW_CONNECTIONS false", quoteIdent(name)),
		fmt.Sprintf("ALTER DATABASE %s IS_TEMPLATE true", quoteIdent(name)),
	} {
		if err := p.exec(ctx, stmt); err != nil {
			return provider.GoldenVersion{}, fmt.Errorf("db.pgurl: seal the golden: %w", err)
		}
	}

	gv := provider.GoldenVersion{
		ID: version, CreatedAt: created, RulesHash: spec.RulesHash,
		Provenance: spec.Provenance, Verified: spec.Verify != nil,
		Attestation: attestation, ProviderRef: name,
	}
	gv.SizeBytes = p.sizeOf(ctx, name)
	return gv, nil
}

// loadSource fills a candidate from the source, or from the seed when there is
// no source.
//
// The @source/ test is the same one the Docker provider makes and it is here
// for the same reason: the conformance suite hands every provider a source URL
// that does not resolve, because what it is testing is the provider and not
// pg_dump.
func (p *Provider) loadSource(ctx context.Context, target secrets.Value, spec provider.GoldenSpec) error {
	fake := spec.SourceURL.IsZero() || strings.Contains(spec.SourceURL.Reveal(), "@source/")
	if spec.Load != nil && !fake {
		// The engine is loading this one, because the manifest asked for a
		// slice rather than the whole database. This provider's job was to
		// produce the empty candidate, which it has.
		return spec.Load(ctx, spec.SourceURL, target)
	}
	if fake {
		return pgcopy.Exec(ctx, target, p.seedSQL)
	}
	return pgcopy.Copy(ctx, spec.SourceURL, target)
}

// sweepCandidates removes candidates old enough that they can only be orphans.
//
// Opportunistic and never an error: a refresh must not fail because a cleanup
// could not run. A candidate is never referenced by anything, so removing an
// old one is unconditionally safe, and without this a killed process leaves a
// full copy of production on the server for ever.
func (p *Provider) sweepCandidates(ctx context.Context) {
	rows, err := p.list(ctx, candidatePrefix)
	if err != nil {
		return
	}
	cutoff := p.clock.Now().Add(-candidateAge)
	for _, r := range rows {
		if r.meta.CreatedAt.IsZero() || r.meta.CreatedAt.Before(cutoff) {
			_ = p.dropDatabase(ctx, r.name)
		}
	}
}

// ListGoldens returns published versions, newest first.
func (p *Provider) ListGoldens(ctx context.Context) ([]provider.GoldenVersion, error) {
	rows, err := p.list(ctx, goldenPrefix)
	if err != nil {
		return nil, err
	}
	out := make([]provider.GoldenVersion, 0, len(rows))
	for _, r := range rows {
		if r.meta.Kind != "golden" {
			// A database whose name says golden and whose marker does not is a
			// refresh that died between the comment and the rename. It is not
			// branchable and saying it is would hand somebody an empty
			// database with a version number on it.
			continue
		}
		out = append(out, provider.GoldenVersion{
			ID: r.meta.Version, CreatedAt: r.meta.CreatedAt, SizeBytes: r.size,
			RulesHash: r.meta.Rules, Provenance: r.meta.Provenance,
			Attestation: r.meta.Attestation, Verified: r.meta.Attestation != "",
			ProviderRef: r.name,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// DestroyGolden removes a version, refusing one a branch still depends on.
func (p *Provider) DestroyGolden(ctx context.Context, version string) error {
	name := goldenPrefix + version
	row, found, err := p.lookup(ctx, name)
	if err != nil {
		return err
	}
	if !found {
		// Removing something already gone succeeds, because retention runs
		// again after a partial failure and a second pass must not fail on
		// what the first one removed.
		return nil
	}
	branches, err := p.list(ctx, branchPrefix)
	if err != nil {
		return err
	}
	count := 0
	for _, b := range branches {
		if b.meta.Version == version {
			count++
		}
	}
	if count > 0 {
		// Dropping it would take the branch's data with it, because a branch
		// is a copy and not a reference. Postgres would refuse anyway once the
		// template is in use, but a coded refusal names the environments and
		// the daemon's would not.
		return aferrors.Coded(aferrors.AFDB005,
			"version", version, "count", strconv.Itoa(count))
	}
	_ = row
	return p.dropDatabase(ctx, name)
}

// Branch creates a database for an environment from a golden version.
func (p *Provider) Branch(ctx context.Context, version, envID string) (provider.Branch, error) {
	name, err := branchName(envID)
	if err != nil {
		return provider.Branch{}, err
	}

	// Idempotency first. A retry after a timeout must find the branch this
	// call may already have created, or the first attempt's database becomes
	// an orphan nobody has an identifier for.
	if row, found, lookupErr := p.lookup(ctx, name); lookupErr != nil {
		return provider.Branch{}, lookupErr
	} else if found {
		if row.meta.Antifailure != marker {
			return provider.Branch{}, p.notOurs(name)
		}
		from := row.meta.Version
		if from == "" {
			from = version
		}
		return provider.Branch{
			EnvID: envID, From: from, ProviderRef: name, CreatedAt: row.meta.CreatedAt,
		}, nil
	}

	golden := goldenPrefix + version
	row, found, err := p.lookup(ctx, golden)
	if err != nil {
		return provider.Branch{}, err
	}
	if !found || row.meta.Kind != "golden" {
		return provider.Branch{}, fmt.Errorf("%w Available: %s",
			aferrors.Coded(aferrors.AFDB004, "version", version), p.describeGoldens(ctx))
	}
	if row.meta.Attestation == "" {
		// The product's central promise, enforced here rather than in a
		// checklist. A golden with no attestation was published by a refresh
		// that ran no verifier, and branching it would put data nothing
		// scanned in front of whoever asked for an environment.
		return provider.Branch{}, aferrors.Coded(aferrors.AFMSK001,
			"version", version, "detail", "the golden carries no attestation, so nothing scanned it")
	}

	if p.maxBranches > 0 {
		existing, listErr := p.list(ctx, branchPrefix)
		if listErr != nil {
			return provider.Branch{}, listErr
		}
		if len(existing) >= p.maxBranches {
			return provider.Branch{}, aferrors.Coded(aferrors.AFDB006,
				"limit", strconv.Itoa(p.maxBranches))
		}
	}

	if err := p.createDatabase(ctx, name, golden); err != nil {
		return provider.Branch{}, err
	}
	created := p.clock.Now().UTC()
	if err := p.comment(ctx, name, meta{
		Antifailure: marker, Kind: "branch", Version: version,
		EnvID: envID, CreatedAt: created,
	}); err != nil {
		// The database exists and the caller is told what it is called, so
		// teardown can remove it. Returning only the error would leave a
		// database nothing names.
		return provider.Branch{EnvID: envID, From: version, ProviderRef: name, CreatedAt: created}, err
	}
	return provider.Branch{EnvID: envID, From: version, ProviderRef: name, CreatedAt: created}, nil
}

// Reset returns a branch to its golden state.
//
// Drop and recreate from the same template rather than replaying a dump, which
// makes the result exact: the sequences come back, because they are part of
// the files being copied. A reset that rewinds rows but not sequences produces
// a primary key collision on the next insert, which surfaces as an application
// bug nobody can reproduce.
func (p *Provider) Reset(ctx context.Context, b provider.Branch) error {
	if b.From == "" {
		return fmt.Errorf("db.pgurl: the branch does not record which golden it came from")
	}
	if err := p.Destroy(ctx, b); err != nil {
		return err
	}
	_, err := p.Branch(ctx, b.From, b.EnvID)
	return err
}

// Destroy removes a branch. Removing one that is already gone succeeds.
func (p *Provider) Destroy(ctx context.Context, b provider.Branch) error {
	names := map[string]bool{}
	if b.ProviderRef != "" {
		names[b.ProviderRef] = true
	}
	if b.EnvID != "" {
		// The derived name as well as the recorded one. They are the same
		// today, and the recorded one is what a journal written by an older
		// build carries; teardown that silently missed one would leave a copy
		// of production on the server for ever.
		derived, err := branchName(b.EnvID)
		if err != nil {
			return err
		}
		names[derived] = true
	}
	if len(names) == 0 {
		return fmt.Errorf("db.pgurl: the branch has neither an identifier nor an environment")
	}
	for name := range names {
		if err := p.dropDatabase(ctx, name); err != nil {
			return err
		}
	}
	return nil
}

// ConnString returns a connection string for a branch.
func (p *Provider) ConnString(ctx context.Context, b provider.Branch, mode provider.ConnMode) (secrets.Value, error) {
	if mode == provider.ConnPooled {
		// Declaring a capability this provider does not have would make the
		// conformance suite pass a behavior it should skip. A pooler in front
		// of this server is the operator's to run and this provider would be
		// guessing at its address.
		return secrets.Value{}, provider.ErrUnsupported
	}
	name := b.ProviderRef
	if name == "" {
		if b.EnvID == "" {
			return secrets.Value{}, aferrors.Coded(aferrors.AFDB014, "env", b.EnvID)
		}
		derived, err := branchName(b.EnvID)
		if err != nil {
			return secrets.Value{}, err
		}
		name = derived
	}
	_, found, err := p.lookup(ctx, name)
	if err != nil {
		return secrets.Value{}, err
	}
	if !found {
		// Which of the two this is depends on what was asked for, and getting
		// it wrong sends somebody to the wrong command: a missing branch is
		// answered by af up, a missing golden by af golden refresh.
		if b.From == "" {
			return secrets.Value{}, aferrors.Coded(aferrors.AFDB014, "env", b.EnvID)
		}
		return secrets.Value{}, aferrors.Coded(aferrors.AFDB004, "version", b.From)
	}
	return p.urlFor(name), nil
}

// Inventory lists everything this provider currently holds.
//
// By marker rather than by name. A database that merely looks like ours is not
// reported, because the leak detector's output is a list of things somebody is
// about to delete.
func (p *Provider) Inventory(ctx context.Context) ([]provider.Resource, error) {
	var out []provider.Resource
	for _, prefix := range []string{goldenPrefix, branchPrefix, candidatePrefix} {
		rows, err := p.list(ctx, prefix)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			out = append(out, provider.Resource{
				Kind:      "database/" + r.meta.Kind,
				ID:        r.name,
				EnvID:     r.meta.EnvID,
				CreatedAt: r.meta.CreatedAt,
				Labels: map[string]string{
					"version": r.meta.Version,
					"size":    strconv.FormatInt(r.size, 10),
					"host":    p.hostport,
				},
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Health reports whether a branch is reachable.
//
// Gone is an answer rather than a fault: teardown asks for health, and a
// provider that errors on a branch it has just removed makes a successful
// teardown look like a failure.
func (p *Provider) Health(ctx context.Context, b provider.Branch) (provider.Health, error) {
	start := p.clock.Now()
	conn, err := p.ConnString(ctx, b, provider.ConnDirect)
	if err != nil {
		return provider.Health{Reachable: false, Detail: "the branch database does not exist"}, nil
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pgcopy.Ping(pingCtx, conn); err != nil {
		return provider.Health{Reachable: false, Detail: shortError(err)}, nil
	}
	return provider.Health{
		Reachable: true, Detail: "accepting connections", Latency: p.clock.Since(start),
	}, nil
}

// row is one database this provider found on the server.
type row struct {
	name string
	size int64
	meta meta
}

// list returns the databases with a prefix that carry this provider's marker.
func (p *Provider) list(ctx context.Context, prefix string) ([]row, error) {
	db, err := p.open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	// pg_database_size is guarded, because it raises for a database this role
	// cannot connect to. One such database on the server would otherwise turn
	// every listing into an error, and the database causing it need not be
	// ours at all.
	rows, err := db.QueryContext(ctx, `
		SELECT d.datname,
		       CASE WHEN pg_catalog.has_database_privilege(d.datname, 'CONNECT')
		            THEN pg_catalog.pg_database_size(d.datname) ELSE 0 END,
		       coalesce(pg_catalog.shobj_description(d.oid, 'pg_database'), '')
		FROM pg_catalog.pg_database d
		WHERE d.datname LIKE $1 || '%'`, prefix)
	if err != nil {
		return nil, p.wrap(err, "list the databases")
	}
	defer func() { _ = rows.Close() }()

	var out []row
	for rows.Next() {
		var r row
		var comment string
		if err := rows.Scan(&r.name, &r.size, &comment); err != nil {
			return nil, p.wrap(err, "read the database listing")
		}
		if err := json.Unmarshal([]byte(comment), &r.meta); err != nil {
			continue // not ours: a comment that is not our JSON was written by somebody else
		}
		if r.meta.Antifailure != marker {
			continue
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, p.wrap(err, "read the database listing")
	}
	return out, nil
}

// lookup returns one database by name, whether or not it is ours.
//
// The distinction matters: a database that exists and is NOT ours must stop a
// destructive path rather than be treated as absent, because treating it as
// absent is how a provider creates a golden over the top of somebody's data.
func (p *Provider) lookup(ctx context.Context, name string) (row, bool, error) {
	db, err := p.open()
	if err != nil {
		return row{}, false, err
	}
	defer func() { _ = db.Close() }()

	var r row
	var comment string
	err = db.QueryRowContext(ctx, `
		SELECT d.datname,
		       CASE WHEN pg_catalog.has_database_privilege(d.datname, 'CONNECT')
		            THEN pg_catalog.pg_database_size(d.datname) ELSE 0 END,
		       coalesce(pg_catalog.shobj_description(d.oid, 'pg_database'), '')
		FROM pg_catalog.pg_database d WHERE d.datname = $1`, name).Scan(&r.name, &r.size, &comment)
	if errors.Is(err, sql.ErrNoRows) {
		return row{}, false, nil
	}
	if err != nil {
		return row{}, false, p.wrap(err, "look for the database "+name)
	}
	_ = json.Unmarshal([]byte(comment), &r.meta)
	return r, true, nil
}

// createDatabase creates one, from a template when one is named.
func (p *Provider) createDatabase(ctx context.Context, name, template string) error {
	if err := checkName(name); err != nil {
		return err
	}
	if existing, found, err := p.lookup(ctx, name); err != nil {
		return err
	} else if found && existing.meta.Antifailure != marker {
		return p.notOurs(name)
	}
	stmt := "CREATE DATABASE " + quoteIdent(name)
	if template != "" {
		if err := checkName(template); err != nil {
			return err
		}
		stmt += " TEMPLATE " + quoteIdent(template)
	}
	if err := p.exec(ctx, stmt); err != nil {
		return p.wrap(err, "create the database "+name)
	}
	return nil
}

// dropDatabase removes one, and refuses a database this provider did not make.
//
// The refusal is the point of the marker. Names collide: a user with their own
// af_g_ database, two projects sharing a server, an environment identifier
// somebody reused. A provider that dropped on the strength of a name would
// eventually destroy data it had never created, and the person would have no
// way to find out what had done it.
func (p *Provider) dropDatabase(ctx context.Context, name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	r, found, err := p.lookup(ctx, name)
	if err != nil {
		return err
	}
	if !found {
		return nil // already gone, which is a success: teardown retries
	}
	if r.meta.Antifailure != marker {
		return p.notOurs(name)
	}
	// A golden is a template and Postgres refuses to drop one, so the mark
	// comes off first. Each statement is its own round trip because DROP
	// DATABASE cannot run inside a transaction block and a multi statement
	// exec is sent as one.
	if err := p.exec(ctx, fmt.Sprintf("ALTER DATABASE %s IS_TEMPLATE false", quoteIdent(name))); err != nil {
		return p.wrap(err, "unmark the template "+name)
	}
	if err := p.terminate(ctx, name); err != nil {
		return err
	}
	if err := p.exec(ctx, "DROP DATABASE IF EXISTS "+quoteIdent(name)+" WITH (FORCE)"); err != nil {
		return p.wrap(err, "drop the database "+name)
	}
	return nil
}

// terminate closes the sessions on a database, so that a rename or a copy is
// not refused by one forgotten psql.
func (p *Provider) terminate(ctx context.Context, name string) error {
	db, err := p.open()
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	_, err = db.ExecContext(ctx, `
		SELECT pg_catalog.pg_terminate_backend(pid)
		FROM pg_catalog.pg_stat_activity
		WHERE datname = $1 AND pid <> pg_catalog.pg_backend_pid()`, name)
	if err != nil {
		return p.wrap(err, "close the sessions on "+name)
	}
	return nil
}

// comment records this provider's metadata on a database.
func (p *Provider) comment(ctx context.Context, name string, m meta) error {
	if err := checkName(name); err != nil {
		return err
	}
	encoded, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("db.pgurl: encode the database metadata: %w", err)
	}
	db, err := p.open()
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	// standard_conforming_strings is set rather than assumed. It has been the
	// default since Postgres 9.1, and an attestation is somebody else's JSON
	// with backslashes in it: on a server where the setting was turned off,
	// doubling the quotes would not be enough and the comment would be written
	// with the escapes interpreted.
	if _, err := db.ExecContext(ctx, "SET standard_conforming_strings = on"); err != nil {
		return p.wrap(err, "prepare the metadata write")
	}
	stmt := fmt.Sprintf("COMMENT ON DATABASE %s IS %s", quoteIdent(name), quoteLiteral(string(encoded)))
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		return p.wrap(err, "record the metadata on "+name)
	}
	return nil
}

// exec runs one statement on the host server.
func (p *Provider) exec(ctx context.Context, stmt string) error {
	db, err := p.open()
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	_, err = db.ExecContext(ctx, stmt)
	return err
}

// open returns a connection to the admin database.
func (p *Provider) open() (*sql.DB, error) {
	db, err := sql.Open("pgx", p.admin.Reveal())
	if err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFDB034,
			"variable", p.variable, "host", p.hostport, "detail", shortError(err))
	}
	// One connection. Everything this provider does is a single statement and
	// a pool would hold connections on somebody else's server for the life of
	// the process.
	db.SetMaxOpenConns(1)
	return db, nil
}

func (p *Provider) waitReady(ctx context.Context, conn secrets.Value) error {
	err := pgcopy.WaitReady(ctx, conn, readyTimeout, p.clock.Now, p.clock.Sleep)
	if err == nil || ctx.Err() != nil {
		return err
	}
	return aferrors.Wrap(err, aferrors.AFDB002, "host", p.hostport)
}

func (p *Provider) wrap(err error, doing string) error {
	return fmt.Errorf("db.pgurl: %s on %s: %w", doing, p.hostport, err)
}

// notOurs is the refusal a name collision produces.
func (p *Provider) notOurs(name string) error {
	return aferrors.Coded(aferrors.AFDB036, "database", name, "host", p.hostport)
}

// sizeOf is best effort: a size that cannot be read is a missing number in a
// listing, not a failed refresh.
func (p *Provider) sizeOf(ctx context.Context, name string) int64 {
	r, found, err := p.lookup(ctx, name)
	if err != nil || !found {
		return 0
	}
	return r.size
}

// describeGoldens names the versions that exist, for a message about one that
// does not. Best effort and never an error: this runs on a path that has
// already failed.
func (p *Provider) describeGoldens(ctx context.Context) string {
	versions, err := p.ListGoldens(ctx)
	if err != nil {
		return "the available versions could not be listed: " + err.Error()
	}
	if len(versions) == 0 {
		return "no golden versions exist on " + p.hostport
	}
	ids := make([]string, 0, len(versions))
	for _, v := range versions {
		ids = append(ids, v.ID)
	}
	if len(ids) > 5 {
		return fmt.Sprintf("%s and %d more", strings.Join(ids[:5], ", "), len(ids)-5)
	}
	return strings.Join(ids, ", ")
}

// urlFor is the admin URL pointing at another database on the same server.
func (p *Provider) urlFor(name string) secrets.Value {
	u, err := url.Parse(p.admin.Reveal())
	if err != nil {
		// Unreachable: New parsed it. Returning the admin URL unchanged would
		// hand somebody a connection to the wrong database, so this is empty
		// instead and the caller's connection fails saying so.
		return secrets.Value{}
	}
	u.Path = "/" + name
	return secrets.NewFrom(u.String(), "pgurl")
}

// nameOK is what a database name this provider creates may contain. It is
// checked before the name reaches a statement, because a name is the one part
// of these statements that cannot be a parameter.
var nameOK = regexp.MustCompile(`^af_[gbc]_[a-z0-9_]{1,57}$`)

func checkName(name string) error {
	if !nameOK.MatchString(name) {
		return fmt.Errorf("db.pgurl: %q is not a name this provider creates, so it will not be "+
			"created or dropped; this is a bug in the provider rather than in your configuration", name)
	}
	return nil
}

// branchName is derived from the environment identifier, so that a retry after
// a crash finds the same database without the journal being intact.
//
// Postgres identifiers are 63 bytes and an environment identifier is not this
// package's to shorten, so a long one is truncated and given a suffix from its
// own hash. Deterministic, because idempotency by environment depends on this
// function returning the same answer every time.
func branchName(envID string) (string, error) {
	if strings.TrimSpace(envID) == "" {
		return "", fmt.Errorf("db.pgurl: an environment identifier is required to name a branch")
	}
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return '_'
		}
	}, envID)
	const room = 63 - len(branchPrefix)
	if len(safe) > room {
		digest := fnv1a(envID)
		safe = safe[:room-9] + "_" + digest
	}
	return branchPrefix + safe, nil
}

// fnv1a is eight hex characters of a hash, used only to keep a truncated name
// distinct from another truncation of the same prefix.
func fnv1a(s string) string {
	const offset, prime = uint32(2166136261), uint32(16777619)
	h := offset
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= prime
	}
	return fmt.Sprintf("%08x", h)
}

// quoteIdent quotes an identifier the way Postgres does.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// quoteLiteral quotes a string literal. Correct under
// standard_conforming_strings, which comment sets on the session first.
func quoteLiteral(s string) string {
	return `'` + strings.ReplaceAll(s, `'`, `''`) + `'`
}

// shortError is the last line of a driver error, which is where the actual
// reason is, and never the connection string.
func shortError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
		msg = msg[i+1:]
	}
	return strings.TrimSpace(msg)
}

// HostPortOf is the host and port of a connection string, with no credential
// in it, or the empty string when the value is not a URL.
//
// It exists so that a command can PRINT where the goldens live. The value it
// reads is a connection string with a password in it, and af status prints
// this line, so the redaction is the point rather than a nicety.
func HostPortOf(v secrets.Value) string {
	_, hostport, err := normalize(v, DefaultVariable)
	if err != nil {
		return ""
	}
	return hostport
}

// normalize parses the admin URL, defaults its database, and returns the host
// and port with no credential in them for use in messages.
func normalize(raw secrets.Value, variable string) (secrets.Value, string, error) {
	u, err := url.Parse(raw.Reveal())
	if err != nil {
		return secrets.Value{}, "", aferrors.Coded(aferrors.AFDB024, "detail", shortError(err))
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return secrets.Value{}, "", aferrors.Coded(aferrors.AFDB024,
			"detail", fmt.Sprintf("the value of %s has scheme %q and has to be a postgres:// URL", variable, u.Scheme))
	}
	if u.Host == "" {
		return secrets.Value{}, "", aferrors.Coded(aferrors.AFDB024,
			"detail", fmt.Sprintf("the value of %s names no host", variable))
	}
	if strings.Trim(u.Path, "/") == "" {
		// The database CREATE DATABASE is issued FROM, which cannot be one of
		// the databases this provider creates and drops. Postgres ships this
		// one on every server.
		u.Path = "/postgres"
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "5432"
	}
	return secrets.NewFrom(u.String(), variable), net.JoinHostPort(host, port), nil
}
