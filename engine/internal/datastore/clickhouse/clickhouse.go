package clickhouse

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/clock"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// engineName is what a manifest datastore declares to reach this, and what the
// masking and verification dialects are registered under.
//
// One constant rather than three literals, and a test requires the other two
// packages to answer to it. A dialect registered under a name nothing looks up
// is a dialect that never runs.
const engineName = "clickhouse"

// The identifiers this provider owns on a server.
//
// Everything it creates is a database whose name begins with one of these and
// whose comment carries the marker below. Nothing else is ever touched: a
// server holding a golden also holds whatever the developer put there, and a
// provider that swept by name alone would eventually drop somebody's data.
const (
	goldenPrefix    = "af_golden_"
	candidatePrefix = "af_cand_"
	branchPrefix    = "af_env_"
	// marker opens the comment of every database this provider made. It is
	// the label the Docker provider stamps on a container, arriving in the one
	// place ClickHouse offers for it, and it is what makes ownership a fact
	// rather than a guess about a name.
	marker = "antifailure:1:"
)

// Provider implements provider.Datastore, which is asserted here rather than
// discovered at a call site: a method whose signature drifted from the
// interface would otherwise be a provider nothing can be given.
var _ provider.Datastore = (*Provider)(nil)

// Provider is the ClickHouse datastore.
//
// One server holds everything: goldens are databases on it, branches are
// databases on it, and a branch is made by attaching the golden's partitions,
// which ClickHouse implements as a hardlink when both are on one disk. That is
// the whole reason the model is a database rather than a container per branch
// the way the Docker Postgres provider works. A ClickHouse image is most of a
// gigabyte and an events table is not, so a committed image per golden would
// spend a gigabyte to store a hundred megabytes, and starting one per branch
// would spend a minute of memory and startup on a store that answers in
// milliseconds once it is up.
type Provider struct {
	name   string
	server *client
	clock  clock.Clock
	// progress receives a line per step of a refresh, and may be nil.
	progress func(string)
}

// Options configure the provider.
type Options struct {
	// ServerURL is the admin connection to the ClickHouse that holds the
	// goldens and the branches. Required: this provider does not start a
	// server, it uses one.
	ServerURL secrets.Value
	// Name is what this store is called in the environment and in the
	// fidelity report, such as "events". Empty uses the engine name.
	Name string
	// Clock is the time source.
	Clock clock.Clock
	// Progress receives a line per step, and may be nil.
	Progress func(string)
}

// New returns a provider against one server.
func New(opts Options) (*Provider, error) {
	c, err := parseURL(opts.ServerURL)
	if err != nil {
		return nil, err
	}
	name := opts.Name
	if name == "" {
		name = engineName
	}
	if opts.Clock == nil {
		opts.Clock = clock.New()
	}
	progress := opts.Progress
	if progress == nil {
		progress = func(string) {}
	}
	return &Provider{name: name, server: c, clock: opts.Clock, progress: progress}, nil
}

// Name identifies the datastore in output and in errors.
func (p *Provider) Name() string { return p.name }

// Capabilities declares what this datastore can do.
func (p *Provider) Capabilities() provider.DatastoreCaps {
	return provider.DatastoreCaps{
		Engine:    engineName,
		Branching: true,
		Golden:    true,
		// FALSE, and measured rather than assumed in either direction.
		//
		// ATTACH PARTITION FROM hardlinks the golden's parts when the source
		// and the destination are on one disk, and a branch of ten thousand
		// rows and a branch of a million take the same few hundred
		// milliseconds here because of it. What this provider cannot see from
		// the client is the server's storage policy: with a multi disk policy,
		// or a source and destination on different volumes, ClickHouse copies
		// the parts instead and branch time becomes proportional to size.
		//
		// So the measurement is published beside the capability rather than
		// inside it. A capability is a promise the engine acts on, and
		// promising size independence on a server whose disks this cannot see
		// would be a promise about somebody else's hardware.
		CopyOnWrite: false,
	}
}

// Close releases the provider's own resources. The HTTP client holds idle
// connections, which is what there is to release.
func (p *Provider) Close() error {
	if p.server != nil {
		p.server.http.CloseIdleConnections()
	}
	return nil
}

