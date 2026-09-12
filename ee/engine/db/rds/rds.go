// Package rds is the database provider for Amazon RDS for PostgreSQL.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
// It is enterprise by the editions rule in the plan: reaching a production RDS
// instance needs an IAM role somebody with an organization grants, which is not
// one developer with their own card.
//
// THE SENTENCE THIS PROVIDER EXISTS TO SAY OUT LOUD.
//
// Plain RDS has no clone. A branch here is a SNAPSHOT RESTORE: RDS provisions a
// new instance and hydrates a new volume from the snapshot, and the volume is
// every byte of the database. So branch time grows with the database, this
// provider declares CopyOnWrite FALSE, and its number in the wave's comparison
// table is minutes rather than seconds.
//
// That is deliberate and it is not hedged. Enterprise Postgres lives in RDS,
// which makes this the row a buyer is most likely to be reading about
// themselves, and a buyer who discovers from their own failed trial that
// branching takes minutes is a lost customer who was told something untrue. A
// buyer who learned it here trusts the rest of the table. Aurora is the fast
// row and it is a different provider with a different mechanism; this one
// refuses to be pointed at an Aurora cluster rather than quietly becoming the
// slow half of it, exactly as the Aurora provider refuses to be pointed here.
//
// The model:
//
//   - The SOURCE is an RDS for PostgreSQL DB instance, named by
//     database.project. It is never written to, and it is read only as a
//     snapshot.
//   - A GOLDEN is a manual DB SNAPSHOT. Building one takes a snapshot of the
//     source, restores that into a candidate instance, applies the masking
//     rules to the candidate, runs the verification scanner against it, and
//     only then snapshots the candidate. The candidate instance and the
//     intermediate snapshot are then deleted, so a published golden costs
//     snapshot storage and no compute.
//   - A BRANCH is an instance restored from a golden snapshot, with its master
//     password rotated away from the one it inherited.
//
// A golden being a snapshot rather than a live instance is the one place this
// mechanism is cheaper than the fast one: an RDS snapshot demonstrably exists
// without an instance attached, so keeping several goldens costs storage
// alone. Nothing else about it is cheaper.
//
// Nothing here is renamed or deleted on the strength of its name. Every
// instance and every snapshot this provider creates carries an antifailure
// tag, and every destructive path reads that tag first, for the reason pgurl
// reads a comment: a customer whose own instance is called af-b-something must
// not lose it to our garbage collection.
//
// Three things this provider deliberately does NOT do, named because a
// capability that is named and not built is worse than one that is absent:
//
//   - It does not implement Reset. RDS has no restore in place and no rewind,
//     so the only way back to golden is to delete the instance and restore the
//     snapshot again, which is what the capability is defined AGAINST. Reset
//     returns provider.ErrUnsupported and the conformance suite skips that
//     behaviour by name rather than passing it silently. internal/db/pgurl
//     reads the same field the other way and declares Reset true for a drop
//     and recreate, and the difference is not an inconsistency: there the
//     recreate is a local file copy costing seconds, and here it is a restore
//     costing minutes, so calling it a reset would be selling a cheap
//     operation that is the expensive one.
//   - It does not implement Subsetting. A candidate here is a restore of the
//     source rather than an empty instance, so there is nothing to load a
//     slice into; a manifest asking for a subset is refused naming this
//     provider rather than copying everything and calling it a subset.
//   - It does not implement IAM database authentication. RDS supports it, it
//     would be the better credential, and it is not here.
//
// WHAT THE TESTS IN THIS PACKAGE PROVE, AND WHAT THEY DO NOT.
//
// The conformance suite runs against a fake RDS control plane over a real
// local Postgres. It proves this provider's logic, the shape of every request
// it sends, that those requests are signed correctly for the right region and
// service, and what it does with each documented response and each documented
// fault. It does NOT prove that AWS accepts those requests, and it CANNOT
// produce a wall clock number for a real RDS snapshot and restore, because the
// thing being timed would be the fake. Every place a number could be mistaken
// for one says so, and where a number was asked for and could not be measured
// this package publishes the refusal rather than an estimate.
//
// THE SHARPEST CASE OF THAT IS THE ONE THAT PASSES, so it is named here rather
// than left to be inferred from a green.
// CopyOnWrite_BranchTimeMatchesTheDeclaration in the shared suite requires a
// provider declaring CopyOnWrite FALSE to show that branch time GREW with the
// data, and it does, every time, because the only branch a fake control plane
// over a local Postgres can make is a real copy. That is true of the simulator
// whatever AWS would have done. The green covers this provider's logic and the
// instrument's ability to refuse the opposite declaration, which the self test
// exercises. It covers nothing about RDS. A pass whose scope is not written
// down is the thing that survives unexamined, because nobody reads a green.
package rds

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the pgx driver

	"github.com/antifailure/antifailure/ee/engine/cloudauth"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

// Name is the value database.provider takes in a manifest.
const Name = "rds"

// enginePostgres is the only engine this provider will touch.
//
// Checked rather than assumed. "aurora-postgresql" restores from a snapshot
// exactly as happily and is a cluster whose branch should be a clone, which is
// the aurora provider and a different number in the comparison table; "mysql"
// produces a database no Postgres tool can read.
const enginePostgres = "postgres"

// engineAurora is the engine this provider refuses by name, so that the
// refusal can say which provider to use instead.
const engineAurora = "aurora-postgresql"

// The tag keys this provider owns. A resource without the first one is
// somebody else's, whatever it is called.
const (
	tagMarker      = "antifailure"
	tagKind        = "antifailure:kind"
	tagVersion     = "antifailure:version"
	tagEnv         = "antifailure:env"
	tagRules       = "antifailure:rules"
	tagProvenance  = "antifailure:provenance"
	tagCreated     = "antifailure:created"
	tagAttestation = "antifailure:attestation"
)

// The values tagKind takes.
const (
	kindCandidate = "candidate"
	kindTransit   = "transit"
	kindGolden    = "golden"
	kindBranch    = "branch"
)

// The identifier prefixes, one per kind.
//
// Short, because an RDS identifier is 63 characters, may not contain an
// underscore, may not hold two consecutive hyphens and may not end in one. The
// environment identifier that goes after the prefix is not this package's to
// shorten, so every character spent here is one a name cannot have.
const (
	goldenPrefix    = "af-g-"
	branchPrefix    = "af-b-"
	candidatePrefix = "af-c-"
	transitPrefix   = "af-t-"
)

// identifierLimit is what RDS allows in a DB instance or DB snapshot
// identifier.
const identifierLimit = 63

// tagValueLimit is the number of characters AWS allows in one tag value.
//
// The attestation is the only thing this provider records that can exceed it,
// so it is split across numbered tags. The split is bounded and the boundary
// REFUSES rather than truncating: a golden whose attestation was silently cut
// in half would still read as verified, and the attestation is the record of
// what was scanned.
const tagValueLimit = 256

// attestationChunks is how many tags the attestation may occupy. Fifty tags
// per resource is the AWS limit and the rest of this provider's metadata takes
// six of them.
const attestationChunks = 32

// candidateAge is how old a candidate has to be before it can only be an
// orphan. A refresh that is still running holds one for as long as the
// snapshot and the restore take, which on a large database is far more than a
// few minutes, so this is deliberately generous: removing a candidate another
// process is masking would turn one slow refresh into a corrupt golden.
const candidateAge = 6 * time.Hour

// DefaultVariable is the manifest named variable this provider derives every
// instance's master password from.
//
// It is NOT the source instance's password, and that is the point. A restore
// inherits the snapshot's master credential, so a provider that did nothing
// would hand production's database password to every preview environment. This
// one rotates each restored instance's master password to an HMAC of this key
// and the instance identifier, immediately after the restore and before
// anything connects. The value is deterministic, so a later process can
// rebuild a connection string without anything having stored a password; it is
// per-instance, so a preview's credential opens the preview and nothing else;
// and the source's own password is never read, never held, and never needed.
const DefaultVariable = "AF_RDS_BRANCH_KEY"

