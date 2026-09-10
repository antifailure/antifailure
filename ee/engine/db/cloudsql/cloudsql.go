// Package cloudsql is the database provider for Google Cloud SQL for
// PostgreSQL.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
// It is enterprise by the editions rule in the plan, and for the same reason
// aurora is: reaching a production Cloud SQL instance needs a service account
// somebody with an organization grants, which is not one developer with their
// own card.
//
// The claim this provider exists to make, in one sentence: a branch is a Cloud
// SQL FAST CLONE, the fast clone is created from an Instant Snapshot, which
// Google documents as a metadata only operation, so the work of branching does
// not depend on how large the database is.
//
// THE SENTENCE THAT HAS TO TRAVEL WITH IT, because it is the whole reason this
// package is more than a translation of the aurora one:
//
// Cloud SQL has TWO clone workflows and they are not distinguished by name at
// the call site. The fast one is metadata only. The standard one takes a full
// backup and provisions from it, and Google documents its duration as scaling
// with the size of the database. Cloud SQL chooses between them from the SHAPE
// OF THE REQUEST, silently, and returns the same operation either way. A
// provider that asks for a clone and reports copy on write is therefore making
// a claim it has not checked, and it will be WRONG in the ordinary case rather
// than in a corner: the conditions that force the slow path are easy to meet by
// accident and there is no error when they are.
//
// Three conditions force the standard workflow, all three established from
// Google's own clone-instance page rather than from a vendor comparison:
//
//   - Naming a zone at all. Not naming a DIFFERENT zone: Google states that
//     re-specifying even the SAME zone falls back to the standard workflow. The
//     fast path requires the field to be ABSENT. This is the trap. A request
//     that pins the zone for the honest reason of keeping a branch next to its
//     golden is exactly the request that stops being a fast clone, and it looks
//     more careful than the one that works.
//   - Asking for a point in time. A clone carrying a PITR timestamp is restored
//     rather than snapshotted, so it takes the standard path.
//   - Disk properties that do not match the source, meaning the disk type,
//     the encryption and the block size.
//
// So this provider does not ask for a clone and hope. It builds the request
// that CAN be fast, refuses to build one that cannot, and then checks the
// result rather than assuming it: fastCloneRequest is the only shape it will
// send, and Capabilities reports CopyOnWrite from that shape rather than from
// the vendor's headline. The refusal names which of the three conditions the
// caller asked for. This is the same defect the ECS lane found in a DNS
// Firewall rule group whose last rule blocked nothing: every field was set,
// every assertion about the fields was true, and the thing the configuration
// was supposed to do did not happen.
//
// The model:
//
//   - The SOURCE is a Cloud SQL for PostgreSQL instance, named by
//     database.project. It is never written to and never read over a
//     connection; it is cloned.
//   - A GOLDEN is a clone of the source, with the masking rules applied and the
//     verification scanner run against it.
//   - A BRANCH is a fast clone of a golden.
//
// WHERE THIS DIFFERS FROM AURORA, and it is not a detail:
//
// Aurora publishes a golden by DELETING the writer instance and keeping the
// volume, because an Aurora cluster's storage exists whether or not an instance
// is attached and is still clonable. A published Aurora golden costs storage
// and no compute. CLOUD SQL HAS NO SUCH THING. An instance is compute and
// storage together and there is no clonable object underneath it. The closest
// available shape is an instance whose activation policy is NEVER, which stops
// the compute and keeps the disk, and this provider uses it because it is the
// cheapest correct option rather than because it is equivalent.
//
// It is NOT equivalent, in a way that is stated rather than smoothed over:
// whether Cloud SQL will fast clone an instance that is stopped is NOT
// established by this lane. Google's clone-instance page does not address a
// stopped source in either direction, and this provider must not assume the
// permissive answer about somebody's bill or somebody's outage. So
// GoldenStopPolicy defaults to GoldenStaysRunning, which costs compute per
// golden and is known to work, and the cheaper policy is opt in with the
// unknown written on it. Settling it takes one clone of one stopped instance in
// one project, which needs an account, and section 10 of the plan says no test
// here may need one.
//
// Two things this provider deliberately does NOT do, named because a provider
// that is named and not built is worse than one that is absent:
//
//   - It does not implement IAM database authentication. Cloud SQL supports it
//     for PostgreSQL, it would be the better credential, and it is not here.
//   - It does not implement Reset. Cloud SQL has no rewind that returns an
//     instance to an earlier state without creating a new instance, so Reset
//     returns provider.ErrUnsupported and the conformance suite skips that
//     behaviour by name rather than passing it silently.
//
// Nothing here is renamed or deleted on the strength of its name. Every
// instance this provider creates carries an antifailure user label, and every
// destructive path reads that label first, for the same reason aurora reads a
// tag and pgurl reads a comment: a customer whose own instance is called
// af-b-something must not lose it to our garbage collection.
package cloudsql

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the pgx driver

	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

