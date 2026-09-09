// Package clickhouse is the ClickHouse datastore, which is the second store an
// environment can hold and the first one that is not the primary Postgres.
//
// It exists because a twin of an analytics product held a masked Postgres and
// zero events. The events live in ClickHouse, ClickHouse came up empty, and
// every query path that mattered ran against nothing while the run went green.
// A golden here is a masked, verified copy of the events, and a branch is an
// environment's own database with the golden's tables attached to it.
//
// HTTP rather than a ClickHouse driver, and that is a decision rather than an
// omission. The interface is documented, stable across every version this
// supports, and speaks the formats the server already writes, so a copy can be
// streamed from one server into another without decoding a single value in Go.
// A driver would add a module to the engine to gain a wire protocol nothing
// here needs, and the one thing a driver is better at, reading typed values,
// is the thing this deliberately does not do: every value it reads is rendered
// by the server as text, because a transform takes a string and returns one.
package clickhouse

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/airgap"
)

// client talks to one ClickHouse over its HTTP interface.
//
// A client is bound to a database, because almost every statement here is
// about one: the golden is a database, a branch is a database, and the scan
// that verifies a golden must never read another one. Reaching a second
// database is an explicit in(name) rather than a parameter on every call.
type client struct {
	// base is scheme://host, with no path.
	base     string
	user     string
	password string
	database string
	http     *http.Client
}

// defaultHTTPTimeout bounds a request that nothing else bounds.
//
// Long, because a golden copy of a real events table is minutes of streaming
// and a timeout that cuts one is a refresh that can never succeed on a large
// store. The context is the real bound: every call here takes one, and the
// caller's deadline is what stops a run.
const defaultHTTPTimeout = 4 * time.Hour

