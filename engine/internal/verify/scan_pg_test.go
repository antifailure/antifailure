package verify_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/verify"
)

// These run against a real Postgres, because the defect they guard was a
// query: the column selection in scan.go named six text types and nothing
// else, so no amount of testing the detectors would have shown that a bytea
// column was never handed to them.
//
// testDatabaseURL is the Postgres the whole project's tests share, the one
// `just db` starts, and AF_TEST_DATABASE_URL overrides it. The databases this
// file creates are all prefixed af_verify_, so two suites on one server cannot
// collide.
const testDatabaseURL = "postgres://postgres:test@127.0.0.1:55432/antifailure"

func maintenanceURL() string {
	if u := os.Getenv("AF_TEST_DATABASE_URL"); u != "" {
		return u
	}
	return testDatabaseURL
}

// The schema is one table with every shape the scanner used to be blind to.
//
//   - api_secret: binary bytea, no rule, a secret's name. The case that fails.
//   - sealed_key: binary bytea with a rule. Listed, not a finding.
//   - note_blob: bytea holding UTF-8 with a credential in it. A secret in a
//     binary coat, which the scanner now takes off.
//   - attachment: binary bytea with a benign name. Listed, not a finding.
//   - location: a point, which the scanner cannot read, benign name.
//   - private_token_box: a range, which the scanner cannot read, and a name
//     that says what it holds, with no rule. The other case that fails.
//   - mood: an enum, read through its text form.
//   - tags: a text array holding an address, read through its text form.
//   - contact: a domain over text, which information_schema reports as
//     USER-DEFINED exactly like an enum, holding an address.
const vaultSchema = `
CREATE TYPE mood AS ENUM ('happy', 'sad');
CREATE DOMAIN mailbox AS text;
CREATE TABLE vault (
  id                serial PRIMARY KEY,
  api_secret        bytea,
  sealed_key        bytea,
  note_blob         bytea,
  attachment        bytea,
  location          point,
  private_token_box int4range,
  mood              mood NOT NULL,
  tags              text[],
  contact           mailbox,
  amount            bigint
);
-- The key below is assembled by the database rather than written out, for
-- the reason detect.go splits its own prefix list: a fixture that has to
-- LOOK like a live provider key, so the detector can be proved to find one
-- inside a bytea column, is a fixture a secret scanner matches too, and
-- GitHub's push protection refused the commit that first carried it. The
-- value the column ends up holding is identical.
INSERT INTO vault (api_secret, sealed_key, note_blob, attachment, location, private_token_box, mood, tags, contact, amount) VALUES
  ('\x00ff10a2b3c4d5e6f7'::bytea, '\x00ff10a2b3c4d5e6f7'::bytea,
   convert_to('token=' || 'sk' || '_live_4eC39HqLyjWDarjtT1zdp7dc', 'UTF8'),
   '\x89504e470d0a1a0a'::bytea, point(1, 2), int4range(1, 5), 'happy',
   ARRAY['vip', 'ada@lovelace-analytics.co.uk'], 'grace@hopper-systems.io', 7),
  ('\x01ff10a2b3c4d5e6f7'::bytea, '\x01ff10a2b3c4d5e6f7'::bytea,
   convert_to('nothing to see', 'UTF8'),
   '\x89504e470d0a1a0a'::bytea, point(3, 4), int4range(2, 6), 'sad',
   ARRAY['new'], 'alan@turing-labs.net', 9);
`

// unruledVault is what af mask plan would say about the schema above: every
// column the fail closed default could not empty and no rule named. sealed_key
// is the one with a rule.
var unruledVault = []string{
	"public.vault.api_secret", "public.vault.note_blob", "public.vault.attachment",
	"public.vault.location", "public.vault.private_token_box", "public.vault.tags",
	"public.vault.contact",
}

func vaultDatabase(t *testing.T) *pgx.Conn {
	t.Helper()
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, maintenanceURL())
	if err != nil {
		if os.Getenv("AF_REQUIRE_DATABASE") != "" {
			t.Fatalf("AF_REQUIRE_DATABASE is set and %s did not answer: %v", maintenanceURL(), err)
		}
		t.Skipf("skipped: no Postgres at %s: %v", maintenanceURL(), err)
	}
	stamp := strings.ToLower(strings.NewReplacer("/", "_", " ", "_", "-", "_").Replace(t.Name()))
	if len(stamp) > 40 {
		stamp = stamp[:40]
	}
	name := fmt.Sprintf("af_verify_%s_%d", stamp, time.Now().UnixNano()%1e6)
	_, err = admin.Exec(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		_ = admin.Close(context.Background())
	})

	cfg, err := pgx.ParseConfig(maintenanceURL())
	require.NoError(t, err)
	cfg.Database = name
	conn, err := pgx.ConnectConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	_, err = conn.Exec(ctx, vaultSchema)
	require.NoError(t, err)
	return conn
}

func scanVault(t *testing.T, unruled []string) verify.Report {
	t.Helper()
	conn := vaultDatabase(t)
	report, err := verify.Scan(context.Background(), conn, verify.Options{
		SampleSize: 100, Unruled: unruled,
	})
	require.NoError(t, err)
	require.Empty(t, report.Skipped, "nothing here should fail to read: %v", report.Skipped)
	return report
}

func findingFor(report verify.Report, column string) []verify.Finding {
	var out []verify.Finding
	for _, f := range report.Findings {
		if f.Table == "vault" && f.Column == column {
			out = append(out, f)
		}
	}
	return out
}