// meta is what a database this provider made carries in its comment.
//
// The comment rather than a table of metadata beside the databases, for the
// reason the Docker provider puts a golden's metadata in an image label: it is
// the only part of a golden that outlives the process that made it, and a
// record kept anywhere else can disagree with what exists. Dropping the
// database takes its metadata with it, which is exactly the behaviour a
// separate table would get wrong.
type meta struct {
	Kind        string    `json:"kind"`
	Version     string    `json:"version,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	RulesHash   string    `json:"rules_hash,omitempty"`
	Provenance  string    `json:"provenance,omitempty"`
	Attestation string    `json:"attestation,omitempty"`
	// Tables are the tables the database holds, which a branch reads to know
	// what to attach.
	Tables []string `json:"tables,omitempty"`
	// Skipped are the source tables the golden does NOT hold, with the reason.
	// A golden that is a subset of production is legitimate and an undeclared
	// one is the blank store this whole wave exists to stop.
	Skipped []skipped `json:"skipped,omitempty"`
	// EnvID and From describe a branch.
	EnvID string `json:"env_id,omitempty"`
	From  string `json:"from,omitempty"`
}

func (m meta) encode() string {
	body, err := json.Marshal(m)
	if err != nil {
		// Every field is a string, a time or a slice of them, so this cannot
		// fail. Written rather than ignored because a comment that silently
		// became empty would make a golden unreadable afterwards.
		return marker
	}
	return marker + base64.StdEncoding.EncodeToString(body)
}

func decodeMeta(comment string) (meta, bool) {
	rest, ok := strings.CutPrefix(comment, marker)
	if !ok {
		return meta{}, false
	}
	body, err := base64.StdEncoding.DecodeString(rest)
	if err != nil {
		return meta{}, false
	}
	var m meta
	if err := json.Unmarshal(body, &m); err != nil {
		return meta{}, false
	}
	return m, true
}

// database is one database this provider owns.
type database struct {
	name string
	meta meta
}

// ours lists the databases this provider made, with their metadata.
//
// The marker in the comment decides, not the name. A developer's own database
// called af_golden_things is not this provider's and is never in this list.
func (p *Provider) ours(ctx context.Context) ([]database, error) {
	var out []database
	err := p.server.rows(ctx,
		"SELECT name, comment FROM system.databases ORDER BY name", nil, func(r [][]byte) error {
			m, ok := decodeMeta(string(r[1]))
			if !ok {
				return nil
			}
			out = append(out, database{name: string(r[0]), meta: m})
			return nil
		})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RefreshGolden builds a new masked, verified copy.
//
// The order is the guarantee and it is not negotiable: create an empty
// candidate, copy the source into it, mask it, verify it, and only then
// publish. Publishing is the tables moving into a database whose comment
// records the attestation, so a version that exists is one that was verified,
// and a verification that fails leaves nothing anything can branch.
func (p *Provider) RefreshGolden(
	ctx context.Context, spec provider.GoldenSpec,
) (provider.GoldenVersion, error) {
	p.sweepCandidates(ctx)

	candidate := candidatePrefix + shortHash(fmt.Sprint(p.clock.Now().UnixNano()))
	if err := p.createDatabase(ctx, candidate, meta{
		Kind: "candidate", CreatedAt: p.clock.Now().UTC(),
	}); err != nil {
		return provider.GoldenVersion{}, err
	}
	// Removed whatever happens. A failed refresh that leaves a database full
	// of production's own data on a shared server is worse than any other
	// failure this function has.
	defer func() { _ = p.dropDatabase(context.WithoutCancel(ctx), candidate) }()

	cand := p.server.in(candidate)
	skips, err := p.loadSource(ctx, cand, spec)
	if err != nil {
		return provider.GoldenVersion{}, err
	}

	if spec.Mask != nil {
		if err := spec.Mask(ctx, cand.url()); err != nil {
			return provider.GoldenVersion{}, fmt.Errorf(
				"datastore.clickhouse: mask the golden candidate: %w", err)
		}
	}
	attestation := ""
	if spec.Verify != nil {
		attestation, err = spec.Verify(ctx, cand.url())
		if err != nil {
			return provider.GoldenVersion{}, fmt.Errorf(
				"datastore.clickhouse: verify the golden candidate: %w", err)
		}
	}

	tables, _, err := readCatalog(ctx, cand)
	if err != nil {
		return provider.GoldenVersion{}, err
	}
	names := make([]string, 0, len(tables))
	for _, t := range tables {
		names = append(names, t.name)
	}

	version := provider.NewGoldenVersionID(p.clock.Now(), spec.RulesHash)
	golden := goldenPrefix + shortHash(version)
	if err := p.createDatabase(ctx, golden, meta{
		Kind: "golden", Version: version, CreatedAt: p.clock.Now().UTC(),
		RulesHash: spec.RulesHash, Provenance: spec.Provenance,
		Attestation: attestation, Tables: names, Skipped: skips,
	}); err != nil {
		return provider.GoldenVersion{}, err
	}
	for _, name := range names {
		if err := p.server.exec(ctx, fmt.Sprintf("RENAME TABLE %s.%s TO %s.%s",
			quoteIdent(candidate), quoteIdent(name),
			quoteIdent(golden), quoteIdent(name)), nil); err != nil {
			// The half published golden is removed rather than left. A
			// database holding some of a version's tables is one a branch
			// would copy and nothing would say was incomplete.
			_ = p.dropDatabase(context.WithoutCancel(ctx), golden)
			return provider.GoldenVersion{}, err
		}
	}

	gv := provider.GoldenVersion{
		ID: version, CreatedAt: p.clock.Now().UTC(), RulesHash: spec.RulesHash,
		Provenance: spec.Provenance, Verified: attestation != "",
		Attestation: attestation, ProviderRef: golden,
	}
	gv.SizeBytes = p.sizeOf(ctx, golden)
	return gv, nil
}

// loadSource copies the source database into the candidate.
//
// A source that is not a ClickHouse URL leaves the candidate empty, which is
// the same answer the Docker Postgres provider gives a project that has not
// connected production: there is a golden, it holds no rows, and whoever asked
// is told so rather than being refused. What is NOT allowed is a source that
// names a server and cannot be read: that is an error, because the alternative
// is publishing an empty golden that looks exactly like a full one.
func (p *Provider) loadSource(
	ctx context.Context, cand *client, spec provider.GoldenSpec,
) ([]skipped, error) {
	if spec.SourceURL.IsZero() || !isHTTPURL(spec.SourceURL) {
		return nil, nil
	}
	src, err := parseURL(spec.SourceURL)
	if err != nil {
		return nil, err
	}
	tables, skips, err := readCatalog(ctx, src)
	if err != nil {
		return nil, err
	}
	for _, t := range tables {
		p.progress(fmt.Sprintf("copying %s (%d rows) into the golden candidate", t.name, t.rows))
		if err := copyTable(ctx, src, cand, t); err != nil {
			return skips, err
		}
	}
	for _, s := range skips {
		p.progress(fmt.Sprintf("the golden does not hold %s: %s", s.Name, s.Reason))
	}
	return skips, nil
}

// isHTTPURL reports whether a source names a server this can read.
//
// The conformance suite passes antifailure://conformance/source, and a project
// with no ClickHouse source passes nothing at all. Both mean the same thing
// here, and neither is an error.
func isHTTPURL(v secrets.Value) bool {
	s := v.Reveal()
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// sweepCandidates removes candidate databases old enough that they can only be
// orphans.
//
// A candidate exists for the minutes between an empty database and a published
// golden, and nothing ever branches one, so removing an old one is
// unconditionally safe. A killed process or a machine that slept leaves one
// holding a copy of production, which is the one kind of leak here that
// matters.
func (p *Provider) sweepCandidates(ctx context.Context) {
	list, err := p.ours(ctx)
	if err != nil {
		return // opportunistic; a refresh must not fail because of it
	}
	cutoff := p.clock.Now().Add(-15 * time.Minute)
	for _, d := range list {
		if d.meta.Kind != "candidate" || !d.meta.CreatedAt.Before(cutoff) {
			continue
		}
		_ = p.dropDatabase(ctx, d.name)
	}
}

// ListGoldens returns published versions, newest first.
func (p *Provider) ListGoldens(ctx context.Context) ([]provider.GoldenVersion, error) {
	list, err := p.ours(ctx)
	if err != nil {
		return nil, err
	}
	var out []provider.GoldenVersion
	for _, d := range list {
		if d.meta.Kind != "golden" {
			continue
		}
		out = append(out, provider.GoldenVersion{
			ID: d.meta.Version, CreatedAt: d.meta.CreatedAt, RulesHash: d.meta.RulesHash,
			Provenance: d.meta.Provenance, Attestation: d.meta.Attestation,
			// Read from the attestation rather than assumed, the way the
			// Docker provider learned to: a refresh with no verifier publishes
			// too, and a golden nothing scanned must not come back saying it
			// was.
			Verified:    d.meta.Attestation != "",
			ProviderRef: d.name,
			SizeBytes:   p.sizeOf(ctx, d.name),
		})
	}
	sortGoldensNewestFirst(out)
	return out, nil
}

// DestroyGolden removes a version, refusing one a branch still depends on.
func (p *Provider) DestroyGolden(ctx context.Context, version string) error {
	list, err := p.ours(ctx)
	if err != nil {
		return err
	}
	name := ""
	for _, d := range list {
		if d.meta.Kind == "branch" && d.meta.From == version {
			// Refused rather than dropped. The branch's parts are hardlinks
			// to the golden's, so dropping the golden is survivable on one
			// filesystem and is not something to rely on across every storage
			// policy a server can have.
			return aferrors.Coded(aferrors.AFDB005, "version", version, "count", "1")
		}
		if d.meta.Kind == "golden" && d.meta.Version == version {
			name = d.name
		}
	}
	if name == "" {
		// Removing one that is already gone succeeds, which is the same
		// contract Destroy has and for the same reason: teardown retries.
		return nil
	}
	return p.dropDatabase(ctx, name)
}

// Branch creates this environment's copy of a golden.
//
// A fresh database with the golden's tables attached to it. ATTACH PARTITION
// FROM is what makes that cheap: the parts of a MergeTree are immutable, so
// the server hardlinks them into the new table when both sit on one disk, and
// anything the environment writes afterwards is written into new parts of its
// own. Two branches of one golden therefore share storage and cannot see each
// other's writes.
func (p *Provider) Branch(ctx context.Context, version, envID string) (provider.Branch, error) {
	if envID == "" {
		return provider.Branch{}, fmt.Errorf("datastore.clickhouse: a branch needs an environment")
	}
	list, err := p.ours(ctx)
	if err != nil {
		return provider.Branch{}, err
	}
	name := branchPrefix + shortHash(envID)
	var golden database
	for _, d := range list {
		switch {
		case d.name == name:
			// Idempotency first. A retry after a timeout must find the branch
			// this call may already have created, or the first attempt's
			// database becomes an orphan nobody has an identifier for.
			return provider.Branch{
				EnvID: envID, From: d.meta.From, ProviderRef: d.name,
				CreatedAt: d.meta.CreatedAt,
			}, nil
		case d.meta.Kind == "golden" && d.meta.Version == version:
			golden = d
		}
	}
	if golden.name == "" {
		return provider.Branch{}, aferrors.Coded(aferrors.AFDB004, "version", version)
	}
	if golden.meta.Attestation == "" {
		// The refusal the whole product rests on, enforced in the provider
		// rather than in a checklist: an unverified golden is one nothing may
		// branch, because that branch would hold production's own data and
		// nothing scanned it.
		return provider.Branch{}, aferrors.Coded(aferrors.AFMSK001, "version", version)
	}

	created := p.clock.Now().UTC()
	if err := p.createDatabase(ctx, name, meta{
		Kind: "branch", CreatedAt: created, EnvID: envID, From: version,
		Tables: golden.meta.Tables, RulesHash: golden.meta.RulesHash,
	}); err != nil {
		return provider.Branch{}, err
	}
	for _, t := range golden.meta.Tables {
		create := fmt.Sprintf("CREATE TABLE %s.%s AS %s.%s",
			quoteIdent(name), quoteIdent(t), quoteIdent(golden.name), quoteIdent(t))
		attach := fmt.Sprintf("ALTER TABLE %s.%s ATTACH PARTITION ALL FROM %s.%s",
			quoteIdent(name), quoteIdent(t), quoteIdent(golden.name), quoteIdent(t))
		for _, sql := range []string{create, attach} {
			if err := p.server.exec(ctx, sql, nil); err != nil {
				// A half attached branch is removed rather than handed back.
				// An environment holding four of a golden's five tables is
				// one whose queries fail in a way that reads as an
				// application bug.
				_ = p.dropDatabase(context.WithoutCancel(ctx), name)
				return provider.Branch{}, err
			}
		}
	}
	return provider.Branch{
		EnvID: envID, From: version, ProviderRef: name, CreatedAt: created,
	}, nil
}

// Destroy removes a branch. Removing one that is already gone succeeds.
//
// Both the recorded reference and the name derived from the environment are
// removed, because a caller holding a branch from before something replaced it
// has a reference to a database that is gone while the replacement is still
// there. Teardown by the stale reference alone would report success over a
// database that is still holding data.
func (p *Provider) Destroy(ctx context.Context, b provider.Branch) error {
	names := map[string]bool{}
	if b.ProviderRef != "" {
		names[b.ProviderRef] = true
	}
	if b.EnvID != "" {
		names[branchPrefix+shortHash(b.EnvID)] = true
	}
	if len(names) == 0 {
		return fmt.Errorf(
			"datastore.clickhouse: the branch has neither an identifier nor an environment")
	}
	for name := range names {
		if !strings.HasPrefix(name, branchPrefix) {
			// Never a golden and never something a person made. A Destroy
			// given a golden's reference would take the version and every
			// branch of it with one statement.
			return fmt.Errorf(
				"datastore.clickhouse: %q is not a branch of this provider and will not be "+
					"dropped", name)
		}
		if err := p.dropDatabase(ctx, name); err != nil {
			return err
		}
	}
	return nil
}

// ConnString returns how to reach a branch.
func (p *Provider) ConnString(ctx context.Context, b provider.Branch) (secrets.Value, error) {
	name, err := p.branchName(ctx, b)
	if err != nil {
		return secrets.Value{}, err
	}
	return p.server.urlFor(name), nil
}

// branchName resolves a branch to the database that holds it.
func (p *Provider) branchName(ctx context.Context, b provider.Branch) (string, error) {
	list, err := p.ours(ctx)
	if err != nil {
		return "", err
	}
	want := map[string]bool{}
	if b.EnvID != "" {
		want[branchPrefix+shortHash(b.EnvID)] = true
	}
	if b.ProviderRef != "" {
		want[b.ProviderRef] = true
	}
	for _, d := range list {
		if d.meta.Kind == "branch" && want[d.name] {
			return d.name, nil
		}
	}
	if b.EnvID != "" {
		return "", aferrors.Coded(aferrors.AFDB014, "env", b.EnvID)
	}
	return "", aferrors.Coded(aferrors.AFDB004, "version", b.From)
}

// Inventory lists everything this provider holds.
//
// Databases with this provider's marker in their comment and nothing else,
// which is what keeps a leak report from proposing to drop a developer's own
// database on a server they also use for something real.
//
// A branch also carries what it HOLDS, and that is what the fidelity report
// reads. The alternative was a method of its own on the datastore interface,
// and this is the smaller change for the same answer: the engine already asks
// every provider for an inventory, the primary database already reports which
// golden a branch came from through a label of exactly this kind, and a
// provider that cannot say gets a report that says so rather than one that
// guesses.
func (p *Provider) Inventory(ctx context.Context) ([]provider.Resource, error) {
	list, err := p.ours(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]provider.Resource, 0, len(list))
	for _, d := range list {
		labels := map[string]string{"engine": engineName}
		if d.meta.Version != "" {
			labels["version"] = d.meta.Version
		}
		if d.meta.From != "" {
			labels["golden"] = d.meta.From
		}
		if d.meta.Kind == "branch" {
			if tables, rows, ok := p.contentsOf(ctx, d.name); ok {
				labels["tables"] = strconv.Itoa(tables)
				labels["rows"] = strconv.FormatInt(rows, 10)
			}
		}
		out = append(out, provider.Resource{
			Kind:      "database/" + d.meta.Kind,
			ID:        d.name,
			EnvID:     d.meta.EnvID,
			CreatedAt: d.meta.CreatedAt,
			Labels:    labels,
		})
	}
	return out, nil
}

// Health reports whether a branch is reachable.
//
// A branch that is gone is unreachable rather than an error, because teardown
// asks for health and an error here turns a completed teardown into a stuck
// one.
func (p *Provider) Health(ctx context.Context, b provider.Branch) (provider.Health, error) {
	start := p.clock.Now()
	name, err := p.branchName(ctx, b)
	if err != nil {
		return provider.Health{Reachable: false, Detail: "the branch database does not exist"}, nil
	}
	ping, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := p.server.in(name).value(ping, "SELECT toString(1)", nil); err != nil {
		return provider.Health{Reachable: false, Detail: err.Error()}, nil
	}
	return provider.Health{
		Reachable: true, Detail: "answering queries", Latency: p.clock.Since(start),
	}, nil
}

// createDatabase makes one of this provider's databases, with its metadata in
// the comment.
//
// Atomic named rather than left to the server's default, because RENAME TABLE
// between databases and EXCHANGE TABLES both need it, and a server configured
// with Ordinary as its default would fail at the end of a refresh rather than
// at the start of one.
func (p *Provider) createDatabase(ctx context.Context, name string, m meta) error {
	return p.server.exec(ctx, fmt.Sprintf("CREATE DATABASE %s ENGINE = Atomic COMMENT %s",
		quoteIdent(name), quoteString(m.encode())), nil)
}

func (p *Provider) dropDatabase(ctx context.Context, name string) error {
	if err := p.server.exec(ctx, "DROP DATABASE IF EXISTS "+quoteIdent(name), nil); err != nil {
		return fmt.Errorf("datastore.clickhouse: dropping %s: %w", name, err)
	}
	return nil
}

// contentsOf reports the tables a branch holds and the rows in them, and false
// when the server would not say.
//
// Read from the server rather than from the metadata the branch was created
// with. That metadata records the tables the golden had at the moment they
// were attached, and a branch is a database an environment then writes to, so
// a report built from it would keep printing the golden's numbers over a store
// whose contents had moved. That is the same defect one level down as the one
// that made a full store read as absent.
//
// Both numbers or neither, because a table count with no row count renders as
// a store that came up empty and the reader cannot tell that from a store that
// really did. Answering false leaves the report saying the provider does not
// record it, which is a sentence somebody can act on.
//
// total_rows is what the server keeps for a MergeTree, exact from its parts,
// so nothing here walks the rows. A table whose engine keeps none, a view or a
// Dictionary, is not counted as a table either: a branch holds the golden's
// MergeTree tables, and a view is a statement rather than data.
func (p *Provider) contentsOf(ctx context.Context, name string) (tables int, rows int64, ok bool) {
	var found bool
	err := p.server.rows(ctx,
		"SELECT toString(count()), toString(ifNull(sum(total_rows), 0)) FROM system.tables "+
			"WHERE database = {db:String} AND total_rows IS NOT NULL",
		map[string]string{"db": name}, func(r [][]byte) error {
			if len(r) != 2 {
				return nil
			}
			t, convErr := strconv.Atoi(strings.TrimSpace(string(r[0])))
			if convErr != nil {
				return convErr
			}
			n, convErr := strconv.ParseInt(strings.TrimSpace(string(r[1])), 10, 64)
			if convErr != nil {
				return convErr
			}
			tables, rows, found = t, n, true
			return nil
		})
	if err != nil || !found {
		return 0, 0, false
	}
	return tables, rows, true
}

// sizeOf reports the bytes a database occupies, and zero when it cannot be
// read.
//
// Best effort on purpose: it is reported to a person and nothing decides
// anything on it, so a server that will not answer system.parts must not turn
// a successful refresh into a failed one.
func (p *Provider) sizeOf(ctx context.Context, name string) int64 {
	v, err := p.server.value(ctx,
		"SELECT toString(sum(bytes_on_disk)) FROM system.parts WHERE active AND database = "+
			"{db:String}", map[string]string{"db": name})
	if err != nil {
		return 0
	}
	var n int64
	if _, err := fmt.Sscan(strings.TrimSpace(v), &n); err != nil {
		return 0
	}
	return n
}

// sortGoldensNewestFirst orders versions the way every caller reads them.
//
// By identifier, which sorts by age because a version identifier begins with
// its timestamp, and that is the same rule the Docker provider uses.
func sortGoldensNewestFirst(v []provider.GoldenVersion) {
	sort.Slice(v, func(i, j int) bool { return v[i].ID > v[j].ID })
}
