package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antifailure/antifailure/engine/internal/clock"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// engineName is what a manifest datastore declares to reach this, and what the
// masking and verification dialects are registered under.
const engineName = "mysql"

// The databases this provider makes on a server.
const (
	goldenPrefix    = "af_golden_"
	candidatePrefix = "af_cand_"
	branchPrefix    = "af_env_"
	// catalogDatabase records which databases this provider made, with their
	// metadata.
	//
	// ClickHouse keeps that in each database's comment, and MySQL has no
	// comment on a database. A table inside each database would do it and
	// would be a table the application sees in its own schema, which is a
	// branch that is not production's shape. So the record is kept beside the
	// databases, and the rule that makes a separate record safe is that a
	// database is this provider's only when its row AND the database both
	// exist: the row is written before the database is created and removed
	// after it is dropped, so a crash between the two leaves a row with
	// nothing behind it, which lists as nothing, rather than a database
	// nothing knows it made.
	catalogDatabase = "af_catalog"
	// placeholderSource is the scheme the conformance suite's stand in source
	// uses. It names nothing, so a refresh given it copies nothing.
	placeholderSource = "antifailure://"
)

// Provider implements provider.Datastore.
var _ provider.Datastore = (*Provider)(nil)

// Provider is the MySQL datastore.
//
// One server holds everything: goldens are databases on it and branches are
// databases on it. A branch is a copy of its golden's tables made by the
// server, INSERT ... SELECT, because MySQL has nothing that shares storage
// between two tables the way a ClickHouse part can be linked into two.
// So a branch costs time and disk in proportion to its golden, and the
// capability says so.
type Provider struct {
	name     string
	server   *server
	db       *sql.DB
	copyPool *sql.DB
	clock    clock.Clock
	progress func(string)

	// catalogMu and catalogReady remember that the record of what this
	// provider owns exists, so the statements that make it run once per
	// provider rather than on every listing. Not a sync.Once, because a
	// server that was not answering the first time must be asked again.
	catalogMu    sync.Mutex
	catalogReady bool
}

// Options configure the provider.
type Options struct {
	// ServerURL is the admin address of the MySQL that holds the goldens and
	// the branches, with no database. Required: this provider does not start a
	// server, it uses one.
	ServerURL secrets.Value
	// Name is what this store is called in the environment and in the
	// fidelity report. Empty uses the engine name.
	Name string
	// Clock is the time source.
	Clock clock.Clock
	// Progress receives a line per step, and may be nil.
	Progress func(string)
}

