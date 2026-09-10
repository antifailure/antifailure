// Package azurepg is the database provider for Azure Database for PostgreSQL
// Flexible Server.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
// It is enterprise by the editions rule in the plan, and for the same reason
// aurora and cloudsql are: reaching a production flexible server needs a
// service principal somebody with a subscription grants, which is not one
// developer with their own card.
//
// THE CLAIM THIS PROVIDER MAKES IS SMALLER THAN THE OTHER TWO, AND THAT IS THE
// POINT OF THE PACKAGE RATHER THAN AN APOLOGY FOR IT.
//
// aurora declares CopyOnWrite true because an Aurora clone shares the source's
// storage volume. cloudsql declares it true because a Cloud SQL fast clone is
// created from an Instant Snapshot, which Google documents as metadata only.
// THIS PROVIDER DECLARES IT FALSE, and it is false on purpose:
//
// A branch here is a point in time RESTORE. Microsoft documents a restore as
// creating a NEW SERVER, and describes the restored server as an independent
// copy: the physical database files are restored from the snapshot backups to
// the new server's data location, and then a recovery process replays write
// ahead log files to bring it to a consistent state. Nothing in Microsoft's
// documentation claims the restored server shares storage with the source, and
// this lane will not infer that it does from the fact that a snapshot restore
// is fast. Those are different claims and the second does not follow.
//
// The temptation to declare it true is real and worth naming, because the
// fast half of the number is genuinely fast. Microsoft states that "the data
// restore operation from a snapshot doesn't depend on the size of data", which
// reads exactly like the copy on write sentence. The SAME PARAGRAPH continues:
// "the recovery process timing that applies the logs (transaction activities to
// replay) might vary, depending on the previous backup of the requested date
// and time and the number of logs to process", and gives the overall recovery
// as "a few minutes up to a few hours". So one half of the operation is flat in
// the size of the data and the other half is not flat in anything a caller
// controls. Quoting the first sentence and declaring copy on write would be
// quoting the fast half of a number whose slow half is the one a person waits
// through, and the conformance suite would then require branch time NOT to grow
// with the database, which is an assertion this provider cannot honestly make.
//
// Declaring it false is NOT a way of avoiding the question. engine/conformance
// requires the OPPOSITE proof of a provider that declares false: that branch
// time does grow with size. So the honest declaration is also the one that
// leaves the behaviour testable, rather than the one that would need excusing.
//
// WHAT THIS PROVIDER STILL DOES BUY, since a false capability invites the
// question of why the package exists: a restore is size independent in its
// snapshot half, it needs no dump and no reload, it produces a server carrying
// the golden's rows without anything reading them over a connection, and it is
// the only mechanism Azure offers that does. The alternative for an Azure user
// today is pg_dump and pg_restore, whose every half scales with the data.
//
// The model:
//
//   - The SOURCE is a flexible server, named by database.project. It is never
//     written to and never read over a connection; it is restored from.
//   - A GOLDEN is a restore of the source, with the masking rules applied and
//     the verification scanner run against it.
//   - A BRANCH is a restore of a golden.
//
// THREE THINGS AZURE DOES NOT CARRY ACROSS A RESTORE, each of which is a real
// defect if a provider assumes otherwise, and each handled here:
//
//   - FIREWALL RULES ARE NOT COPIED. Microsoft lists applying them as a post
//     restore task. A branch created and left alone is therefore a server
//     nobody can connect to, and the failure arrives as a connection timeout
//     rather than as anything naming a firewall. This provider creates the rule
//     it needs and Health reports the branch unreachable until it exists.
//   - SERVER PARAMETERS ARE NOT COPIED. A source tuned for production comes
//     back at the defaults.
//   - PUBLIC AND PRIVATE ACCESS CANNOT BE CROSSED. A server on a virtual
//     network restores only to a virtual network, and one on public access
//     restores only to public access. This provider refuses the crossing up
//     front rather than letting Azure refuse it after provisioning.
//
// Two things this provider deliberately does NOT do:
//
//   - It does not implement Microsoft Entra database authentication. Flexible
//     Server supports it, it would be the better credential, and it is not
//     here.
//   - It does not implement Reset. A restore creates a new server rather than
//     returning an existing one to an earlier state, so Reset returns
//     provider.ErrUnsupported and the conformance suite skips that behaviour by
//     name rather than passing it silently.
//
// Nothing here is renamed or deleted on the strength of its name. Every server
// this provider creates carries an antifailure tag, and every destructive path
// reads that tag first: a customer whose own server is called af-b-something
// must not lose it to our garbage collection. On Azure that check is sharper
// than elsewhere, because DELETING A SERVER DELETES ITS BACKUPS, which
// Microsoft states plainly. There is no recovery from a wrong delete here.
package azurepg

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
const Name = "azurepg"

