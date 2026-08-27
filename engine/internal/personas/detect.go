package personas

import (
	"context"
	"fmt"
	"strings"
)

// Choosing the adapter, which should not be a question anybody is asked.
//
// Two sources, and they answer different questions. The dependency list is
// available at `af init` time, before there is any database to look at, which
// is when the manifest is written. The live schema is available later and is
// the better evidence: a project can depend on @supabase/supabase-js for the
// client and keep its users in its own table, and only the schema knows that.
//
// So detection from dependencies writes the manifest, and detection from the
// schema checks it. Where they disagree the schema wins, because a dependency
// list is a statement of intent and a table is a fact.

// DetectFromDependencies returns the scheme a repository's dependencies imply.
//
// Ordered by BuiltinSchemes rather than by iteration, so a project that
// depends on both Supabase and NextAuth gets a stable answer rather than
// whichever was seen first.
func DetectFromDependencies(deps []string) (Scheme, bool) {
	present := map[string]bool{}
	for _, d := range deps {
		present[strings.ToLower(strings.TrimSpace(d))] = true
	}
	for _, scheme := range BuiltinSchemes {
		for _, pkg := range scheme.Packages {
			if present[strings.ToLower(pkg)] {
				return scheme, true
			}
		}
	}
	return Scheme{}, false
}

// SchemaProbe reads whether a table exists.
//
// An interface so detection can run against anything that can answer that
// question, and so this package does not need a connection to be tested.
type SchemaProbe interface {
	HasTable(ctx context.Context, schema, table string) (bool, error)
	HasColumn(ctx context.Context, schema, table, column string) (bool, error)
}

// ConnProbe answers schema questions from a database connection.
type ConnProbe struct{ Conn Conn }

// HasTable reports whether a table is present.
func (p ConnProbe) HasTable(ctx context.Context, schemaName, table string) (bool, error) {
	var exists bool
	err := p.Conn.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		 WHERE table_schema = $1 AND table_name = $2)`,
		schemaName, table).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("looking for the table %s.%s: %w", schemaName, table, err)
	}
	return exists, nil
}

// HasColumn reports whether a column is present.
func (p ConnProbe) HasColumn(ctx context.Context, schemaName, table, column string) (bool, error) {
	var exists bool
	err := p.Conn.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.columns
		 WHERE table_schema = $1 AND table_name = $2 AND column_name = $3)`,
		schemaName, table, column).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("looking for the column %s.%s.%s: %w",
			schemaName, table, column, err)
	}
	return exists, nil
}

// DetectFromSchema returns the scheme the database actually uses.
//
// This is what the spec asks for, and it is worth more than the dependency
// list: a table is a fact and a package is an intention.
func DetectFromSchema(ctx context.Context, p SchemaProbe) (Scheme, bool, error) {
	for _, scheme := range BuiltinSchemes {
		if scheme.Probe == "" {
			continue
		}
		table, err := parseTable(scheme.Probe)
		if err != nil {
			return Scheme{}, false, err
		}
		found, err := p.HasTable(ctx, table.Schema, table.Name)
		if err != nil {
			return Scheme{}, false, err
		}
		if found {
			return scheme, true, nil
		}
	}
	return Scheme{}, false, nil
}

// candidateUserTables are the names an application's own users table is
// called, most likely first. A list rather than a search because a search
// finds "user_sessions" and "users_audit" too, and picking wrong is worse
// than not picking.
var candidateUserTables = []string{"users", "user", "accounts", "app_users", "members"}

// candidatePasswordColumns are what a password column is called.
var candidatePasswordColumns = []string{
	"encrypted_password", "password_hash", "hashed_password", "password_digest",
	"password", "passwordhash",
}

// candidateRoleColumns are what a role column is called.
var candidateRoleColumns = []string{"role", "user_role", "role_name", "type"}

// InferGeneric describes an application that owns its users table.
//
// Used when neither a known framework nor a configured scheme applies, which
// is the common case for an application somebody wrote themselves. It reports
// what it found rather than guessing at what it did not: a table with no
// password column gives a scheme with no password column, and provisioning
// then says so rather than writing a hash into a column that is not there.
func InferGeneric(ctx context.Context, p SchemaProbe) (Scheme, bool, error) {
	for _, name := range candidateUserTables {
		found, err := p.HasTable(ctx, "public", name)
		if err != nil {
			return Scheme{}, false, err
		}
		if !found {
			continue
		}
		hasEmail, err := p.HasColumn(ctx, "public", name, "email")
		if err != nil {
			return Scheme{}, false, err
		}
		if !hasEmail {
			// A users table with no email is one this cannot provision into,
			// because the manifest identifies a persona by address.
			continue
		}

		t := Table{Schema: "public", Name: name, ID: "id", Email: "email"}
		for _, col := range candidatePasswordColumns {
			has, err := p.HasColumn(ctx, "public", name, col)
			if err != nil {
				return Scheme{}, false, err
			}
			if has {
				t.Password = col
				break
			}
		}
		for _, col := range candidateRoleColumns {
			has, err := p.HasColumn(ctx, "public", name, col)
			if err != nil {
				return Scheme{}, false, err
			}
			if has {
				t.Role = col
				break
			}
		}
		for _, col := range []string{"phone", "phone_number", "mobile"} {
			has, err := p.HasColumn(ctx, "public", name, col)
			if err != nil {
				return Scheme{}, false, err
			}
			if has {
				t.Phone = col
				break
			}
		}
		for _, col := range []string{"created_at", "updated_at"} {
			has, err := p.HasColumn(ctx, "public", name, col)
			if err != nil {
				return Scheme{}, false, err
			}
			if has {
				t.Timestamps = append(t.Timestamps, col)
			}
		}
		return GenericScheme(t, nil), true, nil
	}
	return Scheme{}, false, nil
}