// Name is the value database.provider takes in a manifest.
const Name = "cloudsql"

// The variables this provider reads, all through the engine's credential chain
// rather than from the process environment.
const (
	// ProjectVariable is the Google Cloud project holding the instances.
	ProjectVariable = "AF_CLOUDSQL_PROJECT"
	// RegionVariable is the region the source instance lives in. A Cloud SQL
	// instance is regional and asking the wrong region reports that the
	// instance does not exist rather than that the region is wrong.
	RegionVariable = "AF_CLOUDSQL_REGION"
	// DefaultVariable names the branch key, from which a distinct password is
	// derived for every clone.
	DefaultVariable = "AF_CLOUDSQL_BRANCH_KEY"
	// EndpointVariable overrides the Admin API endpoint, which is what the
	// conformance suite points at its fake.
	EndpointVariable = "AF_CLOUDSQL_ENDPOINT"
	// TierVariable overrides the machine tier new instances are created with.
	// Empty means the source's own tier, which is what keeps the disk
	// properties matching and the clone fast.
	TierVariable = "AF_CLOUDSQL_TIER"
	// GoldenStopVariable opts in to stopping a published golden. See
	// GoldenStopPolicy.
	GoldenStopVariable = "AF_CLOUDSQL_STOP_GOLDENS"
	// TLSModeVariable overrides the sslmode of the connection strings handed
	// back to an environment.
	TLSModeVariable = "AF_CLOUDSQL_TLS_MODE"
)

// labelKey is the user label every instance this provider creates carries.
//
// A label rather than a name prefix, for the reason the package comment gives:
// a name is a string a customer may also have chosen, and reading it back is
// not evidence that we created the thing. Cloud SQL user labels are returned on
// the instance resource, so the check is a read of the same object rather than
// a second call that could answer about a different instance.
const labelKey = "antifailure"

// labelValue is what labelKey is set to. It is not the environment identifier:
// that is carried separately, because a label whose VALUE varies cannot be used
// as the ownership predicate without also parsing it.
const labelValue = "true"

// envLabelKey carries the environment a branch belongs to, for Inventory.
const envLabelKey = "antifailure-env"

// goldenLabelKey marks an instance as a published golden rather than a branch.
//
// Its VALUE is a shortened, label safe form of the version and is not the
// version itself. Cloud SQL label values take only lower case letters, digits,
// hyphens and underscores, and a golden version id contains neither uppercase
// nor anything exotic but is longer than the marker needs to be. The
// authoritative copy of the version lives in versionLabelKey, base32 encoded
// like the rest of the metadata, so nothing has to reconstruct it from a
// truncation.
const goldenLabelKey = "antifailure-golden"

// fromLabelKey carries the golden a branch was cloned from.
//
// It is the label safe rendering rather than the version itself, because it is
// only ever compared against another rendering of the same shape. Nothing reads
// it back as an identifier.
const fromLabelKey = "antifailure-from"

// versionLabelKey carries the authoritative golden version, base32 encoded.
const versionLabelKey = "af-version-0"

// shortVersion is the marker value for goldenLabelKey.
//
// Lower cased and stripped to the label charset. It is a MARKER rather than an
// identifier: nothing reads it back as a version, and versionLabelKey is what
// ListGoldens decodes. Two goldens colliding here would be a cosmetic collision
// in a marker, not two goldens with one id.
func shortVersion(version string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(version) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) > 63 {
		out = out[:63]
	}
	if out == "" {
		return "golden"
	}
	return out
}

