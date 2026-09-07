package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The datastores list, and the one refusal it exists for.
//
// A silent default is how somebody ends up trusting a blank ClickHouse, so a
// datastore that declares no stance is refused rather than assumed to be
// anything. Everything else in this file is the surrounding rules that keep
// that refusal from being the only thing standing up.

const withStore = `
version: 1
name: shop
services:
  - name: web
    port: 3000
datastores:
  - name: events
    engine: clickhouse
`

func TestParse_RefusesADatastoreWithNoStance(t *testing.T) {
	t.Parallel()
	msg := messages(problems(t, mustFail(t, withStore)))
	require.Contains(t, msg, `The datastore "events" declares no stance.`)
	require.Contains(t, msg, "There is no default")
	require.Contains(t, msg, "blank ClickHouse")
}

func TestParse_RefusesAStanceItDoesNotKnow(t *testing.T) {
	t.Parallel()
	// The near miss, because the value somebody writes for an empty store is
	// as likely to be "none" as "empty", and a stance nothing knows would be
	// carried into the report as a word with no behaviour behind it.
	msg := messages(problems(t, mustFail(t, withStore+"    stance: none\n")))
	require.Contains(t, msg, `The stance "none" is not one this engine knows.`)
	require.Contains(t, msg, "golden, empty, derived or topics_only")
}

func TestParse_AcceptsEveryStanceTheEngineDeclares(t *testing.T) {
	t.Parallel()
	// The membership is asserted before it is iterated, and the mutation pass
	// is why. Looping over AllDatastoreStances and checking each one parses is
	// a tautology: delete a stance from the slice and the loop simply stops
	// testing it, so the check went green against a build that had lost
	// topics_only entirely. Naming the four is what makes a shrinking set red.
	require.Equal(t, []schema.DatastoreStance{
		schema.StanceGolden, schema.StanceEmpty,
		schema.StanceDerived, schema.StanceTopicsOnly,
	}, schema.AllDatastoreStances())

	// The control that makes the two refusals above mean something. A check
	// that says no to everything says nothing, and this is also what holds the
	// closed set to the values the validator will actually take.
	for _, stance := range schema.AllDatastoreStances() {
		stance := stance
		t.Run(string(stance), func(t *testing.T) {
			t.Parallel()
			body := withStore + "    stance: " + string(stance) + "\n"
			switch stance {
			case schema.StanceEmpty:
				body += "    because: a cache is rebuilt from the primary\n"
			case schema.StanceDerived:
				body += "    from: primary\n"
			}
			m := mustParse(t, body)
			require.Equal(t, stance, m.Datastores[0].Stance)
		})
	}
}

func TestParse_RefusesAnEmptyStoreThatDoesNotSayWhy(t *testing.T) {
	t.Parallel()
	// empty is a legitimate answer. An invisible empty is not, and the reason
	// is the only thing that tells a decision apart from an oversight once the
	// environment is running.
	msg := messages(problems(t, mustFail(t, withStore+"    stance: empty\n")))
	require.Contains(t, msg, `The datastore "events" starts empty and does not say why.`)
}

func TestParse_RefusesADerivedStoreWithNoSource(t *testing.T) {
	t.Parallel()
	msg := messages(problems(t, mustFail(t, withStore+"    stance: derived\n")))
	require.Contains(t, msg, `The datastore "events" is derived and does not say what from.`)
}

func TestParse_RefusesADerivedStoreWhoseSourceDoesNotExist(t *testing.T) {
	t.Parallel()
	body := withStore + "    stance: derived\n    from: warehouse\n"
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, `No datastore is named "warehouse".`)
}

func TestParse_RefusesADerivedStoreThatReadsItself(t *testing.T) {
	t.Parallel()
	// Separate from the missing source above, because "events" IS a declared
	// name, so the existence check passes and a store rebuilt from itself
	// would be accepted by it.
	body := withStore + "    stance: derived\n    from: events\n"
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, `The datastore "events" is derived from itself.`)
}

func TestParse_AcceptsADerivedStoreDeclaredAboveItsSource(t *testing.T) {
	t.Parallel()
	// The source check runs after every name is known, because a manifest is
	// allowed to declare the store it reads from further down the file. A
	// single pass would refuse this one and the author would have no idea why
	// reordering fixed it.
	body := `
version: 1
name: shop
services:
  - name: web
    port: 3000
datastores:
  - name: search
    engine: elasticsearch
    stance: derived
    from: events
  - name: events
    engine: clickhouse
    stance: golden
`
	m := mustParse(t, body)
	require.Equal(t, "events", m.Datastores[0].From)
}

