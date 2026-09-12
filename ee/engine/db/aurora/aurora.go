// Package aurora is the database provider for Amazon Aurora PostgreSQL.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
// It is enterprise by the editions rule in the plan: reaching a production
// Aurora cluster needs an IAM role somebody with an organization grants, which
// is not one developer with their own card.
//
// The claim this provider exists to make, in one sentence: a branch is an
// Aurora CLONE, the clone shares the source's storage volume and diverges a
// page at a time, so the work of branching does not depend on how large the
// database is. A hundred rows and a terabyte cost the same clone.
//
// The sentence that has to travel with it, because it is the part a
// competitor's slide leaves out: the STORAGE is there in seconds and nobody
// can connect to storage. A clone has no instances, and a preview environment
// needs one, and provisioning it takes minutes. The flat part is real and it
// is the storage; the wall clock to an open connection is dominated by an
// instance coming up, which is also flat in the database's size and is minutes
// rather than seconds. This provider declares an ExpectedBranchLatency in
// minutes for exactly that reason, and benchmark_test.go publishes the halves
// separately rather than one number that would flatter the fast half.
//
// The model:
//
//   - The SOURCE is an Aurora PostgreSQL cluster, named by database.project.
//     It is never written to and never read over a connection; it is cloned.
//   - A GOLDEN is a clone of the source, with the masking rules applied and
//     the verification scanner run against it. The writer remains attached
//     until live AWS evidence establishes that removing it preserves cloning.
//   - A BRANCH is a clone of a golden, plus one writer instance.
//
// Nothing here is renamed or deleted on the strength of its name. Every
// cluster this provider creates carries an antifailure tag, and every
// destructive path reads that tag first, for the reason pgurl reads a comment:
// a customer whose own cluster is called af-b-something must not lose it to
// our garbage collection.
//
// Two things this provider deliberately does NOT do, named because a provider
// that is named and not built is worse than one that is absent:
//
//   - It does not implement IAM database authentication. Aurora supports it,
//     it would be the better credential, and it is not here.
//   - It does not implement Reset. Aurora has no rewind that meets the
//     capability's definition, which is returning a branch to its golden state
//     WITHOUT destroying and recreating it. Backtrack is MySQL only. So Reset
//     returns provider.ErrUnsupported and the conformance suite skips that
//     behaviour by name rather than passing it silently.
package aurora

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
const Name = "aurora"

// engineAuroraPostgres is the only engine this provider will touch.
//
// Checked rather than assumed. An Aurora MySQL cluster clones exactly as
// happily and produces a database no Postgres tool can read, and RDS for
// PostgreSQL is not Aurora at all and cannot be cloned; that one is L2.3's
// provider and this one names it rather than quietly doing something slower.
const engineAuroraPostgres = "aurora-postgresql"

// The tag keys this provider owns. A cluster without the first one is somebody
// else's, whatever it is called.
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
	kindGolden    = "golden"
	kindBranch    = "branch"
)

// The cluster identifier prefixes, one per kind. Short, because an RDS cluster
// identifier is 63 characters and the digest that follows wants the room.
const (
	goldenPrefix = "af-g-"
	branchPrefix = "af-b-"
)

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
// seven of them.
const attestationChunks = 32

// DefaultVariable is the manifest named variable this provider derives every
// cluster's master password from.
//
// It is NOT the source cluster's password, and that is the point. A clone
// inherits the source's master credential, so a provider that did nothing
// would hand production's database password to every preview environment. This
// one rotates each clone's master password to an HMAC of this key and the
// cluster's identifier, immediately after the clone and before anything
// connects. The value is deterministic, so a later process can rebuild a
// connection string without anything having stored a password; it is
// per-cluster, so a preview's credential opens the preview and nothing else;
// and the source's own password is never read, never held, and never needed.
const DefaultVariable = "AF_AURORA_BRANCH_KEY"

// The other variables this provider reads, all through the engine's chain and
// never from the process environment. Only the first is required.
const (
	// RegionVariable names the region the cluster is in. A cluster in eu-west-1
	// does not exist in us-east-1 and the API call would answer that it does
	// not exist, which is a confusing way to learn about a typo.
	RegionVariable = "AWS_REGION"
	// InstanceClassVariable is the writer instance class a branch gets.
	InstanceClassVariable = "AF_AURORA_INSTANCE_CLASS"
	// EndpointVariable overrides the regional RDS endpoint. A customer inside
	// a VPC reaches RDS through an interface endpoint with its own hostname.
	// The request is still signed for the configured region, so an override
	// cannot silently move which region it is valid in.
	EndpointVariable = "AF_AURORA_RDS_ENDPOINT"
	// TLSModeVariable is the sslmode of the connection strings this provider
	// hands out. It defaults to verify-full and there is no path that sets it to
	// disable on its own.
	TLSModeVariable = "AF_AURORA_SSLMODE"
)

// defaultInstanceClass is a small Graviton writer, which is the cheapest class
// Aurora PostgreSQL offers outside Serverless v2.
const defaultInstanceClass = "db.t4g.medium"

// defaultBranchLatency is what this provider expects a branch to take.
//
// Minutes, and the comment at the top of this file says why: the clone is
// seconds and the writer instance is not. It is declared rather than
// discovered so that a provider getting slower fails a conformance behaviour
// instead of degrading quietly, and it is overridable because an account with
// a warm instance class in a busy region is not the same as an empty one.
const defaultBranchLatency = 10 * time.Minute

