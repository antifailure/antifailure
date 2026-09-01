package supabase

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/db/pgcopy"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// Branch name prefixes.
//
// The prefix, and not the annotation, is what says whether a golden is
// published, for the same reason it is on Neon: the attestation does not exist
// until the candidate has been masked and scanned, which is after the branch was
// created, and a rename is the one atomic thing available at that moment.
//
// Unlike Neon, the exact identifier fits in the name. Supabase accepts
// underscores and long names, so af-gv- plus a golden version is the version
// verbatim and reversibly, which matters because a listing is the only place a
// version can be recovered from.
const (
	PrefixCandidate = "af-cand-"
	PrefixGolden    = "af-gv-"
	PrefixEnv       = "af-env-"
)

// MetaSchema is where the attestation is written inside the golden itself.
//
// In the database rather than beside it, because a verification statement is
// about that data and should travel with it. A branch restored from a golden
// carries the row, so anybody holding an environment can read what was scanned
// and what was found without asking this process or the Supabase API.
const MetaSchema = "_antifailure"

// annotationPrefix marks the git_branch field as ours.
//
// Supabase branches have no annotations, which is the one place its API is
// poorer than Neon's for this purpose: a listing returns a name and nothing
// else that a caller can write. git_branch is a free text field that is
// accepted on create, returned by the listing, updatable by PATCH and untouched
// by a rename, which makes it the only durable per branch metadata available.
// It is used for the facts a name cannot carry: which golden an environment
// came from, and which masking rules produced a golden.
//
// The value is deliberately not a plausible git branch name. A project that is
// later connected to GitHub matches branches by this field, and a value like
// "main" would hand our environment to somebody's pull request.
const annotationPrefix = "antifailure/"

// candidateMaxAge is the age past which a candidate can only be an orphan.
//
// A candidate exists for the minute between creating a branch and publishing
// it, and nothing ever branches from one, so removing an old one is
// unconditionally safe. That is what lets the provider heal itself rather than
// wait for somebody to read a leak report. It matters more here than on Neon:
// an orphaned Supabase branch is a running project billed by the hour.
const candidateMaxAge = 2 * time.Hour

// readyTimeout is how long a new branch has to answer a query once Supabase has
// called its project healthy.
const readyTimeout = 180 * time.Second

// Provider is the Supabase implementation of provider.Database.
type Provider struct {
	client       *Client
	clock        clock.Clock
	seedSQL      string
	maxBranches  int
	engine       string
	region       string
	instanceSize string
}

// Options configure a provider.
type Options struct {
	// Token is a Supabase Management API personal access token. Required.
	Token secrets.Value
	// ProjectRef is the Supabase project branches are created in. Required:
	// this provider does not create projects, because a project is a billing
	// boundary and creating one on somebody's behalf is not this tool's call.
	ProjectRef string
	// BaseURL overrides Supabase's API root, for a test server.
	BaseURL string
	Clock   clock.Clock
	// SeedSQL is applied to a candidate when no source database is configured.
	SeedSQL string
	// Version is the Postgres major version to ask branches for.
	Version int
	// Region and InstanceSize default to the parent project's when empty.
	Region       string
	InstanceSize string
	// MaxBranches is the concurrent branch limit. Zero means unlimited. It is
	// configuration rather than something read from the API, because the limit
	// is a property of the plan and Supabase does not report it anywhere this
	// provider can rely on.
	MaxBranches int
	// PollInterval and PollTimeout bound waiting for a branch to come up.
	PollInterval time.Duration
	PollTimeout  time.Duration
}

// New builds a provider.
func New(opts Options) (*Provider, error) {
	if opts.Token.IsZero() {
		return nil, errors.New("db.supabase: a Management API token is required")
	}
	if opts.ProjectRef == "" {
		return nil, errors.New("db.supabase: a project reference is required")
	}
	c := opts.Clock
	if c == nil {
		c = clock.New()
	}
	engine := ""
	if opts.Version > 0 {
		engine = strconv.Itoa(opts.Version)
	}
	return &Provider{
		client: &Client{
			BaseURL:      opts.BaseURL,
			Key:          opts.Token,
			ProjectRef:   opts.ProjectRef,
			Sleep:        c.Sleep,
			PollInterval: opts.PollInterval,
			PollTimeout:  opts.PollTimeout,
		},
		clock:        c,
		seedSQL:      opts.SeedSQL,
		maxBranches:  opts.MaxBranches,
		engine:       engine,
		region:       opts.Region,
		instanceSize: opts.InstanceSize,
	}, nil
}

