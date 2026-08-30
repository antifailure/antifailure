package personas

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Provisioning personas as rows, for every application that owns its users.
//
// The shape of "a user" differs per framework and the differences are all in
// the same few places: which table, which columns, how the password is hashed,
// whether there is a separate identity row, whether a second factor lives in
// its own table. So a scheme is a description rather than code, and the code
// below is written once against the description. Adding support for a
// framework is then a value in a table that somebody can review, which is the
// same choice detect/thirdparty.go made for egress and for the same reason.

// BcryptCost is the work factor personas are hashed with.
//
// Ten, from the spec, and it is a deliberate compromise rather than a default
// left alone. Cost is exponential, and a golden with fifty personas at cost 12
// spends most of a minute hashing accounts nobody attacks: these are fixtures
// in a disposable branch whose passwords are derived per environment. Ten is
// also what most frameworks write, so a hash this produces looks like a hash
// the application produces.
const BcryptCost = 10

// Conn is the part of a database connection this needs.
//
// An interface rather than *pgx.Conn so that a caller can pass a transaction,
// which is what provisioning inside a larger golden refresh wants.
type Conn interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Scheme describes where users live in one authentication framework.
type Scheme struct {
	// Name identifies the scheme, and is what the adapter reports.
	Name string
	// Users is the table an account is a row in.
	Users Table
	// Identities is the table a scheme keeps one row per login provider in,
	// which Supabase requires and a plain users table does not have.
	Identities *Table
	// Factors is the table an enrolled second factor lives in.
	Factors *Table
	// Sessions are the tables holding live sessions and refresh tokens.
	//
	// Emptied when personas are provisioned, and that is the second half of
	// this package's job rather than a tidy-up. A golden is a masked copy of
	// production, and masking a user's name does not invalidate the session
	// row that still authenticates as them. A branch published with those
	// rows intact hands anybody who can reach it a working session belonging
	// to a real customer.
	Sessions []string
	// Probe is a table whose presence proves the scheme is the one in use.
	// Detection from the live schema, which the spec asks for, because a
	// dependency list can be wrong and a table cannot.
	Probe string
}

// Table describes one table an adapter writes.
type Table struct {
	// Schema and Name locate it. Both are quoted before use, so a name from
	// a manifest cannot become SQL.
	Schema string
	Name   string
	// ID is the primary key column.
	ID string
	// IDIsUUID makes the insert fill the key with gen_random_uuid() rather
	// than leaving it to a database default. Supabase needs this:
	// auth.users.id has no default and a missing id is a not-null violation.
	IDIsUUID bool
	// Email is the column an account is matched on, which is what makes
	// provisioning reconcile rather than duplicate.
	Email string
	// Password is the column the hash goes in, empty for a table that keeps
	// no password.
	Password string
	// Phone is the column a persona's number goes in, for an application
	// whose sms sign in reads it from the account rather than asking.
	Phone string
	// Role is the column an application role goes in, when there is one.
	Role string
	// Fixed are columns set to a literal SQL expression on insert: the
	// confirmation timestamps and the metadata defaults that a framework
	// would otherwise set itself.
	Fixed map[string]string
	// Attributes maps a persona attribute name to a column. An attribute
	// with no mapping goes to JSON when the scheme has a JSON column, and is
	// reported as unsupported when it does not, rather than dropped.
	Attributes map[string]string
	// JSON is a JSONB column unmapped attributes are written into.
	JSON string
	// Timestamps are columns set to now() on insert and on update.
	Timestamps []string
}

// qualified returns the quoted table name.
func (t Table) qualified() string {
	if t.Schema == "" {
		return pgx.Identifier{t.Name}.Sanitize()
	}
	return pgx.Identifier{t.Schema, t.Name}.Sanitize()
}

// SQLAdapter provisions personas as rows in a described scheme.
type SQLAdapter struct {
	conn   Conn
	scheme Scheme
	// issuer names this deployment in an otpauth URL.
	issuer string
}

// NewSQLAdapter returns an adapter writing to a database.
func NewSQLAdapter(conn Conn, scheme Scheme, issuer string) *SQLAdapter {
	if issuer == "" {
		issuer = "Antifailure"
	}
	return &SQLAdapter{conn: conn, scheme: scheme, issuer: issuer}
}

