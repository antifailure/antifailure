// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package aurora_test

import (
	"database/sql"
	"strings"
)

// openAdmin is a superuser connection to the test server, for the sweep and
// for the checks that look at the bytes directly.
func openAdmin(url string) (*sql.DB, error) {
	db, err := sql.Open("pgx", url)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// quote quotes a Postgres identifier. Every name reaching it came out of
// pg_database on the server itself, and it is quoted anyway.
func quote(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