// Name identifies the provider in a manifest.
func (p *Provider) Name() string { return "supabase" }

// Capabilities describes what Supabase can do.
func (p *Provider) Capabilities() provider.Caps {
	return provider.Caps{
		Branching: true,
		// Reset is real and is this provider's own: Supabase's branch reset
		// returns a branch to its migration history, which is not the golden's
		// state and would throw away the data an environment was given. Reset
		// here empties the branch and restores the golden, which is the same
		// path a first branch takes, sequences included because pg_restore
		// carries their values.
		Reset: true,
		// A branch is a separate project with its own storage, so branch time
		// is the time to copy the golden's rows and grows with the database.
		// Declaring this true because branching is fast would be declaring the
		// wrong reason: Supabase branches are quick to create and the copy is
		// not free.
		CopyOnWrite: false,
		// The engine's rules are the single implementation of masking, and a
		// provider's masking is a claim where verification is a check.
		ProviderMasking:       false,
		PooledEndpoints:       true,
		MaxConcurrentBranches: p.maxBranches,
		// Measured against the real API: a branch reports healthy in about six
		// seconds and answers a query in about nine, and then the golden has to
		// be copied into it. Generous enough for a cold start across the public
		// internet, tight enough that a hung call fails a test rather than a
		// job.
		ExpectedBranchLatency: 90 * time.Second,
		// Supabase offers 15 and 17 as branch engines. Asking for anything else
		// is refused here rather than by the API, so the message names the
		// versions that would work.
		SupportedVersions: []int{15, 17},
	}
}

// Close releases nothing: the client holds no pool of its own.
func (p *Provider) Close() error { return nil }

// ---------------------------------------------------------------------------
// Goldens
// ---------------------------------------------------------------------------

// RefreshGolden builds a new masked, verified golden version.
func (p *Provider) RefreshGolden(ctx context.Context, spec provider.GoldenSpec) (provider.GoldenVersion, error) {
	if !p.Capabilities().Supports(spec.Version) {
		return provider.GoldenVersion{}, aferrors.Coded(aferrors.AFDB003,
			"found", strconv.Itoa(spec.Version),
			"supported", joinInts(p.Capabilities().SupportedVersions))
	}

	p.sweepCandidates(ctx)

	version := provider.NewGoldenVersionID(p.clock.Now(), spec.RulesHash)
	created := p.clock.Now().UTC()

	candidate, err := p.client.CreateBranch(ctx, CreateBranchRequest{
		Name:           PrefixCandidate + version,
		Region:         p.region,
		InstanceSize:   p.instanceSize,
		PostgresEngine: p.engine,
		// Written at create, not at publish. Both facts are known before the
		// branch exists, and an annotation that has to be added afterwards is
		// one more call that can fail between verifying a golden and being able
		// to say which rules produced it.
		Annotation: annotation{
			Rules: spec.RulesHash, Provenance: spec.Provenance, CreatedAt: created,
		},
	})
	if err != nil {
		return provider.GoldenVersion{}, err
	}

	// Removed unless the refresh gets all the way to the rename. A failed
	// verification that left a branchable copy of unmasked production behind
	// would be the worst possible outcome here, and on Supabase it would also
	// be a running project nobody is watching.
	published := false
	defer func() {
		if !published {
			_ = p.client.DeleteBranch(context.WithoutCancel(ctx), candidate.ID)
		}
	}()

	detail, err := p.client.WaitReady(ctx, candidate.ID)
	if err != nil {
		return provider.GoldenVersion{}, err
	}
	conn := connString(detail)
	if err := p.waitAnswering(ctx, conn); err != nil {
		return provider.GoldenVersion{}, err
	}
	if err := p.loadSource(ctx, conn, spec); err != nil {
		return provider.GoldenVersion{}, err
	}

	if spec.Mask != nil {
		if err := spec.Mask(ctx, conn); err != nil {
			return provider.GoldenVersion{}, fmt.Errorf("db.supabase: mask the golden candidate: %w", err)
		}
	}
	attestation := ""
	if spec.Verify != nil {
		attestation, err = spec.Verify(ctx, conn)
		if err != nil {
			return provider.GoldenVersion{}, fmt.Errorf("db.supabase: verify the golden candidate: %w", err)
		}
	}

	if err := p.writeMeta(ctx, conn, version, spec.RulesHash, attestation, created); err != nil {
		return provider.GoldenVersion{}, err
	}

	// The publish. Everything above can fail and leave nothing branchable;
	// after this the version exists.
	if err := p.client.Rename(ctx, candidate.ID, PrefixGolden+version); err != nil {
		return provider.GoldenVersion{}, fmt.Errorf("db.supabase: publish the golden: %w", err)
	}
	// Set before the wait below, not after. The rename is what publishes, so
	// from here the golden exists and the deferred cleanup must not remove it,
	// whatever happens next.
	published = true

	gv := provider.GoldenVersion{
		ID:          version,
		CreatedAt:   created,
		RulesHash:   spec.RulesHash,
		Provenance:  spec.Provenance,
		Verified:    spec.Verify != nil,
		Attestation: attestation,
		ProviderRef: candidate.ID,
	}

	// Returned only once the listing agrees the golden is there. Supabase
	// acknowledges a rename before its branch listing reflects it, and a caller
	// that branched in that window was told its golden was unverified. Returned
	// WITH the version on failure rather than without, so a caller that has to
	// give up still holds the identifier it needs to destroy what was made.
	if err := p.client.WaitNamed(ctx, PrefixGolden+version); err != nil {
		return gv, err
	}
	return gv, nil
}

