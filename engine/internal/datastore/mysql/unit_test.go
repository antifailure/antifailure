package mysql

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"testing"

	driver "github.com/go-sql-driver/mysql"

	"github.com/stretchr/testify/require"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/masking"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

func TestParseURL_ReadsWhatAManifestWrites(t *testing.T) {
	t.Parallel()
	s, err := parseURL(secrets.New("mysql://app:s3cret@db.internal:3307/shop"))
	require.NoError(t, err)
	require.Equal(t, "app", s.user)
	require.Equal(t, "s3cret", s.password)
	require.Equal(t, "db.internal:3307", s.addr)
	require.Equal(t, "shop", s.database)
	require.Equal(t, "true", s.tls, "a host on another machine defaults to verified TLS")

	s, err = parseURL(secrets.New("mysql://app@db.internal/shop?tls=true"))
	require.NoError(t, err)
	require.Equal(t, "db.internal:3306", s.addr, "a URL with no port means MySQL's own")
	require.Equal(t, "true", s.tls)
}

// A password is percent encoded in a URL. Handed to the driver as text it
// would end the user part of a DSN at its first @ or /.
func TestParseURL_APasswordWithURLSyntaxInItSurvivesTheRoundTrip(t *testing.T) {
	t.Parallel()
	s, err := parseURL(secrets.New("mysql://app:p%40ss%2Fw%3Ard@127.0.0.1:3306/shop"))
	require.NoError(t, err)
	require.Equal(t, "p@ss/w:rd", s.password)

	back, err := parseURL(s.in("af_env_abc").url())
	require.NoError(t, err)
	require.Equal(t, "p@ss/w:rd", back.password)
	require.Equal(t, "af_env_abc", back.database)
}

func TestParseURL_RefusesWhatIsNotAMySQLAddressWithoutQuotingIt(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{
		"postgres://u:hunter2@h/db",
		"http://u:hunter2@h:8123/db",
		"hunter2",
		"mysql://u:hunter2@h/a/b",
		"mysql://u:hunter2@h/db?tls=maybe",
	} {
		_, err := parseURL(secrets.New(bad))
		require.Error(t, err, bad)
		require.NotContains(t, err.Error(), "hunter2", "an error repeated the credential in %q", bad)
	}
}

func TestDatabaseOf(t *testing.T) {
	t.Parallel()
	require.Equal(t, "shop", DatabaseOf(secrets.New("mysql://u:p@h/shop")))
	require.Equal(t, "", DatabaseOf(secrets.New("mysql://u:p@h/")))
	require.Equal(t, "", DatabaseOf(secrets.New("not a url")))
}

// Masking derives every column's key from schema.table.column. A golden
// candidate is a new database each refresh, so the table is described under
// the source's name, and a foreign key into the candidate itself is renamed
// with it so the planner still sees both sides of the join.
func TestMaskingTable_IsNamedForTheSourceAndKeepsItsForeignKeys(t *testing.T) {
	t.Parallel()
	tbl := table{
		name: "orders",
		columns: []column{
			{name: "id", typ: "bigint", unique: true},
			{name: "customer_id", typ: "bigint", foreignKey: "af_cand_1.customers.id"},
			{name: "partner_id", typ: "bigint", foreignKey: "elsewhere.partners.id"},
			{name: "total_cents", typ: "bigint", generated: true},
		},
		primaryKey: []string{"id"},
	}
	mt := tbl.maskingTable("shop", "af_cand_1")
	require.Equal(t, "shop", mt.Schema)
	require.Equal(t, "mysql", mt.Engine)
	require.Equal(t, "shop.customers.id", mt.ColumnNamed("customer_id").ForeignKey)
	require.Equal(t, "elsewhere.partners.id", mt.ColumnNamed("partner_id").ForeignKey,
		"a foreign key into another database is that database's")
	require.True(t, mt.ColumnNamed("total_cents").Generated)
	require.Len(t, tbl.writableColumns(), 3, "a generated column is never written")
}

func TestRewritesKey(t *testing.T) {
	t.Parallel()
	tp := masking.TablePlan{
		OrderBy: []string{"id"},
		Columns: []masking.Assignment{{Column: masking.ColumnInfo{Name: "email"}}},
	}
	require.False(t, rewritesKey(tp))
	tp.Columns = append(tp.Columns, masking.Assignment{Column: masking.ColumnInfo{Name: "id"}})
	require.True(t, rewritesKey(tp))
}

