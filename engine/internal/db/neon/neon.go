package neon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

// Branch name prefixes.
//
// The prefix, and not the annotation, is what says whether a golden is
// published. Neon accepts an annotation when a branch is created and ignores
// one sent afterwards, and the attestation does not exist until the candidate
// has been masked and scanned, which is after the branch was created. A rename
// is the one atomic thing available at the right moment, so publishing is a
// rename and nothing else.
const (
	PrefixCandidate = "af-cand-"
	PrefixGolden    = "af-gv-"
	PrefixEnv       = "af-env-"
)

// MetaSchema is where the attestation is written inside the golden itself.
//
// In the database rather than beside it, because a verification statement is
// about that data and should travel with it. A branch of a golden inherits the
// row for free, so anybody holding an environment can read what was scanned and
// what was found without asking this process.
const MetaSchema = "_antifailure"

// readyTimeout is how long a new compute has to start answering.
const readyTimeout = 120 * time.Second

// candidateMaxAge is the age past which a candidate can only be an orphan.
//
// A candidate exists for the minutes between creating a branch and publishing
// it, and nothing ever branches from one, so removing an old one is
// unconditionally safe. That is what lets the provider heal itself rather than
// wait for somebody to read a leak report.
const candidateMaxAge = 2 * time.Hour

// Provider is the Neon implementation of provider.Database.
type Provider struct {
	client      *Client
	clock       clock.Clock
	seedSQL     string
	database    string
	role        string
	maxBranches int
}

// Options configure a provider.
type Options struct {
	// APIKey is a Neon API key. Required.
	APIKey secrets.Value
	// ProjectID is the Neon project branches are created in. Required: this
	// provider does not create projects, because a project is a billing
	// boundary and creating one on somebody's behalf is not this tool's call.
	ProjectID string
	// BaseURL overrides Neon's API root, for a test server.
	BaseURL string
	Clock   clock.Clock
	// SeedSQL is applied to a candidate when no source database is configured.
	SeedSQL string
	// Database and Role default to what Neon creates with a project.
	Database string
	Role     string
	// MaxBranches is the plan's concurrent branch limit. Zero means unlimited.
	// It is configuration rather than something read from the API, because the
	// limit is a property of the plan and Neon does not report it on a path
	// this provider can rely on.
	MaxBranches int
	// PollInterval and PollTimeout bound waiting for Neon's asynchronous
	// operations.
	PollInterval time.Duration
	PollTimeout  time.Duration
}

// New builds a provider.
func New(opts Options) (*Provider, error) {
	if opts.APIKey.IsZero() {
		return nil, errors.New("db.neon: an API key is required")
	}
	if opts.ProjectID == "" {
		return nil, errors.New("db.neon: a project id is required")
	}
	c := opts.Clock
	if c == nil {
		c = clock.New()
	}
	database := opts.Database
	if database == "" {
		database = "neondb"
	}
	role := opts.Role
	if role == "" {
		role = "neondb_owner"
	}
	return &Provider{
		client: &Client{
			BaseURL:      opts.BaseURL,
			Key:          opts.APIKey,
			ProjectID:    opts.ProjectID,
			Sleep:        c.Sleep,
			PollInterval: opts.PollInterval,
			PollTimeout:  opts.PollTimeout,
		},
		clock:       c,
		seedSQL:     opts.SeedSQL,
		database:    database,
		role:        role,
		maxBranches: opts.MaxBranches,
	}, nil
}

// Name identifies the provider in a manifest.
func (p *Provider) Name() string { return "neon" }

