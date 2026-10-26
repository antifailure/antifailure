package mysql_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/internal/datastore/mysql"
	"github.com/antifailure/antifailure/engine/internal/masking"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/internal/verify"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// Against a real MySQL: AF_TEST_MYSQL_URL when it names one, otherwise the
// machine's managed server, started if it has to be. The same rules the
// ClickHouse suite follows: skipped without Docker, and a failure rather than a
// skip when AF_REQUIRE_DOCKER says this run must have exercised it.
func requireServer(t *testing.T) secrets.Value {
	t.Helper()
	if u := os.Getenv("AF_TEST_MYSQL_URL"); u != "" {
		return secrets.New(u)
	}
	if os.Getenv("AF_SKIP_DOCKER") != "" {
		t.Skip("skipped: AF_SKIP_DOCKER is set and AF_TEST_MYSQL_URL names no server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	server, err := mysql.EnsureLocalServer(ctx, mysql.LocalServerOptions{
		Progress: func(line string) { t.Log(line) },
	})
	if err != nil {
		if os.Getenv("AF_REQUIRE_DOCKER") != "" {
			t.Fatalf("AF_REQUIRE_DOCKER is set, so this cannot be skipped: no MySQL is reachable "+
				"and one could not be started: %v", err)
		}
		t.Skipf("skipped: no MySQL is reachable and one could not be started: %v", err)
	}
	return server.URL
}

func TestConformance_AgainstARealMySQL(t *testing.T) {
	server := requireServer(t)
	conformance.RunDatastore(t, func(t *testing.T) provider.Datastore {
		p, err := mysql.New(mysql.Options{
			ServerURL: server, Name: "accounts",
			Progress: func(line string) { t.Log(line) },
		})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, p.Close()) })
		return p
	}, conformance.DatastoreOptions{Timeout: 3 * time.Minute})
}

// admin opens a connection a test uses to arrange and inspect, as root, with
// no database, utf8mb4, and a permissive sql_mode so a fixture can hold what a
// production made under an older mode holds.
func admin(t *testing.T, server secrets.Value, database string) *sql.DB {
	t.Helper()
	u, err := url.Parse(server.Reveal())
	require.NoError(t, err)
	cfg := driver.NewConfig()
	cfg.User = u.User.Username()
	cfg.Passwd, _ = u.User.Password()
	cfg.Net, cfg.Addr, cfg.DBName = "tcp", u.Host, database
	cfg.TLSConfig = "preferred"
	cfg.Params = map[string]string{"sql_mode": "''", "time_zone": "'+00:00'"}
	connector, err := driver.NewConnector(cfg)
	require.NoError(t, err)
	db := sql.OpenDB(connector)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// source makes a database standing in for production, and removes it after.
func source(t *testing.T, server secrets.Value) (string, *sql.DB) {
	t.Helper()
	name := fmt.Sprintf("prod_%d", time.Now().UnixNano()%1_000_000_000)
	root := admin(t, server, "")
	_, err := root.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci")
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = root.Exec("DROP DATABASE IF EXISTS `" + name + "`") })
	return name, admin(t, server, name)
}

func exec(t *testing.T, db *sql.DB, statements ...string) {
	t.Helper()
	for _, s := range statements {
		_, err := db.Exec(s)
		require.NoError(t, err, s)
	}
}

func liveKey(t *testing.T) *masking.Key {
	t.Helper()
	key, err := masking.NewKey(secrets.New("mysql-datastore-live-test-key"))
	require.NoError(t, err)
	return key
}

func liveRules(t *testing.T) *masking.RuleSet {
	t.Helper()
	rules, err := masking.NewRuleSet([]masking.Rule{
		{Column: "id", Type: "character", Transform: "uuid_remap", Link: "account",
			Why: "An account's key, and the key every reference to it carries."},
		{Column: "account_id", Transform: "uuid_remap", Link: "account",
			Why: "The same account, from the other side of the join."},
		{Column: "full_name", Transform: "preserve",
			Why: "The server computes it from the masked first and last names."},
		{Column: "city", Transform: "preserve", Why: "A city is not a person."},
		{Column: "avatar", Transform: "preserve", Why: "A generated placeholder, not a photograph."},
		{Column: "profile", Transform: "preserve", Why: "Display preferences only."},
		{Column: "status", Transform: "preserve", Why: "An account state."},
		{Column: "legacy_on", Transform: "preserve", Why: "A date the old system wrote."},
		{Column: "seen_at", Transform: "preserve", Why: "When, not who."},
		{Column: "amount_cents", Transform: "preserve", Why: "An amount is not a person."},
	})
	require.NoError(t, err)
	return rules
}

