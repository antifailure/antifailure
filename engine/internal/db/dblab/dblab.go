package dblab

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/db/pgcopy"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// Clone identifier prefixes.
//
// Every clone this provider makes is named from the thing it belongs to, so
// that a retry after a crash finds what the previous attempt created without
// needing the journal to be intact. A clone with neither prefix belongs to
// somebody else: a Database Lab Engine is a shared instance and people create
// clones on it by hand, and reporting those as leaks is how a leak report
// becomes something people learn to ignore.
//
// The engine validates a clone identifier against ^[a-zA-Z0-9][a-zA-Z0-9_.-]*$,
// so the prefix has to start with a letter, and underscores from an
// environment identifier are already legal and are kept.
const (
	PrefixCandidate = "af-cand-"
	PrefixEnv       = "af-env-"
)

// MetaSchema is where the attestation is written inside the golden itself.
//
// In the database rather than beside it, because a verification statement is
// about that data and should travel with it. A clone of a golden inherits the
// row for free, so anybody holding an environment can read what was scanned
// and what was found without asking this process or the engine.
const MetaSchema = "_antifailure"

// readyTimeout is how long a new clone has to start answering.
//
// Longer than the Docker provider's because the engine has to create a ZFS
// dataset, start a container, and let Postgres recover, and shorter than the
// conformance suite's per behaviour timeout so that a stuck clone fails with
// this message rather than with a bare deadline.
const readyTimeout = 180 * time.Second

// candidateMaxAge is the age past which a candidate clone can only be an
// orphan.
//
// A candidate exists for the minutes between cloning the base snapshot and
// committing it, and nothing ever branches from one, so removing an old one is
// unconditionally safe. That is what lets the provider heal itself rather than
// wait for somebody to read a leak report. A candidate left behind on a
// Database Lab Engine is not free: it holds a container, a port out of the
// configured pool, and the difference between its dataset and the snapshot.
const candidateMaxAge = 2 * time.Hour

// defaultDatabase and defaultUser are what a clone's ephemeral role is called.
const (
	defaultDatabase = "postgres"
	defaultUser     = "antifailure"
)

// Provider is the Database Lab Engine implementation of provider.Database.
type Provider struct {
	client   *Client
	clock    clock.Clock
	endpoint string
	// token is kept because the clone password is derived from it. See
	// derivedPassword.
	token       secrets.Value
	seedSQL     string
	database    string
	user        string
	baseName    string
	maxBranches int
	versions    []int
	latency     time.Duration
	// dependentTeardownWait bounds how long DestroyGolden waits for a deleted
	// clone's storage to actually be released. See
	// deleteSnapshotOnceItsCloneStorageIsGone.
	dependentTeardownWait time.Duration
}

// Options configure a provider.
type Options struct {
	// Endpoint is the Database Lab Engine's API root, for example
	// http://127.0.0.1:2345. Required: the engine is self hosted, so there is
	// no account to enumerate and no default instance to find.
	Endpoint string
	// Token is the engine's verification token. Required, and required to be
	// non empty even though the engine permits an empty one, because an engine
	// running without a token is one anybody on the network can create clones
	// of production data on.
	Token secrets.Value
	Clock clock.Clock
	// SeedSQL is applied to a candidate when no source database is configured.
	SeedSQL string
	// Database and User name the ephemeral role a clone is reached through.
	Database string
	User     string
	// BaseSnapshot pins the snapshot refreshes clone from. Empty selects the
	// newest snapshot this provider did not create, which is what the engine's
	// own data retrieval most recently produced.
	BaseSnapshot string
	// MaxBranches is the concurrent clone limit. Zero means unlimited, which
	// is what the engine itself imposes: what actually runs out is the
	// configured port pool and the pool's free space, and both surface from
	// the engine with the numbers an operator needs.
	MaxBranches int
	// SupportedVersions overrides the Postgres majors this instance handles.
	// It is configuration because the answer is a property of the data in the
	// engine's pool, which this provider does not get to choose.
	SupportedVersions []int
	// ExpectedBranchLatency overrides the declared branch latency.
	ExpectedBranchLatency time.Duration
	// DependentTeardownWait bounds how long collecting a golden waits for a
	// deleted clone's storage to be released. Zero uses the default.
	DependentTeardownWait time.Duration

	HTTP         *http.Client
	PollInterval time.Duration
	PollTimeout  time.Duration
}

// DefaultSupportedVersions is what a Database Lab Engine handles when nothing
// says otherwise.
//
// The engine itself is version agnostic; what decides is the Postgres image
// its pool was built with, so this is a default rather than a fact and
// Options.SupportedVersions overrides it. The set matches what the manifest
// schema permits for database.version, because declaring a major no manifest
// can ask for is a capability nobody can reach.
func DefaultSupportedVersions() []int { return []int{14, 15, 16, 17} }