// Capabilities describes what Neon can do.
func (p *Provider) Capabilities() provider.Caps {
	return provider.Caps{
		Branching: true,
		// Neon restores a branch to another branch's state, which is exactly a
		// reset to the golden, sequences included.
		Reset: true,
		// Branches share storage with their parent, so branch time does not
		// grow with database size. This is the reason to use Neon at all.
		CopyOnWrite: true,
		// Neon can create an anonymized branch, but this provider does not use
		// it: the engine's own rules are the single implementation of masking,
		// and a provider's masking is a claim where verification is a check.
		ProviderMasking: false,
		// A candidate here is a branch of the production project's default
		// branch, so it holds the whole database the moment it exists and
		// there is no empty database to load a slice into. Subsetting would
		// have to mean deleting down, which copies everything first and so
		// saves nothing on the one provider where the copy was already free.
		// Declared false rather than left to look like an oversight.
		Subsetting:            false,
		PooledEndpoints:       true,
		MaxConcurrentBranches: p.maxBranches,
		// Generous, because it crosses the public internet to a compute that
		// may be starting cold. Tight enough that a hung call fails the test
		// rather than the job.
		ExpectedBranchLatency: 60 * time.Second,
		SupportedVersions:     []int{14, 15, 16, 17},
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

	parent, err := p.client.DefaultBranch(ctx)
	if err != nil {
		return provider.GoldenVersion{}, err
	}

	version := provider.NewGoldenVersionID(p.clock.Now(), spec.RulesHash)
	created := p.clock.Now().UTC()
	candidate, _, err := p.client.CreateBranch(ctx, CreateBranchRequest{
		Name:         PrefixCandidate + branchSafe(version),
		ParentID:     parent.ID,
		WithEndpoint: true,
		Annotation: map[string]string{
			AnnKind:      "golden",
			AnnVersion:   version,
			AnnRulesHash: spec.RulesHash,
			AnnCreatedAt: created.Format(time.RFC3339),
		},
	})
	if err != nil {
		if candidate.ID != "" {
			_ = p.client.DeleteBranch(context.WithoutCancel(ctx), candidate.ID)
		}
		return provider.GoldenVersion{}, err
	}

	// Removed unless the refresh gets all the way to the rename. A failed
	// verification that left a branchable copy of unmasked production behind
	// would be the worst possible outcome here.
	published := false
	defer func() {
		if !published {
			_ = p.client.DeleteBranch(context.WithoutCancel(ctx), candidate.ID)
		}
	}()

	conn, err := p.client.ConnectionURI(ctx, candidate.ID, p.database, p.role, false)
	if err != nil {
		return provider.GoldenVersion{}, err
	}
	if err := p.waitReady(ctx, conn); err != nil {
		return provider.GoldenVersion{}, err
	}
	if err := p.loadSource(ctx, conn, spec); err != nil {
		return provider.GoldenVersion{}, err
	}

	if spec.Mask != nil {
		if err := spec.Mask(ctx, conn); err != nil {
			return provider.GoldenVersion{}, fmt.Errorf("db.neon: mask the golden candidate: %w", err)
		}
	}
	attestation := ""
	if spec.Verify != nil {
		attestation, err = spec.Verify(ctx, conn)
		if err != nil {
			return provider.GoldenVersion{}, fmt.Errorf("db.neon: verify the golden candidate: %w", err)
		}
	}

	if err := p.writeMeta(ctx, conn, version, spec.RulesHash, attestation, created); err != nil {
		return provider.GoldenVersion{}, err
	}

	// The publish. Everything above can fail and leave nothing branchable;
	// after this the version exists.
	if err := p.rename(ctx, candidate.ID, PrefixGolden+branchSafe(version)); err != nil {
		return provider.GoldenVersion{}, err
	}
	published = true

	gv := provider.GoldenVersion{
		ID: version, CreatedAt: created, RulesHash: spec.RulesHash,
		Verified: spec.Verify != nil, Attestation: attestation, ProviderRef: candidate.ID,
	}
	if fresh, err := p.client.GetBranch(ctx, candidate.ID); err == nil {
		gv.SizeBytes = fresh.LogicalSize
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
		if !strings.HasPrefix(b.Name, PrefixGolden) {
			continue
		}
		out = append(out, p.goldenFrom(b))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

func (p *Provider) goldenFrom(b Branch) provider.GoldenVersion {
	gv := provider.GoldenVersion{
		ID:          b.Annotation[AnnVersion],
		RulesHash:   b.Annotation[AnnRulesHash],
		SizeBytes:   b.LogicalSize,
		ProviderRef: b.ID,
		// A branch carries the golden prefix only because a refresh renamed it
		// after verification returned without an error. The attestation itself
		// is in the branch's own database, because it did not exist when the
		// annotation was written and Neon ignores an annotation sent later.
		Verified: true,
	}
	if gv.ID == "" {
		gv.ID = strings.TrimPrefix(b.Name, PrefixGolden)
	}
	if at, err := time.Parse(time.RFC3339, b.Annotation[AnnCreatedAt]); err == nil {
		gv.CreatedAt = at
	} else {
		gv.CreatedAt = b.CreatedAt
	}
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

	// Refused while anything came from it. Neon would refuse the delete too,
	// but with its own message about children, and a coded error naming the
	// count is what the operator can act on.
	referencing := 0
	for _, b := range branches {
		if b.ParentID == golden.ID && strings.HasPrefix(b.Name, PrefixEnv) {
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
	for _, b := range branches {
		if b.Annotation[AnnEnvID] == envID && strings.HasPrefix(b.Name, PrefixEnv) {
			return provider.Branch{
				EnvID: envID, From: b.Annotation[AnnFrom],
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
		live := countPrefix(branches, PrefixEnv)
		if live >= p.maxBranches {
			return provider.Branch{}, aferrors.Coded(aferrors.AFDB006,
				"limit", strconv.Itoa(p.maxBranches))
		}
	}

	created, _, err := p.client.CreateBranch(ctx, CreateBranchRequest{
		Name:         PrefixEnv + branchSafe(envID),
		ParentID:     golden.ID,
		WithEndpoint: true,
		Annotation: map[string]string{
			AnnKind:      "branch",
			AnnEnvID:     envID,
			AnnFrom:      version,
			AnnCreatedAt: p.clock.Now().UTC().Format(time.RFC3339),
		},
	})
	if err != nil {
		// Neon's own ceiling, which is the real one: the configured limit
		// above is what this provider was told, and the plan is what actually
		// decides. Both arrive as AF-DB-006 so an operator gets one answer.
		if LimitExceeded(err) {
			limit := p.maxBranches
			if limit == 0 {
				limit = countPrefix(branches, PrefixEnv)
			}
			return provider.Branch{}, aferrors.Wrap(err, aferrors.AFDB006,
				"limit", strconv.Itoa(limit))
		}
		// Left in place rather than cleaned up. Inventory reports it, so the
		// leak detector can see it; deleting it here would hide the evidence.
		return provider.Branch{}, err
	}

	return provider.Branch{
		EnvID: envID, From: version, ProviderRef: created.ID,
		CreatedAt: created.CreatedAt,
	}, nil
}

// Reset returns a branch to its golden's state, sequences included.
func (p *Provider) Reset(ctx context.Context, b provider.Branch) error {
	branches, err := p.client.ListBranches(ctx)
	if err != nil {
		return err
	}
	golden, ok := findGolden(branches, b.From)
	if !ok {
		return aferrors.Coded(aferrors.AFDB004, "version", b.From)
	}
	return p.client.RestoreBranch(ctx, b.ProviderRef, golden.ID)
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
	return p.client.ConnectionURI(ctx, b.ProviderRef, p.database, p.role, mode == provider.ConnPooled)
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
		switch {
		case strings.HasPrefix(b.Name, PrefixGolden):
			kind = "golden"
		case strings.HasPrefix(b.Name, PrefixCandidate):
			kind = "candidate"
		case strings.HasPrefix(b.Name, PrefixEnv):
			kind = "branch"
		default:
			// Not ours. A Neon project can hold branches somebody made by
			// hand, and reporting those as leaks is how a leak report becomes
			// something people learn to ignore.
			continue
		}
		out = append(out, provider.Resource{
			Kind:      kind,
			ID:        b.ID,
			EnvID:     b.Annotation[AnnEnvID],
			CreatedAt: b.CreatedAt,
			Labels: map[string]string{
				"name":    b.Name,
				"version": b.Annotation[AnnVersion],
				"from":    b.Annotation[AnnFrom],
				"state":   b.CurrentState,
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
	return provider.Health{
		Reachable: true,
		Latency:   p.clock.Now().Sub(start),
	}, nil
}

// ---------------------------------------------------------------------------
// Internals
// ---------------------------------------------------------------------------

func (p *Provider) waitReady(ctx context.Context, conn secrets.Value) error {
	err := pgcopy.WaitReady(ctx, conn, readyTimeout, p.clock.Now, p.clock.Sleep)
	if err == nil || ctx.Err() != nil {
		return err
	}
	return aferrors.Wrap(err, aferrors.AFDB002, "host", "neon")
}

func (p *Provider) loadSource(ctx context.Context, target secrets.Value, spec provider.GoldenSpec) error {
	if spec.SourceURL.IsZero() || strings.Contains(spec.SourceURL.Reveal(), "@source/") {
		// No real source. A project that has not connected production yet
		// still needs a schema to branch, and the seed is what provides it.
		return pgcopy.Exec(ctx, target, p.seedSQL)
	}
	return pgcopy.Copy(ctx, spec.SourceURL, target)
}

// writeMeta records what was verified, inside the golden itself.
func (p *Provider) writeMeta(ctx context.Context, conn secrets.Value, version, rules, attestation string, at time.Time) error {
	db, err := sql.Open("pgx", conn.Reveal())
	if err != nil {
		return fmt.Errorf("db.neon: open the candidate: %w", err)
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
		return fmt.Errorf("db.neon: create the golden metadata table: %w", err)
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
		return fmt.Errorf("db.neon: record the attestation: %w", err)
	}
	return nil
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
	conn, err := p.client.ConnectionURI(ctx, golden.ID, p.database, p.role, false)
	if err != nil {
		return "", err
	}
	db, err := sql.Open("pgx", conn.Reveal())
	if err != nil {
		return "", fmt.Errorf("db.neon: open the golden: %w", err)
	}
	defer func() { _ = db.Close() }()

	var attestation string
	err = db.QueryRowContext(ctx,
		`SELECT attestation FROM `+MetaSchema+`.golden WHERE version = $1`, version).Scan(&attestation)
	if err != nil {
		return "", fmt.Errorf("db.neon: read the attestation for %s: %w", version, err)
	}
	return attestation, nil
}

func (p *Provider) rename(ctx context.Context, id, name string) error {
	var env struct {
		Operations []Operation `json:"operations"`
	}
	body := map[string]any{"branch": map[string]string{"name": name}}
	if err := p.client.do(ctx, "PATCH",
		"/projects/"+p.client.ProjectID+"/branches/"+id, body, &env); err != nil {
		return fmt.Errorf("db.neon: publish the golden: %w", err)
	}
	return p.client.Await(ctx, env.Operations)
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

func findGolden(branches []Branch, version string) (Branch, bool) {
	for _, b := range branches {
		if !strings.HasPrefix(b.Name, PrefixGolden) {
			continue
		}
		if b.Annotation[AnnVersion] == version || b.Name == PrefixGolden+branchSafe(version) {
			return b, true
		}
	}
	return Branch{}, false
}

func findCandidate(branches []Branch, version string) (Branch, bool) {
	for _, b := range branches {
		if !strings.HasPrefix(b.Name, PrefixCandidate) {
			continue
		}
		if b.Annotation[AnnVersion] == version || b.Name == PrefixCandidate+branchSafe(version) {
			return b, true
		}
	}
	return Branch{}, false
}

// branchSafe turns an identifier into something Neon accepts as a branch name
// and a human can still read. The exact value is kept in the annotation, so
// this only has to be stable and unambiguous, not reversible.
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

func countPrefix(branches []Branch, prefix string) int {
	n := 0
	for _, b := range branches {
		if strings.HasPrefix(b.Name, prefix) {
			n++
		}
	}
	return n
}