// Options configure the provider.
type Options struct {
	// SourceCluster is the Aurora PostgreSQL cluster identifier goldens are
	// cloned from. Required; it is database.project in a manifest.
	SourceCluster string
	// Region is the AWS region the cluster lives in. Required.
	Region string
	// BranchKey is the secret every cluster's master password is derived from.
	// Required.
	BranchKey secret.Value
	// Variable names the manifest variable BranchKey came from, so a refusal
	// can say which one to fix.
	Variable string
	// Endpoint overrides the regional RDS endpoint.
	Endpoint string
	// InstanceClass is the writer instance class.
	InstanceClass string
	// TLSMode is the sslmode of the connection strings handed out.
	TLSMode string
	// MaxBranches is the concurrent branch ceiling. Zero uses Aurora's own
	// clone limit.
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
	// PollInterval is how often a cluster or instance is asked whether it is
	// ready yet.
	PollInterval time.Duration
	// ReadyTimeout bounds waiting for a cluster or an instance to come up.
	ReadyTimeout time.Duration
	// BranchLatency overrides the declared ExpectedBranchLatency.
	BranchLatency time.Duration
	// SkipEngineCheck skips only engine validation. Source account and region
	// identity are always validated. It is not reachable from a manifest: the engine
	// check is what stops this provider being pointed at RDS for PostgreSQL,
	// where a clone is impossible and the honest answer is a different
	// provider.
	SkipEngineCheck bool
}

// Provider is the Aurora PostgreSQL database provider.
type Provider struct {
	scope, arnPrefix, partition string
	loginCatalog                func(context.Context, *sql.DB) ([]string, error)
	closed                      atomic.Bool
	trustMu                     sync.Mutex
	trustDir, trustPath         string
	trustOverride               string
	api                         *client
	source                      string
	branchKey                   secret.Value
	variable                    string
	instanceClass               string
	tlsMode                     string
	maxBranches                 int
	branchLatency               time.Duration
	now                         func() time.Time
	poll                        time.Duration
	readyTimeout                time.Duration
	// major is the source cluster's Postgres major version, read from the
	// cluster rather than taken from the manifest. A clone is the same
	// Postgres the source is, so the manifest cannot choose.
	major int
}

// New returns a provider for the configured source cluster.
//
// It describes the source before it returns, which decides three things in one
// round trip that would otherwise each be discovered at the worst moment: that
// the credentials work, that the cluster exists in that region, and that it is
// Aurora PostgreSQL rather than Aurora MySQL or plain RDS. Learning the third
// one after a refresh has already cloned something is learning it after the
// expensive part.
func New(ctx context.Context, opts Options) (*Provider, error) {
	if opts.SourceCluster == "" {
		return nil, fmt.Errorf(
			"the aurora provider needs database.project, which is the Aurora PostgreSQL " +
				"cluster identifier goldens are cloned from")
	}
	if opts.Region == "" {
		return nil, fmt.Errorf(
			"the aurora provider needs a region (%s); a cluster in eu-west-1 does not "+
				"exist in us-east-1, and asking the wrong region answers that it is not there",
			RegionVariable)
	}
	variable := opts.Variable
	if variable == "" {
		variable = DefaultVariable
	}
	if opts.BranchKey.IsZero() {
		return nil, fmt.Errorf(
			"the aurora provider needs %s, which is the secret every clone's master "+
				"password is derived from. It is not the source cluster's password: this "+
				"provider rotates each clone away from the credential it inherited, so that "+
				"a preview environment never holds production's database password", variable)
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
		source:        opts.SourceCluster,
		branchKey:     opts.BranchKey,
		variable:      variable,
		instanceClass: or(opts.InstanceClass, defaultInstanceClass),
		tlsMode:       or(opts.TLSMode, "verify-full"),
		maxBranches:   opts.MaxBranches,
		branchLatency: opts.BranchLatency,
		now:           opts.Now,
		poll:          opts.PollInterval,
		readyTimeout:  opts.ReadyTimeout,
		major:         0,
		loginCatalog:  customerLogins,
	}
	if p.now == nil {
		p.now = time.Now
	}
	if p.poll <= 0 {
		p.poll = 10 * time.Second
	}
	if p.readyTimeout <= 0 {
		p.readyTimeout = 30 * time.Minute
	}
	if p.branchLatency <= 0 {
		p.branchLatency = defaultBranchLatency
	}
	if p.maxBranches < 0 {
		return nil, fmt.Errorf("a negative branch limit is not meaningful; use zero for unlimited")
	}

	source, found, err := p.api.describeCluster(ctx, opts.SourceCluster)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf(
			"no cluster named %q exists in %s. database.project is an Aurora DB CLUSTER "+
				"identifier, not an instance identifier and not an endpoint hostname",
			opts.SourceCluster, opts.Region)
	}
	if !opts.SkipEngineCheck && source.Engine != engineAuroraPostgres {
		// Refused rather than substituted, which is the rule this provider is
		// most likely to be asked to break. A snapshot restore would work here
		// and would be a byte for byte copy: flat cost quietly becoming linear
		// cost is worse than a refusal, because nobody measures a thing that
		// still appears to work.
		return nil, fmt.Errorf(
			"the cluster %q runs %s, and this provider clones %s. An Aurora clone is "+
				"copy on write and there is no clone for that engine; a snapshot restore "+
				"copies every byte and is a different provider with a different number, "+
				"so this one refuses rather than becoming that one silently",
			opts.SourceCluster, source.Engine, engineAuroraPostgres)
	}
	if err := p.bindSource(source); err != nil {
		return nil, err
	}
	p.major = majorOf(source.EngineVersion)
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

// majorOf reads the major from an Aurora PostgreSQL engine version such as
// "16.4" or "15.6".
func majorOf(version string) int {
	head, _, _ := strings.Cut(version, ".")
	n, err := strconv.Atoi(strings.TrimSpace(head))
	if err != nil || n < 12 || n > 30 {
		// Zero would make Capabilities.Supports answer false for everything,
		// which is a worse failure than assuming the current default: the
		// engine version string is informational here and the clone is
		// whatever the source is regardless of what this parsed.
		return 0
	}
	return n
}