// DefaultDependentTeardownWait is how long collecting a golden waits for a
// deleted clone's storage to be released.
//
// Generous, because what it waits for is a container stopping and a ZFS
// dataset being destroyed on somebody else's schedule, and bounded, because a
// dependent that never goes away is a real refusal and has to be reported as
// one rather than hung on.
const DefaultDependentTeardownWait = 3 * time.Minute

// DefaultBranchLatency is what a thin clone is expected to take.
//
// A clone is a ZFS clone plus a container start, so it does not grow with the
// size of the database, which is the reason to run a Database Lab Engine at
// all. What it does grow with is how busy the engine is, because the engine
// starts the container and then waits for Postgres by shelling out to docker
// and psql in a loop.
//
// Two minutes is measured on the conformance dataset rather than hoped for:
// one clone on an idle engine is about ninety seconds, and a second one
// created while the first is still running is slower. Declaring the ninety
// second figure would have been a claim about the best case, and this number
// exists so that a provider getting slower fails a test rather than degrading
// quietly.
const DefaultBranchLatency = 2 * time.Minute

// New builds a provider.
func New(opts Options) (*Provider, error) {
	if opts.Endpoint == "" {
		return nil, errors.New("db.dblab: a Database Lab Engine endpoint is required")
	}
	if _, err := url.Parse(opts.Endpoint); err != nil {
		return nil, fmt.Errorf("db.dblab: the endpoint %q is not a URL: %w", opts.Endpoint, err)
	}
	if opts.Token.IsZero() {
		return nil, errors.New("db.dblab: a verification token is required")
	}
	c := opts.Clock
	if c == nil {
		c = clock.New()
	}
	database := opts.Database
	if database == "" {
		database = defaultDatabase
	}
	user := opts.User
	if user == "" {
		user = defaultUser
	}
	versions := opts.SupportedVersions
	if len(versions) == 0 {
		versions = DefaultSupportedVersions()
	}
	latency := opts.ExpectedBranchLatency
	if latency <= 0 {
		latency = DefaultBranchLatency
	}
	teardownWait := opts.DependentTeardownWait
	if teardownWait <= 0 {
		teardownWait = DefaultDependentTeardownWait
	}
	return &Provider{
		client: &Client{
			BaseURL:      opts.Endpoint,
			Token:        opts.Token,
			HTTP:         opts.HTTP,
			Sleep:        c.Sleep,
			PollInterval: opts.PollInterval,
			PollTimeout:  opts.PollTimeout,
		},
		clock:       c,
		endpoint:    opts.Endpoint,
		token:       opts.Token,
		seedSQL:     opts.SeedSQL,
		database:    database,
		user:        user,
		baseName:    opts.BaseSnapshot,
		maxBranches: opts.MaxBranches,
		versions:    versions,
		latency:     latency,

		dependentTeardownWait: teardownWait,
	}, nil
}

// Name identifies the provider in a manifest.
func (p *Provider) Name() string { return "dblab" }

// Capabilities describes what a Database Lab Engine can do.
func (p *Provider) Capabilities() provider.Caps {
	return provider.Caps{
		Branching: true,
		// The engine resets a clone to a snapshot in place, which is exactly a
		// reset to the golden, sequences included: what comes back is the
		// filesystem the snapshot recorded, not a replayed dump.
		Reset: true,
		// A clone is a ZFS clone of the golden's dataset, so it shares storage
		// with it and clone time does not grow with database size. This is the
		// reason to run a Database Lab Engine at all.
		CopyOnWrite: true,
		// The engine can run its own data patching during retrieval, but this
		// provider does not use it: the engine's rules are the single
		// implementation of masking, and a provider's masking is a claim where
		// verification is a check.
		ProviderMasking: false,
		// No pooler. A clone is a plain Postgres container the engine started,
		// and it publishes one port. Declaring pooled endpoints would make the
		// conformance suite run a behaviour it should skip, and the suite
		// would pass it by handing out the same string twice.
		PooledEndpoints:       false,
		MaxConcurrentBranches: p.maxBranches,
		ExpectedBranchLatency: p.latency,
		SupportedVersions:     p.versions,
	}
}

// Close releases nothing: the client holds no pool of its own.
func (p *Provider) Close() error { return nil }

// ---------------------------------------------------------------------------
// Goldens
// ---------------------------------------------------------------------------