// The variables this provider reads, all through the engine's credential chain
// rather than from the process environment.
const (
	// SubscriptionVariable is the Azure subscription holding the servers.
	SubscriptionVariable = "AF_AZUREPG_SUBSCRIPTION"
	// ResourceGroupVariable is the resource group the servers live in.
	ResourceGroupVariable = "AF_AZUREPG_RESOURCE_GROUP"
	// LocationVariable is the Azure region. A restore lands in the same region
	// as its source, so this is read to refuse a mismatch rather than to
	// choose one.
	LocationVariable = "AF_AZUREPG_LOCATION"
	// DefaultVariable names the branch key, from which a distinct
	// administrator password is derived for every restore.
	DefaultVariable = "AF_AZUREPG_BRANCH_KEY"
	// EndpointVariable overrides the Resource Manager endpoint, which is what
	// the conformance suite points at its fake.
	EndpointVariable = "AF_AZUREPG_ENDPOINT"
	// FirewallVariable is the CIDR the created firewall rule admits. Without
	// it a branch is a server nobody can reach, so its absence is refused
	// rather than defaulted to the whole internet.
	FirewallVariable = "AF_AZUREPG_ALLOW_CIDR"
	// TLSModeVariable overrides the sslmode of the connection strings.
	TLSModeVariable = "AF_AZUREPG_TLS_MODE"
)

// apiVersion is the Resource Manager API version every request names.
//
// Pinned rather than floating, for the reason ci.yml pins its actions: an API
// version resolved at run time is a shape that can change under a provider
// nobody has touched, and the failure lands on whoever deploys next.
const apiVersion = "2024-08-01"

// tagKey is the resource tag every server this provider creates carries.
//
// A tag rather than a name prefix, because a name is a string a customer may
// also have chosen, and on Azure the cost of getting this wrong is absolute:
// deleting a flexible server deletes its backups too.
const tagKey = "antifailure"

const tagValue = "true"

// envTagKey carries the environment a branch belongs to, for Inventory.
const envTagKey = "antifailure-env"

// goldenTagKey marks a server as a published golden rather than a branch. Its
// value is the version, and versionTagKey carries the authoritative copy.
const goldenTagKey = "antifailure-golden"

// fromTagKey carries the golden a branch was restored from, so DestroyGolden
// can refuse to remove one that is still referenced.
const fromTagKey = "antifailure-from"

// versionTagKey carries the golden version a published server holds.
//
// Written only AFTER verification returns, which makes its presence the exact
// test for "was this published": RefreshGolden sets the ownership marker before
// masking and this one after verifying, so a server carrying the first and not
// the second is a golden whose masking pass did not complete.
const versionTagKey = "antifailure-version"

// defaultAdminUser is the fallback administrator login.
//
// A FALLBACK rather than a constant the provider uses. The administrator login
// is a real field on the flexible server resource, it is chosen when the server
// is created, and assuming it would make this provider set a password on a user
// that may not be the one a branch is reached with. The failure would arrive as
// an authentication error against a server that provisioned perfectly.
const defaultAdminUser = "afadmin"

// systemDatabases are the ones Azure creates and no application uses.
//
// Named explicitly rather than pattern matched, because "starts with azure"
// would also exclude a customer's own database called azure_metrics, and
// excluding somebody's real database is how a branch reports healthy while
// holding none of their rows.
var systemDatabases = map[string]bool{
	"postgres": true, "template0": true, "template1": true,
	"azure_maintenance": true, "azure_sys": true,
}

// pickDatabase chooses which database on a server a connection string names.
//
// The configured name wins. Otherwise the single non system database wins,
// which is the ordinary shape of an application's server. With none, or with
// more than one and no configuration, it falls back to postgres, which always
// exists and connects: an ambiguous guess between two application databases
// would be wrong silently rather than empty obviously.
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

// adminLoginOf is the administrator a connection string authenticates as.
func adminLoginOf(s *server, configured string) string {
	if configured != "" {
		return configured
	}
	if s != nil && s.Properties.AdministratorLogin != "" {
		return s.Properties.AdministratorLogin
	}
	return defaultAdminUser
}

// Access is whether a server is reachable publicly or only on a virtual
// network.
//
// Modelled because Microsoft states a restore CANNOT cross the two, so a
// provider that does not know which side it is on will discover the answer
// after provisioning a server it then has to delete.
type Access string

const (
	// AccessPublic is a server with a public endpoint and firewall rules.
	AccessPublic Access = "public"
	// AccessPrivate is a server delegated to a virtual network.
	AccessPrivate Access = "private"
)

// Options is what New needs. Everything arrives here; nothing is read from the
// process environment.
type Options struct {
	Subscription  string
	ResourceGroup string
	Location      string
	SourceServer  string
	BranchKey     secret.Value
	Variable      string
	Endpoint      string
	AllowCIDR     string
	TLSMode       string
	MaxBranches   int
	PollInterval  time.Duration
	Getenv        func(string) string
	Now           func() time.Time
	HTTPClient    httpDoer
}