// GoldenStopPolicy decides whether a published golden keeps its compute.
//
// The type exists rather than a bool because the two values are not a
// preference and the cheaper one carries an unproven assumption. See the
// package comment.
type GoldenStopPolicy string

const (
	// GoldenStaysRunning leaves a published golden's activation policy ALWAYS.
	// It costs compute for every golden retained and it is KNOWN to clone.
	GoldenStaysRunning GoldenStopPolicy = "running"
	// GoldenIsStopped sets a published golden's activation policy to NEVER,
	// which stops the compute and keeps the disk.
	//
	// Whether Cloud SQL will fast clone a stopped instance is NOT established.
	// Choosing this trades a smaller bill for a risk this lane could not
	// measure, and the provider says so at the point of use rather than only
	// here.
	GoldenIsStopped GoldenStopPolicy = "stopped"
)

// Options is what New needs. Everything arrives here; nothing is read from the
// process environment.
type Options struct {
	// Project is the Google Cloud project holding the instances.
	Project string
	// Region is the region the source instance lives in.
	Region string
	// SourceInstance is the Cloud SQL instance goldens are cloned from, which
	// is database.project in a manifest.
	SourceInstance string
	// BranchKey derives every clone's password. It is not the source
	// instance's password.
	BranchKey secret.Value
	// Variable is the name BranchKey was read from, for error messages that
	// name the thing a person has to go and set.
	Variable string
	// Endpoint overrides the Admin API base address.
	Endpoint string
	// Tier overrides the machine tier. Empty keeps the source's.
	Tier string
	// Database names the database a connection string points at. Empty lets
	// the provider choose; see pickDatabase.
	Database string
	// AdminUser names the administrator to authenticate as. Empty lets the
	// provider read it from the instance; see pickUser.
	AdminUser string
	// ProxyAddress is a host:port that replaces the instance's own address.
	//
	// A real deployment mode rather than test scaffolding: Google's documented
	// way to reach a Cloud SQL instance is the Cloud SQL Auth Proxy, which
	// listens on a local port of the operator's choosing and forwards to the
	// instance, so an installation using it reaches every instance at one
	// local address. Setting this makes the provider hand back that address
	// instead of the one the Admin API reports, and it is also what lets the
	// conformance suite point at a Postgres of its own.
	//
	// The DATABASE still distinguishes one instance from another, which is why
	// the database name is read from the instance rather than assumed.
	ProxyAddress string
	// TLSMode is the sslmode for connection strings. Empty means require.
	TLSMode string
	// StopGoldens selects the golden compute policy.
	StopGoldens GoldenStopPolicy
	// MaxBranches is the declared branch limit, enforced rather than hung on.
	MaxBranches int
	// Getenv resolves the optional settings, already bound to the engine's
	// credential chain.
	Getenv func(string) string
	// Now is the clock, injected so a test does not wait on a real one.
	Now func() time.Time
	// PollInterval is how often a long running operation is re-read.
	//
	// An option rather than a constant precisely so that a suite can be fast
	// without the provider having a second, faster code path that production
	// never runs.
	PollInterval time.Duration
	// HTTPClient is the transport, injected so the conformance suite can point
	// this provider at a fake control plane.
	HTTPClient httpDoer
}

// Provider is the Cloud SQL database provider.
type Provider struct {
	api    *adminAPI
	opts   Options
	now    func() time.Time
	closed bool
}