func TestParse_RefusesASourceOnAStanceThatIsNotDerived(t *testing.T) {
	t.Parallel()
	// A key that reads as configuration and behaves as decoration is the
	// defect this whole file is about, and from on a golden store is one.
	body := withStore + "    stance: golden\n    from: primary\n"
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, `The datastore "events" names a source and its stance is golden.`)
}

func TestParse_RefusesTwoDatastoresWithOneName(t *testing.T) {
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
  - name: events
    engine: redis
    stance: empty
    because: two names is one too many
`
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, `Two datastores are both named "events".`)
}

func TestParse_RefusesADatastoreWithNoEngine(t *testing.T) {
	t.Parallel()
	body := `
version: 1
name: shop
services:
  - name: web
    port: 3000
datastores:
  - name: events
    stance: golden
`
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, "The datastore names no engine.")
}

func TestParse_RefusesADatastoreNameThatIsNotAHostname(t *testing.T) {
	t.Parallel()
	body := `
version: 1
name: shop
services:
  - name: web
    port: 3000
datastores:
  - name: Events Store
    engine: clickhouse
    stance: golden
`
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, `The datastore name "Events Store" is not usable as a hostname.`)
}

// The normalization, which is the half that has to be invisible.

func TestNormalize_TurnsTheDatabaseIntoTheEntryNamedPrimary(t *testing.T) {
	t.Parallel()
	m := mustParse(t, minimal)
	require.Len(t, m.Datastores, 1)

	p := m.Datastores[0]
	require.Equal(t, schema.PrimaryDatastore, p.Name)
	require.Equal(t, "postgres", p.Engine)
	require.Equal(t, schema.StanceGolden, p.Stance)
	require.Equal(t, string(schema.DBDocker), p.Provider)
}

func TestNormalize_CarriesTheDatabaseProviderIntoThePrimaryEntry(t *testing.T) {
	t.Parallel()
	// Not the default, because a test that only ever sees docker cannot tell
	// the field being copied from it being defaulted.
	body := `
version: 1
name: shop
services:
  - name: web
    port: 3000
database:
  provider: neon
  project: shop
  api_key_env: NEON_API_KEY
`
	m := mustParse(t, body)
	require.Equal(t, "neon", m.Datastores[0].Provider)
}

func TestNormalize_AppendsThePrimaryRatherThanPrependingIt(t *testing.T) {
	t.Parallel()
	// The declared store keeps index 0, which is what every validation message
	// about it points at. Prepending would send an author to the line above
	// the one they wrote.
	body := withStore + "    stance: golden\n"
	m := mustParse(t, body)
	require.Len(t, m.Datastores, 2)
	require.Equal(t, "events", m.Datastores[0].Name)
	require.Equal(t, schema.PrimaryDatastore, m.Datastores[1].Name)
}

func TestParse_RefusesAPrimaryThatDisagreesWithTheDatabaseBlock(t *testing.T) {
	t.Parallel()
	// Overwriting it would be the silent behaviour this change exists to
	// remove: one of the two answers wins and nothing says which.
	body := `
version: 1
name: shop
services:
  - name: web
    port: 3000
database:
  provider: docker
datastores:
  - name: primary
    engine: clickhouse
    stance: empty
    because: this manifest disagrees with itself
    provider: neon
`
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, "The datastore named primary runs clickhouse")
	require.Contains(t, msg, "The datastore named primary declares the stance empty")
	require.Contains(t, msg, "says provider neon and database.provider says docker")
}

func TestNormalize_AcceptsAPrimaryThatAgreesWithTheDatabaseBlock(t *testing.T) {
	t.Parallel()
	// The control for the refusal above, and the case that keeps
	// normalization idempotent: the manifest normalization itself emits looks
	// exactly like this, and it has to parse again.
	body := `
version: 1
name: shop
services:
  - name: web
    port: 3000
database:
  provider: docker
datastores:
  - name: primary
    engine: postgres
    provider: docker
    stance: golden
`
	m := mustParse(t, body)
	require.Len(t, m.Datastores, 1)
	require.Equal(t, schema.PrimaryDatastore, m.Datastores[0].Name)
}
