// Package xata is the database provider for Xata.
//
// Of the thirteen managed Postgres vendors in engine/internal/db/managed, Xata
// is the only one whose branching is really branching. Its branching page, read
// on 2026-09-12, says "creating a child branch copies the parent's schema and
// data using a Copy-on-Write storage snapshot, so it completes in seconds even
// for terabyte-scale databases", and its platform is built on CloudNativePG,
// which the same vendor's open source repository and the API document's own
// BranchStatus both name. Every other vendor on that row calls the operation a
// fork and restores a backup, where the clock grows with the data.
//
// So this provider declares Caps.CopyOnWrite TRUE, and what that declaration is
// worth is recorded where a customer facing surface reads it:
// engine/conformance/ledger.go records it as UNPROVEN.
//
// The reason is the harness rather than the provider. The suite in
// conformance_test.go drives a fake Xata control plane over a real local
// Postgres, which proves the provider's logic, its request shapes and its error
// mapping. It cannot exhibit copy on write, because the only way one local
// Postgres can hand back a branch carrying the golden's data is to copy it, so
// that suite does not assert a real service and the copy on write behaviour
// answers unproven rather than timing a copy. TestConformanceAgainstXata runs
// the same suite against the real service, asserting it, and is the run that
// settles the declaration. No account was used to write this file.
//
// THE MODEL, which is Neon's and is not a coincidence.
//
// A Xata project holds the production database on its root branch. A golden is
// a copy on write branch of that root, masked and verified in place and then
// published by a rename. An environment's database is a copy on write branch of
// the golden. Nothing is copied by this provider, which is the whole point of
// choosing a vendor whose branches share storage.
//
// Two consequences are declared rather than implied. Caps.Subsetting is FALSE,
// because a candidate here holds the whole database the moment it exists and
// there is no empty database to load a slice into; subsetting would have to
// mean deleting down, which copies everything first. And Caps.Reset is FALSE,
// for the reason Capabilities gives.
package xata