// Name identifies the adapter.
func (a *SQLAdapter) Name() string { return a.scheme.Name }

// Provision creates the persona's row, or updates the row already holding its
// address.
//
// Reconciling on the address rather than creating unconditionally is the
// property the spec asks for, and the reason is the golden: a persona's
// address may already be there as a real user who has since been masked.
// Two rows with one email is a broken fixture that looks like a broken
// application.
func (a *SQLAdapter) Provision(
	ctx context.Context, p schema.Persona, want Credentials,
) (*Account, error) {
	account := &Account{
		Name: p.Name, Email: p.Email, Phone: p.Phone, Role: p.Role,
		Login: p.Login, Adapter: a.scheme.Name,
	}
	if account.Login == "" {
		account.Login = schema.LoginPassword
	}

	existing, found, err := a.find(ctx, p.Email)
	if err != nil {
		return nil, err
	}

	hash := ""
	if a.scheme.Users.Password != "" && needsPassword(account.Login) {
		h, err := bcrypt.GenerateFromPassword([]byte(want.Password.Reveal()), BcryptCost)
		if err != nil {
			return nil, fmt.Errorf("hashing the password: %w", err)
		}
		hash = string(h)
		account.Password = want.Password
	}

	if found {
		if err := a.update(ctx, existing, p, hash); err != nil {
			return nil, err
		}
		account.Subject = existing
		account.Reconciled = true
	} else {
		id, err := a.insert(ctx, p, hash)
		if err != nil {
			return nil, err
		}
		account.Subject = id
	}

	if a.scheme.Identities != nil {
		if err := a.identity(ctx, account); err != nil {
			return nil, err
		}
	}
	if p.MFA {
		if a.scheme.Factors == nil {
			// Said rather than ignored. A persona that asked for a second
			// factor and did not get one signs in with a password, which
			// looks like the application not enforcing MFA.
			return nil, fmt.Errorf(
				"persona %q asks for mfa and the %s scheme has no table to enrol a factor in",
				p.Name, a.scheme.Name)
		}
		if err := a.enrol(ctx, account, want.TOTPSecret.Reveal()); err != nil {
			return nil, err
		}
		account.TOTPSecret = want.TOTPSecret
	}
	if account.Login == schema.LoginTOTP {
		// A totp login needs the secret whether or not mfa was set, because
		// the secret is the only credential it has.
		if !p.MFA {
			if a.scheme.Factors == nil {
				return nil, fmt.Errorf(
					"persona %q signs in with totp and the %s scheme has no table to enrol a factor in",
					p.Name, a.scheme.Name)
			}
			if err := a.enrol(ctx, account, want.TOTPSecret.Reveal()); err != nil {
				return nil, err
			}
		}
		account.TOTPSecret = want.TOTPSecret
	}
	return account, nil
}

// needsPassword reports whether a strategy signs in with one.
//
// A magic link persona with a password is not wrong, but writing one means the
// account can be signed into two ways, and an application that offers both
// would let the agent take the easy path and never exercise the one the
// manifest asked for.
func needsPassword(s schema.LoginStrategy) bool {
	switch s {
	case schema.LoginPassword, schema.LoginTOTP:
		return true
	default:
		return false
	}
}

