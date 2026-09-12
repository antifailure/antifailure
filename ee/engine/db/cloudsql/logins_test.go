// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
package cloudsql_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/antifailure/antifailure/ee/engine/db/cloudsql"
	"github.com/antifailure/antifailure/engine/pkg/secret"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func TestDefaultCatalogReadsCustomerRolesAndPreservesGoogleRoles(t *testing.T) {
	db, err := sql.Open("pgx", requirePostgres(t))
	require.NoError(t, err)
	customer := "afcs_catalog_" + randomSuffix(t)
	require.NoError(t, execSQL(db, "CREATE ROLE "+pgx.Identifier{customer}.Sanitize()+" LOGIN"))
	managed := "cloudsqlagent"
	created := false
	t.Cleanup(func() {
		_ = execSQL(db, "DROP ROLE IF EXISTS "+pgx.Identifier{customer}.Sanitize())
		if created {
			_ = execSQL(db, "DROP ROLE IF EXISTS "+pgx.Identifier{managed}.Sanitize())
		}
		_ = db.Close()
	})
	var exists bool
	require.NoError(t, db.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)", managed).Scan(&exists))
	if !exists {
		require.NoError(t, execSQL(db, "CREATE ROLE "+pgx.Identifier{managed}.Sanitize()+" LOGIN"))
		created = true
	}
	var canLogin bool
	require.NoError(t, db.QueryRow("SELECT rolcanlogin FROM pg_roles WHERE rolname=$1", managed).Scan(&canLogin))
	require.True(t, canLogin, "the managed-role control must be eligible for login")
	names, err := cloudsql.CustomerLoginsForTest(context.Background(), db)
	require.NoError(t, err)
	require.Contains(t, names, customer)
	require.NotContains(t, names, managed, "the catalog offered a Google service role for modification")
	var current string
	require.NoError(t, db.QueryRow("SELECT current_user").Scan(&current))
	require.NotContains(t, names, current)
	// A disabled role may still own an authenticated connection.
	require.NoError(t, execSQL(db, "ALTER ROLE "+pgx.Identifier{customer}.Sanitize()+" PASSWORD 'AF_FAKE_CATALOG_PASSWORD'"))
	connection, err := url.Parse(requirePostgres(t))
	require.NoError(t, err)
	connection.User = url.UserPassword(customer, "AF_FAKE_CATALOG_PASSWORD")
	active, err := sql.Open("pgx", connection.String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = active.Close() })
	activeSession, err := active.Conn(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = activeSession.Close() })
	var one int
	require.NoError(t, activeSession.QueryRowContext(context.Background(), "SELECT 1").Scan(&one))
	require.NoError(t, execSQL(db, "ALTER ROLE "+pgx.Identifier{customer}.Sanitize()+" NOLOGIN"))
	names, err = cloudsql.CustomerLoginsForTest(context.Background(), db)
	require.NoError(t, err)
	require.Contains(t, names, customer, "an already-disabled login's live session was omitted")
}

func execSQL(db *sql.DB, statement string) error { _, err := db.Exec(statement); return err }