// The other variables this provider reads, all through the engine's chain and
// never from the process environment. Only the first is required.
const (
	// RegionVariable names the region the instance is in. An instance in
	// eu-west-1 does not exist in us-east-1 and the API call would answer that
	// it does not exist, which is a confusing way to learn about a typo.
	RegionVariable = "AWS_REGION"
	// InstanceClassVariable is the instance class a branch gets. An empty
	// value means the class the snapshot's instance had, which is what RDS
	// does when the parameter is absent.
	InstanceClassVariable = "AF_RDS_INSTANCE_CLASS"
	// EndpointVariable overrides the regional RDS endpoint. A customer inside
	// a VPC reaches RDS through an interface endpoint with its own hostname.
	// The request is still signed for the configured region, so an override
	// cannot silently move which region it is valid in.
	EndpointVariable = "AF_RDS_ENDPOINT"
	// TLSModeVariable is the sslmode of the connection strings this provider
	// hands out. It defaults to verify-full, verify-full is the only value
	// accepted for a remote endpoint, and disable is accepted only for a
	// loopback one, which is the test fixture and nothing else.
	TLSModeVariable = "AF_RDS_SSLMODE"
)

// defaultBranchLatency is what this provider DECLARES a branch will take, and
// the distinction between declaring and measuring is the whole of this
// comment.
//
// It is not a measurement. Nobody in this repository has an RDS account, the
// conformance suite runs against a fake control plane over a local Postgres,
// and a number produced by timing that would be a number about this laptop. So
// this is a CEILING the provider undertakes not to exceed: the suite's
// Branch_IsWithinTheDeclaredLatency behaviour fails when a branch is slower
// than what is declared here, which means a real account that got slower
// fails a test instead of degrading quietly.
//
// Fifteen minutes because a restore is two things in series and both are slow.
// RDS provisions a new instance, which is minutes on its own and does not
// depend on the data, and then hydrates a volume from a snapshot in S3, which
// does. Setting it lower would be an optimism nothing here has measured;
// setting it higher would stop the assertion refusing anything. It is
// overridable, because an account restoring a terabyte legitimately needs
// longer and the alternative to a knob is a lane quietly declaring a number
// large enough to never fire.
const defaultBranchLatency = 15 * time.Minute

// DefaultBranchLatency is the ceiling this provider declares when a manifest
// sets none.
//
// Exported so that the benchmark can publish the declaration beside its
// refusal to measure it, rather than repeating a constant that could drift
// away from the one the provider actually uses. A report quoting a number the
// code no longer holds is worse than a report with no number.
func DefaultBranchLatency() time.Duration { return defaultBranchLatency }

// Options configure the provider.
type Options struct {
	// SourceInstance is the RDS for PostgreSQL DB instance identifier goldens
	// are built from. Required; it is database.project in a manifest.
	SourceInstance string
	// Region is the AWS region the instance lives in. Required.
	Region string
	// BranchKey is the secret every instance's master password is derived
	// from. Required.
	BranchKey secret.Value
	// Variable names the manifest variable BranchKey came from, so a refusal
	// can say which one to fix.
	Variable string
	// Endpoint overrides the regional RDS endpoint.
	Endpoint string
	// InstanceClass is the class a candidate and a branch get. Empty means the
	// class recorded in the snapshot, which is what RDS defaults to.
	InstanceClass string
	// TLSMode is the sslmode of the connection strings handed out.
	TLSMode string
	// MaxBranches is the concurrent branch ceiling. Zero means the provider
	// imposes none and the account's own instance quota is the limit.
	MaxBranches int
	// Getenv is how the AWS credential chain reads its environment. It is
	// supplied by the caller so that a provider never reads the process
	// environment itself; the registration wires it to the engine's secret
	// chain.
	Getenv func(string) string
	// Credentials supplies AWS keys directly instead of discovering them.
	Credentials *cloudauth.AWSCredentials
	// Now is the clock. Library code here never calls time.Now, so that
	// timeouts are testable without waiting for wall time.
	Now func() time.Time
	// PollInterval is how often an instance or a snapshot is asked whether it
	// is ready yet.
	PollInterval time.Duration
	// ReadyTimeout bounds waiting for an instance or a snapshot.
	ReadyTimeout time.Duration
	// BranchLatency overrides the declared ExpectedBranchLatency.
	BranchLatency time.Duration
	// SkipEngineCheck skips only the engine validation. The source is still
	// described, and its account, region and networking are still bound,
	// because those decide which resources this provider may touch. It is
	// deliberately not reachable from a manifest: the engine check is what
	// stops this provider being pointed at an Aurora cluster, where the honest
	// answer is the faster provider.
	SkipEngineCheck bool
}

// Provider is the RDS for PostgreSQL database provider.
type Provider struct {
	// scope, arnPrefix and partition are the source instance's identity. See
	// bindSource in security.go.
	scope, arnPrefix, partition string
	// loginCatalog lists the logins a restored instance inherited. It is a
	// field only so the test binary can scope it to one fixture on a shared
	// Postgres; the shipped value is customerLogins.
	loginCatalog func(context.Context, *sql.DB) ([]string, error)
	closed       atomic.Bool
	// The trust bundle file this process wrote for its own connections.
	trustMu             sync.Mutex
	trustDir, trustPath string
	trustOverride       string

	api           *client
	source        string
	branchKey     secret.Value
	variable      string
	instanceClass string
	tlsMode       string
	maxBranches   int
	branchLatency time.Duration
	now           func() time.Time
	poll          time.Duration
	readyTimeout  time.Duration
	// major is the source instance's Postgres major version, read from the
	// instance rather than taken from the manifest. A restore is the same
	// Postgres the snapshot is, so the manifest cannot choose.
	major int
}