func TestCapabilities_DeclareACopyRatherThanSharedStorage(t *testing.T) {
	t.Parallel()
	p, err := New(Options{ServerURL: secrets.New("mysql://root:x@127.0.0.1:1/")})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	require.Equal(t, provider.DatastoreCaps{
		Engine: "mysql", Branching: true, Golden: true, CopyOnWrite: false,
	}, p.Capabilities())
	require.Equal(t, "mysql", p.Name())
}

// Masking knows a candidate's tables by the source's name and the scan reads
// them under the candidate's. What masking reports as copied unchanged is what
// the scan matches against, so it is named the scan's way: under the source's
// name it matched nothing and every unruled column counted as ruled.
func TestUnruledNames_AreTheDatabaseTheScanReads(t *testing.T) {
	t.Parallel()
	tbl := table{
		name: "vault",
		columns: []column{
			{name: "id", typ: "int", unique: true},
			{name: "signing_key", typ: "varbinary(32)"},
		},
		primaryKey: []string{"id"},
	}
	tables := []masking.Table{tbl.maskingTable("shop", "af_cand_1")}
	rules, err := masking.NewRuleSet(nil)
	require.NoError(t, err)
	plan := masking.BuildPlan(tables, rules.Assign(tables), "h")
	require.Contains(t, plan.CopiedUnchangedNames(), "shop.vault.signing_key",
		"precondition: the plan names the column by the source, which the scan never sees")
	require.Equal(t, []string{"af_cand_1.vault.signing_key"}, unruledNames(plan, "af_cand_1"))
}

// The scan refuses an unruled name it could never match rather than letting
// it fall through as a ruled column.
func TestUnruledUnder_RefusesANameUnderAnotherDatabase(t *testing.T) {
	t.Parallel()
	require.NoError(t, unruledUnder("af_cand_1", nil))
	require.NoError(t, unruledUnder("af_cand_1", []string{"af_cand_1.vault.signing_key"}))
	for _, bad := range []string{
		"shop.vault.signing_key",
		"af_cand_10.vault.signing_key",
		"af_cand_1.signing_key",
	} {
		err := unruledUnder("af_cand_1", []string{"af_cand_1.vault.id", bad})
		require.ErrorContains(t, err, bad)
	}
}

// A source in a form this does not read is refused, not taken for no source:
// taken for none, a real production produced an empty golden that verified
// clean. No address, and the conformance suite's placeholder, are none.
func TestOpenSource_RefusesAnAddressItCannotReadWithoutQuotingIt(t *testing.T) {
	t.Parallel()
	p := &Provider{}
	for _, none := range []string{"", "  ", "antifailure://conformance/source"} {
		src, err := p.openSource(context.Background(), secrets.New(none))
		require.NoError(t, err, none)
		require.Nil(t, src, none)
	}
	for _, bad := range []string{
		"mariadb://u:hunter2@db.internal:3306/shop",
		"jdbc:mysql://db.internal:3306/shop?password=hunter2",
		"u:hunter2@tcp(db.internal:3306)/shop",
		"postgres://u:hunter2@db.internal/shop",
	} {
		src, err := p.openSource(context.Background(), secrets.New(bad))
		require.Nil(t, src, bad)
		require.ErrorContains(t, err, "not a mysql:// URL", bad)
		require.NotContains(t, err.Error(), "hunter2", "an error repeated the credential in %q", bad)
	}
	_, err := p.openSource(context.Background(), secrets.New("mysql://u:hunter2@db.internal:3306/"))
	require.ErrorContains(t, err, "names no database")
}

// A source on another machine carries production's credential and unmasked
// rows, so with no tls= it is verified TLS. preferred would accept a
// plaintext downgrade and any certificate.
func TestParseURL_AHostOnAnotherMachineDefaultsToVerifiedTLS(t *testing.T) {
	t.Parallel()
	for _, host := range []string{
		"db.internal", "prod.example.com:3307", "10.0.0.5", "[2001:db8::1]:3306",
		"localhost.example.com", "128.0.0.1",
		// Names that resolve, so whoever controls the resolution decides
		// where they point: never the weaker default on a source.
		"af-mysql:3306", "x.localhost", "db.localhost:3306", "localhost.",
	} {
		s, err := parseURL(secrets.New("mysql://app:pw@" + host + "/shop"))
		require.NoError(t, err, host)
		require.Equal(t, "true", s.tls, host)
		require.False(t, s.tlsStated, host)
		// And it reads back as the same mode from the address it renders.
		back, err := parseURL(s.url())
		require.NoError(t, err)
		require.Equal(t, "true", back.tls, host)
	}
}