// find returns the id of the row already holding an address.
func (a *SQLAdapter) find(ctx context.Context, email string) (string, bool, error) {
	t := a.scheme.Users
	q := fmt.Sprintf("SELECT %s::text FROM %s WHERE lower(%s) = lower($1) LIMIT 1",
		pgx.Identifier{t.ID}.Sanitize(), t.qualified(), pgx.Identifier{t.Email}.Sanitize())

	var id string
	err := a.conn.QueryRow(ctx, q, email).Scan(&id)
	if err != nil {
		if isNoRows(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("looking for an existing %s row: %w", t.Name, err)
	}
	return id, true, nil
}

// insert writes a new account.
func (a *SQLAdapter) insert(ctx context.Context, p schema.Persona, hash string) (string, error) {
	t := a.scheme.Users
	cols := []string{}
	vals := []string{}
	args := []any{}

	add := func(col string, value any) {
		cols = append(cols, pgx.Identifier{col}.Sanitize())
		args = append(args, value)
		vals = append(vals, fmt.Sprintf("$%d", len(args)))
	}

	if t.IDIsUUID {
		// Generated by the database rather than in Go, so this needs no uuid
		// dependency and the value is produced the same way the application's
		// own inserts produce it. RETURNING hands it back either way.
		cols = append(cols, pgx.Identifier{t.ID}.Sanitize())
		vals = append(vals, "gen_random_uuid()")
	}
	add(t.Email, p.Email)
	if t.Password != "" && hash != "" {
		add(t.Password, hash)
	}
	if t.Role != "" && p.Role != "" {
		add(t.Role, p.Role)
	}
	if t.Phone != "" && p.Phone != "" {
		add(t.Phone, p.Phone)
	}

	mapped, unmapped := a.splitAttributes(p.Attributes)
	for _, k := range SortedAttributes(mapped) {
		add(t.Attributes[k], mapped[k])
	}
	if t.JSON != "" && len(unmapped) > 0 {
		add(t.JSON, jsonObject(unmapped))
	} else if len(unmapped) > 0 {
		return "", fmt.Errorf(
			"persona %q sets %s, and the %s scheme has no column or json field for it",
			p.Name, strings.Join(SortedAttributes(unmapped), ", "), a.scheme.Name)
	}

	// Fixed and timestamp columns are SQL expressions rather than parameters,
	// which is why they are appended after the parameterised columns and why
	// their values never come from a manifest.
	for _, col := range sortedKeys(t.Fixed) {
		cols = append(cols, pgx.Identifier{col}.Sanitize())
		vals = append(vals, t.Fixed[col])
	}
	for _, col := range t.Timestamps {
		cols = append(cols, pgx.Identifier{col}.Sanitize())
		vals = append(vals, "now()")
	}

	q := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) RETURNING %s::text",
		t.qualified(), strings.Join(cols, ", "), strings.Join(vals, ", "),
		pgx.Identifier{t.ID}.Sanitize())

	var out string
	if err := a.conn.QueryRow(ctx, q, args...).Scan(&out); err != nil {
		return "", fmt.Errorf("creating persona %q: %w", p.Name, err)
	}
	return out, nil
}

// update reconciles the row already holding the address.
func (a *SQLAdapter) update(ctx context.Context, id string, p schema.Persona, hash string) error {
	t := a.scheme.Users
	sets := []string{}
	args := []any{}

	set := func(col string, value any) {
		args = append(args, value)
		sets = append(sets, fmt.Sprintf("%s = $%d", pgx.Identifier{col}.Sanitize(), len(args)))
	}

	if t.Password != "" && hash != "" {
		set(t.Password, hash)
	}
	if t.Role != "" && p.Role != "" {
		set(t.Role, p.Role)
	}
	if t.Phone != "" && p.Phone != "" {
		set(t.Phone, p.Phone)
	}
	mapped, unmapped := a.splitAttributes(p.Attributes)
	for _, k := range SortedAttributes(mapped) {
		set(t.Attributes[k], mapped[k])
	}
	if t.JSON != "" && len(unmapped) > 0 {
		// Merged rather than replaced, so reconciling a masked real user does
		// not discard the application's own metadata on that row.
		args = append(args, jsonObject(unmapped))
		sets = append(sets, fmt.Sprintf("%s = coalesce(%s, '{}'::jsonb) || $%d::jsonb",
			pgx.Identifier{t.JSON}.Sanitize(), pgx.Identifier{t.JSON}.Sanitize(), len(args)))
	}
	for _, col := range sortedKeys(t.Fixed) {
		sets = append(sets, fmt.Sprintf("%s = %s", pgx.Identifier{col}.Sanitize(), t.Fixed[col]))
	}
	for _, col := range t.Timestamps {
		if strings.Contains(col, "updated") {
			sets = append(sets, fmt.Sprintf("%s = now()", pgx.Identifier{col}.Sanitize()))
		}
	}
	if len(sets) == 0 {
		return nil
	}

	args = append(args, id)
	q := fmt.Sprintf("UPDATE %s SET %s WHERE %s::text = $%d",
		t.qualified(), strings.Join(sets, ", "),
		pgx.Identifier{t.ID}.Sanitize(), len(args))
	if _, err := a.conn.Exec(ctx, q, args...); err != nil {
		return fmt.Errorf("reconciling persona %q: %w", p.Name, err)
	}
	return nil
}