// New returns a provider for the configured source instance.
//
// It describes the source before it returns, which decides three things in one
// round trip that would otherwise each be discovered at the worst moment: that
// the credentials work, that the instance exists in that region, and that it is
// RDS for PostgreSQL rather than Aurora or MySQL. Learning the third one after
// a refresh has already taken a snapshot is learning it after the expensive
// part.
func New(ctx context.Context, opts Options) (*Provider, error) {
	if opts.SourceInstance == "" {
		return nil, fmt.Errorf(
			"the rds provider needs database.project, which is the RDS for PostgreSQL " +
				"DB INSTANCE identifier goldens are built from")
	}
	if opts.Region == "" {
		return nil, fmt.Errorf(
			"the rds provider needs a region (%s); an instance in eu-west-1 does not "+
				"exist in us-east-1, and asking the wrong region answers that it is not there",
			RegionVariable)
	}
	variable := opts.Variable
	if variable == "" {
		variable = DefaultVariable
	}
	if opts.BranchKey.IsZero() {
		return nil, fmt.Errorf(
			"the rds provider needs %s, which is the secret every restored instance's "+
				"master password is derived from. It is not the source instance's password: "+
				"this provider rotates each restore away from the credential it inherited, "+
				"so that a preview environment never holds production's database password",
			variable)
	}
	if opts.MaxBranches < 0 {
		return nil, fmt.Errorf("a negative branch limit is not meaningful; use zero for unlimited")
	}

	p := &Provider{
		api: &client{
			region:   opts.Region,
			endpoint: endpointFor(opts.Region, opts.Endpoint),
			// A getenv that reads nothing rather than a nil one, and that is a
			// guard rather than a formality. cloudauth.AWSChain calls the
			// function it is given without checking it, so a provider built
			// without one would panic on the first request that had to
			// discover credentials. Reading nothing is also the direction this
			// has to fail in: a provider that forgot to wire the engine's
			// chain finds no credentials rather than quietly signing with
			// whatever is exported in the operator's shell.
			chain: cloudauth.NewAWSChain(getenvOr(opts.Getenv), opts.Credentials),
		},
		source:        opts.SourceInstance,
		branchKey:     opts.BranchKey,
		variable:      variable,
		instanceClass: opts.InstanceClass,
		tlsMode:       or(opts.TLSMode, "verify-full"),
		loginCatalog:  customerLogins,
		maxBranches:   opts.MaxBranches,
		branchLatency: opts.BranchLatency,
		now:           opts.Now,
		poll:          opts.PollInterval,
		readyTimeout:  opts.ReadyTimeout,
	}
	if p.now == nil {
		p.now = time.Now
	}
	if p.poll <= 0 {
		p.poll = 10 * time.Second
	}
	if p.readyTimeout <= 0 {
		p.readyTimeout = 45 * time.Minute
	}
	if p.branchLatency <= 0 {
		p.branchLatency = defaultBranchLatency
	}

	if p.tlsMode != "verify-full" && p.tlsMode != "disable" {
		return nil, fmt.Errorf(
			"%s is %q, and this provider accepts verify-full, or disable against a loopback "+
				"endpoint. Anything between them encrypts a copy of production without checking "+
				"who is on the other end", TLSModeVariable, p.tlsMode)
	}

	source, found, err := p.api.describeInstance(ctx, opts.SourceInstance)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf(
			"no DB instance named %q exists in %s. database.project is an RDS DB INSTANCE "+
				"identifier, not a cluster identifier and not an endpoint hostname",
			opts.SourceInstance, opts.Region)
	}
	if !opts.SkipEngineCheck && source.Engine == engineAurora {
		// Refused rather than served, and this is the mirror image of the
		// refusal the aurora provider makes when it is pointed here. A
		// snapshot restore of an Aurora cluster works, so this would appear to
		// succeed while turning a clone that is flat in the database's size
		// into a copy that is not. Nobody measures a thing that still works.
		return nil, fmt.Errorf(
			"%q runs %s, which is Aurora, and this provider restores snapshots of RDS "+
				"for PostgreSQL. A snapshot restore of an Aurora cluster copies every byte "+
				"and takes minutes where a clone takes seconds, so this provider refuses "+
				"rather than becoming the slow half of that one silently. Set "+
				"database.provider to aurora",
			opts.SourceInstance, source.Engine)
	}
	if !opts.SkipEngineCheck && source.Engine != enginePostgres {
		return nil, fmt.Errorf(
			"%q runs %s, and this provider handles %s. A snapshot of another engine "+
				"restores into a database no Postgres tool can read",
			opts.SourceInstance, source.Engine, enginePostgres)
	}
	if err := p.bindSource(source); err != nil {
		return nil, err
	}
	p.major = majorOf(source.EngineVersion)
	if p.major == 0 && opts.SkipEngineCheck {
		p.major = 17
	}
	return p, nil
}

// getenvOr is the credential chain's environment reader, never nil.
//
// cloudauth.AWSChain dereferences the function it is given, so a nil one is a
// panic on the first discovery rather than a refusal. Reading nothing is the
// safe answer: every credential a provider uses is meant to be declared and
// resolved through the engine's chain, so a provider with no reader should find
// nothing rather than fall through to the operator's shell.
func getenvOr(getenv func(string) string) func(string) string {
	if getenv == nil {
		return func(string) string { return "" }
	}
	return getenv
}