import (
	"context"
	"database/sql"
	"fmt"
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

// Branch name prefixes, and the reason publishing is a rename.
//
// Xata's branch object carries no annotation map, so the name is where the kind
// and the identifier live. The prefix, not any field, is what says whether a
// golden is published, because the attestation does not exist until the
// candidate has been masked and scanned, which is after the branch was created.
// The rename is the one atomic thing available at the right moment.
const (
	PrefixCandidate = "af-cand-"
	PrefixGolden    = "af-gv-"
	PrefixEnv       = "af-env-"
)

// MetaSchema is where the attestation is written, inside the golden itself.
//
// In the database rather than beside it, because a verification statement is
// about that data and should travel with it. A copy on write branch of a golden
// inherits the row for free, so anybody holding an environment can read what was
// scanned and what was found without asking this process.
//
// It is also the only place it can go whole. Xata's branch object has no
// annotation map, and its one free text field, description, is limited by the
// document to 255 characters of letters, digits, spaces and - _ . / :, which
// holds a golden version identifier and cannot hold an attestation.
const MetaSchema = "_antifailure"

// readyTimeout is how long a branch has to answer a query once Xata says it is
// ready. Separate from the client's own poll timeout, which bounds how long the
// control plane has to report the branch ready in the first place.
const readyTimeout = 120 * time.Second

// candidateMaxAge is the age past which a candidate can only be an orphan.
//
// A candidate exists for the minutes between creating a branch and publishing
// it, and nothing ever branches from one, so removing an old one is
// unconditionally safe. That is what lets the provider heal itself rather than
// wait for somebody to read a leak report.
const candidateMaxAge = 2 * time.Hour

// Provider is the Xata implementation of provider.Database.
type Provider struct {
	client      *Client
	clock       clock.Clock
	seedSQL     string
	parentName  string
	maxBranches int
}

// Options configure a provider.
type Options struct {
	// APIKey is a Xata API key, sent as a bearer token. Required.
	APIKey secrets.Value
	// OrgID and ProjectID address the project branches are created in. Both
	// required: this provider does not create projects, because a project is a
	// billing boundary and creating one on somebody's behalf is not this
	// tool's call.
	OrgID     string
	ProjectID string
	// ParentBranch is the branch goldens are taken from, which is the one
	// holding production. Empty means the project's own root, which is the
	// branch with no parent.
	ParentBranch string
	// BaseURL overrides Xata's API root, for a test server.
	BaseURL string
	Clock   clock.Clock
	// SeedSQL is applied to a candidate when no source database is configured.
	SeedSQL string
	// MaxBranches is the plan's concurrent branch limit. Zero means unlimited.
	// Configuration rather than something read from the API, because the limit
	// is a property of the plan and this provider has no verified endpoint that
	// reports it.
	MaxBranches int
	// PollInterval and PollTimeout bound waiting for a branch to become ready.
	PollInterval time.Duration
	PollTimeout  time.Duration
	// ProgressEvery is how often a wait for a branch that is still going says
	// so. Zero means thirty seconds.
	ProgressEvery time.Duration
}

// New builds a provider.
//
// It validates and returns. Unlike the pgurl provider it does NOT reach the
// service first, and the difference is which failures are decidable in one
// round trip: pgurl can ask a Postgres whether its role may create databases,
// where a Xata key's scopes are only visible in the answer to a real call. A
// probe that proved only that the key parses would be a check that cannot say
// no.
func New(opts Options) (*Provider, error) {
	if opts.APIKey.IsZero() {
		return nil, aferrors.Coded(aferrors.AFSEC001,
			"names", DefaultAPIKeyVariable, "sources", "the manifest")
	}
	if opts.OrgID == "" || opts.ProjectID == "" {
		return nil, aferrors.Coded(aferrors.AFMAN002,
			"path", "antifailure.yaml",
			"detail", "database.provider is xata and database.project is not "+
				"'<organization>/<project>'; both identifiers are path segments of every "+
				"call this provider makes and neither can be discovered from the other")
	}
	c := opts.Clock
	if c == nil {
		c = clock.New()
	}
	return &Provider{
		client: &Client{
			BaseURL:       opts.BaseURL,
			Key:           opts.APIKey,
			OrgID:         opts.OrgID,
			ProjectID:     opts.ProjectID,
			Sleep:         c.Sleep,
			PollInterval:  opts.PollInterval,
			PollTimeout:   opts.PollTimeout,
			ProgressEvery: opts.ProgressEvery,
		},
		clock:       c,
		seedSQL:     opts.SeedSQL,
		parentName:  opts.ParentBranch,
		maxBranches: opts.MaxBranches,
	}, nil
}

// DefaultAPIKeyVariable is the variable this provider reads when the manifest
// names none.
const DefaultAPIKeyVariable = "XATA_API_KEY"

// Name identifies the provider in a manifest.
func (p *Provider) Name() string { return "xata" }

// Capabilities describes what Xata can do.
func (p *Provider) Capabilities() provider.Caps {
	return provider.Caps{
		Branching: true,
		// FALSE, and it is the honest answer rather than a gap. The one restore
		// the API document has, POST .../branches/{branchID}/restore, is
		// "Create a new branch from a backup of another branch" and is marked
		// internal: it makes a NEW branch, it does not return an existing one
		// to another branch's state. Reset implemented as a delete and a
		// recreate would hand back a different branch on a different
		// connection string, which is not what the caller asked for, and the
		// suite skips the behaviour naming the missing capability rather than
		// passing it silently.
		Reset: false,
		// TRUE. See this file's header for what that is worth today and what
		// has not been run against it.
		CopyOnWrite: true,
		// Xata sells anonymized clones of its own. This provider does not use
		// them: the engine's rules are the single implementation of masking,
		// and a provider's masking is a claim where verification is a check.
		ProviderMasking: false,
		// A candidate is a copy on write branch of the project's parent, so it
		// holds the whole database the moment it exists and there is no empty
		// database to load a slice into. Subsetting would have to mean deleting
		// down, which copies everything first and so saves nothing on the one
		// kind of provider where the copy was already free.
		Subsetting: false,
		// FALSE, and not because Xata has no pooler. The API document's
		// EndpointType includes pooled_rw, "encoded as the hostname suffix in
		// the connection string". But the credentials endpoint takes no
		// endpoint type and returns one connection string, so a pooled string
		// could only be made by rewriting that hostname by a convention, and a
		// provider that built addresses from a convention nobody here could
		// check against an account would be handing out guesses as endpoints.
		PooledEndpoints:       false,
		MaxConcurrentBranches: p.maxBranches,
		// Generous, because it crosses the public internet to a branch that may
		// be waking from scale to zero, and Xata's own figure is "seconds".
		// Tight enough that a branch which stopped being flat fails here rather
		// than degrading quietly, which is the assertion the field exists for.
		ExpectedBranchLatency: 60 * time.Second,
		// The majors a manifest may ask for, and NOT a claim about Xata. The API
		// document publishes the available images per organization, each with
		// its own majorVersion, and a candidate inherits its parent's image, so
		// the answer that decides is the major the project's root branch really
		// runs. RefreshGolden asks the candidate's server for it and refuses a
		// mismatch with AF-DB-003, the way the pgurl provider does, so this list
		// only bounds what reaches that question.
		SupportedVersions: []int{14, 15, 16, 17, 18},
	}
}

// ReportProgressTo is provider.ProgressReporting, and the engine calls it once,
// after opening the provider, with the function that publishes a line as an
// engine.progress event and prints it.
//
// It exists because a Xata branch is created asynchronously and this provider
// waits for it to report ready for up to five minutes, once for every golden
// candidate and once for every environment branch, and until now that wait was
// silent: a first af up sat on a line that did not move for as long as Xata took.
// The lines are written by the client's AwaitReady, which is where the wait is.
func (p *Provider) ReportProgressTo(report func(line string)) {
	p.client.Progress = report
}

// Close releases nothing: the client holds no pool of its own.
func (p *Provider) Close() error { return nil }

// ---------------------------------------------------------------------------
// Goldens
// ---------------------------------------------------------------------------

// RefreshGolden builds a new masked, verified golden version.
//
// Create the candidate, load it, mask it, verify it, record what was verified,
// and only then publish. Publishing is the rename, so there is no window in
// which a half built golden is visible as one, and a failure at any earlier
// step deletes the candidate rather than leaving a branchable copy of unmasked
// production behind.
func (p *Provider) RefreshGolden(ctx context.Context, spec provider.GoldenSpec) (provider.GoldenVersion, error) {
	if !p.Capabilities().Supports(spec.Version) {
		return provider.GoldenVersion{}, aferrors.Coded(aferrors.AFDB003,
			"found", strconv.Itoa(spec.Version),
			"supported", joinInts(p.Capabilities().SupportedVersions))
	}

	p.sweepCandidates(ctx)

	parent, err := p.parentBranch(ctx)
	if err != nil {
		return provider.GoldenVersion{}, err
	}

	version := provider.NewGoldenVersionID(p.clock.Now(), spec.RulesHash)
	created := p.clock.Now().UTC()
	candidate, err := p.client.CreateBranch(ctx, CreateBranchRequest{
		Name:        PrefixCandidate + branchSafe(version),
		ParentID:    parent.ID,
		Description: version,
	})
	if err != nil {
		return provider.GoldenVersion{}, err
	}

	// Removed unless the refresh gets all the way to the rename. A failed
	// verification that left a branchable copy of unmasked production behind
	// would be the worst possible outcome this function has.
	published := false
	defer func() {
		if !published {
			_ = p.client.DeleteBranch(context.WithoutCancel(ctx), candidate.ID)
		}
	}()

	if _, err := p.client.AwaitReady(ctx, candidate.ID); err != nil {
		return provider.GoldenVersion{}, err
	}
	conn, err := p.client.ConnectionString(ctx, candidate.ID)
	if err != nil {
		return provider.GoldenVersion{}, err
	}
	if err := p.waitReady(ctx, conn); err != nil {
		return provider.GoldenVersion{}, err
	}
	// Refused before anything is loaded. A manifest asking for Postgres 17
	// against a project whose root runs 16 would otherwise build the golden on
	// 16 and hand the application a database whose behaviour differs from
	// production in ways that only appear under load.
	major, err := serverMajor(ctx, conn)
	if err != nil {
		return provider.GoldenVersion{}, err
	}
	if major != spec.Version {
		return provider.GoldenVersion{}, aferrors.Coded(aferrors.AFDB003,
			"found", strconv.Itoa(spec.Version), "supported", strconv.Itoa(major))
	}
	if err := p.loadSource(ctx, conn, spec); err != nil {
		return provider.GoldenVersion{}, err
	}

	if spec.Mask != nil {
		if err := spec.Mask(ctx, conn); err != nil {
			return provider.GoldenVersion{}, fmt.Errorf("db.xata: mask the golden candidate: %w", err)
		}
	}
	attestation := ""
	if spec.Verify != nil {
		attestation, err = spec.Verify(ctx, conn)
		if err != nil {
			return provider.GoldenVersion{}, fmt.Errorf("db.xata: verify the golden candidate: %w", err)
		}
	}

	if err := p.writeMeta(ctx, conn, version, spec.RulesHash, spec.Provenance, attestation, created); err != nil {
		return provider.GoldenVersion{}, err
	}

	// The publish. Everything above can fail and leave nothing branchable;
	// after this the version exists.
	if err := p.client.RenameBranch(ctx, candidate.ID, PrefixGolden+branchSafe(version)); err != nil {
		return provider.GoldenVersion{}, fmt.Errorf("db.xata: publish the golden: %w", err)
	}
	published = true

	return provider.GoldenVersion{
		ID: version, CreatedAt: created, RulesHash: spec.RulesHash,
		Provenance: spec.Provenance,
		Verified:   spec.Verify != nil, Attestation: attestation,
		ProviderRef: candidate.ID,
	}, nil
}

// ListGoldens returns published versions, newest first.
//
// The version, the rules hash and the provenance come from the golden's own
// metadata table rather than from the listing, because Xata's branch object has
// nowhere to keep them. That costs one connection per golden and it is the
// truthful place for the data: a listing that reported a rules hash the golden
// itself did not carry would be a claim about a database made without reading
// it. The description field is the fallback and holds the version alone.
func (p *Provider) ListGoldens(ctx context.Context) ([]provider.GoldenVersion, error) {
	branches, err := p.client.ListBranches(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]provider.GoldenVersion, 0, len(branches))
	for _, b := range branches {
		if !strings.HasPrefix(b.Name, PrefixGolden) {
			continue
		}
		out = append(out, p.goldenFrom(ctx, b))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// goldenFrom builds a version from a branch, reading the golden's own metadata.
func (p *Provider) goldenFrom(ctx context.Context, b Branch) provider.GoldenVersion {
	gv := provider.GoldenVersion{
		ID:          b.Description,
		CreatedAt:   b.CreatedAt,
		ProviderRef: b.ID,
		// A branch carries the golden prefix only because a refresh renamed it
		// after verification returned without an error. Recomputing this from
		// "it exists, so it must have passed" would hand back true for a golden
		// nothing ever scanned, and the rename is the evidence.
		Verified: true,
	}
	if gv.ID == "" {
		gv.ID = strings.TrimPrefix(b.Name, PrefixGolden)
	}
	meta, err := p.readMeta(ctx, b.ID)
	if err != nil {
		// Reported as far as the listing can carry it rather than failing the
		// whole call. A golden whose compute will not wake is still a golden
		// the operator has to be able to see and destroy, and a ListGoldens
		// that returned an error would hide every other version too.
		return gv
	}
	if meta.version != "" {
		gv.ID = meta.version
	}
	gv.RulesHash = meta.rulesHash
	gv.Provenance = meta.provenance
	gv.Attestation = meta.attestation
	gv.CreatedAt = meta.createdAt
	return gv
}

// DestroyGolden removes a version. Removing one that does not exist succeeds.
func (p *Provider) DestroyGolden(ctx context.Context, version string) error {
	branches, err := p.client.ListBranches(ctx)
	if err != nil {
		return err
	}
	golden, ok := findGolden(branches, version)
	if !ok {
		return nil
	}

	// Refused while anything came from it. Xata would refuse the delete of a
	// branch with children too, with its own message, and a coded error naming
	// the count is what an operator can act on.
	referencing := 0
	for _, b := range branches {
		if b.Parent() == golden.ID && strings.HasPrefix(b.Name, PrefixEnv) {
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

	// Idempotent by environment. The engine retries after a timeout, and a
	// retry that creates a second branch is how an orphan is made.
	want := PrefixEnv + branchSafe(envID)
	for _, b := range branches {
		if b.Name == want {
			return provider.Branch{
				EnvID: envID, From: b.Description,
				ProviderRef: b.ID, CreatedAt: b.CreatedAt,
			}, nil
		}
	}

	golden, ok := findGolden(branches, version)
	if !ok {
		// A candidate under this version means the refresh did not finish, so
		// it was never verified. Saying "unverified" rather than "missing"
		// tells the operator which of the two problems they have.
		if _, unpublished := findCandidate(branches, version); unpublished {
			return provider.Branch{}, aferrors.Coded(aferrors.AFMSK001, "version", version)
		}
		return provider.Branch{}, aferrors.Coded(aferrors.AFDB004, "version", version)
	}

	if p.maxBranches > 0 {
		if countPrefix(branches, PrefixEnv) >= p.maxBranches {
			return provider.Branch{}, aferrors.Coded(aferrors.AFDB006,
				"limit", strconv.Itoa(p.maxBranches))
		}
	}

	created, err := p.client.CreateBranch(ctx, CreateBranchRequest{
		Name:        want,
		ParentID:    golden.ID,
		Description: version,
	})
	if err != nil {
		// Xata's own ceiling, which is the real one: the configured limit above
		// is what this provider was told, and the plan is what actually
		// decides. Both arrive as AF-DB-006 so an operator gets one answer.
		// Passed through with Xata's own code and message. The document lists
		// 412 for this call and says only that it is a precondition failure,
		// not which precondition, so mapping it to AF-DB-006 would be guessing
		// that it means a branch limit. The configured limit above is the
		// coded one; a refusal from the vendor says in its own words why.
		return provider.Branch{}, err
	}
	if _, err := p.client.AwaitReady(ctx, created.ID); err != nil {
		return provider.Branch{}, err
	}

	return provider.Branch{
		EnvID: envID, From: version, ProviderRef: created.ID,
		CreatedAt: created.CreatedAt,
	}, nil
}

// Reset is unsupported, and Caps.Reset says so.
//
// Returned rather than faked. Xata publishes no endpoint that returns a branch
// to another branch's state, and a reset implemented as a delete and a recreate
// would hand back a different branch on a different connection string while the
// caller's environment still holds the old one.
func (p *Provider) Reset(context.Context, provider.Branch) error {
	return provider.ErrUnsupported
}

// Destroy removes a branch. Removing one that is already gone succeeds.
func (p *Provider) Destroy(ctx context.Context, b provider.Branch) error {
	if b.ProviderRef == "" {
		return nil
	}
	return p.client.DeleteBranch(ctx, b.ProviderRef)
}

// ConnString returns a connection string for a branch, as a secret.
//
// The mode is accepted and ignored, which is what PooledEndpoints false means.
// Xata returns one connection string and documents no pooled variant, so
// returning the same value under two names would make a capability out of a
// synonym.
func (p *Provider) ConnString(ctx context.Context, b provider.Branch, _ provider.ConnMode) (secrets.Value, error) {
	if b.ProviderRef == "" {
		return secrets.Value{}, aferrors.Coded(aferrors.AFDB014, "env", b.EnvID)
	}
	return p.client.ConnectionString(ctx, b.ProviderRef)
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
		kind := ""
		envID := ""
		switch {
		case strings.HasPrefix(b.Name, PrefixGolden):
			kind = "golden"
		case strings.HasPrefix(b.Name, PrefixCandidate):
			kind = "candidate"
		case strings.HasPrefix(b.Name, PrefixEnv):
			kind = "branch"
			// The SANITISED environment identifier, and the difference is
			// stated rather than hidden. Xata's branch object has no field to
			// keep the original in, so what comes back here is what the name
			// carries: lowercased, with anything outside a to z, 0 to 9 and a
			// hyphen replaced by a hyphen. It is enough to find the
			// environment and it is not the identifier the journal holds.
			envID = strings.TrimPrefix(b.Name, PrefixEnv)
		default:
			// Not ours. A Xata project can hold branches somebody made by hand,
			// and reporting those as leaks is how a leak report becomes
			// something people learn to ignore.
			continue
		}
		out = append(out, provider.Resource{
			Kind:      kind,
			ID:        b.ID,
			EnvID:     envID,
			CreatedAt: b.CreatedAt,
			// No state label. A listing's BranchListMetadata carries no
			// status, and a label that always read "reporting no status at
			// all" would be a field that looks informative and never is.
			Labels: map[string]string{
				"name":    b.Name,
				"version": b.Description,
				"parent":  b.Parent(),
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

// parentBranch is the branch goldens are taken from.
//
// Named in the manifest, or the project's root when it is not. The root is the
// branch with no parent, which is what a nullable parentID is for, and it is
// found rather than assumed to be called "main": a project whose root was
// renamed would otherwise have every refresh fail with a message about a branch
// nobody had heard of.
func (p *Provider) parentBranch(ctx context.Context) (Branch, error) {
	branches, err := p.client.ListBranches(ctx)
	if err != nil {
		return Branch{}, err
	}
	if p.parentName != "" {
		for _, b := range branches {
			if b.Name == p.parentName {
				return b, nil
			}
		}
		return Branch{}, aferrors.Coded(aferrors.AFDB004, "version", p.parentName)
	}
	for _, b := range branches {
		if b.Parent() == "" {
			return b, nil
		}
	}
	return Branch{}, fmt.Errorf(
		"db.xata: project %s has no branch without a parent, so there is no root to take "+
			"goldens from. Name one with database.parent_branch", p.client.ProjectID)
}

func (p *Provider) waitReady(ctx context.Context, conn secrets.Value) error {
	err := pgcopy.WaitReady(ctx, conn, readyTimeout, p.clock.Now, p.clock.Sleep)
	if err == nil || ctx.Err() != nil {
		return err
	}
	return aferrors.Wrap(err, aferrors.AFDB002, "host", "xata")
}

// loadSource fills a candidate.
//
// A candidate here is already a copy on write branch of the project's parent,
// so it holds that branch's data the moment it exists. What this does is bring
// in an EXTERNAL source when the manifest names one, which is the case where
// production lives somewhere other than this Xata project.
func (p *Provider) loadSource(ctx context.Context, target secrets.Value, spec provider.GoldenSpec) error {
	if spec.SourceURL.IsZero() || strings.Contains(spec.SourceURL.Reveal(), "@source/") {
		// No real source. A project that has not connected production yet
		// still needs a schema to branch, and the seed is what provides it.
		return pgcopy.Exec(ctx, target, p.seedSQL)
	}
	return pgcopy.Copy(ctx, spec.SourceURL, target)
}

// serverMajor is the Postgres major version the branch's server reports.
func serverMajor(ctx context.Context, conn secrets.Value) (int, error) {
	db, err := sql.Open("pgx", conn.Reveal())
	if err != nil {
		return 0, fmt.Errorf("db.xata: open the candidate: %w", err)
	}
	defer func() { _ = db.Close() }()
	var num int
	if err := db.QueryRowContext(ctx, "SELECT current_setting('server_version_num')::int").Scan(&num); err != nil {
		return 0, fmt.Errorf("db.xata: read the candidate's server version: %w", err)
	}
	return num / 10000, nil
}

// goldenMeta is what a golden records about itself.
type goldenMeta struct {
	version     string
	rulesHash   string
	provenance  string
	attestation string
	createdAt   time.Time
}

// writeMeta records what was verified, inside the golden itself.
func (p *Provider) writeMeta(ctx context.Context, conn secrets.Value, version, rules, provenance, attestation string, at time.Time) error {
	db, err := sql.Open("pgx", conn.Reveal())
	if err != nil {
		return fmt.Errorf("db.xata: open the candidate: %w", err)
	}
	defer func() { _ = db.Close() }()

	script := `
		CREATE SCHEMA IF NOT EXISTS ` + MetaSchema + `;
		CREATE TABLE IF NOT EXISTS ` + MetaSchema + `.golden (
			version     text PRIMARY KEY,
			rules_hash  text NOT NULL,
			provenance  text NOT NULL,
			attestation text NOT NULL,
			created_at  timestamptz NOT NULL
		);`
	if _, err := db.ExecContext(ctx, script); err != nil {
		return fmt.Errorf("db.xata: create the golden metadata table: %w", err)
	}
	_, err = db.ExecContext(ctx,
		`INSERT INTO `+MetaSchema+`.golden (version, rules_hash, provenance, attestation, created_at)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (version) DO UPDATE SET
		   rules_hash = EXCLUDED.rules_hash,
		   provenance = EXCLUDED.provenance,
		   attestation = EXCLUDED.attestation,
		   created_at = EXCLUDED.created_at`,
		version, rules, provenance, attestation, at)
	if err != nil {
		return fmt.Errorf("db.xata: record the attestation: %w", err)
	}
	return nil
}

// readMeta reads back what a golden recorded about itself.
//
// A branch inherits the row from the golden it came from, so this answers for
// an environment as well: whoever holds one can read what was scanned and what
// was found. The newest row wins, because a branch of a branch could carry more
// than one.
func (p *Provider) readMeta(ctx context.Context, branchID string) (goldenMeta, error) {
	conn, err := p.client.ConnectionString(ctx, branchID)
	if err != nil {
		return goldenMeta{}, err
	}
	db, err := sql.Open("pgx", conn.Reveal())
	if err != nil {
		return goldenMeta{}, fmt.Errorf("db.xata: open branch %s: %w", branchID, err)
	}
	defer func() { _ = db.Close() }()

	var m goldenMeta
	err = db.QueryRowContext(ctx,
		`SELECT version, rules_hash, provenance, attestation, created_at
		   FROM `+MetaSchema+`.golden ORDER BY created_at DESC LIMIT 1`).
		Scan(&m.version, &m.rulesHash, &m.provenance, &m.attestation, &m.createdAt)
	if err != nil {
		return goldenMeta{}, fmt.Errorf("db.xata: read the golden metadata of %s: %w", branchID, err)
	}
	return m, nil
}

// Attestation reads back what a golden was verified as holding.
//
// Not part of the provider interface, because reading it means waking the
// golden's compute and no caller on the hot path needs it. It is here so that
// the claim in a listing can be checked against the golden itself.
func (p *Provider) Attestation(ctx context.Context, version string) (string, error) {
	branches, err := p.client.ListBranches(ctx)
	if err != nil {
		return "", err
	}
	golden, ok := findGolden(branches, version)
	if !ok {
		return "", aferrors.Coded(aferrors.AFDB004, "version", version)
	}
	meta, err := p.readMeta(ctx, golden.ID)
	if err != nil {
		return "", err
	}
	return meta.attestation, nil
}

// sweepCandidates removes candidates old enough that they can only be orphans.
func (p *Provider) sweepCandidates(ctx context.Context) {
	branches, err := p.client.ListBranches(ctx)
	if err != nil {
		return
	}
	cutoff := p.clock.Now().Add(-candidateMaxAge)
	for _, b := range branches {
		if !strings.HasPrefix(b.Name, PrefixCandidate) {
			continue
		}
		if b.CreatedAt.After(cutoff) {
			continue
		}
		_ = p.client.DeleteBranch(ctx, b.ID)
	}
}

// findGolden matches by the NAME the version produces rather than by reversing
// a name back into a version.
//
// branchSafe is lossy on purpose, and reversing it would be guessing. Comparing
// forwards is exact: two different versions cannot produce the same name unless
// they differ only in characters branchSafe folds, and a version identifier is
// a timestamp and a hash where the folded characters are a fixed underscore.
func findGolden(branches []Branch, version string) (Branch, bool) {
	return findPrefixed(branches, PrefixGolden, version)
}

func findCandidate(branches []Branch, version string) (Branch, bool) {
	return findPrefixed(branches, PrefixCandidate, version)
}

func findPrefixed(branches []Branch, prefix, version string) (Branch, bool) {
	want := prefix + branchSafe(version)
	for _, b := range branches {
		if b.Name == want {
			return b, true
		}
		// The description is the authoritative copy of the version and it is
		// checked too, so a branch somebody renamed by hand is still found.
		if strings.HasPrefix(b.Name, prefix) && b.Description != "" && b.Description == version {
			return b, true
		}
	}
	return Branch{}, false
}

func countPrefix(branches []Branch, prefix string) int {
	n := 0
	for _, b := range branches {
		if strings.HasPrefix(b.Name, prefix) {
			n++
		}
	}
	return n
}

// branchSafe reduces an identifier to what a branch name may carry.
//
// The same folding the Neon provider uses, and for the same reason: an
// environment identifier comes from a pull request or a person and a provider
// that put it into a URL path unfolded would be one branch name away from a
// request nobody wrote. The API document gives a branch name no pattern, so this
// is the conservative reading rather than a transcription of a published rule,
// and saying so is the difference between a decision and a claim.
func branchSafe(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func joinInts(vs []int) string {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ", ")
}
