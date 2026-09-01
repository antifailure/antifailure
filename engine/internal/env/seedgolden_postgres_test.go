package env

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/internal/verify"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The masking guarantee on the seeded golden path, against a real Postgres.
//
// A fake would agree with whatever the hooks said, and what the hooks said was
// the problem: Verify returned the literal {"rows":0} without opening a
// connection, and the docker provider reports Verified as `spec.Verify != nil`,
// which is true. So a database nothing had read was published as verified and
// could be branched, while the README says a scanner reads back every column of
// every table and signs an attestation and that an unverified golden cannot be
// branched, enforced in code rather than in a checklist.
//
// These run against a server or they do not run, for the same reason the
// fidelity suite does. AF_REQUIRE_DATABASE turns the skip into a failure so
// that a package cannot report ok having examined nothing.

const seedGoldenTestDatabaseURL = "postgres://postgres:test@127.0.0.1:55432/antifailure"

// seedCandidate builds an empty database standing in for the golden candidate,
// and returns its URL.
func seedCandidate(t *testing.T) string {
	t.Helper()
	url := seedGoldenTestDatabaseURL
	if u := os.Getenv("AF_TEST_SEED_DATABASE_URL"); u != "" {
		url = u
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		if os.Getenv("AF_REQUIRE_DATABASE") != "" {
			t.Fatalf("AF_REQUIRE_DATABASE is set and there is no usable Postgres: %v", err)
		}
		t.Skipf("no Postgres for the seeded golden hooks: %v", err)
	}
	name := fmt.Sprintf("af_seed_%d", time.Now().UnixNano())
	_, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
	require.NoError(t, err)
	require.NoError(t, admin.Close(ctx))

	own := replaceDatabase(url, name)
	t.Cleanup(func() {
		c := context.WithoutCancel(ctx)
		if a, err := pgx.Connect(c, url); err == nil {
			_, _ = a.Exec(c, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
			_ = a.Close(c)
		}
	})
	return own
}

func seedOrchestrator(t *testing.T, seed string) *Orchestrator {
	t.Helper()
	// The key comes from the environment, so MaskingKey returns before it
	// reaches the state database. What is under test is the two hooks, not
	// where a key is stored.
	o, err := New(Options{
		Root:     t.TempDir(),
		Manifest: &schema.Manifest{Name: "app", Database: &schema.Database{Seed: seed}},
		Branch:   "main",
		Clock:    clock.New(),
		Redactor: redact.New(),
		Getenv: func(k string) string {
			if k == MaskingKeyEnv {
				return "a-project-key-long-enough-to-be-accepted"
			}
			return ""
		},
	})
	require.NoError(t, err)
	return o
}

func seedSpec(t *testing.T, o *Orchestrator, seed string) (spec specHooks) {
	t.Helper()
	key, err := o.MaskingKey(context.Background(), nil)
	require.NoError(t, err)
	rules, hash, err := o.rules()
	require.NoError(t, err)
	// The real digest rather than a literal, because it is what the call site
	// passes and it is signed into the attestation. A seeded golden stamped
	// with a provenance nothing else computes is one selection can never match.
	prov, err := o.provenanceOf()
	require.NoError(t, err)
	s := o.seedGoldenSpec(nil, seed, key, rules, hash, prov.digest())
	require.NotNil(t, s.Mask, "the seeded golden must carry a mask hook")
	require.NotNil(t, s.Verify, "the seeded golden must carry a verify hook")
	require.Equal(t, prov.digest(), s.Provenance,
		"a golden published with no provenance is one pickGolden can never choose")
	return specHooks{mask: s.Mask, verify: s.Verify}
}

type specHooks struct {
	mask   func(context.Context, secrets.Value) error
	verify func(context.Context, secrets.Value) (string, error)
}

// The hook returned a constant. It never opened a connection, so it could not
// have failed, so it could not have refused anything.
func TestSeededGolden_VerifyActuallyOpensAConnection(t *testing.T) {
	o := seedOrchestrator(t, "")
	hooks := seedSpec(t, o, "")

	// A port nothing listens on. {"rows":0} would have come back clean.
	_, err := hooks.verify(context.Background(),
		secrets.New("postgres://postgres:test@127.0.0.1:1/nothing"))
	require.Error(t, err, "the verify hook returned without reaching a database")
}

// A seed of ordinary fake data is published, and the attestation it publishes
// is a real signed one rather than a constant.
func TestSeededGolden_SignsWhatItScanned(t *testing.T) {
	url := seedCandidate(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	require.NoError(t, err)
	_, err = conn.Exec(ctx, `CREATE TABLE customers (id int primary key, email text);
		INSERT INTO customers VALUES (1, 'alice@example.com'), (2, 'bob@example.org');`)
	require.NoError(t, err)
	require.NoError(t, conn.Close(ctx))

	o := seedOrchestrator(t, "")
	hooks := seedSpec(t, o, "")
	att, err := hooks.verify(ctx, secrets.New(url))
	require.NoError(t, err, "example.com and example.org are synthetic, so an ordinary seed must pass")

	// What af fidelity reads back. The constant carried no public key, so
	// fidelity.go:160 told the customer their attestation did not match its own
	// signature and had been changed after it was signed, about a document this
	// product wrote.
	var doc struct {
		PublicKey string `json:"public_key"`
		Signature string `json:"signature"`
		Report    struct {
			Columns int `json:"columns"`
		} `json:"report"`
	}
	require.NoError(t, json.Unmarshal([]byte(att), &doc))
	require.NotEmpty(t, doc.PublicKey, "an attestation with no public key is the one af fidelity calls tampered with")
	require.NotEmpty(t, doc.Signature)
	require.NotEqual(t, `{"rows":0}`, att)
	require.Greater(t, doc.Report.Columns, 0, "the scan read no columns")

	// The exact check af fidelity runs, rather than an inference from the
	// fields being present. {"rows":0} unmarshals into an Attestation without
	// error, so the constant reached this line and failed it, which is what
	// produced the accusation.
	var a verify.Attestation
	require.NoError(t, json.Unmarshal([]byte(att), &a))
	require.True(t, a.Verify(),
		"af fidelity would tell the customer this was changed after it was signed")
}

// The claim the README makes, run rather than described: a seed that is a
// trimmed production export is refused, and an unverified golden is never
// published, so it can never be branched.
func TestSeededGolden_RefusesASeedCarryingRealAddresses(t *testing.T) {
	url := seedCandidate(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	require.NoError(t, err)
	_, err = conn.Exec(ctx, `CREATE TABLE customers (id int primary key, email text);
		INSERT INTO customers VALUES (1, 'daniel.okafor@northwind-logistics.co.uk');`)
	require.NoError(t, err)
	require.NoError(t, conn.Close(ctx))

	o := seedOrchestrator(t, "")
	hooks := seedSpec(t, o, "")
	_, err = hooks.verify(ctx, secrets.New(url))
	require.Error(t, err, "a real address in a seeded golden was published as verified")
	require.Equal(t, aferrors.AFMSK002, codeOf(err))
}