func or(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// majorOf reads the major from an RDS PostgreSQL engine version such as
// "16.4" or "15.6".
func majorOf(version string) int {
	head, _, _ := strings.Cut(version, ".")
	n, err := strconv.Atoi(strings.TrimSpace(head))
	if err != nil || n < 12 || n > 30 {
		// Zero would make Capabilities.Supports answer false for everything,
		// which is a worse failure than reporting nothing: the restore is
		// whatever the snapshot is regardless of what this parsed.
		return 0
	}
	return n
}

// Name identifies the provider, and is what appears in a manifest.
func (p *Provider) Name() string { return Name }

// Capabilities describes what this provider can do.
//
// CopyOnWrite is FALSE, and it is the most important word in this file. A
// restore from a snapshot hydrates a new volume with every byte of the
// database, so branch time grows with the data. Declaring it true would put a
// flat number in the wave's comparison table for the provider whose number is
// not flat, and the buyer reading that row is the one most likely to be on
// plain RDS.
//
// The declaration is now falsifiable rather than decorative.
// CopyOnWrite_BranchTimeMatchesTheDeclaration in engine/conformance/cow.go
// times a branch of a small golden and of one half a gibibyte larger and
// requires that the extra time EXCEEDS what the machine's noise can account
// for when the declaration is false, and that it does not when it is true.
// Exactly one of those holds for any measurement, so the suite refuses one of
// the two possible declarations on every run. Declaring true here fails that
// behaviour against the package's own conformance run, and that is checked
// rather than asserted: internal_test.go inverts this field and requires the
// behaviour to go red.
//
// Reset and Subsetting are false for reasons the package comment gives in
// full. Both are capabilities RDS could be made to appear to have and does not
// have.
func (p *Provider) Capabilities() provider.Caps {
	return provider.Caps{
		Branching:             true,
		Reset:                 false,
		CopyOnWrite:           false,
		ProviderMasking:       false,
		PooledEndpoints:       false,
		Subsetting:            false,
		MaxConcurrentBranches: p.maxBranches,
		ExpectedBranchLatency: p.branchLatency,
		SupportedVersions:     []int{p.major},
	}
}

// Close removes the trust bundle file this process wrote, and refuses every
// later operation. This provider holds no long lived connection: every
// operation that needs Postgres opens a connection, uses it, and closes it,
// and everything else is an HTTPS request.
func (p *Provider) Close() error { return p.closeTrust() }

// APICalls is how many control plane requests this provider has sent.
//
// Exported because it is the ONE number about this provider's cost that can be
// measured without an AWS account: it is a property of the provider's own
// logic rather than of AWS's response time. benchmark_test.go publishes it,
// and publishes beside it the refusal to convert it into seconds.
func (p *Provider) APICalls() int64 { return p.api.calls.Load() }

// ---------------------------------------------------------------------------
// Goldens
// ---------------------------------------------------------------------------

// RefreshGolden builds a new masked, verified golden version.
//
// The order is the one every provider in this repository follows and it is not
// negotiable: produce a candidate, mask it, verify it, and only then publish.
// Publishing here is the creation of the golden snapshot itself, which is why
// there is no window in which a half built golden is visible as one: the
// listing reads snapshots tagged golden, and the only such snapshot is created
// after the verifier has returned.
//
// Four control plane operations before anything is masked, and they are the
// mechanism rather than an implementation detail: snapshot the source, restore
// that snapshot into a candidate instance, wait for the instance, rotate its
// password. A cluster clone is one operation. This is the difference the
// comparison table is about.
func (p *Provider) RefreshGolden(ctx context.Context, spec provider.GoldenSpec) (result provider.GoldenVersion, retErr error) {
	if p.closed.Load() {
		return provider.GoldenVersion{}, fmt.Errorf("rds: provider is closed")
	}
	if spec.Mask == nil || spec.Verify == nil {
		// Refused rather than published unverified. A golden here is handed to
		// preview environments as a copy of production, and one built without
		// a masking step or a scanner is a copy of production nobody checked.
		return provider.GoldenVersion{}, fmt.Errorf(
			"rds: a golden needs both a masking step and a verification scanner, and this "+
				"refresh was given %s", missingSteps(spec))
	}
	if p.major != 0 && !p.Capabilities().Supports(spec.Version) {
		return provider.GoldenVersion{}, fmt.Errorf(
			"the manifest asks for Postgres %d and %s runs %d. A snapshot restores as the "+
				"version it was taken from, so this provider cannot change it",
			spec.Version, p.source, p.major)
	}
	if spec.Load != nil {
		// Refused rather than ignored. Caps.Subsetting is false, so the engine
		// should never set this, and a provider that accepted it and restored
		// the whole source anyway would have made a manifest key that reads as
		// configuration and behaves as decoration.
		return provider.GoldenVersion{}, fmt.Errorf(
			"the manifest asks for a subset and the rds provider cannot take one. A " +
				"candidate here is a restore of the source instance rather than an empty " +
				"database, so there is nothing to load a slice into")
	}

	// What a killed refresh left behind, before this one adds to it. A
	// candidate is a full restore of production with an instance attached, so
	// one abandoned by a process that died between the restore and the publish
	// goes on billing until somebody notices it in a console. It is swept on
	// the way in rather than by a timer, because a refresh is the only moment
	// this provider is certainly running.
	p.sweepCandidates(ctx)

	created := p.now().UTC()
	version := provider.NewGoldenVersionID(created, spec.RulesHash)
	// The scope is in the digest, so two sources in one account that refresh
	// in the same instant cannot be handed the same identifiers.
	short := shortHash(p.scope + "\n" + version)
	transit := transitPrefix + short
	candidate := candidatePrefix + short
	golden := goldenPrefix + short

	base := map[string]string{
		tagMarker:  Name,
		tagScope:   p.scope,
		tagVersion: version,
		tagCreated: created.Format(time.RFC3339Nano),
	}

	// Everything from here can fail, and anything left behind is something
	// somebody pays for. The intermediate snapshot and the candidate instance
	// are removed on every path, and neither is ever tagged golden, so a
	// process killed between any two of these lines leaves something the
	// inventory reports and ListGoldens does not publish.
	//
	// The candidate is removed only when this attempt is known to own it. A
	// restore whose response was lost may have created nothing, or something
	// at that name that is not ours, and deleting on a guess is how a provider
	// destroys a resource it never made.
	publish := false
	candidateOurs := false
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Minute)
		defer cancel()
		if candidateOurs {
			if err := p.destroyInstance(cleanup, candidate); err != nil && retErr != nil {
				result = provider.GoldenVersion{ID: version, ProviderRef: candidate}
				retErr = fmt.Errorf("%w; the candidate %s could not be removed: %v", retErr, candidate, err)
			}
		}
		_ = p.destroySnapshot(cleanup, transit)
		if !publish {
			_ = p.destroySnapshot(cleanup, golden)
		}
	}()

	if _, err := p.api.createSnapshot(ctx, transit, p.source, withKind(base, kindTransit)); err != nil {
		return provider.GoldenVersion{}, p.refreshError(err)
	}
	if err := p.waitSnapshot(ctx, transit); err != nil {
		return provider.GoldenVersion{}, err
	}
	ours, err := p.restore(ctx, candidate, transit, withKind(base, kindCandidate))
	candidateOurs = ours
	if err != nil {
		if !ours && uncertainCreation(err) {
			return provider.GoldenVersion{ID: version, ProviderRef: candidate}, p.refreshError(err)
		}
		return provider.GoldenVersion{}, p.refreshError(err)
	}
	live, err := p.waitInstance(ctx, candidate)
	if err != nil {
		return provider.GoldenVersion{}, err
	}
	live, err = p.rotate(ctx, live)
	if err != nil {
		return provider.GoldenVersion{}, err
	}
	connection, err := p.connString(live)
	if err != nil {
		return provider.GoldenVersion{}, err
	}

	// Before the masking step connects with anything it was handed, every
	// production login the restore carried is closed. The masking step is
	// code the customer wrote, and it should not be running in a database a
	// production credential can also open.
	if err := p.disableInheritedLogins(ctx, connection); err != nil {
		return provider.GoldenVersion{}, err
	}
	if err := spec.Mask(ctx, connection); err != nil {
		return provider.GoldenVersion{}, fmt.Errorf("db.rds: mask the golden candidate: %w", err)
	}
	attestation, err := spec.Verify(ctx, connection)
	if err != nil {
		// Nothing is published. The golden snapshot has not been taken at
		// all, so there is no artifact carrying production data that failed
		// its own scan sitting in the account waiting to be branched.
		return provider.GoldenVersion{}, fmt.Errorf("db.rds: verify the golden candidate: %w", err)
	}
	if attestation == "" {
		return provider.GoldenVersion{}, fmt.Errorf(
			"db.rds: the verification scanner returned no attestation, so nothing records " +
				"what it looked at and the golden is not published")
	}
	// Again, after the customer's code has run. Masking and verification run
	// with the administrator's credential and either could have created a
	// login, and the snapshot taken next is what every branch inherits.
	if err := p.disableInheritedLogins(ctx, connection); err != nil {
		return provider.GoldenVersion{}, err
	}

	tags := withKind(base, kindGolden)
	tags[tagRules] = spec.RulesHash
	tags[tagProvenance] = spec.Provenance
	chunks, err := chunkAttestation(attestation)
	if err != nil {
		return provider.GoldenVersion{}, err
	}
	for k, v := range chunks {
		tags[k] = v
	}
	tags[tagPrepared] = p.receipt(kindGolden, golden, tags)

	// Every connection this provider opened is closed by the time this runs.
	// A snapshot of a live instance is consistent whatever is connected, so
	// that is not why; it is that the candidate is deleted immediately
	// afterwards and an open session would make the delete refuse.
	snap, err := p.api.createSnapshot(ctx, golden, candidate, tags)
	if err != nil {
		return provider.GoldenVersion{}, p.refreshError(err)
	}
	if err := p.waitSnapshot(ctx, golden); err != nil {
		return provider.GoldenVersion{}, err
	}
	// Read back rather than trusted. A control plane that created the snapshot
	// and recorded none of its tags has published nothing any later process
	// can find or branch, and saying so here is the difference between a
	// refresh that fails and one that succeeds into an empty listing.
	actual, found, err := p.api.describeSnapshot(ctx, golden)
	if err != nil {
		return provider.GoldenVersion{}, err
	}
	if !found || !p.preparedSnapshot(actual) || joinAttestation(tagMap(actual.Tags)) != attestation {
		return provider.GoldenVersion{}, fmt.Errorf(
			"db.rds: the golden snapshot %s was created and its publication tags were not "+
				"recorded, so no branch could find it", golden)
	}

	publish = true
	return provider.GoldenVersion{
		ID:          version,
		CreatedAt:   created,
		SizeBytes:   snap.AllocatedStorage * (1 << 30),
		RulesHash:   spec.RulesHash,
		Provenance:  spec.Provenance,
		Verified:    true,
		Attestation: attestation,
		ProviderRef: golden,
	}, nil
}

// missingSteps names which of the two callbacks a refresh was not given.
func missingSteps(spec provider.GoldenSpec) string {
	switch {
	case spec.Mask == nil && spec.Verify == nil:
		return "neither"
	case spec.Mask == nil:
		return "no masking step"
	default:
		return "no verification scanner"
	}
}