// ListGoldens returns published versions, newest first.
func (p *Provider) ListGoldens(ctx context.Context) ([]provider.GoldenVersion, error) {
	branches, err := p.client.ListBranches(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]provider.GoldenVersion, 0, len(branches))
	for _, b := range branches {
		if !b.IsOurs() || !strings.HasPrefix(b.Name, PrefixGolden) {
			continue
		}
		ann := parseAnnotation(b.GitBranch)
		gv := provider.GoldenVersion{
			ID:          strings.TrimPrefix(b.Name, PrefixGolden),
			RulesHash:   ann.Rules,
			Provenance:  ann.Provenance,
			ProviderRef: b.ID,
			// A branch carries the golden prefix only because a refresh renamed
			// it after verification returned without an error. The attestation
			// itself is in the branch's own database, because it did not exist
			// when the branch was created.
			Verified:  true,
			CreatedAt: b.CreatedAt,
		}
		if !ann.CreatedAt.IsZero() {
			gv.CreatedAt = ann.CreatedAt
		}
		out = append(out, gv)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// DestroyGolden removes a version. Removing one that does not exist succeeds.
func (p *Provider) DestroyGolden(ctx context.Context, version string) error {
	branches, err := p.client.ListBranches(ctx)
	if err != nil {
		return err
	}
	golden, ok := findNamed(branches, PrefixGolden+version)
	if !ok {
		return nil
	}

	// Refused while anything came from it. Supabase would not refuse, because
	// a branch of a golden is not its child here: an environment is an
	// independent database that the golden's rows were copied into, and the
	// platform has no idea they are related. So the refusal is entirely this
	// provider's, and without it deleting a golden would silently orphan every
	// environment's ability to reset.
	referencing := 0
	for _, b := range branches {
		if !b.IsOurs() || !strings.HasPrefix(b.Name, PrefixEnv) {
			continue
		}
		if parseAnnotation(b.GitBranch).From == version {
			referencing++
		}
	}
	if referencing > 0 {
		return aferrors.Coded(aferrors.AFDB005,
			"version", version, "count", strconv.Itoa(referencing))
	}
	return p.client.DeleteBranch(ctx, golden.ID)
}

// ---------------------------------------------------------------------------
// Branches
// ---------------------------------------------------------------------------

// Branch creates a database for an environment from a golden version.
func (p *Provider) Branch(ctx context.Context, version, envID string) (provider.Branch, error) {
	branches, err := p.client.ListBranches(ctx)
	if err != nil {
		return provider.Branch{}, err
	}

	name := PrefixEnv + branchSafe(envID)

	// Idempotent by environment. The engine retries after a timeout, and a
	// retry that creates a second branch is how an orphan is made, which here
	// is a second running project billed by the hour.
	if existing, ok := findNamed(branches, name); ok {
		b := provider.Branch{
			EnvID:       envID,
			From:        parseAnnotation(existing.GitBranch).From,
			ProviderRef: existing.ID,
			CreatedAt:   existing.CreatedAt,
		}
		if b.From == "" {
			b.From = version
		}
		// Returning it is not enough. Creating a branch and filling it are two
		// calls, and the retry that reaches this line is most likely a retry
		// because the second one failed. Handing back the branch without
		// looking would hand back an EMPTY database, and an environment whose
		// database is empty because a copy failed silently is the failure this
		// whole product exists to make impossible.
		filled, err := p.holdsVersion(ctx, b, b.From)
		if err != nil {
			// Not knowing is reported rather than guessed. Refilling on a bad
			// answer would empty a branch an application has been writing to,
			// and returning it would be the silent gap above.
			return b, err
		}
		if filled {
			return b, nil
		}
		if err := p.fill(ctx, b, version); err != nil {
			return b, err
		}
		return b, nil
	}

	if _, ok := findNamed(branches, PrefixGolden+version); !ok {
		// A candidate under this version means the refusal is about masking
		// rather than about a missing version, so the operator is told which of
		// the two problems they have. Both claims are strong enough to be worth
		// one more look first: Supabase acknowledges a rename before its
		// listing shows it, and publishing a golden IS a rename, so a stale
		// listing here would tell somebody their verified golden had failed
		// verification.
		fresh, refreshErr := p.client.ListBranches(ctx)
		if refreshErr == nil {
			branches = fresh
		}
		if _, ok := findNamed(branches, PrefixGolden+version); !ok {
			if _, unpublished := findNamed(branches, PrefixCandidate+version); unpublished {
				return provider.Branch{}, aferrors.Coded(aferrors.AFMSK001, "version", version)
			}
			return provider.Branch{}, aferrors.Coded(aferrors.AFDB004, "version", version)
		}
	}

	if p.maxBranches > 0 {
		live := 0
		for _, b := range branches {
			if b.IsOurs() && strings.HasPrefix(b.Name, PrefixEnv) {
				live++
			}
		}
		if live >= p.maxBranches {
			return provider.Branch{}, aferrors.Coded(aferrors.AFDB006,
				"limit", strconv.Itoa(p.maxBranches))
		}
	}

	created, err := p.client.CreateBranch(ctx, CreateBranchRequest{
		Name:           name,
		Region:         p.region,
		InstanceSize:   p.instanceSize,
		PostgresEngine: p.engine,
		Annotation:     annotation{From: version, EnvID: envID},
	})
	if err != nil {
		if Conflict(err) {
			// Somebody else won the race between the listing above and this
			// create. Their branch is the one that exists, and returning it is
			// what makes this idempotent rather than merely usually idempotent.
			if again, listErr := p.client.ListBranches(ctx); listErr == nil {
				if existing, ok := findNamed(again, name); ok {
					return provider.Branch{
						EnvID:       envID,
						From:        parseAnnotation(existing.GitBranch).From,
						ProviderRef: existing.ID,
						CreatedAt:   existing.CreatedAt,
					}, nil
				}
			}
		}
		// Left in place rather than cleaned up. Inventory reports it, so the
		// leak detector can see it; deleting it here would hide the evidence.
		return provider.Branch{}, err
	}

	b := provider.Branch{
		EnvID: envID, From: version, ProviderRef: created.ID, CreatedAt: created.CreatedAt,
	}
	if err := p.fill(ctx, b, version); err != nil {
		// The branch exists and is empty. It is reported by Inventory and it
		// carries the environment's identity, so the caller can destroy it and
		// the leak detector can see it if the caller does not.
		return b, err
	}
	return b, nil
}

// holdsVersion reports whether a branch already holds the golden it should.
//
// The golden writes its own version into _antifailure.golden before it is
// published, and a copy carries that schema along with everything else, so an
// environment branch that was filled says which version filled it. That makes
// "was this branch ever filled" a question with an answer, rather than
// something a caller has to assume.
func (p *Provider) holdsVersion(ctx context.Context, b provider.Branch, version string) (bool, error) {
	conn, err := p.ConnString(ctx, b, provider.ConnDirect)
	if err != nil {
		return false, err
	}
	db, err := sql.Open("pgx", conn.Reveal())
	if err != nil {
		return false, fmt.Errorf("db.supabase: open branch %s: %w", b.ProviderRef, err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	var present bool
	err = db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM `+MetaSchema+`.golden WHERE version = $1
		)`, version).Scan(&present)
	if err != nil {
		// The table is missing on a branch that was created and never filled,
		// which is the case this function exists to detect, so it is an answer
		// rather than a failure.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && (pgErr.Code == undefinedTable || pgErr.Code == invalidSchemaName) {
			return false, nil
		}
		return false, fmt.Errorf("db.supabase: ask branch %s which golden filled it: %w",
			b.ProviderRef, err)
	}
	return present, nil
}

// Postgres error codes for "that table is not there" and "that schema is not
// there", which are the two shapes of a branch that was never filled.
const (
	undefinedTable    = "42P01"
	invalidSchemaName = "3F000"
)

// fill copies a golden into a branch, which is what makes it an environment's
// database rather than an empty project.
func (p *Provider) fill(ctx context.Context, b provider.Branch, version string) error {
	target, err := p.branchConn(ctx, b.ProviderRef)
	if err != nil {
		return err
	}
	if err := p.waitAnswering(ctx, target); err != nil {
		return err
	}
	golden, err := p.goldenConn(ctx, version)
	if err != nil {
		return err
	}
	return p.restore(ctx, golden, target)
}

// Reset returns a branch to its golden's state, sequences included.
func (p *Provider) Reset(ctx context.Context, b provider.Branch) error {
	version := b.From
	if version == "" {
		// A caller that has been through a process restart may hold a branch it
		// read back from the journal, and af up and af test are separate
		// processes. The annotation is the durable copy of the same fact, so it
		// answers when the caller cannot.
		branches, err := p.client.ListBranches(ctx)
		if err != nil {
			return err
		}
		for _, existing := range branches {
			if existing.ID == b.ProviderRef {
				version = parseAnnotation(existing.GitBranch).From
			}
		}
	}
	if version == "" {
		// Neither the caller nor the branch itself knows which golden this came
		// from, so there is nothing to reset it to. Naming the branch rather
		// than a version nobody has, because the branch is the thing the
		// operator can actually go and look at.
		return aferrors.Coded(aferrors.AFDB004,
			"version", "the one branch "+b.ProviderRef+" was created from, which it does not record")
	}
	return p.fill(ctx, b, version)
}

// Destroy removes a branch. Removing one that is already gone succeeds.
func (p *Provider) Destroy(ctx context.Context, b provider.Branch) error {
	if b.ProviderRef == "" {
		return nil
	}
	return p.client.DeleteBranch(ctx, b.ProviderRef)
}

// ConnString returns a connection string for a branch, as a secret.
func (p *Provider) ConnString(ctx context.Context, b provider.Branch, mode provider.ConnMode) (secrets.Value, error) {
	detail, err := p.client.GetDetail(ctx, b.ProviderRef)
	if err != nil {
		return secrets.Value{}, err
	}
	if mode != provider.ConnPooled {
		return connString(detail), nil
	}
	entries, err := p.client.PoolerConfig(ctx, detail.Ref)
	if err != nil {
		return secrets.Value{}, err
	}
	pooler, ok := primaryPooler(entries)
	if !ok {
		return secrets.Value{}, fmt.Errorf(
			"db.supabase: branch %s reports no primary pooler, so there is no pooled "+
				"connection string to hand out", b.ProviderRef)
	}
	return pooledConnString(pooler, detail.DBPass), nil
}

// Inventory lists everything this provider holds, which the leak detector
// compares against the journal.
func (p *Provider) Inventory(ctx context.Context) ([]provider.Resource, error) {
	branches, err := p.client.ListBranches(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]provider.Resource, 0, len(branches))
	for _, b := range branches {
		// Not ours. A Supabase project holds branches somebody made by hand,
		// and the default branch, which stands for production itself. Reporting
		// those as leaks is how a leak report becomes something people learn to
		// ignore, and acting on them would be very much worse.
		if !b.IsOurs() {
			continue
		}
		kind := ""
		switch {
		case strings.HasPrefix(b.Name, PrefixGolden):
			kind = "golden"
		case strings.HasPrefix(b.Name, PrefixCandidate):
			kind = "candidate"
		default:
			kind = "branch"
		}
		ann := parseAnnotation(b.GitBranch)
		out = append(out, provider.Resource{
			Kind:      kind,
			ID:        b.ID,
			EnvID:     ann.EnvID,
			CreatedAt: b.CreatedAt,
			Labels: map[string]string{
				"name":    b.Name,
				"ref":     b.ProjectRef,
				"from":    ann.From,
				"state":   b.PreviewStatus,
				"stage":   b.Status,
				"version": strings.TrimPrefix(b.Name, PrefixGolden),
			},
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Health reports whether a branch is reachable.
func (p *Provider) Health(ctx context.Context, b provider.Branch) (provider.Health, error) {
	start := p.clock.Now()
	conn, err := p.ConnString(ctx, b, provider.ConnDirect)
	if err != nil {
		// A branch that is gone is unreachable, not an error. Teardown checks
		// health, and erroring here would make a successful teardown look like
		// a failure.
		return provider.Health{Reachable: false, Detail: "no connection string: " + err.Error()}, nil
	}
	attempt, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := pgcopy.Ping(attempt, conn); err != nil {
		return provider.Health{Reachable: false, Detail: err.Error()}, nil
	}
	return provider.Health{Reachable: true, Latency: p.clock.Now().Sub(start)}, nil
}

// ---------------------------------------------------------------------------
// Internals
// ---------------------------------------------------------------------------

func (p *Provider) waitAnswering(ctx context.Context, conn secrets.Value) error {
	err := pgcopy.WaitReady(ctx, conn, readyTimeout, p.clock.Now, p.clock.Sleep)
	if err == nil || ctx.Err() != nil {
		return err
	}
	return aferrors.Wrap(err, aferrors.AFDB002, "host", "supabase")
}

func (p *Provider) branchConn(ctx context.Context, id string) (secrets.Value, error) {
	detail, err := p.client.WaitReady(ctx, id)
	if err != nil {
		return secrets.Value{}, err
	}
	return connString(detail), nil
}

func (p *Provider) goldenConn(ctx context.Context, version string) (secrets.Value, error) {
	branches, err := p.client.ListBranches(ctx)
	if err != nil {
		return secrets.Value{}, err
	}
	golden, ok := findNamed(branches, PrefixGolden+version)
	if !ok {
		return secrets.Value{}, aferrors.Coded(aferrors.AFDB004, "version", version)
	}
	return p.branchConn(ctx, golden.ID)
}

func (p *Provider) loadSource(ctx context.Context, target secrets.Value, spec provider.GoldenSpec) error {
	if spec.SourceURL.IsZero() || strings.Contains(spec.SourceURL.Reveal(), "@source/") {
		// No real source. A project that has not connected production yet still
		// needs a schema to branch, and the seed is what provides it.
		return pgcopy.Exec(ctx, target, p.seedSQL)
	}
	return p.restore(ctx, spec.SourceURL, target)
}

// writeMeta records what was verified, inside the golden itself.
func (p *Provider) writeMeta(ctx context.Context, conn secrets.Value, version, rules, attestation string, at time.Time) error {
	db, err := sql.Open("pgx", conn.Reveal())
	if err != nil {
		return fmt.Errorf("db.supabase: open the candidate: %w", err)
	}
	defer func() { _ = db.Close() }()

	script := `
		CREATE SCHEMA IF NOT EXISTS ` + MetaSchema + `;
		CREATE TABLE IF NOT EXISTS ` + MetaSchema + `.golden (
			version     text PRIMARY KEY,
			rules_hash  text NOT NULL,
			attestation text NOT NULL,
			created_at  timestamptz NOT NULL
		);`
	if _, err := db.ExecContext(ctx, script); err != nil {
		return fmt.Errorf("db.supabase: create the golden metadata table: %w", err)
	}
	_, err = db.ExecContext(ctx,
		`INSERT INTO `+MetaSchema+`.golden (version, rules_hash, attestation, created_at)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (version) DO UPDATE SET
		   rules_hash = EXCLUDED.rules_hash,
		   attestation = EXCLUDED.attestation,
		   created_at = EXCLUDED.created_at`,
		version, rules, attestation, at)
	if err != nil {
		return fmt.Errorf("db.supabase: record the attestation: %w", err)
	}
	return nil
}

// Attestation reads back what a golden was verified as holding.
//
// Not part of the provider interface, because reading it means connecting to
// the golden and no caller on the hot path needs it. It is here so that the
// claim in a listing can be checked against the golden itself.
func (p *Provider) Attestation(ctx context.Context, version string) (string, error) {
	conn, err := p.goldenConn(ctx, version)
	if err != nil {
		return "", err
	}
	db, err := sql.Open("pgx", conn.Reveal())
	if err != nil {
		return "", fmt.Errorf("db.supabase: open the golden: %w", err)
	}
	defer func() { _ = db.Close() }()

	var attestation string
	err = db.QueryRowContext(ctx,
		`SELECT attestation FROM `+MetaSchema+`.golden WHERE version = $1`, version).Scan(&attestation)
	if err != nil {
		return "", fmt.Errorf("db.supabase: read the attestation for %s: %w", version, err)
	}
	return attestation, nil
}

// sweepCandidates removes candidates old enough that they can only be orphans.
func (p *Provider) sweepCandidates(ctx context.Context) {
	branches, err := p.client.ListBranches(ctx)
	if err != nil {
		return
	}
	cutoff := p.clock.Now().Add(-candidateMaxAge)
	for _, b := range branches {
		if !b.IsOurs() || !strings.HasPrefix(b.Name, PrefixCandidate) {
			continue
		}
		if b.CreatedAt.After(cutoff) {
			continue
		}
		_ = p.client.DeleteBranch(ctx, b.ID)
	}
}

func findNamed(branches []Branch, name string) (Branch, bool) {
	for _, b := range branches {
		if b.IsOurs() && b.Name == name {
			return b, true
		}
	}
	return Branch{}, false
}

// branchSafe turns an identifier into something Supabase accepts as a branch
// name and a human can still read.
//
// Unlike Neon's equivalent, underscores survive, which is why a golden version
// round trips through a name exactly. Environment identifiers are compared
// rather than reversed, so a character this drops costs nothing as long as the
// mapping is stable.
func branchSafe(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	// Long enough for any identifier the engine generates, short enough to stay
	// inside what the API accepted when this was measured.
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

func joinInts(vs []int) string {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ", ")
}

// annotation is the metadata a branch carries in its git_branch field.
type annotation struct {
	From       string
	EnvID      string
	Rules      string
	Provenance string
	CreatedAt  time.Time
}

// String encodes an annotation as one line of key=value pairs.
//
// Percent encoded, so that a value containing a separator cannot forge a second
// field. The values this provider writes are identifiers and hashes and would
// survive naive concatenation; the encoding is here because the next value
// somebody adds might not.
func (a annotation) String() string {
	parts := make([]string, 0, 5)
	add := func(k, v string) {
		if v != "" {
			parts = append(parts, k+"="+url.QueryEscape(v))
		}
	}
	add("from", a.From)
	add("env", a.EnvID)
	add("rules", a.Rules)
	add("project", a.Provenance)
	if !a.CreatedAt.IsZero() {
		add("created", a.CreatedAt.UTC().Format(time.RFC3339))
	}
	return annotationPrefix + strings.Join(parts, ";")
}

// parseAnnotation reads back what String wrote, and returns an empty annotation
// for anything it did not write.
//
// A branch whose git_branch names a real git branch belongs to somebody's pull
// request, not to us, and reading it as metadata would invent a relationship
// that does not exist.
func parseAnnotation(s string) annotation {
	if !strings.HasPrefix(s, annotationPrefix) {
		return annotation{}
	}
	var a annotation
	for _, part := range strings.Split(strings.TrimPrefix(s, annotationPrefix), ";") {
		key, raw, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		value, err := url.QueryUnescape(raw)
		if err != nil {
			continue
		}
		switch key {
		case "from":
			a.From = value
		case "env":
			a.EnvID = value
		case "rules":
			a.Rules = value
		case "project":
			a.Provenance = value
		case "created":
			if at, err := time.Parse(time.RFC3339, value); err == nil {
				a.CreatedAt = at
			}
		}
	}
	return a
}