// identity writes the scheme's identity row for an account.
//
// Reconciled explicitly rather than with ON CONFLICT DO NOTHING. A conflict
// clause without a target relies on whatever unique constraint the table
// happens to have, and a schema missing that constraint silently gets a second
// identity row on every run instead of an error. Looking first works on any
// schema and says what it means.
func (a *SQLAdapter) identity(ctx context.Context, account *Account) error {
	t := *a.scheme.Identities

	data := jsonObject(map[string]string{
		"sub": account.Subject, "email": account.Email,
		"email_verified": "true", "provider_id": account.Subject,
	})

	var existing string
	err := a.conn.QueryRow(ctx, fmt.Sprintf(
		"SELECT %s::text FROM %s WHERE %s::text = $1 LIMIT 1",
		pgx.Identifier{t.ID}.Sanitize(), t.qualified(), pgx.Identifier{t.ID}.Sanitize()),
		account.Subject).Scan(&existing)
	if err == nil {
		if t.JSON == "" {
			return nil
		}
		_, err = a.conn.Exec(ctx, fmt.Sprintf("UPDATE %s SET %s = $1::jsonb WHERE %s::text = $2",
			t.qualified(), pgx.Identifier{t.JSON}.Sanitize(),
			pgx.Identifier{t.ID}.Sanitize()), data, account.Subject)
		if err != nil {
			return fmt.Errorf("reconciling the identity row for %q: %w", account.Name, err)
		}
		return nil
	}
	if !isNoRows(err) {
		return fmt.Errorf("looking for the identity row for %q: %w", account.Name, err)
	}

	cols := []string{pgx.Identifier{t.ID}.Sanitize(), pgx.Identifier{t.Email}.Sanitize()}
	args := []any{account.Subject, account.Subject}
	vals := []string{"$1", "$2"}

	if t.JSON != "" {
		args = append(args, data)
		cols = append(cols, pgx.Identifier{t.JSON}.Sanitize())
		vals = append(vals, fmt.Sprintf("$%d::jsonb", len(args)))
	}
	for _, col := range sortedKeys(t.Fixed) {
		cols = append(cols, pgx.Identifier{col}.Sanitize())
		vals = append(vals, t.Fixed[col])
	}
	for _, col := range t.Timestamps {
		cols = append(cols, pgx.Identifier{col}.Sanitize())
		vals = append(vals, "now()")
	}

	q := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		t.qualified(), strings.Join(cols, ", "), strings.Join(vals, ", "))
	if _, err := a.conn.Exec(ctx, q, args...); err != nil {
		return fmt.Errorf("writing the identity row for %q: %w", account.Name, err)
	}
	return nil
}

// enrol writes the second factor.
//
// Reconciled on the account and the friendly name, so provisioning twice
// leaves one factor with the current secret rather than two factors, one of
// which the application might choose. That is the ordering that matters here:
// a golden is provisioned once and a branch reconciles the same personas, so
// the second run is the normal case rather than the exception.
func (a *SQLAdapter) enrol(ctx context.Context, account *Account, secret string) error {
	t := *a.scheme.Factors
	name := t.Fixed["friendly_name"]

	where := fmt.Sprintf("%s::text = $1", pgx.Identifier{t.ID}.Sanitize())
	args := []any{account.Subject}
	if name != "" {
		where += fmt.Sprintf(" AND %s = %s", pgx.Identifier{"friendly_name"}.Sanitize(), name)
	}

	var found int
	err := a.conn.QueryRow(ctx,
		fmt.Sprintf("SELECT 1 FROM %s WHERE %s LIMIT 1", t.qualified(), where), args...).Scan(&found)
	if err == nil {
		args = append(args, secret)
		_, err = a.conn.Exec(ctx, fmt.Sprintf("UPDATE %s SET %s = $%d WHERE %s",
			t.qualified(), pgx.Identifier{t.Password}.Sanitize(), len(args), where), args...)
		if err != nil {
			return fmt.Errorf("updating the second factor for %q: %w", account.Name, err)
		}
		return nil
	}
	if !isNoRows(err) {
		return fmt.Errorf("looking for a second factor for %q: %w", account.Name, err)
	}

	cols := []string{pgx.Identifier{t.ID}.Sanitize(), pgx.Identifier{t.Password}.Sanitize()}
	insertArgs := []any{account.Subject, secret}
	vals := []string{"$1", "$2"}
	for _, col := range sortedKeys(t.Fixed) {
		cols = append(cols, pgx.Identifier{col}.Sanitize())
		vals = append(vals, t.Fixed[col])
	}
	for _, col := range t.Timestamps {
		cols = append(cols, pgx.Identifier{col}.Sanitize())
		vals = append(vals, "now()")
	}

	q := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		t.qualified(), strings.Join(cols, ", "), strings.Join(vals, ", "))
	if _, err := a.conn.Exec(ctx, q, insertArgs...); err != nil {
		return fmt.Errorf("enrolling a second factor for %q: %w", account.Name, err)
	}
	return nil
}