func TestPublicationRevokesUnlistedLoginsAndExistingSessions(t *testing.T) {
	for _, alreadyDisabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "login", true: "already-nologin"}[alreadyDisabled], func(t *testing.T) {
			server := newFake(t, seedSQL)
			require.NoError(t, server.AddUser(sourceInstance, "source-reader", "BUILT_IN", "AF_FAKE_SOURCE_PASSWORD"))
			sourceURL, err := server.UserURL(sourceInstance, "source-reader", "AF_FAKE_SOURCE_PASSWORD")
			require.NoError(t, err)
			sourceDB, err := sql.Open("pgx", sourceURL)
			require.NoError(t, err)
			defer func() { _ = sourceDB.Close() }()
			var sourceSession *sql.Conn
			defer func() {
				if sourceSession != nil {
					_ = sourceSession.Close()
				}
			}()
			var one int
			opts := options(t, server)
			target := ""
			opts.HTTPClient = cancellingTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/clone") {
					body, err := io.ReadAll(req.Body)
					if err != nil {
						return nil, err
					}
					req.Body = io.NopCloser(strings.NewReader(string(body)))
					var request struct {
						CloneContext struct {
							Destination string `json:"destinationInstanceName"`
						}
					}
					if err := json.Unmarshal(body, &request); err != nil {
						return nil, err
					}
					target = request.CloneContext.Destination
				}
				return http.DefaultClient.Do(req)
			})
			p, err := newScoped(context.Background(), server, opts)
			require.NoError(t, err)
			defer func() { _ = p.Close() }()
			var userDB, adminDB *sql.DB
			var userSession, adminSession *sql.Conn
			role := ""
			defer func() {
				if userSession != nil {
					_ = userSession.Close()
				}
				if adminSession != nil {
					_ = adminSession.Close()
				}
				if userDB != nil {
					_ = userDB.Close()
				}
				if adminDB != nil {
					_ = adminDB.Close()
				}
			}()
			spec := goldenSpec()
			verify := spec.Verify
			spec.Verify = func(ctx context.Context, raw secret.Value) (string, error) {
				sourceSession, err = sourceDB.Conn(ctx)
				require.NoError(t, err)
				require.NoError(t, sourceSession.QueryRowContext(ctx, "SELECT 1").Scan(&one))
				require.NoError(t, server.AddSQLLogin(target, "sql-only-reader", "AF_FAKE_SQL_PASSWORD"))
				connection, err := server.UserURL(target, "sql-only-reader", "AF_FAKE_SQL_PASSWORD")
				require.NoError(t, err)
				parsed, err := url.Parse(connection)
				require.NoError(t, err)
				role = parsed.User.Username()
				userDB, err = sql.Open("pgx", connection)
				require.NoError(t, err)
				userSession, err = userDB.Conn(ctx)
				require.NoError(t, err)
				require.NoError(t, userSession.QueryRowContext(ctx, "SELECT 1").Scan(&one))
				adminDB, err = sql.Open("pgx", raw.Reveal())
				require.NoError(t, err)
				adminSession, err = adminDB.Conn(ctx)
				require.NoError(t, err)
				if alreadyDisabled {
					_, err = adminSession.ExecContext(ctx, "ALTER ROLE "+pgx.Identifier{role}.Sanitize()+" NOLOGIN")
					require.NoError(t, err)
					require.NoError(t, userSession.QueryRowContext(ctx, "SELECT 1").Scan(&one), "NOLOGIN alone must leave the existing-session control alive")
				}
				return verify(ctx, raw)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err = p.RefreshGolden(ctx, spec)
			require.NoError(t, err)
			require.Error(t, userSession.QueryRowContext(context.Background(), "SELECT 1").Scan(&one), "an inherited secondary session survived publication")
			require.Error(t, adminSession.QueryRowContext(context.Background(), "SELECT 1").Scan(&one), "an inherited administrator session survived publication")
			require.NoError(t, sourceSession.QueryRowContext(context.Background(), "SELECT 1").Scan(&one), "the source session was terminated")
			check, err := sql.Open("pgx", requirePostgres(t))
			require.NoError(t, err)
			defer func() { _ = check.Close() }()
			var login, passwordCleared bool
			require.NoError(t, check.QueryRow("SELECT rolcanlogin,rolpassword IS NULL FROM pg_authid WHERE rolname=$1", role).Scan(&login, &passwordCleared))
			require.False(t, login)
			require.True(t, passwordCleared)
		})
	}
}

func TestLoginCatalogFailurePreventsPublication(t *testing.T) {
	server := newFake(t, seedSQL)
	p := newProvider(t, server)
	cloudsql.SetLoginCatalogForTest(p, func(context.Context, *sql.DB) ([]string, error) {
		return nil, fmt.Errorf("fixture catalog unavailable")
	})
	_, err := p.RefreshGolden(context.Background(), goldenSpec())
	require.ErrorContains(t, err, "catalog unavailable")
	require.Equal(t, 1, server.ResourceCount(), "a golden was published without knowing its inherited logins")
}