// Provider is the Azure Database for PostgreSQL provider.
type Provider struct {
	api    *armAPI
	opts   Options
	now    func() time.Time
	closed bool
}

// New builds a provider from already resolved options.
//
// It does no network call, for the reason aurora's and cloudsql's New do not:
// a provider that reached out here would make `af up` fail at construction with
// a network error in place of the manifest error the caller actually has.
func New(opts Options) (*Provider, error) {
	switch {
	case opts.SourceServer == "":
		return nil, fmt.Errorf("azurepg: SourceServer is empty")
	case opts.Subscription == "":
		return nil, fmt.Errorf("azurepg: Subscription is empty")
	case opts.ResourceGroup == "":
		return nil, fmt.Errorf("azurepg: ResourceGroup is empty")
	case opts.BranchKey.IsZero():
		return nil, fmt.Errorf("azurepg: BranchKey is empty")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 2 * time.Second
	}
	api, err := newARMAPI(opts)
	if err != nil {
		return nil, err
	}
	return &Provider{api: api, opts: opts, now: opts.Now}, nil
}

// Name identifies the provider, and is what appears in a manifest.
func (p *Provider) Name() string { return Name }

// Capabilities describes what this provider can do.
//
// CopyOnWrite is FALSE, and the package comment carries the whole argument for
// why declaring it true would be quoting the fast half of a number. It is the
// one field in this struct that a competitor's slide would have set the other
// way.
//
// ExpectedBranchLatency is fifteen minutes rather than the six cloudsql
// declares, and the difference is not a guess about slower hardware. Microsoft
// gives the overall recovery as "from a few minutes up to a few hours", and the
// upper half of that range is log replay whose duration depends on transaction
// activity on the source rather than on anything this provider chooses. Fifteen
// minutes is a declaration a caller can plan against; it is not a promise, and
// the conformance suite is what would catch it drifting.
func (p *Provider) Capabilities() provider.Caps {
	return provider.Caps{
		Branching:             true,
		Reset:                 false,
		CopyOnWrite:           false,
		ProviderMasking:       false,
		PooledEndpoints:       false,
		Subsetting:            false,
		MaxConcurrentBranches: p.opts.MaxBranches,
		ExpectedBranchLatency: 15 * time.Minute,
		SupportedVersions:     []int{13, 14, 15, 16, 17},
	}
}

// Close releases the provider's own resources.
func (p *Provider) Close() error {
	p.closed = true
	return nil
}

// branchPassword derives a distinct administrator password for one server.
//
// Azure requires an administrator password to contain three of upper case,
// lower case, digits and symbols, and to be between 8 and 128 characters. A
// hex digest satisfies none of those rules on its own, so the shape is built
// deliberately rather than hoped for: a fixed upper case letter, a fixed
// symbol, a fixed digit and then the digest. Getting this wrong produces an
// Azure refusal at create time that names the policy and not the code.
func (p *Provider) branchPassword(server string) string {
	mac := hmac.New(sha256.New, []byte(p.opts.BranchKey.Reveal()))
	mac.Write([]byte(server))
	return "Af1!" + hex.EncodeToString(mac.Sum(nil))[:28]
}

// serverName builds the flexible server name for a branch or golden.
//
// Azure flexible server names are 3 to 63 characters, lower case letters,
// digits and hyphens, and must not start or end with a hyphen. They are also
// globally unique within a region's DNS zone, which is why the environment
// identifier is hashed rather than used directly: two customers with an
// environment called "staging" must not collide.
func (p *Provider) serverName(prefix, envID string) string {
	sum := sha256.Sum256([]byte(envID))
	return fmt.Sprintf("af-%s-%s", prefix, hex.EncodeToString(sum[:])[:16])
}

// port is the port a branch is reached on.
func (p *Provider) port() int {
	if p.opts.Port > 0 {
		return p.opts.Port
	}
	return 5432
}

func (p *Provider) tlsMode() string {
	if p.opts.TLSMode != "" {
		return p.opts.TLSMode
	}
	// require rather than verify-full: Azure presents a public CA certificate
	// that a client has to trust out of band, and verify-full without the root
	// installed fails to connect rather than connecting less safely.
	return "require"
}

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

func sortVersionsNewestFirst(in []provider.GoldenVersion) {
	sort.SliceStable(in, func(i, j int) bool {
		return in[i].CreatedAt.After(in[j].CreatedAt)
	})
}

// isOurs reports whether a server carries this provider's ownership tag.
func isOurs(s *server) bool {
	if s == nil {
		return false
	}
	return s.Tags[tagKey] == tagValue
}

// normaliseServerName trims what a person is likely to paste.
//
// A flexible server's fully qualified domain name is what the portal shows most
// prominently, so it is what somebody puts in database.project. Taking the
// first label is tolerance on the read boundary; the write boundary stays
// strict.
func normaliseServerName(raw string) string {
	if i := strings.Index(raw, "."); i > 0 {
		return raw[:i]
	}
	return raw
}
