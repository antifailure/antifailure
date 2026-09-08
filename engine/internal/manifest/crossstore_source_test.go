package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// source_url_env, which is what makes a second store's SCHEMA readable at all.
//
// Without it the cross store check would compare a declared ClickHouse against
// nothing, report that it had no second store, and be correct and useless. The
// field is a variable NAME rather than a connection string for the reason
// every other credential in this manifest is, and the refusal below is there
// because pasting the string is the mistake somebody actually makes, and that
// mistake commits a production credential.

const withSourcedStore = `
version: 1
name: shop
services:
  - name: web
    port: 3000
database:
  source_url_env: PRODUCTION_DATABASE_URL
datastores:
  - name: events
    engine: clickhouse
    stance: golden
    source_url_env: CLICKHOUSE_URL
`

func TestNormalize_ThePrimaryTakesItsSourceFromTheDatabaseBlock(t *testing.T) {
	t.Parallel()
	m := mustParse(t, withSourcedStore)

	var primary schema.Datastore
	for _, d := range m.Datastores {
		if d.Name == schema.PrimaryDatastore {
			primary = d
		}
	}
	require.Equal(t, "PRODUCTION_DATABASE_URL", primary.SourceURLEnv,
		"a manifest that already names its production database must not have to name it "+
			"a second time, or the cross store check would compare one store against nothing")
}

func TestNormalize_ADeclaredStoreKeepsItsOwnSource(t *testing.T) {
	t.Parallel()
	m := mustParse(t, withSourcedStore)
	for _, d := range m.Datastores {
		if d.Name == "events" {
			require.Equal(t, "CLICKHOUSE_URL", d.SourceURLEnv)
			return
		}
	}
	t.Fatal("the declared datastore is missing from the manifest")
}

// The mistake this refusal exists for, and it is not a typo. Pasting the
// connection string writes a production credential into a file that is
// committed, and a message that only said the value was invalid would send
// somebody looking for a typo in a URL that should not be there.
func TestParse_RefusesAConnectionStringWhereAVariableNameBelongs(t *testing.T) {
	t.Parallel()
	body := `
version: 1
name: shop
services:
  - name: web
    port: 3000
datastores:
  - name: events
    engine: clickhouse
    stance: golden
    source_url_env: https://user:hunter2@clickhouse.example.com:8443/af
`
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, "which is not the name of an environment variable")
	require.Contains(t, msg, "treat it as exposed")
	require.NotContains(t, msg, "hunter2",
		"a refusal that prints the credential back puts it in the terminal, the log and "+
			"the transcript, which is the thing the refusal exists to prevent")
	require.NotContains(t, msg, "clickhouse.example.com")
}

// The control that keeps the refusal above from being a check that says no to
// everything.
func TestParse_AcceptsAnOrdinaryVariableName(t *testing.T) {
	t.Parallel()
	m := mustParse(t, withSourcedStore)
	require.Len(t, m.Datastores, 2)
}

// A plain typo is printed, because a message that hides the actual mistake
// helps nobody, and a short value with no scheme in it is not a credential.
func TestParse_APlainTypoIsQuotedBack(t *testing.T) {
	t.Parallel()
	body := `
version: 1
name: shop
services:
  - name: web
    port: 3000
datastores:
  - name: events
    engine: clickhouse
    stance: golden
    source_url_env: clickhouse-url
`
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, `"clickhouse-url"`,
		"a hyphen is not legal in a variable name and the reader has to see which "+
			"character was wrong")
}

// Two sources for one store is refused rather than one of them silently
// winning, which is the same rule the primary's engine, stance and provider
// already follow.
func TestParse_RefusesAPrimaryWithADifferentSourceVariable(t *testing.T) {
	t.Parallel()
	body := `
version: 1
name: shop
services:
  - name: web
    port: 3000
database:
  source_url_env: PRODUCTION_DATABASE_URL
datastores:
  - name: primary
    engine: postgres
    stance: golden
    source_url_env: OTHER_DATABASE_URL
`
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, "reads OTHER_DATABASE_URL and database.source_url_env names PRODUCTION_DATABASE_URL")
	require.Contains(t, msg, "They are the same store")
}

// The primary's entry is MADE by normalization out of the database: block, so
// a credential pasted into database.source_url_env is refused by the datastore
// check even though nobody wrote a datastores entry. That is the only thing
// refusing it: validate.go says elsewhere that nothing validates a manifest
// against the JSON Schema at parse time, so the pattern the schema carries on
// that field is documentation rather than a gate.
//
// What the message must not do is send them to a key that is not in their
// file. Being told to look at datastores[1].source_url_env, about a value they
// now have to treat as exposed, when what they wrote was database:, is the
// worst possible minute to be given the wrong line.
func TestParse_ACredentialInTheDatabaseBlockIsRefusedAndNamesThatBlock(t *testing.T) {
	t.Parallel()
	body := `
version: 1
name: shop
services:
  - name: web
    port: 3000
database:
  source_url_env: postgres://user:hunter2@db.example.com:5432/shop
`
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, "The database: block gives",
		"the refusal names the field the author wrote, not the entry normalization made")
	require.NotContains(t, msg, "datastores[",
		"a key that is not in their file is not where to send somebody handling an "+
			"exposed credential")
	require.NotContains(t, msg, "hunter2")
	require.NotContains(t, msg, "db.example.com")
	require.Contains(t, msg, "treat it as exposed")
}

// The control, so the branch above is choosing between two fields rather than
// renaming every refusal. A declared store still gets its own path.
func TestParse_ACredentialInADeclaredStoreStillNamesThatStore(t *testing.T) {
	t.Parallel()
	body := `
version: 1
name: shop
services:
  - name: web
    port: 3000
database:
  source_url_env: PRODUCTION_DATABASE_URL
datastores:
  - name: events
    engine: clickhouse
    stance: golden
    source_url_env: https://user:hunter2@clickhouse.example.com:8443/af
`
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, `The datastore "events" gives`)
	require.NotContains(t, msg, "The database: block gives")
}