// New returns a provider against one server.
func New(opts Options) (*Provider, error) {
	s, err := parseURL(opts.ServerURL)
	if err != nil {
		return nil, err
	}
	s = s.in("")
	if host, _, _ := net.SplitHostPort(s.addr); host == LocalServerName && !s.tlsStated {
		// The managed server, reached by its container name. It has no
		// certificate, and this is the provider's own connection to the
		// server it runs, never an address somebody configured as a source,
		// so the name is trusted here and nowhere else.
		s.tls = "preferred"
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
	db, err := s.open(sessionPlain)
	if err != nil {
		return nil, err
	}
	copyPool, err := s.open(sessionCopy)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Provider{
		name: name, server: s, db: db, copyPool: copyPool,
		clock: opts.Clock, progress: progress,
	}, nil
}

// Name identifies the datastore in output and in errors.
func (p *Provider) Name() string { return p.name }

// Capabilities declares what this datastore can do.
func (p *Provider) Capabilities() provider.DatastoreCaps {
	return provider.DatastoreCaps{
		Engine:    engineName,
		Branching: true,
		Golden:    true,
		// A branch is a full copy of its golden. See the type.
		CopyOnWrite: false,
	}
}

// Close releases the provider's connections.
func (p *Provider) Close() error {
	return errors.Join(p.db.Close(), p.copyPool.Close())
}

// meta is what this provider records about a database it made.
type meta struct {
	Kind        string    `json:"kind"`
	Version     string    `json:"version,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	RulesHash   string    `json:"rules_hash,omitempty"`
	Provenance  string    `json:"provenance,omitempty"`
	Attestation string    `json:"attestation,omitempty"`
	// Tables are the tables the database holds, which a branch reads to know
	// what to copy.
	Tables []string `json:"tables,omitempty"`
	// Skipped are the source objects the golden does NOT hold, with the reason.
	Skipped []skipped `json:"skipped,omitempty"`
	// Source is the database the golden was copied from, which is the schema
	// name masking knew it by.
	Source string `json:"source,omitempty"`
	// Charset and Collation are the database's defaults, carried from the
	// source so that a table the application creates in a branch gets what it
	// would get in production.
	Charset   string `json:"charset,omitempty"`
	Collation string `json:"collation,omitempty"`
	// EnvID and From describe a branch.
	EnvID string `json:"env_id,omitempty"`
	From  string `json:"from,omitempty"`
}

// database is one database this provider owns.
type database struct {
	name string
	meta meta
}

// ensureCatalog makes the record of what this provider owns, once per server.
func (p *Provider) ensureCatalog(ctx context.Context) error {
	p.catalogMu.Lock()
	defer p.catalogMu.Unlock()
	if p.catalogReady {
		return nil
	}
	for _, stmt := range []string{
		"CREATE DATABASE IF NOT EXISTS " + quoteIdent(catalogDatabase) +
			" CHARACTER SET utf8mb4 COLLATE utf8mb4_bin",
		"CREATE TABLE IF NOT EXISTS " + quoteIdent(catalogDatabase) + ".`owned` (" +
			"`name` VARCHAR(64) NOT NULL PRIMARY KEY, `meta` LONGTEXT NOT NULL) " +
			"ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin",
	} {
		if _, err := p.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("datastore.mysql: preparing the record of what this provider "+
				"owns: %w", err)
		}
	}
	p.catalogReady = true
	return nil
}

// ours lists the databases this provider made, with their metadata.
//
// The two lists are read separately and joined here rather than in SQL,
// because information_schema's names and the record's are compared under two
// different collations, and MySQL refuses to compare those rather than choose
// one.
func (p *Provider) ours(ctx context.Context) ([]database, error) {
	if err := p.ensureCatalog(ctx); err != nil {
		return nil, err
	}
	exists := map[string]bool{}
	err := each(ctx, p.db, "SELECT SCHEMA_NAME FROM information_schema.SCHEMATA", nil,
		func(r *sql.Rows) error {
			var name string
			if err := r.Scan(&name); err != nil {
				return err
			}
			exists[name] = true
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("datastore.mysql: listing databases: %w", err)
	}
	var out []database
	err = each(ctx, p.db,
		"SELECT `name`, `meta` FROM "+quoteIdent(catalogDatabase)+".`owned` ORDER BY `name`",
		nil, func(r *sql.Rows) error {
			var name, body string
			if err := r.Scan(&name, &body); err != nil {
				return err
			}
			var m meta
			if json.Unmarshal([]byte(body), &m) != nil || !exists[name] {
				return nil
			}
			out = append(out, database{name: name, meta: m})
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("datastore.mysql: reading what this provider owns: %w", err)
	}
	return out, nil
}

// createDatabase records a database and then creates it. See catalogDatabase
// for why in that order.
func (p *Provider) createDatabase(ctx context.Context, name string, m meta) error {
	if err := p.ensureCatalog(ctx); err != nil {
		return err
	}
	body, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("datastore.mysql: recording %s: %w", name, err)
	}
	if _, err := p.db.ExecContext(ctx,
		"REPLACE INTO "+quoteIdent(catalogDatabase)+".`owned` (`name`, `meta`) VALUES (?, ?)",
		name, string(body)); err != nil {
		return fmt.Errorf("datastore.mysql: recording %s: %w", name, err)
	}
	create := "CREATE DATABASE " + quoteIdent(name)
	if m.Charset != "" {
		create += " CHARACTER SET " + m.Charset
	}
	if m.Collation != "" {
		create += " COLLATE " + m.Collation
	}
	if _, err := p.db.ExecContext(ctx, create); err != nil {
		_, _ = p.db.ExecContext(context.WithoutCancel(ctx),
			"DELETE FROM "+quoteIdent(catalogDatabase)+".`owned` WHERE `name` = ?", name)
		return fmt.Errorf("datastore.mysql: creating %s: %w", name, err)
	}
	return nil
}

// dropDatabase drops a database and then forgets it.
func (p *Provider) dropDatabase(ctx context.Context, name string) error {
	if _, err := p.db.ExecContext(ctx, "DROP DATABASE IF EXISTS "+quoteIdent(name)); err != nil {
		return fmt.Errorf("datastore.mysql: dropping %s: %w", name, err)
	}
	if _, err := p.db.ExecContext(ctx,
		"DELETE FROM "+quoteIdent(catalogDatabase)+".`owned` WHERE `name` = ?", name); err != nil {
		return fmt.Errorf("datastore.mysql: forgetting %s: %w", name, err)
	}
	return nil
}

// RefreshGolden builds a new masked, verified copy.
//
// The order is the ClickHouse provider's and it is the guarantee: an empty
// candidate, the source copied into it, masked, verified, and only then
// published. Publishing is the tables moving into a database recorded with the
// attestation, so a version that exists is one that was verified.
func (p *Provider) RefreshGolden(ctx context.Context, spec provider.GoldenSpec) (provider.GoldenVersion, error) {
	p.sweepCandidates(ctx)

	source, err := p.openSource(ctx, spec.SourceURL)
	if err != nil {
		return provider.GoldenVersion{}, err
	}
	charset, collation := "", ""
	if source != nil {
		defer func() { _ = source.db.Close() }()
		if charset, collation, err = defaultsOf(ctx, source.db, source.server.database); err != nil {
			return provider.GoldenVersion{}, err
		}
	}

	candidate := candidatePrefix + shortHash(fmt.Sprint(p.clock.Now().UnixNano()))
	if err := p.createDatabase(ctx, candidate, meta{
		Kind: "candidate", CreatedAt: p.clock.Now().UTC(), Charset: charset, Collation: collation,
	}); err != nil {
		return provider.GoldenVersion{}, err
	}
	// Removed whatever happens. A failed refresh that leaves a database full of
	// production's own data on this server is worse than any other failure here.
	defer func() { _ = p.dropDatabase(context.WithoutCancel(ctx), candidate) }()

	skips, err := p.loadSource(ctx, source, candidate)
	if err != nil {
		return provider.GoldenVersion{}, err
	}
	candURL := p.server.in(candidate).url()
	if spec.Mask != nil {
		if err := spec.Mask(ctx, candURL); err != nil {
			return provider.GoldenVersion{}, fmt.Errorf(
				"datastore.mysql: mask the golden candidate: %w", err)
		}
	}
	attestation := ""
	if spec.Verify != nil {
		if attestation, err = spec.Verify(ctx, candURL); err != nil {
			return provider.GoldenVersion{}, fmt.Errorf(
				"datastore.mysql: verify the golden candidate: %w", err)
		}
	}

	tables, _, err := readCatalog(ctx, p.db, candidate)
	if err != nil {
		return provider.GoldenVersion{}, err
	}
	names := make([]string, 0, len(tables))
	for _, t := range tables {
		names = append(names, t.name)
	}

	version := provider.NewGoldenVersionID(p.clock.Now(), spec.RulesHash)
	golden := goldenPrefix + shortHash(version)
	sourceName := ""
	if source != nil {
		sourceName = source.server.database
	}
	if err := p.createDatabase(ctx, golden, meta{
		Kind: "golden", Version: version, CreatedAt: p.clock.Now().UTC(),
		RulesHash: spec.RulesHash, Provenance: spec.Provenance, Attestation: attestation,
		Tables: names, Skipped: skips, Source: sourceName,
		Charset: charset, Collation: collation,
	}); err != nil {
		return provider.GoldenVersion{}, err
	}
	if len(names) > 0 {
		// One statement, so the tables move together or not at all, and a
		// foreign key between two of them is carried with both.
		pairs := make([]string, len(names))
		for i, n := range names {
			pairs[i] = fmt.Sprintf("%s.%s TO %s.%s",
				quoteIdent(candidate), quoteIdent(n), quoteIdent(golden), quoteIdent(n))
		}
		if _, err := p.copyPool.ExecContext(ctx, "RENAME TABLE "+strings.Join(pairs, ", ")); err != nil {
			_ = p.dropDatabase(context.WithoutCancel(ctx), golden)
			return provider.GoldenVersion{}, fmt.Errorf(
				"datastore.mysql: publishing the golden: %w", err)
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

// sourceConn is production, opened for one refresh.
type sourceConn struct {
	server *server
	db     *sql.DB
}

// openSource opens the source a refresh copies, or returns nil when there is
// none to copy.
//
// None means no address at all, which is a project that has not connected
// production, or the conformance suite's antifailure:// placeholder, which
// stands for the same thing. Both leave the golden empty, the answer the
// ClickHouse provider and the Docker Postgres one give, and the engine says so
// out loud.
//
// Anything else is an address somebody meant, and it is read or refused. This
// used to treat every address that was not mysql:// as none, so a
// mariadb://, a jdbc:mysql:// or a Go driver DSN like user:pass@tcp(host)/shop,
// each of them a real production that somebody configured, produced an empty
// golden that verified clean and published looking exactly like a full one.
// A mysql:// address that names no database is refused for the same reason.
func (p *Provider) openSource(ctx context.Context, v secrets.Value) (*sourceConn, error) {
	raw := strings.TrimSpace(v.Reveal())
	if raw == "" || strings.HasPrefix(raw, placeholderSource) {
		return nil, nil
	}
	if !strings.HasPrefix(raw, "mysql://") {
		// The value is a credential, so it is not quoted back.
		return nil, fmt.Errorf(
			"datastore.mysql: the source address is not a mysql:// URL, so it cannot be read and " +
				"the golden would hold nothing; a mariadb:// URL, a jdbc: URL or a driver DSN is " +
				"not accepted. Write it as mysql://user:password@host:port/database")
	}
	s, err := parseURL(v)
	if err != nil {
		return nil, err
	}
	if s.database == "" {
		return nil, fmt.Errorf(
			"datastore.mysql: the source address names no database, so there is nothing to copy; " +
				"end it with /<database>")
	}
	db, err := s.open(sessionCopy)
	if err != nil {
		return nil, err
	}
	// Reached now rather than at the first read, so a source that cannot give
	// the TLS its address requires is refused as that, by name, before a
	// candidate exists.
	if err := ping(ctx, db); err != nil {
		_ = db.Close()
		return nil, s.refuseUnverified(fmt.Errorf("datastore.mysql: reaching the source: %w", err))
	}
	return &sourceConn{server: s, db: db}, nil
}

// defaultsOf reads a database's default character set and collation.
func defaultsOf(ctx context.Context, q querier, name string) (charset, collation string, err error) {
	found := false
	err = each(ctx, q, "SELECT DEFAULT_CHARACTER_SET_NAME, DEFAULT_COLLATION_NAME "+
		"FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = ?", []any{name},
		func(r *sql.Rows) error {
			found = true
			return r.Scan(&charset, &collation)
		})
	if err != nil {
		return "", "", fmt.Errorf("datastore.mysql: reading the source database: %w", err)
	}
	if !found {
		return "", "", fmt.Errorf(
			"datastore.mysql: the source server has no database called %s, or this user cannot "+
				"see it", name)
	}
	return charset, collation, nil
}

// loadSource copies the source database into the candidate.
//
// Every table is read inside one read only REPEATABLE READ transaction, which
// is what mysqldump --single-transaction does: InnoDB gives the transaction one
// snapshot from its first read, so a golden is production at one instant
// rather than each table at the moment it happened to be copied, and an order
// written between two tables cannot arrive without its customer.
func (p *Provider) loadSource(ctx context.Context, src *sourceConn, candidate string) ([]skipped, error) {
	if src == nil {
		return nil, nil
	}
	tx, err := src.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("datastore.mysql: opening a snapshot of the source: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	tables, skips, err := readCatalog(ctx, tx, src.server.database)
	if err != nil {
		return nil, err
	}
	ddl, err := p.server.in(candidate).open(sessionDDL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = ddl.Close() }()
	rows, err := p.server.in(candidate).open(sessionCopy)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	for _, t := range tables {
		create, err := showCreate(ctx, tx, t.name)
		if err != nil {
			return skips, err
		}
		if _, err := ddl.ExecContext(ctx, create); err != nil {
			return skips, fmt.Errorf("datastore.mysql: creating %s in the golden candidate: %w",
				t.name, err)
		}
		p.progress(fmt.Sprintf("copying %s (about %d rows) into the golden candidate", t.name, t.rows))
		if _, err := copyRows(ctx, tx, rows, t); err != nil {
			return skips, err
		}
	}
	for _, s := range skips {
		p.progress(fmt.Sprintf("the golden does not hold %s: %s", s.Name, s.Reason))
	}
	return skips, nil
}

// sweepCandidates removes candidates old enough that they can only be orphans.
func (p *Provider) sweepCandidates(ctx context.Context) {
	list, err := p.ours(ctx)
	if err != nil {
		return // opportunistic; a refresh must not fail because of it
	}
	cutoff := p.clock.Now().Add(-15 * time.Minute)
	for _, d := range list {
		if d.meta.Kind == "candidate" && d.meta.CreatedAt.Before(cutoff) {
			_ = p.dropDatabase(ctx, d.name)
		}
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
			// Read from the attestation rather than assumed: a refresh with no
			// verifier publishes too, and a golden nothing scanned must not
			// come back saying it was.
			Verified:    d.meta.Attestation != "",
			ProviderRef: d.name,
			SizeBytes:   p.sizeOf(ctx, d.name),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// DestroyGolden removes a version, refusing one a branch still records as its
// source. A branch here is a copy and would survive, but a golden is what a
// teardown and a fidelity report read a branch's origin from.
func (p *Provider) DestroyGolden(ctx context.Context, version string) error {
	list, err := p.ours(ctx)
	if err != nil {
		return err
	}
	name := ""
	for _, d := range list {
		if d.meta.Kind == "branch" && d.meta.From == version {
			return aferrors.Coded(aferrors.AFDB005, "version", version, "count", "1")
		}
		if d.meta.Kind == "golden" && d.meta.Version == version {
			name = d.name
		}
	}
	if name == "" {
		return nil
	}
	return p.dropDatabase(ctx, name)
}

// Branch creates this environment's copy of a golden: a new database, each
// table created from the golden's own definition and filled from it by the
// server.
func (p *Provider) Branch(ctx context.Context, version, envID string) (provider.Branch, error) {
	if envID == "" {
		return provider.Branch{}, fmt.Errorf("datastore.mysql: a branch needs an environment")
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
			// Idempotency first: a retry after a timeout finds the branch the
			// first attempt made.
			return provider.Branch{
				EnvID: envID, From: d.meta.From, ProviderRef: d.name, CreatedAt: d.meta.CreatedAt,
			}, nil
		case d.meta.Kind == "golden" && d.meta.Version == version:
			golden = d
		}
	}
	if golden.name == "" {
		return provider.Branch{}, aferrors.Coded(aferrors.AFDB004, "version", version)
	}
	if golden.meta.Attestation == "" {
		// An unverified golden is one nothing may branch, because that branch
		// would hold production's own data and nothing scanned it.
		return provider.Branch{}, aferrors.Coded(aferrors.AFMSK001, "version", version)
	}

	created := p.clock.Now().UTC()
	if err := p.createDatabase(ctx, name, meta{
		Kind: "branch", CreatedAt: created, EnvID: envID, From: version,
		Tables: golden.meta.Tables, RulesHash: golden.meta.RulesHash, Source: golden.meta.Source,
		Charset: golden.meta.Charset, Collation: golden.meta.Collation,
	}); err != nil {
		return provider.Branch{}, err
	}
	if err := p.fillBranch(ctx, golden, name); err != nil {
		// A half filled branch is removed rather than handed back. An
		// environment holding four of five tables fails in a way that reads as
		// an application bug.
		_ = p.dropDatabase(context.WithoutCancel(ctx), name)
		return provider.Branch{}, err
	}
	return provider.Branch{EnvID: envID, From: version, ProviderRef: name, CreatedAt: created}, nil
}

func (p *Provider) fillBranch(ctx context.Context, golden database, branch string) error {
	from, err := p.server.in(golden.name).open(sessionPlain)
	if err != nil {
		return err
	}
	defer func() { _ = from.Close() }()
	to, err := p.server.in(branch).open(sessionDDL)
	if err != nil {
		return err
	}
	defer func() { _ = to.Close() }()
	tables, _, err := readCatalog(ctx, from, golden.name)
	if err != nil {
		return err
	}
	for _, t := range tables {
		create, err := showCreate(ctx, from, t.name)
		if err != nil {
			return err
		}
		if _, err := to.ExecContext(ctx, create); err != nil {
			return fmt.Errorf("datastore.mysql: creating %s in the branch: %w", t.name, err)
		}
		if err := copyWithin(ctx, p.copyPool, golden.name, branch, t); err != nil {
			return err
		}
	}
	return nil
}

// Destroy removes a branch. Removing one that is already gone succeeds.
//
// Both the recorded reference and the name derived from the environment are
// removed, for the reason the ClickHouse provider gives: a stale reference
// alone would report success over a replacement that still holds data.
func (p *Provider) Destroy(ctx context.Context, b provider.Branch) error {
	names := map[string]bool{}
	if b.ProviderRef != "" {
		names[b.ProviderRef] = true
	}
	if b.EnvID != "" {
		names[branchPrefix+shortHash(b.EnvID)] = true
	}
	if len(names) == 0 {
		return fmt.Errorf("datastore.mysql: the branch has neither an identifier nor an environment")
	}
	for name := range names {
		if !strings.HasPrefix(name, branchPrefix) {
			// Never a golden and never something a person made.
			return fmt.Errorf(
				"datastore.mysql: %q is not a branch of this provider and will not be dropped", name)
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
	return p.server.in(name).url(), nil
}

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

// Inventory lists everything this provider holds. A branch also carries what
// it holds, which is what the fidelity report reads.
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
			Kind: "database/" + d.meta.Kind, ID: d.name, EnvID: d.meta.EnvID,
			CreatedAt: d.meta.CreatedAt, Labels: labels,
		})
	}
	return out, nil
}

// contentsOf counts a branch's tables and rows, exactly, or reports that it
// could not.
//
// Counted rather than read from information_schema, whose TABLE_ROWS is an
// estimate for InnoDB that can be off by half, and the fidelity report prints
// this as what the store holds. Bounded, so a large branch costs a report its
// row count rather than its patience: both numbers or neither, because a table
// count with no rows reads as a store that came up empty.
func (p *Provider) contentsOf(ctx context.Context, name string) (tables int, rows int64, ok bool) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	read, _, err := readCatalog(ctx, p.db, name)
	if err != nil {
		return 0, 0, false
	}
	for _, t := range read {
		var n int64
		q := fmt.Sprintf("SELECT COUNT(*) FROM %s.%s", quoteIdent(name), quoteIdent(t.name))
		if err := p.db.QueryRowContext(ctx, q).Scan(&n); err != nil {
			return 0, 0, false
		}
		rows += n
	}
	return len(read), rows, true
}

// Health reports whether a branch answers. A branch that is gone is
// unreachable rather than an error, because teardown asks.
func (p *Provider) Health(ctx context.Context, b provider.Branch) (provider.Health, error) {
	start := p.clock.Now()
	name, err := p.branchName(ctx, b)
	if err != nil {
		return provider.Health{Reachable: false, Detail: "the branch database does not exist"}, nil
	}
	db, err := p.server.in(name).open(sessionPlain)
	if err != nil {
		return provider.Health{Reachable: false, Detail: err.Error()}, nil
	}
	defer func() { _ = db.Close() }()
	probe, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := ping(probe, db); err != nil {
		return provider.Health{Reachable: false, Detail: err.Error()}, nil
	}
	return provider.Health{Reachable: true, Detail: "answering queries", Latency: p.clock.Since(start)}, nil
}

// sizeOf reports the bytes a database's tables and indexes occupy, and zero
// when it cannot be read. Reported to a person, so best effort.
func (p *Provider) sizeOf(ctx context.Context, name string) int64 {
	var n sql.NullInt64
	err := p.db.QueryRowContext(ctx,
		"SELECT SUM(DATA_LENGTH + INDEX_LENGTH) FROM information_schema.TABLES WHERE TABLE_SCHEMA = ?",
		name).Scan(&n)
	if err != nil || !n.Valid {
		return 0
	}
	return n.Int64
}