// refreshError turns the AWS faults a refresh meets into sentences that name
// the next step.
func (p *Provider) refreshError(err error) error {
	switch {
	case isCode(err, faultSnapshotQuota):
		return fmt.Errorf(
			"this account is at its manual DB snapshot quota, and a golden IS a manual "+
				"snapshot here. Remove goldens you no longer branch from, with af golden "+
				"rm, or raise the quota: %w", err)
	case isCode(err, faultInstanceQuota), isCode(err, faultStorageQuota):
		return fmt.Errorf(
			"this account is at its RDS instance or storage quota, so the candidate "+
				"instance a refresh restores into could not be created: %w", err)
	case isCode(err, faultNoCapacity):
		return fmt.Errorf(
			"the instance class %q has no capacity in an availability zone this subnet "+
				"group covers. Set %s to a class the region has: %w",
			p.instanceClass, InstanceClassVariable, err)
	case isCode(err, faultAccessDenied):
		return fmt.Errorf(
			"the credentials this provider found are not allowed to snapshot and restore "+
				"in this account. It needs rds:CreateDBSnapshot, "+
				"rds:RestoreDBInstanceFromDBSnapshot, rds:ModifyDBInstance, "+
				"rds:AddTagsToResource, rds:DeleteDBInstance, rds:DeleteDBSnapshot and the "+
				"describes: %w", err)
	}
	return err
}