// verifier is the engine's verification half, minus the signature: the scan,
// and a refusal naming the first thing it found.
func verifier(t *testing.T) func(context.Context, secrets.Value) (string, error) {
	return func(ctx context.Context, candidate secrets.Value) (string, error) {
		report, err := mysql.Scan(ctx, candidate, mysql.ScanOptions{
			Now: time.Now, Progress: func(line string) { t.Log("scan: " + line) },
		})
		if err != nil {
			return "", err
		}
		if len(report.Findings) > 0 {
			f := report.Findings[0]
			return "", fmt.Errorf("%s.%s still holds %s", f.Table, f.Column, f.Detector)
		}
		if len(report.Skipped) > 0 {
			return "", fmt.Errorf("the scan could not read %v", report.Skipped[0])
		}
		return fmt.Sprintf("scanned %d columns", report.Columns), nil
	}
}

// seedShop writes the fixture: every shape a MySQL golden has to carry and
// that a naive copy loses.
func seedShop(t *testing.T, db *sql.DB, accounts int) {
	t.Helper()
	exec(t, db,
		`CREATE TABLE accounts (
			id CHAR(36) NOT NULL PRIMARY KEY,
			email VARCHAR(255) NOT NULL UNIQUE,
			first_name VARCHAR(64) NOT NULL,
			last_name VARCHAR(64) NOT NULL,
			full_name VARCHAR(130) AS (CONCAT(first_name, ' ', last_name)) STORED,
			city VARCHAR(64) CHARACTER SET latin1 COLLATE latin1_swedish_ci NULL,
			avatar VARBINARY(16) NULL,
			profile JSON NULL,
			status ENUM('active','closed') NOT NULL DEFAULT 'active',
			legacy_on DATE NULL,
			seen_at TIMESTAMP(3) NULL
		) ENGINE=InnoDB`,
		`CREATE TABLE payments (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
			account_id CHAR(36) NOT NULL,
			amount_cents BIGINT NOT NULL,
			CONSTRAINT payments_account FOREIGN KEY (account_id) REFERENCES accounts (id)
		) ENGINE=InnoDB AUTO_INCREMENT=9007199254740990`,
		// A table whose counter is ahead of its rows, the way any table is
		// after rows at its end were deleted. A branch that did not carry the
		// counter would hand out an identifier production already used.
		`CREATE TABLE tickets (
			id INT NOT NULL AUTO_INCREMENT PRIMARY KEY,
			subject VARCHAR(64) NOT NULL
		) ENGINE=InnoDB AUTO_INCREMENT=1000`,
		`CREATE VIEW big_spenders AS
			SELECT a.email, SUM(p.amount_cents) AS spent FROM accounts a
			JOIN payments p ON p.account_id = a.id GROUP BY a.email`,
		// Enough rows that the accounts table, whose key is remapped, spans
		// more than one of masking's twenty thousand row chunks.
		`SET SESSION cte_max_recursion_depth = 100000`,
		fmt.Sprintf(`INSERT INTO accounts (id, email, first_name, last_name, city, avatar, profile,
			status, legacy_on, seen_at)
			WITH RECURSIVE n (i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < %d)
			SELECT UUID(), CONCAT('person', i, '@acme-customers.com'), 'Ana', CONCAT('Real', i),
				'Zürich', UNHEX('FF00FE01C3'), JSON_OBJECT('theme', 'dark', 'n', i),
				IF(i %% 7 = 0, 'closed', 'active'), '0000-00-00',
				'2024-03-10 01:30:00.125'
			FROM n`, accounts),
		`INSERT INTO payments (account_id, amount_cents)
			SELECT id, 1999 FROM accounts ORDER BY email LIMIT 500`,
	)
}