func (p *Provider) Name() string { return Name }

// Capabilities describes what this provider can do.
func (p *Provider) Capabilities() provider.Caps {
	versions := []int{13, 14, 15, 16, 17}
	if p.major != 0 && !containsInt(versions, p.major) {
		// The source's own major, added rather than replacing the list, so a
		// cluster on a version this file has not heard of is still usable. A
		// clone is the same Postgres the source is, so refusing it here would
		// refuse a cluster that demonstrably works.
		versions = append(versions, p.major)
		sort.Ints(versions)
	}
	return provider.Caps{
		Branching: true,
		// Aurora has no rewind that returns a branch to its golden state
		// without destroying it. Backtrack is Aurora MySQL only.
		Reset: false,
		// The distinguishing claim. It is true because every branch is
		// RestoreType=copy-on-write and this provider sends no other value.
		CopyOnWrite: true,
		// The masking is the engine's, run over a connection to the clone.
		// Aurora has nothing that would apply it.
		ProviderMasking: false,
		// RDS Proxy is the pooled endpoint and it is a separate resource with
		// its own IAM and its own subnet group. It is not created here, so the
		// capability is false and the conformance behaviour skips by name
		// rather than passing against a second copy of the direct string.
		PooledEndpoints: false,
		// A clone has the whole database the moment it exists; there is no
		// empty candidate to load a slice into. That is the distinction
		// Caps.Subsetting documents, and Aurora is the side of it that cannot.
		Subsetting:            false,
		MaxConcurrentBranches: p.branchLimit(),
		ExpectedBranchLatency: p.branchLatency,
		SupportedVersions:     versions,
	}
}

// auroraCloneLimit is how many clones AWS allows from one source volume.
//
// Fifteen, and it is a real ceiling rather than a soft one: the sixteenth call
// is refused. Declaring it means the conformance suite exercises the refusal
// and the engine can say "the limit is reached" instead of passing an AWS
// fault code to somebody.
const auroraCloneLimit = 15

func (p *Provider) branchLimit() int {
	if p.maxBranches > 0 {
		return p.maxBranches
	}
	return auroraCloneLimit
}