// A weaker mode written in the address is the author's stated choice.
func TestParseURL_AnExplicitOptOutIsHonoured(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"preferred", "skip-verify", "false", "true"} {
		s, err := parseURL(secrets.New("mysql://app:pw@db.internal/shop?tls=" + mode))
		require.NoError(t, err, mode)
		require.Equal(t, mode, s.tls)
		require.True(t, s.tlsStated, mode)
		back, err := parseURL(s.url())
		require.NoError(t, err)
		require.Equal(t, mode, back.tls, "the rendered address lost the stated mode %s", mode)
	}
}

// A server on this machine, or the managed store server, usually has no
// certificate a verifier would accept, and nothing between here and it is
// another network, so it defaults to preferred.
func TestParseURL_LoopbackDefaultsToPreferred(t *testing.T) {
	t.Parallel()
	for _, host := range []string{
		"127.0.0.1:3306", "127.5.5.5", "[::1]:3306", "localhost", "LOCALHOST:3306",
		"[::ffff:127.0.0.1]:3306",
	} {
		s, err := parseURL(secrets.New("mysql://root:pw@" + host + "/"))
		require.NoError(t, err, host)
		require.Equal(t, "preferred", s.tls, host)
		require.NotContains(t, s.url().Reveal(), "tls=",
			"a default is not written into the branch address an application is handed")
	}
	s, err := parseURL(secrets.New("mysql://root:pw@127.0.0.1/?tls=true"))
	require.NoError(t, err)
	require.Equal(t, "true", s.tls, "loopback is a default, not a ceiling")
}

// A verified connection that fails is AF-DB-046, naming the host and the way
// out and never the credential. Anything else passes through unchanged.
func TestRefuseUnverified_NamesTheHostAndTheOptOut(t *testing.T) {
	t.Parallel()
	s, err := parseURL(secrets.New("mysql://app:hunter2@db.internal/shop"))
	require.NoError(t, err)
	for _, cause := range []error{
		fmt.Errorf("reaching the source: %w", driver.ErrNoTLS),
		fmt.Errorf("reaching the source: %w", x509.UnknownAuthorityError{}),
		errors.New("tls: first record does not look like a TLS handshake"),
	} {
		got := s.refuseUnverified(cause)
		require.ErrorContains(t, got, "AF-DB-046", cause.Error())
		require.ErrorContains(t, got, "db.internal")
		var coded *aferrors.Error
		require.ErrorAs(t, got, &coded)
		require.Contains(t, coded.NextStep(), "?tls=preferred")
		require.NotContains(t, got.Error(), "hunter2")
	}
	plain := errors.New("dial tcp: connection refused")
	require.Same(t, plain, s.refuseUnverified(plain))

	weak, err := parseURL(secrets.New("mysql://app:pw@db.internal/shop?tls=skip-verify"))
	require.NoError(t, err)
	tlsErr := fmt.Errorf("x: %w", driver.ErrNoTLS)
	require.Same(t, tlsErr, weak.refuseUnverified(tlsErr),
		"a mode the author chose is not reported as one chosen for them")
}

// af explain says which mode a source uses, and that a weak one exposes the
// credential, without ever quoting the address.
func TestTLSMode_SaysWhatTheWeakModesExpose(t *testing.T) {
	t.Parallel()
	mode, stated, sentence, ok := TLSMode(secrets.New("mysql://app:hunter2@db.internal/shop"))
	require.True(t, ok)
	require.Equal(t, "true", mode)
	require.False(t, stated)
	require.Contains(t, sentence, "the default for db.internal")
	for _, weak := range []string{"preferred", "skip-verify", "false"} {
		mode, stated, sentence, ok := TLSMode(secrets.New(
			"mysql://app:hunter2@db.internal/shop?tls=" + weak))
		require.True(t, ok)
		require.Equal(t, weak, mode)
		require.True(t, stated)
		require.Contains(t, sentence, "read the credential", weak)
		require.NotContains(t, sentence, "hunter2")
	}
	_, _, _, ok = TLSMode(secrets.New("mariadb://app:hunter2@db.internal/shop"))
	require.False(t, ok)
}

// The managed server's container name is trusted on the provider's own
// connection to it, and only there: the same name in a source address is
// verified, which the test above holds.
func TestNew_TheManagedServerByNameKeepsPreferred(t *testing.T) {
	t.Parallel()
	p, err := New(Options{ServerURL: secrets.New("mysql://root:x@" + LocalServerName + ":3306/")})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	require.Equal(t, "preferred", p.server.tls)

	stated, err := New(Options{ServerURL: secrets.New("mysql://root:x@" + LocalServerName +
		":3306/?tls=true")})
	require.NoError(t, err)
	t.Cleanup(func() { _ = stated.Close() })
	require.Equal(t, "true", stated.server.tls, "a mode the address states is still honoured")
}