// ListGoldens returns published versions, newest first.
//
// By tag rather than by name. A snapshot that merely looks like ours is not a
// golden, and this listing is what branching selects from.
func (p *Provider) ListGoldens(ctx context.Context) ([]provider.GoldenVersion, error) {
	snapshots, err := p.api.listSnapshots(ctx)
	if err != nil {
		return nil, err
	}
	var out []provider.GoldenVersion
	for _, s := range snapshots {
		tags := tagMap(s.Tags)
		// Prepared, not merely tagged golden. The receipt is what shows this
		// provider published it for this source after verification, and a
		// snapshot anybody else tagged golden is not something to branch.
		if !p.preparedSnapshot(s) {
			continue
		}
		attestation := joinAttestation(tags)
		out = append(out, provider.GoldenVersion{
			ID:         tags[tagVersion],
			CreatedAt:  parseTime(tags[tagCreated]),
			SizeBytes:  s.AllocatedStorage * (1 << 30),
			RulesHash:  tags[tagRules],
			Provenance: tags[tagProvenance],
			// Recomputed from the attestation rather than from a flag, for the
			// reason every provider here records: a refresh with no verifier
			// publishes honestly with Verified false, and a provider that
			// answered "it exists, so it must have passed" would hand back
			// true for a golden nothing ever scanned.
			Verified:    attestation != "",
			Attestation: attestation,
			ProviderRef: s.Identifier,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// DestroyGolden removes a version. Removing one that does not exist succeeds.
//
// It refuses a version a branch came from, because the branch is a restore of
// that snapshot and the engine's own garbage collection asks this question
// before it deletes anything. Removing it would not break the branch, which is
// an independent instance by then, and that is exactly why the refusal has to
// be here rather than left to AWS: nothing would fail, and the version a
// running environment says it came from would stop existing.
func (p *Provider) DestroyGolden(ctx context.Context, version string) error {
	if version == "" {
		return nil
	}
	snapshot, found, err := p.goldenFor(ctx, version)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	instances, err := p.api.listInstances(ctx)
	if err != nil {
		return err
	}
	for _, in := range instances {
		tags := tagMap(in.Tags)
		if p.ownsInstance(in) && tags[tagKind] == kindBranch && tags[tagVersion] == version {
			return coded(codeGoldenReferenced, fmt.Sprintf(
				"the golden %s is still branched by the environment %s. Tear that "+
					"environment down first", version, tags[tagEnv]))
		}
	}
	return p.destroySnapshot(ctx, snapshot.Identifier)
}

// goldenFor finds the golden snapshot carrying a version identifier.
func (p *Provider) goldenFor(ctx context.Context, version string) (dbSnapshot, bool, error) {
	// The identifier is derived rather than searched for, which is one
	// describe instead of a listing of the whole account. The listing is still
	// the fallback, because a golden built by an older version of this
	// provider, or renamed, would be invisible to a derivation and visible to
	// the listing.
	matches := func(s dbSnapshot) bool {
		tags := tagMap(s.Tags)
		return p.ownsSnapshot(s) && tags[tagKind] == kindGolden && tags[tagVersion] == version
	}
	name := goldenPrefix + shortHash(p.scope+"\n"+version)
	snap, found, err := p.api.describeSnapshot(ctx, name)
	if err != nil {
		return dbSnapshot{}, false, err
	}
	if found && matches(snap) {
		return snap, true, nil
	}
	all, err := p.api.listSnapshots(ctx)
	if err != nil {
		return dbSnapshot{}, false, err
	}
	for _, s := range all {
		if matches(s) {
			return s, true, nil
		}
	}
	return dbSnapshot{}, false, nil
}

// sweepCandidates removes candidates and intermediate snapshots old enough
// that they can only be orphans.
//
// Opportunistic and never an error: a refresh must not fail because a cleanup
// could not run. Neither a candidate nor a transit snapshot is ever referenced
// by anything, so removing an old one is unconditionally safe, and without
// this a killed process leaves a full copy of production billing in the
// account for ever.
func (p *Provider) sweepCandidates(ctx context.Context) {
	cutoff := p.now().Add(-candidateAge)
	if instances, err := p.api.listInstances(ctx); err == nil {
		for _, in := range instances {
			tags := tagMap(in.Tags)
			if !p.ownsInstance(in) || tags[tagKind] != kindCandidate {
				continue
			}
			if at := parseTime(tags[tagCreated]); at.IsZero() || at.Before(cutoff) {
				_ = p.destroyInstance(ctx, in.Identifier)
			}
		}
	}
	if snapshots, err := p.api.listSnapshots(ctx); err == nil {
		for _, s := range snapshots {
			tags := tagMap(s.Tags)
			if !p.ownsSnapshot(s) || tags[tagKind] != kindTransit {
				continue
			}
			if at := parseTime(tags[tagCreated]); at.IsZero() || at.Before(cutoff) {
				_ = p.destroySnapshot(ctx, s.Identifier)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Branches
// ---------------------------------------------------------------------------

// Branch creates a database for an environment from a golden version.
func (p *Provider) Branch(ctx context.Context, version, envID string) (provider.Branch, error) {
	if p.closed.Load() {
		return provider.Branch{}, fmt.Errorf("rds: provider is closed")
	}
	name, err := branchName(p.scope, envID)
	if err != nil {
		return provider.Branch{}, err
	}
	// One branch creation per source at a time in this process, so the limit
	// below is counted and used without another caller counting the same
	// headroom in between.
	release, err := p.admit(ctx)
	if err != nil {
		return provider.Branch{}, err
	}
	defer release()

	// Idempotency first, and before the limit. Branching twice for one
	// environment returns the branch that exists, and an environment that
	// already has one must not be refused for being one past a ceiling it is
	// not adding to.
	//
	// Returned only when it is exactly the branch that was asked for and it
	// finished preparing. An instance at this name for another version,
	// another environment, or one interrupted before its inherited logins were
	// closed is refused rather than adopted, with its identifier in the
	// handle so that teardown can still find it.
	handle := provider.Branch{EnvID: envID, From: version, ProviderRef: name}
	if existing, found, err := p.api.describeInstance(ctx, name); err != nil {
		return provider.Branch{}, err
	} else if found {
		tags := tagMap(existing.Tags)
		if !p.ownsInstance(existing) {
			return provider.Branch{}, fmt.Errorf(
				"%w: %s exists in this account and was not created by this provider for "+
					"this source, so this provider will not take it over", ErrNotOurs, name)
		}
		if tags[tagEnv] != envID || tags[tagVersion] != version || !p.preparedInstance(existing) {
			return handle, fmt.Errorf(
				"rds: %s exists and is not a finished branch of %s for this environment, so it "+
					"is not handed out. Tear the environment down and branch again", name, version)
		}
		handle.CreatedAt = parseTime(tags[tagCreated])
		return handle, nil
	}

	snapshot, found, err := p.goldenFor(ctx, version)
	if err != nil {
		return provider.Branch{}, err
	}
	if !found {
		return provider.Branch{}, coded(codeNoSuchGolden, fmt.Sprintf(
			"there is no golden version %s in this account and region. %s",
			version, p.describeGoldens(ctx)))
	}
	if joinAttestation(tagMap(snapshot.Tags)) == "" || !p.preparedSnapshot(snapshot) {
		// The product's central promise, enforced here rather than in a
		// checklist. A golden with no attestation, or with a receipt this key
		// did not write, was never scanned by this provider, and a preview
		// environment restored from one is a copy of production nobody checked.
		return provider.Branch{}, coded(codeUnverifiedGolden, fmt.Sprintf(
			"the golden %s carries no verification attestation this provider published, so "+
				"nothing has scanned it for unmasked data and it cannot be branched", version))
	}

	if p.maxBranches > 0 {
		live, err := p.countBranches(ctx)
		if err != nil {
			return provider.Branch{}, err
		}
		if live >= p.maxBranches {
			// Fast, because hanging is the failure mode that turns a branch
			// cap into a mystery: the user sees an af up that never returns.
			return provider.Branch{}, coded(codeBranchLimit, fmt.Sprintf(
				"this project allows %d concurrent branches and %d exist. Tear one down, "+
					"or raise database.max_branches", p.maxBranches, live))
		}
	}

	created := p.now().UTC()
	handle.CreatedAt = created
	tags := map[string]string{
		tagMarker:  Name,
		tagKind:    kindBranch,
		tagVersion: version,
		tagEnv:     envID,
		tagCreated: created.Format(time.RFC3339Nano),
	}
	if _, err := p.restore(ctx, name, snapshot.Identifier, tags); err != nil {
		if uncertainCreation(err) {
			// Whether AWS created it is unknown, or known and the response was
			// lost. Either way the handle carries this attempt, even when the
			// lookup that would have settled it failed too, because an
			// instance that does exist and has no handle bills until somebody
			// finds it in a console. Destroy checks the attempt before it
			// deletes, so the handle can remove only what this attempt made.
			handle.ProviderRef = name + "#" + tags[tagAttempt]
			return handle, err
		}
		return provider.Branch{}, p.refreshError(err)
	}
	live, err := p.waitInstance(ctx, name)
	if err != nil {
		return handle, err
	}
	live, err = p.rotate(ctx, live)
	if err != nil {
		return handle, err
	}
	connection, err := p.connString(live)
	if err != nil {
		return handle, err
	}
	// The golden was closed before it was published, so this normally finds
	// nothing. It is run anyway because the snapshot is the only thing that
	// was checked, and a login the restore inherited from anywhere else would
	// otherwise reach a preview environment.
	if err := p.disableInheritedLogins(ctx, connection); err != nil {
		return handle, err
	}
	if err := p.markPrepared(ctx, live); err != nil {
		return handle, err
	}
	return handle, nil
}

// countBranches is how many branch instances this provider currently holds.
func (p *Provider) countBranches(ctx context.Context) (int, error) {
	instances, err := p.api.listInstances(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, in := range instances {
		if p.ownsInstance(in) && tagMap(in.Tags)[tagKind] == kindBranch {
			n++
		}
	}
	return n, nil
}

// describeGoldens is the second half of the refusal when a version is missing:
// what this account does hold.
func (p *Provider) describeGoldens(ctx context.Context) string {
	versions, err := p.ListGoldens(ctx)
	if err != nil {
		return "The account could not be listed to say what it does hold: " + err.Error()
	}
	if len(versions) == 0 {
		return "This account holds no goldens at all; af golden refresh builds one."
	}
	names := make([]string, 0, len(versions))
	for _, v := range versions {
		names = append(names, v.ID)
	}
	if len(names) > 5 {
		names = names[:5]
	}
	return "It holds " + strings.Join(names, ", ") + "."
}

// Reset is not supported, and the package comment says why at length.
//
// RDS has no restore in place and no rewind. The only way back to a golden is
// to delete the instance and restore the snapshot again, which is the thing
// the capability is defined against, and it costs a full restore rather than
// the cheap operation a caller reaching for Reset is reaching for.
func (p *Provider) Reset(context.Context, provider.Branch) error {
	return provider.ErrUnsupported
}

// Destroy removes a branch. Removing one that is already gone succeeds,
// because teardown retries.
func (p *Provider) Destroy(ctx context.Context, b provider.Branch) error {
	name := b.ProviderRef
	attempt := ""
	if base, nonce, ok := strings.Cut(name, "#"); ok {
		name, attempt = base, nonce
	}
	if name == "" {
		if b.EnvID == "" {
			return nil
		}
		derived, err := branchName(p.scope, b.EnvID)
		if err != nil {
			return err
		}
		name = derived
	}
	// The branch the engine recorded, and nothing else at that name. An
	// instance whose attempt, environment or version differ from the handle is
	// somebody else's even when it carries our tags, and a teardown is the
	// last place a guess should be allowed to delete a database.
	if attempt != "" || b.EnvID != "" || b.From != "" {
		in, found, err := p.api.describeInstance(ctx, name)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		tags := tagMap(in.Tags)
		if (attempt != "" && tags[tagAttempt] != attempt) ||
			(b.EnvID != "" && tags[tagEnv] != b.EnvID) ||
			(b.From != "" && tags[tagVersion] != b.From) {
			return fmt.Errorf("%w: %s is not the branch this teardown was handed", ErrNotOurs, name)
		}
	}
	return p.destroyInstance(ctx, name)
}

// destroyInstance deletes one instance this provider owns.
//
// It reads the tag before it deletes. A customer whose own instance is called
// af-b-something must not lose it to our teardown, and "it matched our prefix"
// is not ownership.
func (p *Provider) destroyInstance(ctx context.Context, name string) error {
	deadline := p.now().Add(p.readyTimeout)
	for {
		in, found, err := p.api.describeInstance(ctx, name)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		if !p.ownsInstance(in) {
			return fmt.Errorf(
				"%w: %s was not created by this provider for this source, so this provider "+
					"will not delete it", ErrNotOurs, name)
		}
		if in.Status == "deleting" {
			// Already on its way out, which is what a retried teardown finds.
			return nil
		}
		err = p.api.deleteInstance(ctx, name)
		switch {
		case err == nil:
			return nil
		case isCode(err, faultInstanceNotFound):
			return nil
		case isCode(err, faultInvalidInstance):
			// An instance that is still creating, modifying or backing up
			// refuses a delete. Giving up on the first refusal would leave the
			// expensive half of an environment behind, so the retry is the
			// point rather than a nicety.
			if !p.now().Before(deadline) {
				return fmt.Errorf(
					"%s would not delete within %s because RDS kept reporting it in state %q: %w",
					name, p.readyTimeout, in.Status, err)
			}
			if err := p.sleep(ctx); err != nil {
				return err
			}
		default:
			return err
		}
	}
}

// destroySnapshot deletes one snapshot this provider owns.
func (p *Provider) destroySnapshot(ctx context.Context, name string) error {
	snap, found, err := p.api.describeSnapshot(ctx, name)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if !p.ownsSnapshot(snap) {
		return fmt.Errorf(
			"%w: the snapshot %s was not created by this provider for this source, so this "+
				"provider will not delete it", ErrNotOurs, name)
	}
	err = p.api.deleteSnapshot(ctx, name)
	if err != nil && isCode(err, faultSnapshotNotFound) {
		return nil
	}
	return err
}

// ---------------------------------------------------------------------------
// Connections, inventory and health
// ---------------------------------------------------------------------------

// ConnString returns a connection string for a branch.
func (p *Provider) ConnString(ctx context.Context, b provider.Branch, mode provider.ConnMode) (secret.Value, error) {
	if mode == provider.ConnPooled {
		// Declaring a capability this provider does not have would make the
		// conformance suite pass a behaviour it should skip. RDS Proxy is the
		// pooler here, it is a separate resource with its own endpoint, and
		// this provider would be guessing at its address.
		return secret.Value{}, provider.ErrUnsupported
	}
	if p.closed.Load() {
		return secret.Value{}, fmt.Errorf("rds: provider is closed")
	}
	name := b.ProviderRef
	if base, _, ok := strings.Cut(name, "#"); ok {
		name = base
	}
	if name == "" {
		if b.EnvID == "" {
			return secret.Value{}, fmt.Errorf(
				"db.rds: a branch with neither a provider reference nor an environment " +
					"identifier names nothing")
		}
		derived, err := branchName(p.scope, b.EnvID)
		if err != nil {
			return secret.Value{}, err
		}
		name = derived
	}
	in, found, err := p.api.describeInstance(ctx, name)
	if err != nil {
		return secret.Value{}, err
	}
	if !found {
		if b.From == "" {
			return secret.Value{}, fmt.Errorf(
				"db.rds: there is no instance for the environment %s", b.EnvID)
		}
		return secret.Value{}, coded(codeNoSuchGolden, fmt.Sprintf(
			"there is no instance branched from %s for the environment %s", b.From, b.EnvID))
	}
	tags := tagMap(in.Tags)
	if !p.preparedInstance(in) || (b.EnvID != "" && tags[tagEnv] != b.EnvID) ||
		(b.From != "" && tags[tagVersion] != b.From) {
		// No connection string for anything but a finished branch of this
		// source, for this environment. A string is a credential, and handing
		// one out for an instance whose inherited logins were never closed is
		// handing out production's access with a new password on top.
		return secret.Value{}, fmt.Errorf(
			"rds: %s is not a finished branch for this environment, so no connection string "+
				"is handed out for it", name)
	}
	return p.connString(in)
}

// connString builds the URL for one live instance.
//
// The password is DERIVED rather than stored, from the manifest named branch
// key and the instance identifier. Nothing in this product writes a database
// password anywhere, and a later process rebuilds the same string from the
// same two inputs.
func (p *Provider) connString(in dbInstance) (secret.Value, error) {
	if p.closed.Load() {
		return secret.Value{}, fmt.Errorf("rds: provider is closed")
	}
	if in.IAMEnabled {
		return secret.Value{}, fmt.Errorf(
			"rds: %s accepts IAM database authentication, which the restore turned off, so "+
				"something changed it and the derived password is not the only way in",
			in.Identifier)
	}
	if in.Address == "" {
		return secret.Value{}, fmt.Errorf(
			"db.rds: %s reports no endpoint address. RDS gives an instance an address "+
				"once it is available, so this is an instance that is still coming up",
			in.Identifier)
	}
	database := in.DBName
	if database == "" {
		// What RDS calls an instance with no initial database name. A restore
		// carries the source's DBName, so this only happens for an instance
		// created without one, and postgres is the database that always
		// exists.
		database = "postgres"
	}
	user := in.MasterUsername
	if user == "" {
		user = "postgres"
	}
	port := in.Port
	if port == 0 {
		port = 5432
	}
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(user, p.passwordFor(in.Identifier)),
		Host:   net.JoinHostPort(in.Address, strconv.Itoa(port)),
		Path:   "/" + database,
	}
	// Plaintext is for the loopback fixture and nothing else. A copy of
	// production crossing a network unencrypted, or encrypted to a server
	// nobody checked, is the exposure this whole provider exists to avoid.
	if p.tlsMode == "disable" && !loopbackHost(in.Address) {
		return secret.Value{}, fmt.Errorf(
			"rds: sslmode disable is limited to a loopback endpoint, and %s is not one; use "+
				"verify-full", in.Address)
	}
	q := u.Query()
	q.Set("sslmode", p.tlsMode)
	if p.tlsMode != "disable" {
		path, err := p.trustFile()
		if err != nil {
			return secret.Value{}, err
		}
		q.Set("sslrootcert", path)
	}
	u.RawQuery = q.Encode()
	return secret.NewFrom(u.String(), "db.rds:"+in.Identifier), nil
}

// loopbackHost reports whether an address never leaves the machine.
func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// passwordFor is the master password of one instance.
//
// An HMAC of the manifest named branch key and the instance identifier, so it
// is deterministic, per-instance, and never written down. Base64 in the URL
// safe alphabet because RDS refuses a master password containing a slash, a
// double quote, an at sign or a space, and that alphabet contains none of them.
func (p *Provider) passwordFor(instance string) string {
	mac := hmac.New(sha256.New, []byte(p.branchKey.Reveal()))
	mac.Write([]byte(instance))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Inventory lists everything this provider currently holds.
//
// By tag rather than by name, because the leak detector's output is a list of
// things somebody is about to delete. Snapshots are in it as well as
// instances: a golden here IS a snapshot, and an inventory that reported only
// instances would tell the detector that every golden had already been
// removed.
//
// ONE THING THE SHARED LEAK DETECTOR CANNOT SEE HERE, stated rather than left
// to be discovered. It records the golden VERSION identifier,
// gv_<stamp>_<hash>, and looks for it as a substring of the identifiers
// reported below. A Postgres database can be named after that string and
// internal/db/pgurl's goldens are, so its goldens are covered. An RDS snapshot
// identifier may not contain an underscore, so this provider's goldens are
// named from a digest and no substring match can find them: a DestroyGolden
// that quietly did nothing would leave that detector green. The version is in
// the labels, and rds_test.go asks the same question locally, against this
// inventory, where it can be answered.
func (p *Provider) Inventory(ctx context.Context) ([]provider.Resource, error) {
	instances, err := p.api.listInstances(ctx)
	if err != nil {
		return nil, err
	}
	snapshots, err := p.api.listSnapshots(ctx)
	if err != nil {
		return nil, err
	}
	var out []provider.Resource
	for _, in := range instances {
		tags := tagMap(in.Tags)
		if !p.ownsInstance(in) {
			continue
		}
		out = append(out, provider.Resource{
			Kind:      "instance/" + tags[tagKind],
			ID:        in.Identifier,
			EnvID:     tags[tagEnv],
			CreatedAt: parseTime(tags[tagCreated]),
			Labels: map[string]string{
				"version": tags[tagVersion],
				"status":  in.Status,
				"storage": strconv.FormatInt(in.AllocatedStorage, 10),
			},
		})
	}
	for _, s := range snapshots {
		tags := tagMap(s.Tags)
		if !p.ownsSnapshot(s) {
			continue
		}
		out = append(out, provider.Resource{
			Kind:      "snapshot/" + tags[tagKind],
			ID:        s.Identifier,
			EnvID:     tags[tagEnv],
			CreatedAt: parseTime(tags[tagCreated]),
			Labels: map[string]string{
				"version": tags[tagVersion],
				"status":  s.Status,
				"storage": strconv.FormatInt(s.AllocatedStorage, 10),
			},
		})
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
	start := p.now()
	conn, err := p.ConnString(ctx, b, provider.ConnDirect)
	if err != nil {
		return provider.Health{Reachable: false, Detail: shortError(err)}, nil
	}
	db, err := sql.Open("pgx", conn.Reveal())
	if err != nil {
		return provider.Health{Reachable: false, Detail: shortError(err)}, nil
	}
	defer func() { _ = db.Close() }()
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return provider.Health{Reachable: false, Detail: shortError(err)}, nil
	}
	return provider.Health{
		Reachable: true, Detail: "accepting connections", Latency: p.now().Sub(start),
	}, nil
}

// ---------------------------------------------------------------------------
// Waiting
// ---------------------------------------------------------------------------

// waitInstance polls until an instance is available.
func (p *Provider) waitInstance(ctx context.Context, name string) (dbInstance, error) {
	deadline := p.now().Add(p.readyTimeout)
	last := ""
	for {
		in, found, err := p.api.describeInstance(ctx, name)
		if err != nil {
			return dbInstance{}, err
		}
		if found {
			last = in.Status
			switch in.Status {
			case "available":
				return in, nil
			case "failed", "incompatible-restore", "incompatible-parameters", "inaccessible-encryption-credentials":
				// Terminal. Waiting for the timeout would turn a refusal that
				// RDS made in seconds into three quarters of an hour of
				// silence.
				return dbInstance{}, fmt.Errorf(
					"the instance %s reached the terminal state %q, so it will not become "+
						"available and this provider is not going to wait for the timeout",
					name, in.Status)
			}
		}
		if !p.now().Before(deadline) {
			return dbInstance{}, fmt.Errorf(
				"the instance %s was still %q after %s. A restore provisions an instance "+
					"and hydrates its volume from the snapshot, and both are slower for a "+
					"larger database; set a longer timeout if this account restores "+
					"something large", name, or(last, "absent"), p.readyTimeout)
		}
		if err := p.sleep(ctx); err != nil {
			return dbInstance{}, err
		}
	}
}

// waitSnapshot polls until a snapshot is available.
func (p *Provider) waitSnapshot(ctx context.Context, name string) error {
	deadline := p.now().Add(p.readyTimeout)
	last := ""
	for {
		s, found, err := p.api.describeSnapshot(ctx, name)
		if err != nil {
			return err
		}
		if found {
			last = s.Status
			switch s.Status {
			case "available":
				return nil
			case "failed":
				return fmt.Errorf("the snapshot %s failed, so nothing can be restored from it", name)
			}
		}
		if !p.now().Before(deadline) {
			return fmt.Errorf(
				"the snapshot %s was still %q after %s", name, or(last, "absent"), p.readyTimeout)
		}
		if err := p.sleep(ctx); err != nil {
			return err
		}
	}
}

// rotate sets an instance's master password to the derived one and waits for
// the modification to land.
//
// It happens before anything connects, and that ordering is the point rather
// than a detail: a restored instance carries the SOURCE's master credential
// until this call, and an environment handed the connection string before it
// would be holding production's database password.
func (p *Provider) rotate(ctx context.Context, in dbInstance) (dbInstance, error) {
	if err := p.api.rotatePassword(ctx, in.Identifier, p.passwordFor(in.Identifier)); err != nil {
		return dbInstance{}, err
	}
	// Waited for rather than assumed. ModifyDBInstance with ApplyImmediately
	// puts the instance in "modifying" and the new password is not in force
	// until it is available again, so a provider that connected straight after
	// this call would authenticate with a credential the instance does not yet
	// have.
	return p.waitInstance(ctx, in.Identifier)
}

// sleep waits one poll interval or reports the context's cancellation.
func (p *Provider) sleep(ctx context.Context) error {
	timer := time.NewTimer(p.poll)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ---------------------------------------------------------------------------
// Names, tags and small helpers
// ---------------------------------------------------------------------------

// withKind copies the base tags and sets the kind, so that three calls cannot
// share one map and overwrite each other's kind.
func withKind(base map[string]string, kind string) map[string]string {
	out := make(map[string]string, len(base)+1)
	for k, v := range base {
		out[k] = v
	}
	out[tagKind] = kind
	return out
}

// chunkAttestation splits an attestation across numbered tags.
//
// It REFUSES rather than truncating. A golden whose attestation was silently
// cut in half would still read as verified, and the attestation is the record
// of what was scanned; half of one is not a smaller record, it is a document
// that no longer parses.
func chunkAttestation(attestation string) (map[string]string, error) {
	out := map[string]string{}
	if attestation == "" {
		return out, nil
	}
	rest := attestation
	for i := 1; rest != ""; i++ {
		if i > attestationChunks {
			return nil, fmt.Errorf(
				"the verification attestation is %d characters and does not fit in the %d "+
					"tags of %d characters this provider reserves for it. It is refused "+
					"rather than truncated: half an attestation still reads as verified",
				len(attestation), attestationChunks, tagValueLimit)
		}
		n := tagValueLimit
		if len(rest) < n {
			n = len(rest)
		}
		out[tagAttestation+"."+strconv.Itoa(i)] = rest[:n]
		rest = rest[n:]
	}
	return out, nil
}

// joinAttestation puts a chunked attestation back together.
//
// In numbered order, and it stops at the first missing chunk rather than
// skipping it. A gap means the tags were not all written, and concatenating
// across one would produce a document that looks whole and is not.
func joinAttestation(tags map[string]string) string {
	var b strings.Builder
	for i := 1; i <= attestationChunks; i++ {
		chunk, ok := tags[tagAttestation+"."+strconv.Itoa(i)]
		if !ok {
			break
		}
		b.WriteString(chunk)
	}
	return b.String()
}

// branchName derives an RDS instance identifier from an environment
// identifier.
//
// RDS is stricter than Postgres about names: letters, digits and hyphens only,
// it must begin with a letter, it may not end with a hyphen and it may not
// contain two consecutive ones. So the environment identifier is sanitised,
// runs of anything else collapse to one hyphen, and EIGHT HEX CHARACTERS OF A
// DIGEST ARE ALWAYS APPENDED. Always, rather than only when the name is too
// long, because the collapse is lossy: env_a-1 and env_a__1 sanitise to the
// same string, and two environments sharing one database is the worst failure
// this file could have.
func branchName(scope, envID string) (string, error) {
	if strings.TrimSpace(envID) == "" {
		return "", fmt.Errorf("db.rds: an environment identifier is required to name a branch")
	}
	// The source's scope is in the digest, so one environment identifier used
	// against two sources in one account names two instances rather than one
	// that each would try to adopt.
	digest := shortHash(scope + "\n" + envID)
	const room = identifierLimit - len(branchPrefix) - 9
	safe := sanitize(envID, room)
	if safe == "" {
		return branchPrefix + digest, nil
	}
	return branchPrefix + safe + "-" + digest, nil
}

// sanitize lowercases, keeps letters and digits, collapses everything else to
// one hyphen, and never exceeds a length.
//
// The length is checked BEFORE the hyphen and the character are written rather
// than at the top of the loop, and the difference is one byte of overrun that
// an identifier at the limit cannot afford: a separator written at limit minus
// one and a character after it puts the result one over, which RDS refuses for
// a name that every shorter environment identifier produced correctly.
func sanitize(s string, limit int) string {
	var b strings.Builder
	pendingHyphen := false
	for _, r := range strings.ToLower(s) {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') {
			pendingHyphen = true
			continue
		}
		need := 1
		if pendingHyphen && b.Len() > 0 {
			need = 2
		}
		if b.Len()+need > limit {
			break
		}
		if need == 2 {
			b.WriteByte('-')
		}
		pendingHyphen = false
		b.WriteRune(r)
	}
	return strings.Trim(b.String(), "-")
}

// shortHash is eight hex characters of a SHA-256, used to keep a derived
// identifier distinct and inside the length RDS allows.
func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:4])
}

// parseTime reads a tag value this provider wrote, and answers the zero time
// for anything it did not.
func parseTime(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	at, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return at.UTC()
}

// shortError is one line of a driver error, for a Health detail that has to fit
// on a terminal.
func shortError(err error) string {
	text := strings.TrimSpace(err.Error())
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	if len(text) > 200 {
		text = text[:200]
	}
	return text
}

var _ provider.Database = (*Provider)(nil)