// RefreshGolden builds a new masked, verified golden version.
//
// The sequence is the whole point and the order is not negotiable: clone the
// base snapshot, load the data, mask it, verify it, and only then commit the
// clone into a snapshot. A provider that commits before verifying has
// published an unverified copy, and a provider that verifies before masking
// has attested to the unmasked data, which is worse than not verifying at all.
//
// It is also why a Database Lab Engine needs a candidate clone at all. The
// engine's own retrieval brings production in as it is, and there is no way to
// mask a snapshot in place; the only way to get masked data into a snapshot is
// to put it into a clone first and commit that clone.
func (p *Provider) RefreshGolden(ctx context.Context, spec provider.GoldenSpec) (provider.GoldenVersion, error) {
	if !p.Capabilities().Supports(spec.Version) {
		return provider.GoldenVersion{}, aferrors.Coded(aferrors.AFDB003,
			"found", strconv.Itoa(spec.Version),
			"supported", joinInts(p.versions))
	}

	p.sweepCandidates(ctx)

	base, err := p.baseSnapshot(ctx)
	if err != nil {
		return provider.GoldenVersion{}, err
	}

	version := provider.NewGoldenVersionID(p.clock.Now(), spec.RulesHash)
	created := p.clock.Now().UTC()
	cloneID := PrefixCandidate + cloneSafe(version)

	candidate, err := p.createClone(ctx, cloneID, base.ID)
	if err != nil {
		// Whatever the engine did with the request, the identifier is
		// deterministic, so a clone it created despite the error is findable
		// and removable rather than an orphan nobody has a name for.
		_ = p.client.DeleteClone(context.WithoutCancel(ctx), cloneID)
		return provider.GoldenVersion{}, err
	}

	// Removed unless the refresh gets all the way to the commit. A failed
	// verification that left a clone of unmasked production running would be
	// the worst possible outcome here, and it would be reachable over the
	// network on the port the engine published.
	published := false
	defer func() {
		if !published {
			_ = p.client.DeleteClone(context.WithoutCancel(ctx), cloneID)
		}
	}()

	conn := p.connString(candidate)
	if err := p.waitReady(ctx, conn); err != nil {
		return provider.GoldenVersion{}, err
	}
	if err := p.loadSource(ctx, conn, spec); err != nil {
		return provider.GoldenVersion{}, err
	}

	if spec.Mask != nil {
		if err := spec.Mask(ctx, conn); err != nil {
			return provider.GoldenVersion{}, fmt.Errorf("db.dblab: mask the golden candidate: %w", err)
		}
	}
	attestation := ""
	if spec.Verify != nil {
		attestation, err = spec.Verify(ctx, conn)
		if err != nil {
			// Nothing is committed. The candidate is removed by the deferred
			// call above, so a failed verification leaves no snapshot anyone
			// could branch by accident.
			return provider.GoldenVersion{}, fmt.Errorf("db.dblab: verify the golden candidate: %w", err)
		}
	}

	if err := p.writeMeta(ctx, conn, version, spec.RulesHash, attestation, created); err != nil {
		return provider.GoldenVersion{}, err
	}

	message := encodeMeta(meta{
		Version:           version,
		RulesHash:         spec.RulesHash,
		CreatedAt:         created.Format(time.RFC3339),
		Verified:          spec.Verify != nil,
		AttestationSHA256: sha256Hex(attestation),
	})

	// The publish. Everything above can fail and leave nothing branchable;
	// after this the version exists.
	snapshotID, err := p.client.SnapshotClone(ctx, cloneID, message)
	if err != nil {
		return provider.GoldenVersion{}, fmt.Errorf("db.dblab: commit the golden candidate: %w", err)
	}
	published = true

	// The candidate has done its job. The engine keeps the snapshot when the
	// clone goes, which is exactly what its own documentation says, and a
	// candidate left running would hold a port and a container for nothing.
	if err := p.client.DeleteClone(ctx, cloneID); err != nil {
		// Not fatal: the golden exists and is branchable, and the candidate is
		// reported by Inventory, so the leak detector can see it and the next
		// refresh's sweep will remove it.
		_ = err
	}

	gv := provider.GoldenVersion{
		ID: version, CreatedAt: created, RulesHash: spec.RulesHash,
		Verified: spec.Verify != nil, Attestation: attestation, ProviderRef: snapshotID,
	}
	if fresh, err := p.client.GetSnapshot(ctx, snapshotID); err == nil {
		gv.SizeBytes = fresh.LogicalSize
	}
	return gv, nil
}