func containsInt(xs []int, n int) bool {
	for _, x := range xs {
		if x == n {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Goldens
// ---------------------------------------------------------------------------

// RefreshGolden clones the source, masks it, verifies it, and publishes.
//
// spec.SourceURL is not read, and that is worth saying out loud because every
// other provider in this repository reads it. There is no connection to
// production here at all: the copy is made by the storage layer from the
// cluster named in the manifest, so the data never crosses a network this
// process is on and no credential for the source database is ever held. The
// masking and the verification connect to the CLONE.
//
// spec.Load is likewise never called. Caps.Subsetting is false, so the engine
// does not set it, and there is nothing it could do: the clone is complete
// before this provider can run a line of SQL.
func (p *Provider) RefreshGolden(ctx context.Context, spec provider.GoldenSpec) (result provider.GoldenVersion, retErr error) {
	if spec.Mask == nil || spec.Verify == nil {
		return result, fmt.Errorf("aurora: masking and verification callbacks are required")
	}
	if p.closed.Load() {
		return provider.GoldenVersion{}, fmt.Errorf("aurora: provider is closed")
	}
	// What a killed refresh left behind, before this one adds to it. A
	// candidate is a full clone of production with an instance attached, so
	// one abandoned by a process that died between the clone and the publish
	// goes on billing until somebody notices it in a console. It is swept on
	// the way in rather than by a timer, because a refresh is the only moment
	// this provider is certainly running.
	p.sweepCandidates(ctx)

	created := p.now().UTC()
	version := provider.NewGoldenVersionID(created, spec.RulesHash)
	cluster := goldenPrefix + shortHash(p.scope+"\n"+version)

	tags := map[string]string{
		tagMarker:     Name,
		tagKind:       kindCandidate,
		tagVersion:    version,
		tagRules:      spec.RulesHash,
		tagProvenance: spec.Provenance,
		tagCreated:    created.Format(time.RFC3339Nano),
	}

	accepted, cloneErr := p.clone(ctx, p.source, cluster, tags)
	if cloneErr != nil {
		if accepted {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
			defer cancel()
			if err := p.destroyCluster(cleanup, cluster); err == nil {
				return provider.GoldenVersion{}, p.refreshError(cloneErr)
			}
		}
		if !uncertainCreation(cloneErr) {
			return provider.GoldenVersion{}, p.refreshError(cloneErr)
		}
		return provider.GoldenVersion{ID: version, ProviderRef: cluster}, p.refreshError(cloneErr)
	}

	// Everything from here can fail, and a candidate left behind is a cluster
	// somebody pays for. It is removed on every failing path, and it is tagged
	// candidate rather than golden throughout, so a process killed between two
	// of these lines leaves something the inventory reports and ListGoldens
	// does not publish.
	publish := false
	defer func() {
		if publish {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
		defer cancel()
		if err := p.destroyCluster(cleanup, cluster); err != nil {
			result = provider.GoldenVersion{ID: version, ProviderRef: cluster}
			retErr = fmt.Errorf("%w; candidate cleanup failed: %v", retErr, err)
		}
	}()

	live, err := p.provision(ctx, cluster)
	if err != nil {
		return provider.GoldenVersion{}, err
	}

	connection, err := p.connString(live)
	if err != nil {
		return provider.GoldenVersion{}, err
	}

	if spec.Mask != nil {
		if err := spec.Mask(ctx, connection); err != nil {
			return provider.GoldenVersion{}, err
		}
	}
	attestation := ""
	if spec.Verify != nil {
		attestation, err = spec.Verify(ctx, connection)
		if err != nil {
			// Nothing is published. The deferred cleanup removes the clone, so
			// there is no cluster carrying production data that failed its own
			// scan sitting in the account waiting to be branched.
			return provider.GoldenVersion{}, err
		}
	}

	if attestation == "" {
		return provider.GoldenVersion{}, fmt.Errorf("aurora: verification returned no attestation")
	}
	if err := p.disableInheritedLogins(ctx, connection); err != nil {
		return provider.GoldenVersion{}, err
	}
	published := map[string]string{tagKind: kindGolden, tagPrepared: p.receipt(live)}
	chunks, err := chunkAttestation(attestation)
	if err != nil {
		return provider.GoldenVersion{}, err
	}
	for k, v := range chunks {
		published[k] = v
	}
	if err := p.api.setTags(ctx, live.ARN, published); err != nil {
		return provider.GoldenVersion{}, err
	}
	actual, found, err := p.api.describeCluster(ctx, cluster)
	if err != nil {
		return provider.GoldenVersion{}, err
	}
	if !found || !p.prepared(actual) || actual.tags()[tagKind] != kindGolden || joinAttestation(actual.tags()) != attestation {
		return provider.GoldenVersion{}, fmt.Errorf("aurora: golden publication receipt was not recorded")
	}

	// THE GOLDEN KEEPS ITS WRITER INSTANCE, and that is a decision rather than
	// an omission.
	//
	// Deleting it would be the obvious saving. A cluster's storage survives
	// without compute, cloning is a cluster level operation, and a published
	// golden that costs storage and no compute is what makes keeping several
	// of them affordable. It ought to work.
	//
	// NOBODY HERE HAS AN AURORA ACCOUNT. The only instrument that could say
	// whether cloning a cluster with no instances attached actually works is
	// the fake in ee/engine/db/aurora/fakerds, which this repository wrote,
	// and a fake agreeing with the assumption that produced it is not
	// evidence. Shipping an untested cost saving that silently breaks
	// branching is worse than the standing cost, so the instance stays until
	// somebody with an account has run it.
	publish = true
	return provider.GoldenVersion{
		ID:          version,
		CreatedAt:   created,
		SizeBytes:   live.AllocatedStorage * (1 << 30),
		RulesHash:   spec.RulesHash,
		Provenance:  spec.Provenance,
		Verified:    attestation != "",
		Attestation: attestation,
		ProviderRef: cluster,
	}, nil
}

// refreshError turns the AWS faults a refresh meets into sentences that name
// the next step.
func (p *Provider) refreshError(err error) error {
	switch {
	case isCode(err, faultQuotaExceeded), isCode(err, faultStorageQuota), isCode(err, faultSnapshotQuota):
		return fmt.Errorf("this account is at its Aurora quota, so the clone was refused: %w", err)
	case isCode(err, faultAccessDenied):
		return fmt.Errorf(
			"the AWS credentials this provider found may not clone %s. It needs "+
				"rds:RestoreDBClusterToPointInTime on the source cluster and "+
				"rds:CreateDBInstance, rds:AddTagsToResource, rds:ModifyDBCluster and the "+
				"matching Describe and Delete actions on the clones: %w", p.source, err)
	case isCode(err, faultInvalidRestoreTime):
		return fmt.Errorf(
			"the source cluster %s has no restorable time yet, which is what a cluster "+
				"created in the last few minutes looks like: %w", p.source, err)
	default:
		return err
	}
}

// provision brings a freshly cloned cluster up: a writer instance, a rotated
// master password, and a cluster that answers.
func (p *Provider) provision(ctx context.Context, cluster string) (dbCluster, error) {
	if err := p.api.createInstance(ctx, cluster, writerOf(cluster), p.instanceClass, map[string]string{
		tagMarker: Name,
	}); err != nil {
		return dbCluster{}, err
	}
	if err := p.waitInstance(ctx, writerOf(cluster)); err != nil {
		return dbCluster{}, err
	}
	if _, err := p.waitCluster(ctx, cluster); err != nil {
		return dbCluster{}, err
	}
	// Rotated before anything connects and before anything is published. A
	// clone inherits the source's master password, so the window in which this
	// cluster is openable with production's credential is the window between
	// the clone and this line. The writer can have an endpoint during this
	// interval; preparation revokes inherited logins and sessions before return.
	if err := p.api.setMasterPassword(ctx, cluster, p.passwordFor(cluster)); err != nil {
		return dbCluster{}, err
	}
	live, err := p.waitCluster(ctx, cluster)
	if err != nil {
		return dbCluster{}, err
	}
	connection, err := p.connString(live)
	if err != nil {
		return dbCluster{}, err
	}
	if err := p.disableInheritedLogins(ctx, connection); err != nil {
		return dbCluster{}, err
	}
	return live, nil
}

// ListGoldens returns published versions, newest first.
//
// From tags alone, in one API call, which is what deleting the golden's writer
// instance costs and buys. There is nothing to connect to, so everything a
// version reports has to be recorded on the cluster: the identifier, the rules
// digest, the provenance, and the attestation split across numbered tags.
func (p *Provider) ListGoldens(ctx context.Context) ([]provider.GoldenVersion, error) {
	clusters, err := p.api.describeClusters(ctx, "")
	if err != nil {
		return nil, err
	}
	var out []provider.GoldenVersion
	for _, c := range clusters {
		tags := c.tags()
		if !p.owned(c) || tags[tagKind] != kindGolden {
			continue
		}
		if !p.prepared(c) {
			continue
		}
		attestation := joinAttestation(tags)
		created, _ := time.Parse(time.RFC3339Nano, tags[tagCreated])
		out = append(out, provider.GoldenVersion{
			ID:        tags[tagVersion],
			CreatedAt: created,
			SizeBytes: c.AllocatedStorage * (1 << 30),
			RulesHash: tags[tagRules],
			// Recorded rather than recomputed. A provider that answered
			// "it exists, so it must have passed" would report true for a
			// golden nothing ever scanned, which is the one lie this product
			// cannot afford.
			Provenance:  tags[tagProvenance],
			Verified:    attestation != "",
			Attestation: attestation,
			ProviderRef: c.Identifier,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// DestroyGolden removes a version, refusing one a branch came from.
func (p *Provider) DestroyGolden(ctx context.Context, version string) error {
	clusters, err := p.api.describeClusters(ctx, "")
	if err != nil {
		return err
	}
	var target string
	referencing := 0
	for _, c := range clusters {
		tags := c.tags()
		if !p.owned(c) || tags[tagVersion] != version {
			continue
		}
		switch tags[tagKind] {
		case kindGolden, kindCandidate:
			target = c.Identifier
		case kindBranch:
			referencing++
		}
	}
	if referencing > 0 {
		// The branch's data would go with it. An Aurora clone shares the
		// source volume, so removing the golden underneath a live branch is
		// not a bookkeeping problem, it is the environment's database.
		return coded(codeGoldenReferenced, fmt.Sprintf(
			"the golden %s is still referenced by %d environments and cannot be collected",
			version, referencing))
	}
	if target == "" {
		// Removing something already gone succeeds, because the collector
		// retries and the engine calls this during teardown.
		return nil
	}
	return p.destroyCluster(ctx, target)
}

// ---------------------------------------------------------------------------
// Branches
// ---------------------------------------------------------------------------

// Branch clones a golden for one environment.
//
// The clone is the flat part. Everything else in this function is one API call
// or a wait for an instance. Security SQL reads role and session metadata,
// never customer tables, so its work is independent of customer data size. benchmark_test.go measures exactly that and says what it could
// not measure.
func (p *Provider) Branch(ctx context.Context, version string, envID string) (provider.Branch, error) {
	if p.closed.Load() {
		return provider.Branch{}, fmt.Errorf("aurora: provider is closed")
	}
	release, err := p.admit(ctx)
	if err != nil {
		return provider.Branch{}, err
	}
	defer release()
	clusters, err := p.api.describeClusters(ctx, "")
	if err != nil {
		return provider.Branch{}, err
	}

	name := p.branchName(envID)
	golden := ""
	goldenVerified := false
	branches := 0
	for _, c := range clusters {
		tags := c.tags()
		if !p.owned(c) {
			continue
		}
		switch tags[tagKind] {
		case kindGolden:
			if tags[tagVersion] == version {
				golden = c.Identifier
				goldenVerified = joinAttestation(tags) != "" && p.prepared(c)
			}
		case kindBranch:
			branches++
			if c.Identifier == name {
				if tags[tagEnv] != envID || tags[tagVersion] != version || !p.prepared(c) {
					return provider.Branch{EnvID: envID, From: version, ProviderRef: name}, fmt.Errorf("aurora: existing branch identity or preparation receipt does not match")
				}
				// Idempotent by environment. The engine retries after a
				// timeout, and a retry that made a second cluster would be an
				// orphan nothing names and nothing pays attention to.
				return provider.Branch{
					EnvID: envID, From: tags[tagVersion],
					ProviderRef: c.Identifier,
					CreatedAt:   parseTagTime(tags[tagCreated]),
				}, nil
			}
		}
	}

	if golden == "" {
		return provider.Branch{}, coded(codeNoSuchGolden,
			fmt.Sprintf("the golden version %s no longer exists", version))
	}
	if !goldenVerified {
		return provider.Branch{}, coded(codeUnverifiedGolden, fmt.Sprintf(
			"the golden %s has no valid verification attestation and cannot be branched",
			version))
	}
	if limit := p.branchLimit(); limit > 0 && branches >= limit {
		// Fast, and with the code the engine knows. Hanging is the failure
		// mode that turns a branch cap into a mystery, and AWS's own refusal
		// would arrive as DBClusterQuotaExceededFault after a round trip.
		return provider.Branch{}, coded(codeBranchLimit, fmt.Sprintf(
			"the provider's concurrent branch limit (%d) is reached", limit))
	}

	created := p.now().UTC()
	tags := map[string]string{
		tagMarker:  Name,
		tagKind:    kindBranch,
		tagVersion: version,
		tagEnv:     envID,
		tagCreated: created.Format(time.RFC3339Nano),
	}
	if _, err := p.clone(ctx, golden, name, tags); err != nil {
		if isCode(err, faultQuotaExceeded) {
			return provider.Branch{}, coded(codeBranchLimit, fmt.Sprintf(
				"AWS refused the clone because this account is at its Aurora quota, "+
					"which is a lower ceiling than the declared limit of %d", p.branchLimit()))
		}
		if !uncertainCreation(err) {
			return provider.Branch{}, err
		}
		return provider.Branch{EnvID: envID, From: version, ProviderRef: name + "#" + tags[tagAttempt], CreatedAt: created}, err
	}
	live, err := p.provision(ctx, name)
	if err != nil {
		// The cluster exists and is named in the error's own resource, and the
		// inventory reports it, so teardown and the leak detector can both
		// find it. It is not removed here: a half provisioned branch is
		// evidence while somebody is diagnosing why an instance would not come
		// up.
		return provider.Branch{EnvID: envID, From: version, ProviderRef: name, CreatedAt: created}, err
	}
	if err := p.markPrepared(ctx, live); err != nil {
		return provider.Branch{EnvID: envID, From: version, ProviderRef: name, CreatedAt: created}, err
	}
	return provider.Branch{
		EnvID: envID, From: version, ProviderRef: name, CreatedAt: created,
	}, nil
}

// Reset is not implemented, and the capability says so.
//
// Aurora's only rewind is Backtrack, which is Aurora MySQL. Destroying the
// clone and cloning again would work and is exactly what Caps.Reset says this
// is not: the capability distinguishes returning a branch to its golden state
// from destroying and recreating it, and the engine chooses between the two.
// Answering yes here by recreating would take the choice away.
func (p *Provider) Reset(context.Context, provider.Branch) error {
	return provider.ErrUnsupported
}

// Destroy removes a branch. Removing one already gone succeeds.
func (p *Provider) Destroy(ctx context.Context, b provider.Branch) error {
	name := b.ProviderRef
	attempt := ""
	if parts := strings.SplitN(name, "#", 2); len(parts) == 2 {
		name, attempt = parts[0], parts[1]
	}
	if name == "" && b.EnvID != "" {
		name = p.branchName(b.EnvID)
	}
	if name == "" {
		return nil
	}
	if attempt != "" || b.EnvID != "" || b.From != "" {
		c, found, err := p.api.describeCluster(ctx, name)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		tags := c.tags()
		if (attempt != "" && tags[tagAttempt] != attempt) || (b.EnvID != "" && tags[tagEnv] != b.EnvID) || (b.From != "" && tags[tagVersion] != b.From) {
			return fmt.Errorf("%w: branch cleanup identity mismatch", ErrNotOurs)
		}
	}
	return p.destroyCluster(ctx, name)
}

// sweepCandidates removes clones that a killed refresh abandoned.
//
// Only candidates, and only ones older than the window a refresh could still
// be inside. A candidate is a cluster tagged by this provider whose kind was
// never flipped to golden, so it is unreachable by ListGoldens and unbranchable
// by anything, and it is also a full clone of production with an instance
// attached. Best effort: a sweep that failed must not fail the refresh that
// triggered it, because the refresh is the useful work and the sweep is
// tidying somebody else's crash.
func (p *Provider) sweepCandidates(ctx context.Context) {
	clusters, err := p.api.describeClusters(ctx, "")
	if err != nil {
		return
	}
	cutoff := p.now().Add(-candidateAge)
	for _, c := range clusters {
		tags := c.tags()
		if !p.owned(c) || tags[tagKind] != kindCandidate {
			continue
		}
		created := firstTime(parseTagTime(tags[tagCreated]), c.Created.Time)
		if created.IsZero() || created.After(cutoff) {
			// A candidate a refresh could still be filling. Removing one that
			// another process is masking would turn one slow refresh into a
			// corrupt one, and a clone of a large database is not quick.
			continue
		}
		_ = p.destroyCluster(ctx, c.Identifier)
	}
}

// candidateAge is how old a candidate has to be before it can only be an
// orphan. Deliberately generous, for the reason sweepCandidates gives.
const candidateAge = 6 * time.Hour

// destroyCluster removes a cluster and its instances, checking the marker
// first.
//
// The marker check is the rule this provider does not bend. A customer whose
// own cluster happens to be called af-b-something must not lose it to our
// teardown, and the only thing separating the two is a tag we wrote.
func (p *Provider) destroyCluster(ctx context.Context, name string) error {
	cluster, found, err := p.api.describeCluster(ctx, name)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if !p.owned(cluster) {
		// Coded rather than a bare sentence, because this is the refusal that
		// stops a misconfigured project operating on somebody else's
		// infrastructure, and a caller has to be able to tell it apart from an
		// AWS failure without matching on English.
		return fmt.Errorf("%w: %s is left alone. Nothing here is removed on the "+
			"strength of its name, because a customer whose own cluster is called "+
			"%s must not lose it to our teardown", ErrNotOurs, name, name)
	}
	for _, m := range cluster.Members {
		if err := p.api.deleteInstance(ctx, m.Instance); err != nil {
			return err
		}
	}
	// The instances are deleting and the cluster delete is issued behind them.
	// AWS refuses a cluster delete while an instance is still attached, and
	// that refusal is InvalidDBClusterStateFault, so it is waited out rather
	// than reported: teardown that gave up here would leave the expensive half.
	return p.retryClusterDelete(ctx, name)
}

func (p *Provider) retryClusterDelete(ctx context.Context, name string) error {
	deadline := p.now().Add(p.readyTimeout)
	for {
		err := p.api.deleteCluster(ctx, name)
		if err == nil {
			_, found, lookupErr := p.api.describeCluster(ctx, name)
			if lookupErr != nil {
				return lookupErr
			}
			if !found {
				return nil
			}
			err = fmt.Errorf("aurora: deleted cluster still exists")
		} else if !isCode(err, faultInvalidState) {
			return err
		}
		if p.now().After(deadline) {
			return fmt.Errorf(
				"the cluster %s still had instances attached after %s and could not be "+
					"deleted: %w", name, p.readyTimeout, err)
		}
		if err := sleep(ctx, p.poll); err != nil {
			return err
		}
	}
}

// ConnString returns a connection string for a branch.
//
// The password is derived rather than stored, which is why this works in a
// process that did not create the branch. Nothing wrote a credential anywhere:
// not a tag, not the state directory, not the journal.
func (p *Provider) ConnString(ctx context.Context, b provider.Branch, mode provider.ConnMode) (secret.Value, error) {
	if mode == provider.ConnPooled {
		// Declared false in Capabilities, so the engine does not ask. Refusing
		// rather than handing back the direct string means a build that starts
		// asking gets an error instead of a pool that is not one.
		return secret.Value{}, provider.ErrUnsupported
	}
	name := b.ProviderRef
	if name == "" && b.EnvID != "" {
		name = p.branchName(b.EnvID)
	}
	if name == "" {
		return secret.Value{}, fmt.Errorf("aurora: branch identity is required")
	}
	cluster, found, err := p.api.describeCluster(ctx, name)
	if err != nil {
		return secret.Value{}, err
	}
	if !found {
		return secret.Value{}, fmt.Errorf("no branch cluster named %s exists", b.ProviderRef)
	}
	if !p.prepared(cluster) || (b.EnvID != "" && cluster.tags()[tagEnv] != b.EnvID) || (b.From != "" && cluster.tags()[tagVersion] != b.From) {
		return secret.Value{}, fmt.Errorf("aurora: branch identity or preparation receipt does not match")
	}
	return p.connString(cluster)
}

func (p *Provider) connString(c dbCluster) (secret.Value, error) {
	if p.closed.Load() {
		return secret.Value{}, fmt.Errorf("aurora: provider is closed")
	}
	if c.IAMEnabled {
		return secret.Value{}, fmt.Errorf("aurora: IAM database authentication was not disabled")
	}
	if c.Endpoint == "" {
		return secret.Value{}, fmt.Errorf(
			"the cluster %s reports no endpoint, which is what a cluster with no writer "+
				"instance looks like", c.Identifier)
	}
	user := or(c.MasterUsername, "postgres")
	database := or(c.DatabaseName, "postgres")
	port := c.Port
	if port == 0 {
		port = 5432
	}
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(user, p.passwordFor(c.Identifier)),
		Host:   net.JoinHostPort(c.Endpoint, strconv.Itoa(port)),
		Path:   "/" + database,
	}
	// Plaintext is for the loopback fixture and nothing else. Azure and Cloud
	// SQL refuse it for a remote server, and a copy of production is no less
	// sensitive on AWS.
	if p.tlsMode == "disable" && !loopbackHost(c.Endpoint) {
		return secret.Value{}, fmt.Errorf("aurora: sslmode disable is limited to a loopback endpoint; use verify-full")
	}
	q := url.Values{}
	q.Set("sslmode", p.tlsMode)
	if p.tlsMode != "disable" {
		if p.tlsMode != "verify-full" {
			return secret.Value{}, fmt.Errorf("aurora: sslmode must be verify-full")
		}
		path, err := p.trustFile()
		if err != nil {
			return secret.Value{}, err
		}
		q.Set("sslrootcert", path)
	}
	u.RawQuery = q.Encode()
	return secret.New(u.String()), nil
}

func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Inventory lists every cluster this provider holds.
func (p *Provider) Inventory(ctx context.Context) ([]provider.Resource, error) {
	clusters, err := p.api.describeClusters(ctx, "")
	if err != nil {
		return nil, err
	}
	out := make([]provider.Resource, 0, len(clusters))
	for _, c := range clusters {
		tags := c.tags()
		if !p.owned(c) {
			continue
		}
		kind := tags[tagKind]
		if kind == "" {
			kind = "unknown"
		}
		out = append(out, provider.Resource{
			Kind:      "cluster/" + kind,
			ID:        c.Identifier,
			EnvID:     tags[tagEnv],
			CreatedAt: firstTime(parseTagTime(tags[tagCreated]), c.Created.Time),
			Labels: map[string]string{
				"version": tags[tagVersion],
				"status":  c.Status,
				"engine":  c.Engine,
			},
		})
	}
	return out, nil
}

// Health reports whether a branch is reachable.
//
// Gone is an answer rather than a fault. Teardown asks for health, so a
// provider that errored on a branch it had just removed would make a
// successful teardown look like a failure.
func (p *Provider) Health(ctx context.Context, b provider.Branch) (provider.Health, error) {
	started := p.now()
	cluster, found, err := p.api.describeCluster(ctx, b.ProviderRef)
	if err != nil {
		return provider.Health{}, err
	}
	if !found {
		return provider.Health{
			Reachable: false,
			Detail:    "the cluster " + b.ProviderRef + " no longer exists",
			Latency:   p.now().Sub(started),
		}, nil
	}
	if !p.prepared(cluster) || cluster.Endpoint == "" {
		return provider.Health{
			Reachable: false,
			Detail:    "the cluster " + b.ProviderRef + " reports status " + cluster.Status,
			Latency:   p.now().Sub(started),
		}, nil
	}
	connection, err := p.connString(cluster)
	if err != nil {
		return provider.Health{Reachable: false, Detail: err.Error(),
			Latency: p.now().Sub(started)}, nil
	}
	if err := ping(ctx, connection); err != nil {
		// The status said available and the connection did not open, which is
		// a security group or a subnet far more often than a broken database,
		// and saying which was asked is what shortens that hunt.
		return provider.Health{
			Reachable: false,
			Detail:    "the cluster is available and the connection was refused: " + err.Error(),
			Latency:   p.now().Sub(started),
		}, nil
	}
	return provider.Health{Reachable: true, Detail: "available", Latency: p.now().Sub(started)}, nil
}

// Close removes only the private CA directory this provider created.
func (p *Provider) Close() error { return p.closeTrust() }

// ---------------------------------------------------------------------------
// Waiting, connecting, and the small pure helpers
// ---------------------------------------------------------------------------

func (p *Provider) waitCluster(ctx context.Context, name string) (dbCluster, error) {
	deadline := p.now().Add(p.readyTimeout)
	for {
		cluster, found, err := p.api.describeCluster(ctx, name)
		if err != nil {
			return dbCluster{}, err
		}
		if found && cluster.Status == "available" {
			return cluster, nil
		}
		if p.now().After(deadline) {
			status := "absent"
			if found {
				status = cluster.Status
			}
			return dbCluster{}, fmt.Errorf(
				"the cluster %s was %s after %s rather than available", name, status, p.readyTimeout)
		}
		if err := sleep(ctx, p.poll); err != nil {
			return dbCluster{}, err
		}
	}
}

func (p *Provider) waitInstance(ctx context.Context, name string) error {
	deadline := p.now().Add(p.readyTimeout)
	for {
		instance, found, err := p.api.describeInstance(ctx, name)
		if err != nil {
			return err
		}
		if found && instance.Status == "available" {
			return nil
		}
		if p.now().After(deadline) {
			status := "absent"
			if found {
				status = instance.Status
			}
			return fmt.Errorf(
				"the writer instance %s was %s after %s rather than available. This is the "+
					"slow half of a branch and it is not the clone: the storage was ready "+
					"immediately and an instance takes minutes", name, status, p.readyTimeout)
		}
		if err := sleep(ctx, p.poll); err != nil {
			return err
		}
	}
}

// sleep waits, and returns the context's error if it is cancelled first.
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ping opens a connection and closes it.
func ping(ctx context.Context, connection secret.Value) error {
	db, err := sql.Open("pgx", connection.Reveal())
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return db.PingContext(ctx)
}

// passwordFor derives a cluster's master password.
//
// Deterministic on purpose: a connection string has to be rebuildable by a
// process that did not create the branch, and the alternatives are storing a
// password somewhere or asking AWS to rotate it again. An HMAC of the operator
// held key and the cluster identifier is neither. The output is base64url,
// whose alphabet avoids every character Aurora refuses in a master password.
func (p *Provider) passwordFor(cluster string) string {
	mac := hmac.New(sha256.New, []byte(p.branchKey.Reveal()))
	mac.Write([]byte("antifailure/aurora/master/v1\n"))
	mac.Write([]byte(cluster))
	// Prefixed with a letter because Aurora refuses a password beginning with
	// a slash, and shortened to well inside the hundred character ceiling.
	return "Af" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))[:30]
}

// shortHash is the twelve characters of an identifier that survive into a
// cluster name.
//
// A cluster identifier may hold letters, digits and single hyphens, must begin
// with a letter, and is at most 63 characters. A golden version identifier
// carries underscores and an environment identifier is not this package's to
// shorten, so neither can be used directly and both are hashed. Twelve hex
// characters is 48 bits, which for the tens of clusters an account holds is
// not a collision anybody will meet.
func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:32]
}

// writerOf is the instance identifier for a cluster's writer.
func writerOf(cluster string) string { return cluster + "-w" }

func parseTagTime(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

func firstTime(values ...time.Time) time.Time {
	for _, v := range values {
		if !v.IsZero() {
			return v
		}
	}
	return time.Time{}
}

// chunkAttestation splits an attestation across numbered tags.
//
// A tag value holds 256 characters and an attestation is a signed statement
// that does not. It is split rather than truncated, and the split REFUSES past
// its own ceiling rather than dropping the tail: a golden whose attestation was
// silently cut in half would still read as verified, and the attestation is
// the only record of what the scanner looked at.
func chunkAttestation(attestation string) (map[string]string, error) {
	out := map[string]string{}
	if attestation == "" {
		return out, nil
	}
	if len(attestation) > tagValueLimit*attestationChunks {
		return nil, fmt.Errorf(
			"the verification attestation is %d characters and an Aurora golden can "+
				"record %d, because AWS allows %d characters per tag and this provider "+
				"reserves %d tags for it. It is refused rather than truncated: a golden "+
				"carrying half an attestation would still read as verified",
			len(attestation), tagValueLimit*attestationChunks, tagValueLimit, attestationChunks)
	}
	for i := 0; i*tagValueLimit < len(attestation); i++ {
		start := i * tagValueLimit
		end := start + tagValueLimit
		if end > len(attestation) {
			end = len(attestation)
		}
		out[tagAttestation+":"+strconv.Itoa(i)] = attestation[start:end]
	}
	return out, nil
}

// joinAttestation reassembles what chunkAttestation split.
//
// A gap in the numbering returns nothing rather than the pieces on either side
// of it. Half an attestation is not a shorter attestation, it is an unverified
// golden, and returning the prefix would make Verified true.
func joinAttestation(tags map[string]string) string {
	var b strings.Builder
	for i := 0; ; i++ {
		chunk, ok := tags[tagAttestation+":"+strconv.Itoa(i)]
		if !ok {
			break
		}
		b.WriteString(chunk)
	}
	joined := b.String()
	if joined == "" {
		return ""
	}
	// Every chunk except the last is full. One that is not means a chunk in
	// the middle went missing, and the pieces either side of the hole are not
	// an attestation.
	count := 0
	for i := 0; ; i++ {
		if _, ok := tags[tagAttestation+":"+strconv.Itoa(i)]; !ok {
			break
		}
		count++
	}
	for i := 0; i < count-1; i++ {
		if len(tags[tagAttestation+":"+strconv.Itoa(i)]) != tagValueLimit {
			return ""
		}
	}
	return joined
}

// APICalls is how many control plane requests this provider has made.
//
// Exported for the benchmark, which measures the work a branch does rather
// than how long AWS takes to do it. The second is what a customer feels and
// needs an account to measure; the first is a property of this code and can be
// measured anywhere, and it is the half of the flat branch claim that is
// falsifiable without spending money.
func (p *Provider) APICalls() int64 { return p.api.calls.Load() }

var _ provider.Database = (*Provider)(nil)