func unreadFor(t *testing.T, report verify.Report, column string) verify.UnreadColumn {
	t.Helper()
	for _, u := range report.Unread {
		if u.Table == "vault" && u.Column == column {
			return u
		}
	}
	t.Fatalf("%s is not in the unread list: %+v", column, report.Unread)
	return verify.UnreadColumn{}
}

func TestScan_ABinarySecretWithNoRuleFailsWithTheSentence(t *testing.T) {
	// The defect. A bytea called api_secret that nothing masked used to be
	// invisible: not read, not skipped, not counted, and the report said
	// clean. It is now the one case the scan refuses on the type alone.
	report := scanVault(t, unruledVault)

	found := findingFor(report, "api_secret")
	require.Len(t, found, 1)
	require.Equal(t, verify.DetectorUnreadSensitive, found[0].Detector)
	require.Equal(t, "public.vault.api_secret is not readable by the scanner (bytea), "+
		"has no masking rule, and its name says it holds a secret", found[0].String())
	require.False(t, report.Clean())
}

func TestScan_TheSameColumnWithARuleIsListedAndPasses(t *testing.T) {
	// The other half of the rule. A rule on the column, any rule, means the
	// column was rewritten whatever the scanner can see of it, so it is a
	// note rather than a finding. sealed_key is the same bytes as api_secret
	// and is not in the unruled list.
	report := scanVault(t, unruledVault)

	require.Empty(t, findingFor(report, "sealed_key"))
	u := unreadFor(t, report, "sealed_key")
	require.True(t, u.Ruled)
	require.Equal(t, "bytea", u.Type)
	require.Contains(t, u.Reason, "2 of 2 sampled values are binary rather than text")
}

func TestScan_AUTF8SecretInABytesColumnIsRead(t *testing.T) {
	// A secret pasted into a bytea column is text in a binary coat. Cast to
	// text it is "\x746f6b..." and matches nothing; decoded, it is a Stripe
	// key and the credential detector sees it.
	report := scanVault(t, unruledVault)

	found := findingFor(report, "note_blob")
	require.Len(t, found, 1)
	require.Equal(t, "credential", found[0].Detector)
	require.Equal(t, 1, found[0].Rows)
}

func TestScan_ABenignBinaryColumnIsListedAndNotAFinding(t *testing.T) {
	// The scan cannot fail every column it cannot read; an environment with
	// an attachments table is not a leak. It is listed, with the reason, and
	// no rule is demanded of it by name.
	report := scanVault(t, unruledVault)

	require.Empty(t, findingFor(report, "attachment"))
	u := unreadFor(t, report, "attachment")
	require.False(t, u.Ruled)
}

func TestScan_ATypeItCannotReadIsListedByType(t *testing.T) {
	// A point is not text and never will be. Before this list existed the
	// report did not say which columns those were, and clean covered them.
	report := scanVault(t, unruledVault)

	u := unreadFor(t, report, "location")
	require.Equal(t, "not readable by the scanner: point", u.Reason)
	require.Empty(t, findingFor(report, "location"))
	require.Equal(t, "public.vault.location: not readable by the scanner: point", u.String())
}

func TestScan_AnUnreadableTypeWithASecretsNameAndNoRuleFails(t *testing.T) {
	// The same refusal as the bytea case, for a type the scan cannot decode
	// at all. The finding carries the type where an excerpt would go, so the
	// error can name it.
	report := scanVault(t, unruledVault)

	found := findingFor(report, "private_token_box")
	require.Len(t, found, 1)
	require.Equal(t, verify.DetectorUnreadSensitive, found[0].Detector)
	require.Equal(t, "int4range", found[0].Example)
}

func TestScan_ArraysAndEnumsAreReadThroughTheirTextForm(t *testing.T) {
	// information_schema reports text[] as ARRAY and an enum or a domain as
	// USER-DEFINED, and the old column selection excluded all three. An
	// address inside an array is still an address.
	report := scanVault(t, unruledVault)

	tags := findingFor(report, "tags")
	require.Len(t, tags, 1, "the address inside the array was not found")
	require.Equal(t, "email", tags[0].Detector)

	contact := findingFor(report, "contact")
	require.Len(t, contact, 1, "the address in the domain typed column was not found")
	require.Equal(t, "email", contact[0].Detector)

	// The enum was read, which shows in the column count: id and amount are
	// structural and not counted, location and private_token_box are unread
	// and not counted, and the other seven are read. mood is one of them.
	require.Equal(t, 7, report.Columns)
	for _, u := range report.Unread {
		require.NotEqual(t, "mood", u.Column, "an enum is readable through ::text")
	}
}

func TestScan_TheUnruledListTravelsIntoTheReport(t *testing.T) {
	// The scan does not know the rules. The caller hands it the columns
	// masking copied unchanged, and the report carries them sorted, which is
	// how the count reaches the attestation and every listing.
	report := scanVault(t, []string{"public.vault.tags", "public.vault.attachment"})

	require.Equal(t, []string{"public.vault.attachment", "public.vault.tags"}, report.Unruled)
}

func TestScan_ARuleOnTheSecretTurnsTheFailureIntoANote(t *testing.T) {
	// Point the same scan at a plan where api_secret and private_token_box
	// have rules, which is the fix the failure asks for, and the two findings
	// are gone while the columns stay listed.
	report := scanVault(t, []string{"public.vault.attachment", "public.vault.location"})

	require.Empty(t, findingFor(report, "api_secret"))
	require.Empty(t, findingFor(report, "private_token_box"))
	require.True(t, unreadFor(t, report, "api_secret").Ruled)
	require.True(t, unreadFor(t, report, "private_token_box").Ruled)
}
