package pgurl

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/db/managed"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// The failure this file is about.
//
// AF-DB-035 ends "Run: ALTER ROLE {role} CREATEDB", which is the right answer
// on a server you administer and cannot be carried out on most of the managed
// Postgres this provider gets pointed at. Heroku Postgres hands out no
// superuser at all and provisions one database per add on; a Tiger Cloud
// service holds exactly one database and its own troubleshooting page says so.
// On either of them the engine printed a statement the reader has no role to
// run, and the cost of that is not cosmetic: they try it, it fails with a
// permission error, and the tool has spent their time telling them to do
// something impossible instead of telling them the thing that works, which is
// that the vendor is the SOURCE and the host server is somewhere else.

func TestARefusalNamesTheVendorWhenTheGrantIsImpossible(t *testing.T) {
	err := refuseCreateDB("tsdbadmin", "service.project.tsdb.cloud.timescale.com:30477", "PGURL_ADMIN_URL")

	require.True(t, errors.Is(err, aferrors.Coded(aferrors.AFDB037)),
		"a host whose vendor documents that a second database cannot be created gets the "+
			"vendor specific refusal, not the one telling them to run ALTER ROLE. Got: %v", err)

	var coded *aferrors.Error
	require.True(t, aferrors.As(err, &coded))
	require.Contains(t, coded.Error(), "Tiger Cloud",
		"the message names the vendor, because the reader's next question is whether "+
			"this is about their setup or their host")
	require.NotContains(t, coded.NextStep(), "ALTER ROLE",
		"the whole point is that this reader cannot run that statement")
	require.Contains(t, coded.NextStep(), "source_url_env",
		"and the remedy is the one that works: keep the vendor as the source, which "+
			"needs read access only")
	require.Contains(t, coded.NextStep(), "tigerdata.com",
		"with the page the verdict was read from, because a verdict about somebody "+
			"else's product goes stale and a reader has to be able to check it")
	require.Contains(t, coded.NextStep(), "2026-09-12",
		"and the date it was read, for the same reason")
}

func TestARefusalOnAnUnrecognisedServerKeepsTheGeneralRemedy(t *testing.T) {
	// The other side of the assertion, and it is the side that stops the
	// registry making things worse. ALTER ROLE is the CORRECT answer on a
	// Postgres somebody administers, which is most of them, and a change that
	// replaced it everywhere with "point this at a server you administer"
	// would have taken a working instruction away from the majority to give a
	// better one to a minority.
	err := refuseCreateDB("app", "db.internal.example:5432", "PGURL_ADMIN_URL")

	require.True(t, errors.Is(err, aferrors.Coded(aferrors.AFDB035)),
		"an unrecognised host is somebody's own server and the grant is available there")

	var coded *aferrors.Error
	require.True(t, aferrors.As(err, &coded))
	require.Contains(t, coded.NextStep(), "ALTER ROLE app CREATEDB")
	require.Contains(t, coded.NextStep(), "source_url_env",
		"and it also carries the way out for a server with no role that may grant it. "+
			"A Heroku Postgres host is an EC2 name nothing can recognise, so a Heroku user "+
			"reads THIS message, and without the second remedy it tells them to run a "+
			"statement Heroku gives nobody the role to run")
}

func TestAnUnverifiedVendorGetsTheGeneralRemedyRatherThanAGuess(t *testing.T) {
	// Fly Managed Postgres documents creating additional databases through its
	// dashboard and flyctl and says nothing about whether a SQL role carries
	// CREATEDB, so the registry answers Unverified. The refusal has to follow
	// that: telling a Fly user their vendor forbids the grant would be a guess
	// printed as a citation, and unverified is in the registry precisely so
	// that silence never has to be written up as a decision.
	//
	// Prisma Postgres is used here rather than Fly because Fly carries no host
	// suffix to recognise, and what is being checked is the branch where a
	// vendor IS recognised and its verdict is not No.
	err := refuseCreateDB("prisma", "db.prisma.io:5432", "PGURL_ADMIN_URL")

	require.True(t, errors.Is(err, aferrors.Coded(aferrors.AFDB035)),
		"a recognised vendor whose documentation did not answer the question gets the "+
			"general remedy, because the alternative is a refusal that cites a page "+
			"which does not say what the refusal claims it says")
}

// TestTheRoleProbeReachesTheVendorAwareRefusal is the call site proof.
//
// Everything above tests a function, and a function nothing calls is a dead,
// shippable gap that looks like a working feature. The general refusal cannot
// prove the wiring on its own: TestNewRefusesARoleThatCannotCreateDatabases
// stays green if New returns AF-DB-035 directly and never asks which vendor it
// is talking to. So this drives New against a real Postgres with a role that
// really cannot create databases, with the host recognised as Tiger Cloud, and
// requires the vendor refusal to come out of New itself.
func TestTheRoleProbeReachesTheVendorAwareRefusal(t *testing.T) {
	admin := requirePostgres(t)
	ctx := context.Background()
	requireSuperuser(t, admin)

	const role = "af_pgurl_test_vendor_nocreate"
	execAdmin(t, admin, "DROP ROLE IF EXISTS "+role)
	execAdmin(t, admin, fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD 'test' NOCREATEDB", role))
	t.Cleanup(func() { execAdmin(t, admin, "DROP ROLE IF EXISTS "+role) })

	tiger, ok := managed.Recognize("service.project.tsdb.cloud.timescale.com:30477")
	require.True(t, ok, "the fixture vendor has to be one the registry recognises")
	local := HostPortOf(secrets.New(admin))
	saved := recognize
	recognize = func(hostport string) (managed.Vendor, bool) {
		if hostport == local {
			return tiger, true
		}
		return saved(hostport)
	}
	t.Cleanup(func() { recognize = saved })

	_, err := New(ctx, Options{
		AdminURL: secrets.New(withRole(admin, role, "test")),
		Variable: "AF_PGURL_ADMIN_URL",
		Clock:    clock.New(),
	})
	require.Error(t, err, "a role without CREATEDB cannot do any part of this provider's job")
	require.True(t, errors.Is(err, aferrors.Coded(aferrors.AFDB037)),
		"New refused a role without CREATEDB on a host recognised as Tiger Cloud, and the "+
			"refusal was not the vendor one, so the registry is not on the path New takes. "+
			"Got: %v", err)
}