// TruncateSessions empties the scheme's session and token tables.
//
// This is the half of the objective that is not about personas: no real
// session survives masking. Masking rewrites a user's name and address and
// leaves untouched the session row that still authenticates as them, because
// a session token is not personal data by any rule a scanner applies. A
// branch published with those rows hands anybody who reaches it a working
// login belonging to a real customer.
//
// Tables missing from the database are skipped rather than failing: a scheme
// describes the tables a framework can have, and a given application may not
// use all of them.
func (a *SQLAdapter) TruncateSessions(ctx context.Context) ([]string, error) {
	var emptied []string
	for _, name := range a.scheme.Sessions {
		table, err := parseTable(name)
		if err != nil {
			return nil, err
		}
		exists, err := a.tableExists(ctx, table)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		// CASCADE because refresh tokens reference sessions, and a plain
		// truncate of one of a pair fails on the foreign key.
		if _, err := a.conn.Exec(ctx, "TRUNCATE TABLE "+table.qualified()+" CASCADE"); err != nil {
			return nil, fmt.Errorf("emptying %s: %w", name, err)
		}
		emptied = append(emptied, name)
	}
	return emptied, nil
}

// tableExists reports whether a table is in the database.
func (a *SQLAdapter) tableExists(ctx context.Context, t Table) (bool, error) {
	schemaName := t.Schema
	if schemaName == "" {
		schemaName = "public"
	}
	var exists bool
	err := a.conn.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		 WHERE table_schema = $1 AND table_name = $2)`,
		schemaName, t.Name).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("looking for the table %s: %w", t.Name, err)
	}
	return exists, nil
}

// splitAttributes divides a persona's attributes into those with a column and
// those without.
func (a *SQLAdapter) splitAttributes(attrs map[string]string) (mapped, unmapped map[string]string) {
	mapped = map[string]string{}
	unmapped = map[string]string{}
	for k, v := range attrs {
		if _, ok := a.scheme.Users.Attributes[k]; ok {
			mapped[k] = v
			continue
		}
		unmapped[k] = v
	}
	return mapped, unmapped
}

// isNoRows reports whether a query found nothing.
//
// Compared against pgx.ErrNoRows rather than matched on the message, because
// a message match is a test that passes until somebody rewords an error and
// then quietly treats a real failure as an empty result.
func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// parseTable splits a possibly qualified table name.
func parseTable(name string) (Table, error) {
	parts := strings.Split(name, ".")
	switch len(parts) {
	case 1:
		return Table{Schema: "public", Name: parts[0]}, nil
	case 2:
		return Table{Schema: parts[0], Name: parts[1]}, nil
	default:
		return Table{}, fmt.Errorf("%q is not a table name", name)
	}
}

// sortedKeys returns a map's keys in a stable order, so two runs produce the
// same statement.
func sortedKeys(m map[string]string) []string { return SortedAttributes(m) }

// jsonObject renders attributes as a JSON object.
//
// Built here rather than with encoding/json because every value is a string
// and the escaping needed is small and total, and because the output has to be
// ordered for the same reason the column list is.
func jsonObject(attrs map[string]string) string {
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range SortedAttributes(attrs) {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(quoteJSON(k))
		b.WriteByte(':')
		b.WriteString(quoteJSON(attrs[k]))
	}
	b.WriteByte('}')
	return b.String()
}

func quoteJSON(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