// baseSnapshot is what a refresh clones from.
//
// It is the newest snapshot this provider did not create, which is what the
// engine's own retrieval most recently produced. Cloning the newest snapshot
// of any kind would be wrong in a way that is easy to miss: the second refresh
// would start from the first refresh's golden, so masking would run over
// already masked data, the seed would collide with the schema it created last
// time, and every golden after the first would be a descendant of one rather
// than an independent copy of production.
func (p *Provider) baseSnapshot(ctx context.Context) (Snapshot, error) {
	snapshots, err := p.client.ListSnapshots(ctx)
	if err != nil {
		return Snapshot{}, p.wrapAuth(err)
	}
	if p.baseName != "" {
		for _, s := range snapshots {
			if s.ID == p.baseName {
				return s, nil
			}
		}
		return Snapshot{}, aferrors.Coded(aferrors.AFDB009,
			"endpoint", p.endpoint,
			"detail", fmt.Sprintf("the pinned base snapshot %s is not on this instance", p.baseName))
	}

	candidates := make([]Snapshot, 0, len(snapshots))
	for _, s := range snapshots {
		if _, ours := decodeMeta(s.Message); ours {
			continue
		}
		candidates = append(candidates, s)
	}
	if len(candidates) == 0 {
		detail := "its data retrieval has not produced one yet"
		if len(snapshots) > 0 {
			detail = fmt.Sprintf("all %d of its snapshots are Antifailure goldens, "+
				"and a golden is never the base for another", len(snapshots))
		}
		return Snapshot{}, aferrors.Coded(aferrors.AFDB009,
			"endpoint", p.endpoint, "detail", detail)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return newerThan(candidates[i], candidates[j])
	})
	return candidates[0], nil
}