// The whole of what a MySQL golden is: production copied at one instant,
// masked with the project key, scanned, published, and branched, with nothing
// a person can be found by left in it and nothing the application depends on
// taken out of it.
func TestRefresh_MasksVerifiesPublishesAndBranchesACopyOfProduction(t *testing.T) {
	server := requireServer(t)
	srcName, src := source(t, server)
	const accounts = 25_000
	seedShop(t, src, accounts)

	p, err := mysql.New(mysql.Options{ServerURL: server, Name: "shop",
		Progress: func(line string) { t.Log(line) }})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	sourceURL := secrets.New(strings.TrimSuffix(server.Reveal(), "/") + "/" + srcName)
	var masked mysql.MaskResult
	gv, err := p.RefreshGolden(ctx, provider.GoldenSpec{
		SourceURL: sourceURL, RulesHash: "rules-live", Provenance: "project:shop",
		Mask: func(ctx context.Context, candidate secrets.Value) error {
			var err error
			masked, err = mysql.Mask(ctx, candidate, mysql.MaskOptions{
				Key: liveKey(t), Rules: liveRules(t), RulesHash: "rules-live",
				Schema: mysql.DatabaseOf(sourceURL), Progress: func(l string) { t.Log(l) },
			})
			return err
		},
		Verify: verifier(t),
	})
	require.NoError(t, err)
	require.True(t, gv.Verified)
	require.EqualValues(t, accounts+500, masked.Rows)
	t.Cleanup(func() { _ = p.DestroyGolden(context.Background(), gv.ID) })

	branch, err := p.Branch(ctx, gv.ID, "env-live-1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Destroy(context.Background(), branch) })
	branchURL, err := p.ConnString(ctx, branch)
	require.NoError(t, err)
	env := admin(t, server, mysql.DatabaseOf(branchURL))

	count := func(q string) int {
		var n int
		require.NoError(t, env.QueryRow(q).Scan(&n), q)
		return n
	}

	// Nobody is left. Every address is synthetic and still unique, every last
	// name was rewritten, and the stored column computed from them followed.
	require.Equal(t, accounts, count("SELECT COUNT(*) FROM accounts"))
	require.Zero(t, count("SELECT COUNT(*) FROM accounts WHERE email LIKE '%acme-customers%'"))
	require.Equal(t, accounts, count("SELECT COUNT(*) FROM accounts WHERE email LIKE '%@example.test'"))
	require.Equal(t, accounts, count("SELECT COUNT(DISTINCT email) FROM accounts"))
	require.Zero(t, count("SELECT COUNT(*) FROM accounts WHERE last_name LIKE 'Real%'"))
	require.Zero(t, count("SELECT COUNT(*) FROM accounts WHERE full_name <> CONCAT(first_name, ' ', last_name)"))

	// Every key was remapped exactly once. It spans two chunks, and a key
	// masked twice would leave its payments pointing at nothing.
	var originalIDs int
	require.NoError(t, src.QueryRow(
		"SELECT COUNT(*) FROM accounts a JOIN "+quoteDB(mysql.DatabaseOf(branchURL))+".accounts b ON a.id = b.id").
		Scan(&originalIDs))
	require.Zero(t, originalIDs, "an account kept its production key")
	require.Equal(t, 500, count("SELECT COUNT(*) FROM payments p JOIN accounts a ON a.id = p.account_id"),
		"a payment no longer joins its account, so its key was masked a different number of times")

	// What the application depends on arrived as production stored it: a
	// latin1 string, bytes that are not UTF-8, a zero date, an instant, a
	// document, an enum, a foreign key, and the next AUTO_INCREMENT value.
	var city, avatar, profile, status, legacy, seen string
	require.NoError(t, env.QueryRow(`SELECT city, HEX(avatar), profile->>'$.theme', status,
		CAST(legacy_on AS CHAR), CAST(seen_at AS CHAR) FROM accounts
		WHERE status = 'active' LIMIT 1`).
		Scan(&city, &avatar, &profile, &status, &legacy, &seen))
	require.Equal(t, "Zürich", city)
	require.Equal(t, "FF00FE01C3", avatar)
	require.Equal(t, "dark", profile)
	require.Equal(t, "active", status)
	require.Equal(t, "0000-00-00", legacy)
	require.Equal(t, "2024-03-10 01:30:00.125", seen)
	require.Equal(t, accounts/7, count("SELECT COUNT(*) FROM accounts WHERE status = 'closed'"))
	var fk int
	require.NoError(t, env.QueryRow(`SELECT COUNT(*) FROM information_schema.REFERENTIAL_CONSTRAINTS
		WHERE CONSTRAINT_SCHEMA = DATABASE() AND REFERENCED_TABLE_NAME = 'accounts'`).Scan(&fk))
	require.Equal(t, 1, fk, "the branch lost the foreign key from payments to accounts")
	_, err = env.Exec("INSERT INTO payments (account_id, amount_cents) VALUES ('missing', 1)")
	require.Error(t, err, "the branch's foreign key is not enforced")
	res, err := env.Exec(`INSERT INTO payments (account_id, amount_cents)
		SELECT id, 1 FROM accounts LIMIT 1`)
	require.NoError(t, err)
	next, err := res.LastInsertId()
	require.NoError(t, err)
	require.Greater(t, next, int64(9007199254740990)+499,
		"a branch handed out an identifier a copied payment already has")
	res, err = env.Exec(`INSERT INTO tickets (subject) VALUES ('first in the branch')`)
	require.NoError(t, err)
	next, err = res.LastInsertId()
	require.NoError(t, err)
	require.EqualValues(t, 1000, next, "the branch did not carry the table's AUTO_INCREMENT position")

	// The view is not in the golden, and the golden says so.
	require.Zero(t, count("SELECT COUNT(*) FROM information_schema.VIEWS WHERE TABLE_SCHEMA = DATABASE()"))

	// The branch is its own: a write to it is not in the golden, and another
	// environment's branch does not see it.
	other, err := p.Branch(ctx, gv.ID, "env-live-2")
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Destroy(context.Background(), other) })
	otherURL, err := p.ConnString(ctx, other)
	require.NoError(t, err)
	require.Equal(t, 500, func() int {
		var n int
		require.NoError(t, admin(t, server, mysql.DatabaseOf(otherURL)).
			QueryRow("SELECT COUNT(*) FROM payments").Scan(&n))
		return n
	}())

	inv, err := p.Inventory(ctx)
	require.NoError(t, err)
	found := false
	for _, r := range inv {
		if r.ID == branch.ProviderRef {
			found = true
			require.Equal(t, "3", r.Labels["tables"])
			require.Equal(t, fmt.Sprint(accounts+502), r.Labels["rows"],
				"the accounts, the payments and the one payment and one ticket written above")
			require.Equal(t, gv.ID, r.Labels["golden"])
		}
	}
	require.True(t, found, "the branch is missing from the inventory")
}