// New builds a provider from already resolved options.
//
// It does no network call. A provider that reached out here would make
// `af up` fail at construction with a network error in place of the manifest
// error the caller actually has, which is the shape aurora's own New avoids.
func New(ctx context.Context, opts Options) (*Provider, error) {
	if opts.SourceInstance == "" {
		return nil, fmt.Errorf("cloudsql: SourceInstance is empty")
	}
	if opts.Project == "" {
		return nil, fmt.Errorf("cloudsql: Project is empty")
	}
	if opts.Region == "" {
		return nil, fmt.Errorf("cloudsql: Region is empty")
	}
	if opts.BranchKey.IsZero() {
		return nil, fmt.Errorf("cloudsql: BranchKey is empty")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.StopGoldens == "" {
		opts.StopGoldens = GoldenStaysRunning
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 2 * time.Second
	}
	if opts.StopGoldens != GoldenStaysRunning && opts.StopGoldens != GoldenIsStopped {
		return nil, fmt.Errorf(
			"cloudsql: %s is %q, which is not one of %q or %q",
			GoldenStopVariable, opts.StopGoldens, GoldenStaysRunning, GoldenIsStopped)
	}
	api, err := newAdminAPI(opts)
	if err != nil {
		return nil, err
	}
	return &Provider{api: api, opts: opts, now: opts.Now}, nil
}

// Name identifies the provider, and is what appears in a manifest.
func (p *Provider) Name() string { return Name }

// Capabilities describes what this provider can do.
//
// CopyOnWrite is true because every clone this provider issues is a
// fastCloneRequest and it will refuse rather than send any other shape. That is
// a statement about the request this code builds, not about a measurement of
// Google's storage, and conformance's own verdict rules are what stop it being
// published as the second thing: the suite reports the copy on write behaviour
// as unproven for any run that does not assert a real service, and this
// provider's suite does not assert one.
//
// ExpectedBranchLatency is in MINUTES rather than seconds, for the reason
// aurora's is. The Instant Snapshot is metadata only and is there in seconds;
// nobody can connect to a snapshot. A branch needs an instance, provisioning
// one takes minutes, and that minute count is also flat in the size of the
// database. Publishing the snapshot's seconds here would be quoting the fast
// half of a number whose slow half is the one a person waits through.
func (p *Provider) Capabilities() provider.Caps {
	return provider.Caps{
		Branching:             true,
		Reset:                 false,
		CopyOnWrite:           true,
		ProviderMasking:       false,
		PooledEndpoints:       false,
		Subsetting:            false,
		MaxConcurrentBranches: p.opts.MaxBranches,
		ExpectedBranchLatency: 6 * time.Minute,
		SupportedVersions:     []int{13, 14, 15, 16, 17},
	}
}

// Close releases the provider's own resources.
func (p *Provider) Close() error {
	p.closed = true
	return nil
}

// branchPassword derives a distinct password for one instance from the branch
// key.
//
// Derived rather than stored, and derived from a key that is NOT the source
// instance's password, so that a preview environment never holds production's
// database credential. Changing the key changes every branch's password, which
// is the intended way to revoke them all at once.
func (p *Provider) branchPassword(instance string) string {
	mac := hmac.New(sha256.New, []byte(p.opts.BranchKey.Reveal()))
	mac.Write([]byte(instance))
	return "af" + hex.EncodeToString(mac.Sum(nil))[:30]
}

// instanceName builds the Cloud SQL instance id for a branch.
//
// Cloud SQL instance ids are at most 98 characters, lower case letters, digits
// and hyphens, and must start with a letter. They are also NOT reusable for a
// week after deletion, which is why the environment identifier is hashed with
// the clock rather than used directly: an environment torn down and brought
// back under the same name inside that window would otherwise be refused by a
// name Google is still holding.
func (p *Provider) instanceName(prefix, envID string) string {
	sum := sha256.Sum256([]byte(envID))
	return fmt.Sprintf("af-%s-%s", prefix, hex.EncodeToString(sum[:])[:16])
}

// tlsMode is the sslmode used in every connection string this provider hands
// back.
//
// require rather than disable by default, and it is worth saying why the
// default is not verify-full: Cloud SQL's server certificate is signed by a per
// instance CA that a client has to be given out of band, so verify-full without
// that file fails to connect rather than connecting less safely. The stronger
// mode is available through the variable for a caller who has distributed the
// CA.
func (p *Provider) tlsMode() string {
	if p.opts.TLSMode != "" {
		return p.opts.TLSMode
	}
	return "require"
}

// address is where a client should connect for one instance.
//
// The proxy address wins when it is set, because an installation running the
// Cloud SQL Auth Proxy reaches every instance through it and the address the
// Admin API reports is not routable from there.
func (p *Provider) address(in *instance) (string, int, error) {
	if p.opts.ProxyAddress != "" {
		host, port, err := net.SplitHostPort(p.opts.ProxyAddress)
		if err != nil {
			return "", 0, fmt.Errorf(
				"cloudsql: ProxyAddress is %q, which is not host:port: %w",
				p.opts.ProxyAddress, err)
		}
		number, err := strconv.Atoi(port)
		if err != nil {
			return "", 0, fmt.Errorf(
				"cloudsql: ProxyAddress is %q, whose port is not a number: %w",
				p.opts.ProxyAddress, err)
		}
		return host, number, nil
	}
	host := primaryAddress(in)
	if host == "" {
		return "", 0, fmt.Errorf(
			"cloudsql: instance %q has no reachable IP address. An instance created "+
				"with only private networking answers on a VPC address, and this process "+
				"is not on that network. Set ProxyAddress if you reach Cloud SQL through "+
				"the Auth Proxy", in.Name)
	}
	// Cloud SQL for PostgreSQL always listens on 5432 and the Admin API
	// exposes no port field, so this is a constant rather than a read.
	return host, 5432, nil
}

// connString assembles a connection string for one instance.
func (p *Provider) connString(host string, port int, user, password, database string) secret.Value {
	u := &url.URL{
		Scheme: "postgresql",
		User:   url.UserPassword(user, password),
		Host:   host + ":" + strconv.Itoa(port),
		Path:   "/" + database,
	}
	q := u.Query()
	q.Set("sslmode", p.tlsMode())
	u.RawQuery = q.Encode()
	return secret.New(u.String())
}

// pickUser chooses the administrator a connection string authenticates as.
//
// The configured name wins. Otherwise the first user the instance reports wins,
// which on a stock Cloud SQL instance is postgres. Falling back to the literal
// "postgres" when the collection is empty keeps the ordinary case working
// without making the name an assumption everywhere else.
func pickUser(configured string, found []user) string {
	if configured != "" {
		return configured
	}
	for _, u := range found {
		if u.Name != "" {
			return u.Name
		}
	}
	return "postgres"
}

// systemDatabases are the ones Cloud SQL creates and no application uses.
//
// Named explicitly rather than pattern matched, because "starts with pg_" would
// also exclude a customer's own database called pg_analytics, and excluding
// somebody's real database is how a branch reports healthy while holding none
// of their rows.
var systemDatabases = map[string]bool{
	"postgres": true, "template0": true, "template1": true, "cloudsqladmin": true,
}

// pickDatabase chooses which database on an instance a connection string names.
//
// The configured name wins. Otherwise the single non system database wins,
// which is the ordinary shape of an application's instance. With none, or with
// more than one and no configuration, it falls back to postgres, which always
// exists and connects: an ambiguous guess between two application databases
// would be worse than the maintenance database, because it would be wrong
// silently rather than empty obviously.
func pickDatabase(configured string, found []database) string {
	if configured != "" {
		return configured
	}
	var candidates []string
	for _, d := range found {
		if !systemDatabases[d.Name] {
			candidates = append(candidates, d.Name)
		}
	}
	if len(candidates) == 1 {
		return candidates[0]
	}
	return "postgres"
}

// sortVersionsNewestFirst orders goldens the way ListGoldens promises.
func sortVersionsNewestFirst(in []provider.GoldenVersion) {
	sort.SliceStable(in, func(i, j int) bool {
		return in[i].CreatedAt.After(in[j].CreatedAt)
	})
}

// isOurs reports whether an instance carries this provider's ownership label.
//
// The predicate every destructive path runs first. It reads the label off the
// instance resource that the same call returned, rather than trusting the name,
// because a name is a string a customer may also have chosen.
func isOurs(in *instance) bool {
	if in == nil {
		return false
	}
	return in.Settings.UserLabels[labelKey] == labelValue
}

// normaliseInstanceID trims what a person is likely to paste.
//
// A Cloud SQL "connection name" is project:region:instance and it is what the
// console shows most prominently, so it is what somebody puts in
// database.project. Accepting it and taking the last segment is tolerance on
// the read boundary; the write boundary stays strict.
func normaliseInstanceID(raw string) string {
	if i := strings.LastIndex(raw, ":"); i >= 0 {
		return raw[i+1:]
	}
	return raw
}
