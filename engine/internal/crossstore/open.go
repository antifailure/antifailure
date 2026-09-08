package crossstore

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/antifailure/antifailure/engine/internal/datastore/clickhouse"
	"github.com/antifailure/antifailure/engine/internal/masking"
)

// Opening a store to read its schema, engine by engine.
//
// Neither case here reads a schema itself, and that is the point. The Postgres
// catalog reader is masking.ReadCatalog and the ClickHouse one is
// clickhouse.Catalog, both of which already ship and both of which the
// refresh path uses. A second reader written here would be a second opinion
// about what a column's type means and whether it can be written to, kept in
// step with the first by nobody, and the failure mode of a stale one is a
// check comparing a plan that will never run.
//
// An engine with no case here is REFUSED BY NAME rather than attempted as
// Postgres. A wrong guess would connect, fail on a statement, and report a
// broken store where the real answer is that this build cannot read that kind
// of store yet, and the two send somebody to completely different places.

// Open opens a store for reading its catalog.
func Open(ctx context.Context, s Store) (Catalog, error) {
	if s.URL.IsZero() {
		return nil, fmt.Errorf(
			"the variable %s is unset or empty, so there is no connection string for %s",
			orUnnamedVar(s.Var), s.Name)
	}
	switch strings.ToLower(strings.TrimSpace(s.Engine)) {
	case "postgres", "postgresql", "":
		return openPostgres(ctx, s)
	case "clickhouse":
		return &clickHouseCatalog{store: s}, nil
	default:
		return nil, fmt.Errorf(
			"this build reads the schema of a postgres and of a clickhouse, and %s runs %s; "+
				"the cross store check compares the stores it can read and says which it could not",
			s.Name, s.Engine)
	}
}

func orUnnamedVar(name string) string {
	if name == "" {
		return "that store's source_url_env"
	}
	return name
}

// postgresCatalog reads a Postgres through the connection the rest of the
// engine uses.
type postgresCatalog struct{ conn *pgx.Conn }

func openPostgres(ctx context.Context, s Store) (Catalog, error) {
	conn, err := pgx.Connect(ctx, s.URL.Reveal())
	if err != nil {
		// The variable's NAME and not the value. A connection string is a
		// credential, and what the reader needs is which setting to look at.
		return nil, fmt.Errorf("connecting with %s: %w", orUnnamedVar(s.Var), redactedConnectError(err))
	}
	return &postgresCatalog{conn: conn}, nil
}

func (p *postgresCatalog) Tables(ctx context.Context) ([]masking.Table, error) {
	return masking.ReadCatalog(ctx, p.conn)
}

func (p *postgresCatalog) Skipped() []string { return nil }

func (p *postgresCatalog) Close() error { return p.conn.Close(context.Background()) }

// clickHouseCatalog reads a ClickHouse through the provider that already
// refreshes, masks and branches one.
//
// It holds no connection of its own: clickhouse.Catalog opens one per call
// over the HTTP interface and closes it, which is why Close here has nothing
// to do and says so rather than pretending.
type clickHouseCatalog struct {
	store   Store
	skipped []string
}

func (c *clickHouseCatalog) Tables(ctx context.Context) ([]masking.Table, error) {
	tables, skipped, err := clickhouse.Catalog(ctx, c.store.URL)
	if err != nil {
		return nil, fmt.Errorf("reading the schema named by %s: %w",
			orUnnamedVar(c.store.Var), redactedConnectError(err))
	}
	c.skipped = skipped
	return tables, nil
}

// Skipped names the tables the reader deliberately did not return, so a caller
// can say what it did not look at rather than quietly looking at less.
func (c *clickHouseCatalog) Skipped() []string { return c.skipped }

func (c *clickHouseCatalog) Close() error { return nil }

// redactedConnectError keeps a connection string out of a message.
//
// A pgx dial failure quotes the URL it was given, password included, and that
// message reaches a report and a log. What a reader needs is what went wrong,
// and the variable name is already in the sentence this wraps.
func redactedConnectError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return errors.New(firstLine(urlErr.Err.Error()))
	}
	msg := err.Error()
	if strings.Contains(msg, "://") {
		return errors.New(
			"the connection failed and the message named the connection string, so it is " +
				"not repeated here; check the host, the port and the credential in the " +
				"variable above")
	}
	return errors.New(firstLine(msg))
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}