// ListGoldens returns published versions, newest first.
func (p *Provider) ListGoldens(ctx context.Context) ([]provider.GoldenVersion, error) {
	snapshots, err := p.client.ListSnapshots(ctx)
	if err != nil {
		return nil, p.wrapAuth(err)
	}
	out := make([]provider.GoldenVersion, 0, len(snapshots))
	for _, s := range snapshots {
		m, ours := decodeMeta(s.Message)
		if !ours {
			continue
		}
		out = append(out, provider.GoldenVersion{
			ID:          m.Version,
			CreatedAt:   m.createdAt(s.CreatedAt.Time),
			SizeBytes:   s.LogicalSize,
			RulesHash:   m.RulesHash,
			Verified:    m.Verified,
			ProviderRef: s.ID,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// DestroyGolden removes a version. Removing one that does not exist succeeds.
func (p *Provider) DestroyGolden(ctx context.Context, version string) error {
	golden, ok, err := p.findGolden(ctx, version)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	// Refused while anything came from it. The engine would refuse the delete
	// too, with its own message about dependent datasets, but a coded error
	// naming the count is what an operator can act on, and asking first means
	// the answer is the same whether the dependent clone is one of ours or one
	// somebody made by hand.
	referencing, err := p.cloneCount(ctx, golden.ID)
	if err != nil {
		return err
	}
	if referencing > 0 {
		return aferrors.Coded(aferrors.AFDB005,
			"version", version, "count", strconv.Itoa(referencing))
	}

	if err := p.deleteSnapshotOnceItsCloneStorageIsGone(ctx, golden.ID); err != nil {
		if NotFound(err) {
			return nil
		}
		if HasDependents(err) {
			// Still refused after waiting, so this is a real dependent rather
			// than one on its way out: a clone created between the count above
			// and now, or one somebody made by hand. The engine's refusal is
			// the authority.
			return aferrors.Wrap(err, aferrors.AFDB005, "version", version, "count", "1")
		}
		return fmt.Errorf("db.dblab: remove the golden snapshot %s: %w", version, err)
	}
	return nil
}

// deleteSnapshotOnceItsCloneStorageIsGone removes a snapshot, waiting out the
// window in which a deleted clone's dataset still exists.
//
// A clone leaves the engine's API before its storage is released. The engine
// forgets the clone, then sends its identifier down an observing channel, and
// only then destroys the ZFS dataset (internal/cloning/base.go, destroyClone).
// A ZFS snapshot cannot be destroyed while a dataset cloned from it survives,
// so for the seconds in between, deleting the golden fails with "cannot delete
// snapshot ... because it has dependent datasets".
//
// This is a real ordering, not a theoretical one: it made every golden the
// conformance suite created outlive the behaviour that created it, and the
// leak check at the end reported eleven snapshots the suite had genuinely
// asked to have removed. "The clone is gone" and "the clone's storage is gone"
// are two different events and the API only reports the first.
//
// Waiting rather than forcing. The force flag would delete the dependent
// datasets along with the snapshot, which on a shared engine is somebody
// else's environment.
func (p *Provider) deleteSnapshotOnceItsCloneStorageIsGone(ctx context.Context, id string) error {
	deadline := p.clock.Now().Add(p.dependentTeardownWait)
	backoff := 500 * time.Millisecond
	for {
		err := p.client.DeleteSnapshot(ctx, id, false)
		if err == nil || !HasDependents(err) {
			return err
		}
		if !p.clock.Now().Before(deadline) {
			return err
		}
		if sleepErr := p.clock.Sleep(ctx, backoff); sleepErr != nil {
			return sleepErr
		}
		if backoff < 5*time.Second {
			backoff *= 2
		}
	}
}

// ---------------------------------------------------------------------------
// Branches
// ---------------------------------------------------------------------------

// Branch creates a database for an environment from a golden version.
func (p *Provider) Branch(ctx context.Context, version, envID string) (provider.Branch, error) {
	cloneID := PrefixEnv + cloneSafe(envID)

	// Idempotency first. The engine retries after a timeout, and a retry that
	// creates a second clone is how an orphan is made. The identifier is
	// derived from the environment rather than generated, which is what makes
	// this findable without the journal.
	if existing, err := p.client.GetClone(ctx, cloneID); err == nil {
		return p.branchFrom(existing, envID, version), nil
	} else if !NotFound(err) {
		return provider.Branch{}, p.wrapAuth(err)
	}

	golden, ok, err := p.findGolden(ctx, version)
	if err != nil {
		return provider.Branch{}, err
	}
	if !ok {
		// A snapshot that exists under this identifier but carries no golden
		// metadata is unmasked data the engine's retrieval brought in. Telling
		// somebody it is missing would be a lie they could work around by
		// naming it more precisely; refusing it as unverified is the product's
		// central promise.
		if p.snapshotExists(ctx, version) {
			return provider.Branch{}, aferrors.Coded(aferrors.AFMSK001, "version", version)
		}
		return provider.Branch{}, aferrors.Coded(aferrors.AFDB004, "version", version)
	}
	if m, _ := decodeMeta(golden.Message); !m.Verified {
		return provider.Branch{}, aferrors.Coded(aferrors.AFMSK001, "version", version)
	}

	if p.maxBranches > 0 {
		live, err := p.countBranches(ctx)
		if err != nil {
			return provider.Branch{}, err
		}
		if live >= p.maxBranches {
			return provider.Branch{}, aferrors.Coded(aferrors.AFDB006,
				"limit", strconv.Itoa(p.maxBranches))
		}
	}

	clone, err := p.createClone(ctx, cloneID, golden.ID)
	if err != nil {
		// Left in place rather than cleaned up. Inventory reports it, so the
		// leak detector can see it; deleting it here would hide the evidence
		// of a clone that failed halfway.
		return provider.Branch{}, err
	}
	b := p.branchFrom(clone, envID, version)
	if err := p.waitReady(ctx, p.connString(clone)); err != nil {
		// The clone exists and is recorded, so the caller can tear it down.
		// Leaving it running and unusable would be worse.
		return b, err
	}
	return b, nil
}

// Reset returns a branch to its golden's state, sequences included.
func (p *Provider) Reset(ctx context.Context, b provider.Branch) error {
	golden, ok, err := p.findGolden(ctx, b.From)
	if err != nil {
		return err
	}
	if !ok {
		return aferrors.Coded(aferrors.AFDB004, "version", b.From)
	}
	// By identifier rather than by "latest". The engine's latest snapshot may
	// be a golden from a refresh that happened while this environment was up,
	// and resetting to that would silently move an environment onto data it
	// never branched from.
	if err := p.client.ResetClone(ctx, p.cloneID(b), golden.ID); err != nil {
		if NotFound(err) {
			return aferrors.Coded(aferrors.AFDB004, "version", b.From)
		}
		return fmt.Errorf("db.dblab: reset the branch for %s: %w", b.EnvID, err)
	}
	return nil
}

// Destroy removes a branch. Removing one that is already gone succeeds.
//
// Both the recorded reference and the derived identifier are removed, and that
// is not belt and braces: the two can differ, because a Branch value handed
// back by a failed create carries the identifier the create used while the
// engine may know it under nothing at all. Teardown by one alone would report
// success and leave a running Postgres, which is exactly the leak the journal
// exists to prevent.
func (p *Provider) Destroy(ctx context.Context, b provider.Branch) error {
	refs := map[string]bool{}
	if b.ProviderRef != "" {
		refs[b.ProviderRef] = true
	}
	if b.EnvID != "" {
		refs[PrefixEnv+cloneSafe(b.EnvID)] = true
	}
	if len(refs) == 0 {
		return fmt.Errorf("db.dblab: the branch has neither an identifier nor an environment")
	}
	for ref := range refs {
		if err := p.client.DeleteClone(ctx, ref); err != nil {
			return fmt.Errorf("db.dblab: remove the branch %s: %w", ref, err)
		}
	}
	return nil
}

// ConnString returns a connection string for a branch, as a secret.
func (p *Provider) ConnString(ctx context.Context, b provider.Branch, mode provider.ConnMode) (secrets.Value, error) {
	if mode == provider.ConnPooled {
		// Declaring a capability this provider does not have would make the
		// conformance suite pass a behaviour it should skip.
		return secrets.Value{}, provider.ErrUnsupported
	}
	clone, err := p.client.GetClone(ctx, p.cloneID(b))
	if err != nil {
		if NotFound(err) {
			return secrets.Value{}, aferrors.Coded(aferrors.AFDB004, "version", b.From)
		}
		return secrets.Value{}, p.wrapAuth(err)
	}
	return p.connString(clone), nil
}

// Inventory lists what this provider currently holds.
//
// Clones are reported by prefix and snapshots by their commit message, so a
// resource somebody made by hand on a shared engine never appears here. That
// is what keeps the leak detector from proposing to delete a colleague's
// clone of the same instance.
func (p *Provider) Inventory(ctx context.Context) ([]provider.Resource, error) {
	var out []provider.Resource

	clones, err := p.client.ListClones(ctx)
	if err != nil {
		return nil, p.wrapAuth(err)
	}
	for _, c := range clones {
		kind := ""
		envID := ""
		switch {
		case strings.HasPrefix(c.ID, PrefixCandidate):
			kind = "candidate"
		case strings.HasPrefix(c.ID, PrefixEnv):
			kind = "branch"
			envID = strings.TrimPrefix(c.ID, PrefixEnv)
		default:
			continue
		}
		out = append(out, provider.Resource{
			Kind:      "clone/" + kind,
			ID:        c.ID,
			EnvID:     envID,
			CreatedAt: c.CreatedAt.Time,
			Labels: map[string]string{
				"snapshot": c.SnapshotID(),
				"state":    c.Status.Code,
				"port":     c.DB.Port,
			},
		})
	}

	snapshots, err := p.client.ListSnapshots(ctx)
	if err != nil {
		return nil, p.wrapAuth(err)
	}
	for _, s := range snapshots {
		m, ours := decodeMeta(s.Message)
		if !ours {
			continue
		}
		out = append(out, provider.Resource{
			Kind: "snapshot/golden", ID: s.ID, CreatedAt: m.createdAt(s.CreatedAt.Time),
			Labels: map[string]string{
				"version": m.Version,
				"size":    strconv.FormatInt(s.LogicalSize, 10),
				"clones":  strconv.Itoa(s.NumClones),
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
		return provider.Health{Reachable: false, Detail: "the clone does not exist"}, nil
	}
	attempt, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := pgcopy.Ping(attempt, conn); err != nil {
		return provider.Health{Reachable: false, Detail: err.Error()}, nil
	}
	return provider.Health{
		Reachable: true,
		Detail:    "accepting connections",
		Latency:   p.clock.Now().Sub(start),
	}, nil
}

// ---------------------------------------------------------------------------
// Internals
// ---------------------------------------------------------------------------

// createClone starts a clone and waits for it to settle.
func (p *Provider) createClone(ctx context.Context, cloneID, snapshotID string) (Clone, error) {
	req := CreateCloneRequest{ID: cloneID}
	req.Snapshot.ID = snapshotID
	req.DB.Username = p.user
	req.DB.Password = p.derivedPassword(cloneID)
	req.DB.DBName = p.database
	// Not restricted. A golden candidate is masked by running the engine's own
	// rules against it, and those rewrite every table the rules name; a role
	// that cannot write is a role that cannot be masked through.
	req.DB.Restricted = false

	created, err := p.client.CreateClone(ctx, req)
	if err != nil {
		if Unauthorized(err) {
			return Clone{}, p.wrapAuth(err)
		}
		return Clone{}, fmt.Errorf("db.dblab: create the clone %s: %w", cloneID, err)
	}
	settled, err := p.client.AwaitClone(ctx, created.ID)
	if err != nil {
		return created, err
	}
	return settled, nil
}

// connString builds a connection string for a clone.
func (p *Provider) connString(c Clone) secrets.Value {
	host := p.reachableHost(c.DB.Host)
	port := c.DB.Port
	if port == "" {
		port = "5432"
	}
	dbname := c.DB.DBName
	if dbname == "" {
		dbname = p.database
	}
	user := c.DB.Username
	if user == "" {
		user = p.user
	}
	// The user info is rendered by net/url rather than concatenated, so a
	// username or password carrying a character that means something in a URL
	// is escaped rather than silently changing which host is connected to.
	// sslmode is disabled because a clone is a container the engine started on
	// a host the operator chose; it has no certificate, and asking for one
	// would make every connection fail rather than make any of them safer.
	return secrets.New(fmt.Sprintf("postgres://%s@%s/%s?sslmode=disable",
		url.UserPassword(user, p.derivedPassword(c.ID)).String(),
		hostPort(host, port), dbname))
}

// reachableHost turns the host the engine reports into one this process can
// reach.
//
// The engine reports a clone's host from its own point of view, and its
// default configuration binds clones to the loopback interface. That is
// correct for the engine and useless to a client on another machine: an
// engine running on dblab.internal reports 127.0.0.1, and connecting to
// 127.0.0.1 from here reaches this machine's own Postgres or nothing at all.
// So a loopback or empty answer is replaced by the host the endpoint names,
// which is by definition a host that reaches this engine.
func (p *Provider) reachableHost(reported string) string {
	if reported != "" && !isLoopback(reported) {
		return reported
	}
	if u, err := url.Parse(p.endpoint); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	if reported != "" {
		return reported
	}
	return "127.0.0.1"
}

func isLoopback(host string) bool {
	switch host {
	case "127.0.0.1", "localhost", "0.0.0.0", "::1", "[::1]":
		return true
	}
	return false
}

func hostPort(host, port string) string {
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return host + ":" + port
}

// derivedPassword is the password a clone's ephemeral role is created with.
//
// It is derived rather than remembered because the engine does not give a
// clone's password back: it records the role's name, database and owner and
// deliberately drops the password. A provider that kept it in memory would
// hand out a working connection string until the process restarted and a
// broken one afterwards, and `af up` and `af test` are separate processes, so
// "afterwards" means the very next command.
//
// The key is the engine's own verification token, which this process already
// holds and which already grants the right to create clones of this data, so
// deriving from it grants nothing new. The clone identifier is mixed in so
// that two clones never share a password. Nothing is written to disk.
func (p *Provider) derivedPassword(cloneID string) string {
	mac := hmac.New(sha256.New, []byte(p.token.Reveal()))
	_, _ = mac.Write([]byte("antifailure-dblab-clone:" + cloneID))
	// Half the digest, which is 128 bits and comfortably past the engine's own
	// 60 bit entropy floor for a clone password.
	return hex.EncodeToString(mac.Sum(nil))[:32]
}

func sha256Hex(s string) string {
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// cloneID is the identifier the engine knows a branch by.
//
// The derived name is preferred over the recorded reference because the two
// can differ after a failed create, and the derived one is the one the engine
// was asked for.
func (p *Provider) cloneID(b provider.Branch) string {
	if b.EnvID != "" {
		return PrefixEnv + cloneSafe(b.EnvID)
	}
	return b.ProviderRef
}

func (p *Provider) branchFrom(c Clone, envID, version string) provider.Branch {
	from := version
	if from == "" {
		from = c.SnapshotID()
	}
	created := c.CreatedAt.Time
	if created.IsZero() {
		created = p.clock.Now().UTC()
	}
	return provider.Branch{
		EnvID: envID, From: from, ProviderRef: c.ID, CreatedAt: created,
	}
}

func (p *Provider) waitReady(ctx context.Context, conn secrets.Value) error {
	err := pgcopy.WaitReady(ctx, conn, readyTimeout, p.clock.Now, p.clock.Sleep)
	if err == nil || ctx.Err() != nil {
		return err
	}
	return aferrors.Wrap(err, aferrors.AFDB002, "host", p.endpoint)
}

func (p *Provider) loadSource(ctx context.Context, target secrets.Value, spec provider.GoldenSpec) error {
	if spec.SourceURL.IsZero() || strings.Contains(spec.SourceURL.Reveal(), "@source/") {
		// No real source configured. The candidate already holds whatever the
		// engine's retrieval brought in, which for a Database Lab Engine is
		// the usual case and the reason to run one: production's shape is
		// already there. The seed adds a known schema on top for a project
		// that has not connected anything yet.
		if p.seedSQL == "" {
			return nil
		}
		return pgcopy.Exec(ctx, target, p.seedSQL)
	}
	return pgcopy.Copy(ctx, spec.SourceURL, target)
}

// writeMeta records what was verified, inside the golden itself.
func (p *Provider) writeMeta(ctx context.Context, conn secrets.Value, version, rules, attestation string, at time.Time) error {
	db, err := sql.Open("pgx", conn.Reveal())
	if err != nil {
		return fmt.Errorf("db.dblab: open the candidate: %w", err)
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
		return fmt.Errorf("db.dblab: create the golden metadata table: %w", err)
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
		return fmt.Errorf("db.dblab: record the attestation: %w", err)
	}
	return nil
}

// findGolden resolves a version identifier to the snapshot that holds it.
//
// Both forms are accepted: the version this provider minted, and the engine's
// own snapshot identifier. The second matters because everything an operator
// sees on the engine's own interface is the snapshot identifier, and refusing
// it would make the two views of the same thing incompatible.
func (p *Provider) findGolden(ctx context.Context, version string) (Snapshot, bool, error) {
	snapshots, err := p.client.ListSnapshots(ctx)
	if err != nil {
		return Snapshot{}, false, p.wrapAuth(err)
	}
	var found []Snapshot
	for _, s := range snapshots {
		m, ours := decodeMeta(s.Message)
		if !ours {
			continue
		}
		if m.Version == version || s.ID == version {
			found = append(found, s)
		}
	}
	if len(found) == 0 {
		return Snapshot{}, false, nil
	}
	// Newest wins, deterministically. Two goldens can share a version
	// identifier only if two refreshes finished in the same second with the
	// same rules hash, which is possible and would otherwise make which one
	// gets branched depend on the order the engine happened to list them in.
	sort.Slice(found, func(i, j int) bool { return newerThan(found[i], found[j]) })
	return found[0], true, nil
}

// snapshotExists reports whether the engine holds a snapshot under this
// identifier at all, golden or not.
func (p *Provider) snapshotExists(ctx context.Context, id string) bool {
	snapshots, err := p.client.ListSnapshots(ctx)
	if err != nil {
		return false
	}
	for _, s := range snapshots {
		if s.ID == id {
			return true
		}
	}
	return false
}

// cloneCount reports how many clones currently come from a snapshot.
//
// Counted from the clone listing rather than from the snapshot's own numClones
// because the two disagree while a clone is being deleted, and the direction
// of the disagreement matters: a stale numClones would refuse a collection
// that is legitimate, and an operator who is told a version is referenced by
// something that no longer exists has no way to act on it.
func (p *Provider) cloneCount(ctx context.Context, snapshotID string) (int, error) {
	clones, err := p.client.ListClones(ctx)
	if err != nil {
		return 0, p.wrapAuth(err)
	}
	n := 0
	for _, c := range clones {
		if c.Status.Code == StatusDeleting {
			continue
		}
		if c.SnapshotID() == snapshotID {
			n++
		}
	}
	return n, nil
}

func (p *Provider) countBranches(ctx context.Context) (int, error) {
	clones, err := p.client.ListClones(ctx)
	if err != nil {
		return 0, p.wrapAuth(err)
	}
	n := 0
	for _, c := range clones {
		if strings.HasPrefix(c.ID, PrefixEnv) {
			n++
		}
	}
	return n, nil
}

// sweepCandidates removes candidate clones old enough that they can only be
// orphans.
func (p *Provider) sweepCandidates(ctx context.Context) {
	clones, err := p.client.ListClones(ctx)
	if err != nil {
		return // sweeping is opportunistic; a refresh must not fail because of it
	}
	cutoff := p.clock.Now().Add(-candidateMaxAge)
	for _, c := range clones {
		if !strings.HasPrefix(c.ID, PrefixCandidate) {
			continue
		}
		// A clone with no readable creation time is swept. The only way to get
		// one is a state file this client could not parse, and a candidate is
		// never referenced, so removing it cannot take anything away.
		if !c.CreatedAt.IsZero() && c.CreatedAt.After(cutoff) {
			continue
		}
		_ = p.client.DeleteClone(ctx, c.ID)
	}
}

// wrapAuth turns the engine's rejection of the verification token into
// something that says which credential to fix.
//
// Everything else passes through: an engine that is down, a connection that
// was refused, and a 500 are all things the caller should see as they are.
func (p *Provider) wrapAuth(err error) error {
	if err == nil {
		return nil
	}
	if Unauthorized(err) {
		return aferrors.Wrap(err, aferrors.AFDB008,
			"provider", "dblab", "endpoint", p.endpoint)
	}
	return err
}

// cloneSafe turns an identifier into one the engine accepts.
//
// The engine validates against ^[a-zA-Z0-9][a-zA-Z0-9_.-]*$, so an
// environment identifier only needs anything outside that set replaced. It is
// applied to the part after the prefix, and the prefix already starts with a
// letter, so the first character rule is satisfied by construction.
func cloneSafe(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '_', r == '.', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// newerThan orders snapshots newest first, preferring the recorded creation
// time and falling back to the identifier, which the engine builds from a
// timestamp and therefore sorts by age as a string.
func newerThan(a, b Snapshot) bool {
	if !a.CreatedAt.Equal(b.CreatedAt.Time) {
		return a.CreatedAt.After(b.CreatedAt.Time)
	}
	if !a.DataStateAt.Equal(b.DataStateAt.Time) {
		return a.DataStateAt.After(b.DataStateAt.Time)
	}
	return a.ID > b.ID
}

func joinInts(vs []int) string {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ", ")
}