// parseURL reads a connection string into a client.
//
// The shape is an ordinary HTTP URL, http://user:password@host:8123/database,
// which is what AF_TEST_CLICKHOUSE_URL already takes and what the provider
// hands out for a branch. An empty path is the server's default database.
func parseURL(v secrets.Value) (*client, error) {
	if v.IsZero() {
		return nil, fmt.Errorf("clickhouse: the connection string is empty")
	}
	u, err := url.Parse(v.Reveal())
	if err != nil {
		// The URL is never printed, because it carries the password.
		return nil, fmt.Errorf("clickhouse: the connection string is not a URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf(
			"clickhouse: the connection string names the scheme %q and this speaks the "+
				"HTTP interface, so it has to be http or https", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("clickhouse: the connection string names no host")
	}
	password, _ := u.User.Password()
	db := strings.TrimPrefix(u.Path, "/")
	if db == "" {
		db = "default"
	}
	return &client{
		base:     u.Scheme + "://" + u.Host,
		user:     u.User.Username(),
		password: password,
		database: db,
		http:     airgap.Client(airgap.SiteClickHouse, defaultHTTPTimeout),
	}, nil
}

// in returns a client for another database on the same server, sharing the
// same HTTP client so that connections are pooled across both.
func (c *client) in(database string) *client {
	other := *c
	other.database = database
	return &other
}

// url renders this client's own connection string, as a secret.
func (c *client) url() secrets.Value { return c.urlFor(c.database) }

// urlFor renders the connection string for a database on this server.
func (c *client) urlFor(database string) secrets.Value {
	u := &url.URL{Path: "/" + database}
	rest := strings.TrimPrefix(c.base, "http://")
	scheme := "http"
	if strings.HasPrefix(c.base, "https://") {
		rest = strings.TrimPrefix(c.base, "https://")
		scheme = "https"
	}
	u.Scheme = scheme
	u.Host = rest
	if c.user != "" {
		u.User = url.UserPassword(c.user, c.password)
	}
	return secrets.New(u.String())
}

// post runs one statement and returns the response body reader.
//
// The statement goes in the body and the settings in the query string, which
// is what lets a statement be any size: a URL has a length nobody agrees on
// and a masking run builds statements from a schema.
func (c *client) post(
	ctx context.Context, sql string, params, settings map[string]string,
) (io.ReadCloser, error) {
	q := url.Values{}
	q.Set("database", c.database)
	for k, v := range params {
		q.Set("param_"+k, v)
	}
	for k, v := range settings {
		q.Set(k, v)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.base+"/?"+q.Encode(), strings.NewReader(sql))
	if err != nil {
		return nil, err
	}
	c.authorize(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: %s: %w", firstLine(sql), err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		_ = resp.Body.Close()
		return nil, fmt.Errorf("clickhouse: %s: the server said %d: %s",
			firstLine(sql), resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return resp.Body, nil
}

func (c *client) authorize(req *http.Request) {
	if c.user == "" && c.password == "" {
		return
	}
	req.SetBasicAuth(c.user, c.password)
}

// exec runs a statement that returns nothing.
func (c *client) exec(ctx context.Context, sql string, params map[string]string) error {
	return c.execWith(ctx, sql, params, nil)
}

// execWith runs a statement with settings.
//
// mutations_sync is set on every statement rather than on the ones that
// mutate, because an ALTER that returns before it has rewritten anything is
// indistinguishable from one that has, and a caller reading the rows back
// would be reading whichever half of the truth arrived first.
func (c *client) execWith(
	ctx context.Context, sql string, params, settings map[string]string,
) error {
	all := map[string]string{"mutations_sync": "2"}
	for k, v := range settings {
		all[k] = v
	}
	body, err := c.post(ctx, sql, params, all)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()
	_, err = io.Copy(io.Discard, body)
	return err
}

// rows runs a statement and hands each row to a function, one at a time.
//
// Streamed rather than collected, because this reads the distinct values of a
// column and a column can hold every event property an analytics product has
// ever recorded. The bytes handed to yield are valid until it returns.
func (c *client) rows(
	ctx context.Context, sql string, params map[string]string, yield func([][]byte) error,
) error {
	body, err := c.post(ctx, sql+" FORMAT RowBinaryWithNamesAndTypes", params, nil)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()
	return decodeRowBinary(bufio.NewReaderSize(body, 64<<10), yield)
}

// value runs a statement expected to return one row of one column.
func (c *client) value(ctx context.Context, sql string, params map[string]string) (string, error) {
	var out string
	seen := 0
	err := c.rows(ctx, sql, params, func(r [][]byte) error {
		if len(r) != 1 {
			return fmt.Errorf("clickhouse: %s returned %d columns and this reads one",
				firstLine(sql), len(r))
		}
		seen++
		out = string(r[0])
		return nil
	})
	if err != nil {
		return "", err
	}
	if seen != 1 {
		return "", fmt.Errorf("clickhouse: %s returned %d rows and this reads one",
			firstLine(sql), seen)
	}
	return out, nil
}

// decodeRowBinary reads RowBinaryWithNamesAndTypes.
//
// It reads String and refuses everything else, rather than guessing at a
// width: a decoder that skipped an unexpected type would silently misalign
// every column after it, and a misaligned scan is a scan that reports the
// wrong column clean.
//
// Nullable and LowCardinality are unwrapped rather than refused, and that
// second one is not cosmetic. LowCardinality(String) is what an analytics
// schema gives every event name, every property key and most of its
// identifiers, and RowBinary writes one as a plain length prefixed string, so
// the only thing standing between a scan and a column of them is whether this
// recognises the name in the header.
func decodeRowBinary(r *bufio.Reader, yield func([][]byte) error) error {
	n, err := binary.ReadUvarint(r)
	if err != nil {
		if errors.Is(err, io.EOF) {
			// A statement that returned no header at all returned nothing,
			// which is what an empty result of a DDL shaped query looks like.
			return nil
		}
		return fmt.Errorf("clickhouse: reading the column count: %w", err)
	}
	for i := uint64(0); i < n; i++ {
		if _, err := readString(r); err != nil {
			return fmt.Errorf("clickhouse: reading column name %d: %w", i, err)
		}
	}
	nullable := make([]bool, n)
	for i := uint64(0); i < n; i++ {
		typ, err := readString(r)
		if err != nil {
			return fmt.Errorf("clickhouse: reading column type %d: %w", i, err)
		}
		base, isNullable := unwrapType(string(typ))
		if base != "String" {
			return fmt.Errorf(
				"clickhouse: column %d came back as %s and this decoder reads String; a "+
					"statement that returns anything else has to render it, because "+
					"guessing at a width misaligns every column after it", i, typ)
		}
		nullable[i] = isNullable
	}

	row := make([][]byte, n)
	for {
		if _, err := r.Peek(1); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		for i := uint64(0); i < n; i++ {
			if nullable[i] {
				flag, err := r.ReadByte()
				if err != nil {
					return err
				}
				if flag == 1 {
					row[i] = nil
					continue
				}
			}
			v, err := readString(r)
			if err != nil {
				return err
			}
			row[i] = v
		}
		if err := yield(row); err != nil {
			return err
		}
	}
}

// unwrapType removes the constructors that decorate a type without changing
// what it holds, and reports whether one of them was Nullable.
func unwrapType(t string) (base string, nullable bool) {
	base = strings.TrimSpace(t)
	for {
		switch {
		case strings.HasPrefix(base, "Nullable(") && strings.HasSuffix(base, ")"):
			base = base[len("Nullable(") : len(base)-1]
			nullable = true
		case strings.HasPrefix(base, "LowCardinality(") && strings.HasSuffix(base, ")"):
			base = base[len("LowCardinality(") : len(base)-1]
		default:
			return strings.TrimSpace(base), nullable
		}
	}
}

func readString(r *bufio.Reader) ([]byte, error) {
	n, err := binary.ReadUvarint(r)
	if err != nil {
		return nil, err
	}
	v := make([]byte, n)
	if _, err := io.ReadFull(r, v); err != nil {
		return nil, err
	}
	return v, nil
}

// firstLine is what an error quotes of a statement.
//
// One line and bounded, because these statements are generated from a schema
// and one of them can name every column of a wide events table. An error
// nobody can read is one somebody pastes into a search box rather than acts on.
func firstLine(sql string) string {
	s := strings.TrimSpace(sql)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:120] + "..."
	}
	return s
}

// quoteIdent quotes an identifier in backticks, doubling any it contains.
//
// The same rule the masking dialect uses, and it is here as well because this
// package addresses databases, which a masking plan never does.
func quoteIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// quoteString renders a string literal.
//
// Used only for values this package produces itself, a database comment and a
// name it derived, never for data out of a store: everything read from a store
// travels as a parameter or as a length prefixed value.
func quoteString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`)
	return "'" + r.Replace(s) + "'"
}

// insert runs a statement whose body is data rather than the statement.
//
// The statement travels in the query string and the body carries the rows,
// which is how the HTTP interface takes an INSERT. It is also what lets a copy
// stream: the reader here can be another server's response body.
func (c *client) insert(ctx context.Context, statement string, body io.Reader) error {
	q := url.Values{}
	q.Set("database", c.database)
	q.Set("query", statement)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/?"+q.Encode(), body)
	if err != nil {
		return err
	}
	c.authorize(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("clickhouse: %s: %w", firstLine(statement), err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("clickhouse: %s: the server said %d: %s",
			firstLine(statement), resp.StatusCode, strings.TrimSpace(string(out)))
	}
	return nil
}

// shortHash is a stable short identifier for a name.
//
// Used where this package generates an identifier from something a person
// chose: a database name from an environment identifier, a scratch table name
// from a table name. Sanitising instead would map two different names onto one
// identifier, and two environments sharing a branch database is the worst
// failure this package could have.
func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:6])
}