// A copy nothing masked holds production's addresses, and a refresh whose scan
// finds one publishes nothing and leaves no copy of production behind.
func TestRefresh_PublishesNothingWhenTheScanFindsAPerson(t *testing.T) {
	server := requireServer(t)
	srcName, src := source(t, server)
	exec(t, src,
		`CREATE TABLE contacts (id INT PRIMARY KEY, email VARCHAR(255) NOT NULL)`,
		`INSERT INTO contacts VALUES (1, 'a.person@acme-customers.com'),
			(2, 'b.person@acme-customers.com')`)

	p, err := mysql.New(mysql.Options{ServerURL: server, Name: "contacts"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	ctx := context.Background()
	before, err := p.Inventory(ctx)
	require.NoError(t, err)

	_, err = p.RefreshGolden(ctx, provider.GoldenSpec{
		SourceURL: secrets.New(strings.TrimSuffix(server.Reveal(), "/") + "/" + srcName),
		RulesHash: "rules-unmasked", Provenance: "project:contacts",
		Verify: verifier(t),
	})
	require.ErrorContains(t, err, "email")

	after, err := p.Inventory(ctx)
	require.NoError(t, err)
	require.Equal(t, len(before), len(after),
		"a refused refresh left a database behind, and it holds production's data")
}

// The cross store check reads a MySQL the way masking does.
func TestCatalog_DescribesKeysReferencesAndComputedColumns(t *testing.T) {
	server := requireServer(t)
	srcName, src := source(t, server)
	seedShop(t, src, 10)

	tables, skipped, err := mysql.Catalog(context.Background(),
		secrets.New(strings.TrimSuffix(server.Reveal(), "/")+"/"+srcName))
	require.NoError(t, err)
	require.Equal(t, []string{"big_spenders"}, skipped)
	byName := map[string]masking.Table{}
	for _, tbl := range tables {
		byName[tbl.Name] = tbl
	}
	accounts, payments := byName["accounts"], byName["payments"]
	require.Equal(t, []string{"id"}, accounts.PrimaryKey)
	require.True(t, accounts.ColumnNamed("email").Unique)
	require.True(t, accounts.ColumnNamed("full_name").Generated)
	d, err := masking.DialectFor("mysql")
	require.NoError(t, err)
	require.Equal(t, "character", d.Canonical(accounts.ColumnNamed("id").Type))
	require.Equal(t, srcName+".accounts.id", payments.ColumnNamed("account_id").ForeignKey)
	require.Equal(t, "bigint unsigned", payments.ColumnNamed("id").SQLType)
}

func quoteDB(name string) string { return "`" + strings.ReplaceAll(name, "`", "``") + "`" }

// A secret no rule covered and the scanner cannot read refuses the golden.
//
// The engine's own wiring, in miniature: masking names the source database as
// its schema, and the scan is told which columns masking copied unchanged by
// the names masking returned. Those two once disagreed about what the database
// was called, shop.vault.signing_key against af_cand_<hash>.vault.signing_key,
// so the scan matched no unruled column and counted every one as ruled. A
// ruled column the scanner cannot read is a note, so bytes that are not UTF-8
// in a column named like a key, which nothing masked, published.
//
// The column is signing_key rather than api_key because the default rules
// hash an api_key, and this needs a column no rule reaches; the precondition
// below proves that it is one.
func TestRefresh_RefusesAnUnreadSecretNoRuleCovered(t *testing.T) {
	server := requireServer(t)
	srcName, src := source(t, server)
	exec(t, src,
		`CREATE TABLE vault (
			id INT NOT NULL PRIMARY KEY,
			signing_key VARBINARY(32) NOT NULL,
			private_spot POINT NULL
		) ENGINE=InnoDB`,
		`INSERT INTO vault VALUES
			(1, UNHEX('FF00FE01C3A9DEADBEEF'), ST_GeomFromText('POINT(1 2)')),
			(2, UNHEX('C3FF28A0E2828A'), NULL)`)
	sourceURL := secrets.New(strings.TrimSuffix(server.Reveal(), "/") + "/" + srcName)

	p, err := mysql.New(mysql.Options{ServerURL: server, Name: "vault"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	refresh := func(rules *masking.RuleSet) (provider.GoldenVersion, mysql.MaskResult, verify.Report, error) {
		var masked mysql.MaskResult
		var report verify.Report
		gv, err := p.RefreshGolden(ctx, provider.GoldenSpec{
			SourceURL: sourceURL, RulesHash: "rules-vault", Provenance: "project:vault",
			Mask: func(ctx context.Context, candidate secrets.Value) error {
				var err error
				masked, err = mysql.Mask(ctx, candidate, mysql.MaskOptions{
					Key: liveKey(t), Rules: rules, RulesHash: "rules-vault",
					Schema: mysql.DatabaseOf(sourceURL),
				})
				return err
			},
			Verify: func(ctx context.Context, candidate secrets.Value) (string, error) {
				var err error
				report, err = mysql.Scan(ctx, candidate, mysql.ScanOptions{
					Unruled: masked.CopiedUnchanged, Now: time.Now,
				})
				if err != nil {
					return "", err
				}
				if len(report.Findings) > 0 {
					f := report.Findings[0]
					return "", fmt.Errorf("%s.%s still holds %s", f.Table, f.Column, f.Detector)
				}
				if len(report.Skipped) > 0 {
					return "", fmt.Errorf("the scan could not read %v", report.Skipped[0])
				}
				return fmt.Sprintf("scanned %d columns", report.Columns), nil
			},
		})
		if err == nil {
			t.Cleanup(func() { _ = p.DestroyGolden(context.Background(), gv.ID) })
		}
		return gv, masked, report, err
	}

	before, err := p.ListGoldens(ctx)
	require.NoError(t, err)
	unruled, err := masking.NewRuleSet([]masking.Rule{
		{Column: "id", Transform: "preserve", Why: "A row number."},
	})
	require.NoError(t, err)
	_, masked, report, err := refresh(unruled)
	require.Len(t, masked.CopiedUnchanged, 2,
		"precondition: both columns must be ones no rule covered, or this proves nothing")
	require.ErrorContains(t, err, "vault.private_spot still holds "+verify.DetectorUnreadSensitive,
		"an unmasked secret the scanner could not read was published")
	var refused []string
	for _, f := range report.Findings {
		require.Equal(t, verify.DetectorUnreadSensitive, f.Detector)
		refused = append(refused, f.Column)
	}
	require.Equal(t, []string{"private_spot", "signing_key"}, refused,
		"the binary key and the spatial column must both be refused, each through its own path")
	after, err := p.ListGoldens(ctx)
	require.NoError(t, err)
	require.Len(t, after, len(before), "a refused refresh published a golden")

	// The control: the same data, with a person having decided both columns
	// ship as they are. Ruled, unread is a note, and the golden publishes. If
	// this refused too, the refusal above would be the scan failing on these
	// types rather than the scan knowing nobody decided.
	decided, err := masking.NewRuleSet([]masking.Rule{
		{Column: "id", Transform: "preserve", Why: "A row number."},
		{Column: "signing_key", Transform: "preserve", Why: "A test key, revoked."},
		{Column: "private_spot", Transform: "preserve", Why: "A test point."},
	})
	require.NoError(t, err)
	gv, masked, report, err := refresh(decided)
	require.NoError(t, err)
	require.True(t, gv.Verified)
	require.Empty(t, masked.CopiedUnchanged)
	require.Len(t, report.Unread, 2, "both columns are still unread, and now ruled")
}

// A source address in a form the provider does not read is refused before
// anything is made, rather than taken to mean no source and published empty.
func TestRefresh_RefusesASourceAddressItCannotRead(t *testing.T) {
	server := requireServer(t)
	srcName, src := source(t, server)
	exec(t, src, `CREATE TABLE contacts (id INT PRIMARY KEY, email VARCHAR(255) NOT NULL)`,
		`INSERT INTO contacts VALUES (1, 'a.person@acme-customers.com')`)
	u, err := url.Parse(server.Reveal())
	require.NoError(t, err)

	p, err := mysql.New(mysql.Options{ServerURL: server, Name: "contacts"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	ctx := context.Background()
	before, err := p.Inventory(ctx)
	require.NoError(t, err)

	password, _ := u.User.Password()
	for _, addr := range []string{
		"mariadb://" + u.User.String() + "@" + u.Host + "/" + srcName,
		u.User.Username() + ":" + password + "@tcp(" + u.Host + ")/" + srcName,
	} {
		gv, err := p.RefreshGolden(ctx, provider.GoldenSpec{
			SourceURL: secrets.New(addr), RulesHash: "rules-addr", Provenance: "project:contacts",
			Verify: func(context.Context, secrets.Value) (string, error) { return "clean", nil },
		})
		if err == nil {
			// The failure this test exists for is a published golden, so it
			// removes one rather than leaving it on the server it found.
			t.Cleanup(func() { _ = p.DestroyGolden(context.Background(), gv.ID) })
		}
		require.ErrorContains(t, err, "not a mysql:// URL")
	}
	after, err := p.Inventory(ctx)
	require.NoError(t, err)
	require.Equal(t, len(before), len(after), "a refused source still made something")
}

// A source whose address requires verified TLS and whose server cannot give
// it is refused by name, before anything is made. Required explicitly here,
// because the test server is on loopback, where the default is preferred; a
// host on another machine gets the same requirement with no tls= at all.
func TestRefresh_RefusesASourceThatCannotProveItsCertificate(t *testing.T) {
	server := requireServer(t)
	srcName, src := source(t, server)
	exec(t, src, `CREATE TABLE contacts (id INT PRIMARY KEY)`)
	u, err := url.Parse(server.Reveal())
	require.NoError(t, err)

	p, err := mysql.New(mysql.Options{ServerURL: server, Name: "contacts"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	ctx := context.Background()
	before, err := p.Inventory(ctx)
	require.NoError(t, err)

	gv, err := p.RefreshGolden(ctx, provider.GoldenSpec{
		SourceURL: secrets.New("mysql://" + u.User.String() + "@" + u.Host + "/" + srcName +
			"?tls=true"),
		RulesHash: "rules-tls", Provenance: "project:contacts",
		Verify: func(context.Context, secrets.Value) (string, error) { return "clean", nil },
	})
	if err == nil {
		t.Cleanup(func() { _ = p.DestroyGolden(context.Background(), gv.ID) })
	}
	require.ErrorContains(t, err, "AF-DB-046")
	require.ErrorContains(t, err, u.Hostname())
	password, _ := u.User.Password()
	if password != "" {
		require.NotContains(t, err.Error(), password)
	}
	after, err := p.Inventory(ctx)
	require.NoError(t, err)
	require.Equal(t, len(before), len(after), "a refused source still made something")
}
