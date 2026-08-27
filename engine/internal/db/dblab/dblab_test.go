package dblab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

func testProvider(t *testing.T, h http.Handler) *Provider {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	p, err := New(Options{
		Endpoint:     srv.URL,
		Token:        secrets.New("test-token"),
		PollInterval: time.Millisecond,
		PollTimeout:  2 * time.Second,
	})
	require.NoError(t, err)
	return p
}

func TestNewRefusesConfigurationItCannotWorkWithout(t *testing.T) {
	_, err := New(Options{Token: secrets.New("t")})
	require.ErrorContains(t, err, "endpoint is required")

	_, err = New(Options{Endpoint: "http://127.0.0.1:2345"})
	require.ErrorContains(t, err, "verification token is required")
}

// The capability set is the contract the conformance suite reads to decide
// which behaviours to run. Declaring one this provider does not have makes the
// suite run a behaviour it should skip, and pass it for the wrong reason.
func TestCapabilitiesSayWhatTheEngineActuallyDoes(t *testing.T) {
	p, err := New(Options{Endpoint: "http://127.0.0.1:2345", Token: secrets.New("t")})
	require.NoError(t, err)
	caps := p.Capabilities()

	require.True(t, caps.Branching)
	require.True(t, caps.Reset, "the engine resets a clone to a snapshot in place")
	require.True(t, caps.CopyOnWrite, "a clone is a ZFS clone of the golden's dataset")
	require.False(t, caps.ProviderMasking, "the engine's rules are the single implementation of masking")
	require.False(t, caps.PooledEndpoints, "a clone is a plain Postgres container with one port")
	require.Positive(t, caps.ExpectedBranchLatency)
	require.NotEmpty(t, caps.SupportedVersions)
}

// A pooled connection string must be refused rather than answered with the
// direct one, which is what would make the suite's pooled behaviour pass while
// the capability is false.
func TestPooledIsRefusedRatherThanFaked(t *testing.T) {
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	_, err := p.ConnString(context.Background(),
		provider.Branch{EnvID: "env_1"}, provider.ConnPooled)
	require.ErrorIs(t, err, provider.ErrUnsupported)
}

// ---------------------------------------------------------------------------
// Golden metadata
// ---------------------------------------------------------------------------

func TestGoldenMetadataRoundTrips(t *testing.T) {
	at := time.Date(2026, 8, 27, 1, 2, 3, 0, time.UTC)
	msg := encodeMeta(meta{
		Version: "gv_20260827010203_abcdef12", RulesHash: "abcdef12",
		CreatedAt: at.Format(time.RFC3339), Verified: true,
		AttestationSHA256: sha256Hex(`{"scanner":"x"}`),
	})
	got, ours := decodeMeta(msg)
	require.True(t, ours)
	require.Equal(t, "gv_20260827010203_abcdef12", got.Version)
	require.Equal(t, "abcdef12", got.RulesHash)
	require.True(t, got.Verified)
	require.True(t, got.createdAt(time.Time{}).Equal(at))
	require.Equal(t, metaVersion, got.Antifailure)
}

// A Database Lab Engine is shared. Its own retrieval writes snapshots, and
// people commit clones by hand with a note. Reading one of those as a golden
// would hand somebody a branch of unmasked production, which is the one thing
// the product promises cannot happen.
func TestOnlyOurOwnCommitMessagesAreGoldens(t *testing.T) {
	for _, message := range []string{
		"",
		"-",
		"initial commit",
		"fixed the thing",
		`{"some":"other json"}`,
		`{"antifailure":1}`,          // no version
		`{"version":"gv_x"}`,         // no marker
		`not json {"antifailure":1}`, // marker present but the message is prose
	} {
		t.Run(fmt.Sprintf("%q", message), func(t *testing.T) {
			_, ours := decodeMeta(message)
			require.False(t, ours, "a snapshot with this message was read as a golden")
		})
	}
}

func TestAGoldenWithAnUnreadableCreationTimeFallsBackRatherThanVanishing(t *testing.T) {
	m, ours := decodeMeta(`{"antifailure":1,"version":"gv_x","created_at":"tuesday"}`)
	require.True(t, ours)
	fallback := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	require.True(t, m.createdAt(fallback).Equal(fallback))
}

// ---------------------------------------------------------------------------
// Identifiers, hosts and passwords
// ---------------------------------------------------------------------------

// The engine validates clone identifiers against this expression and refuses
// anything else with a 400 that reads like a usage error.
var engineCloneID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

func TestEveryCloneIdentifierIsOneTheEngineAccepts(t *testing.T) {
	for _, envID := range []string{
		"env_conformance00001",
		"env-with-hyphens",
		"env.with.dots",
		"env with spaces",
		"env/with/slashes",
		"env@with:punctuation",
		"ENV_UPPER",
		"9starts-with-a-digit",
		"",
	} {
		t.Run(fmt.Sprintf("%q", envID), func(t *testing.T) {
			for _, id := range []string{PrefixEnv + cloneSafe(envID), PrefixCandidate + cloneSafe(envID)} {
				require.True(t, engineCloneID.MatchString(id),
					"the engine would refuse the clone identifier %q", id)
			}
		})
	}
}

// The identifier is derived rather than generated so that a retry after a
// crash finds what the previous attempt created without the journal.
func TestTheCloneIdentifierIsDerivedFromTheEnvironment(t *testing.T) {
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	a := p.cloneID(provider.Branch{EnvID: "env_1", ProviderRef: "stale-reference"})
	b := p.cloneID(provider.Branch{EnvID: "env_1"})
	require.Equal(t, a, b)
	require.Equal(t, PrefixEnv+"env_1", a)
}

// The engine reports a clone's host from its own point of view and binds
// clones to loopback by default. An engine on another machine reports
// 127.0.0.1, and connecting there from here reaches this machine.
func TestALoopbackHostIsReplacedByOneThatReachesTheEngine(t *testing.T) {
	p, err := New(Options{Endpoint: "http://dblab.internal:2345", Token: secrets.New("t")})
	require.NoError(t, err)

	for _, reported := range []string{"", "127.0.0.1", "localhost", "0.0.0.0", "::1"} {
		require.Equal(t, "dblab.internal", p.reachableHost(reported),
			"a clone reported at %q would have been connected to on this machine", reported)
	}
	// A host the engine reports that is not loopback is the engine's own
	// answer and is believed, because it is the only party that knows.
	require.Equal(t, "10.0.0.7", p.reachableHost("10.0.0.7"))
}

// The engine records a clone's user, database and owner and deliberately not
// its password, so GET /clone always answers with an empty one. A provider
// that remembered it in memory would work until the process exited, and af up
// and af test are separate processes.
func TestThePasswordIsDerivedAndSurvivesANewProcess(t *testing.T) {
	build := func() *Provider {
		p, err := New(Options{Endpoint: "http://127.0.0.1:2345", Token: secrets.New("the-token")})
		require.NoError(t, err)
		return p
	}
	first, second := build(), build()
	require.Equal(t, first.derivedPassword("af-env-x"), second.derivedPassword("af-env-x"),
		"a fresh provider derived a different password, so the connection string would break")

	require.NotEqual(t, first.derivedPassword("af-env-x"), first.derivedPassword("af-env-y"),
		"two clones share a password")

	other, err := New(Options{Endpoint: "http://127.0.0.1:2345", Token: secrets.New("another-token")})
	require.NoError(t, err)
	require.NotEqual(t, first.derivedPassword("af-env-x"), other.derivedPassword("af-env-x"),
		"the password does not depend on the token")

	// The engine refuses a clone password below sixty bits of entropy.
	require.Len(t, first.derivedPassword("af-env-x"), 32)
	require.NotContains(t, first.derivedPassword("af-env-x"), "the-token")
}

// The connection string is what reaches an application's environment. It has
// to be a secret in the type system, not by convention.
func TestTheConnectionStringIsRedactedAndEscaped(t *testing.T) {
	p, err := New(Options{
		Endpoint: "http://127.0.0.1:2345", Token: secrets.New("t"),
		User: "user with spaces", Database: "appdb",
	})
	require.NoError(t, err)

	conn := p.connString(Clone{ID: "af-env-x", DB: Database{Host: "127.0.0.1", Port: "6000"}})
	rendered := fmt.Sprintf("%v %s", conn, conn)
	require.NotContains(t, rendered, "postgres", "the connection string rendered in the clear")

	revealed := conn.Reveal()
	require.True(t, strings.HasPrefix(revealed, "postgres://"))
	require.Contains(t, revealed, "@127.0.0.1:6000/appdb")
	require.Contains(t, revealed, "user%20with%20spaces",
		"a username with a space was concatenated rather than escaped")
}

// ---------------------------------------------------------------------------
// Refusals
// ---------------------------------------------------------------------------

// snapshotsHandler answers the two read calls the provider makes, so a test
// can describe an engine by what it holds rather than by routing.
func snapshotsHandler(snapshots []Snapshot, clones []Clone) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/snapshots":
			_ = json.NewEncoder(w).Encode(snapshots)
		case r.URL.Path == "/clones":
			_ = json.NewEncoder(w).Encode(clones)
		case strings.HasPrefix(r.URL.Path, "/clone/"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"NOT_FOUND","message":"clone not found"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func goldenSnapshot(id, version string) Snapshot {
	return Snapshot{ID: id, Message: encodeMeta(meta{Version: version, Verified: true})}
}

func TestBranchingAVersionTheEngineDoesNotHoldIsAFDB004(t *testing.T) {
	p := testProvider(t, snapshotsHandler(nil, nil))
	_, err := p.Branch(context.Background(), "gv_19700101000000_deadbeef", "env_1")
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFDB004))
}

// This is the product's central promise, and on this provider it is reachable
// in a way it is not on the others: a Database Lab Engine is full of snapshots
// its own retrieval made, holding production data that nothing has masked, and
// they are named in the engine's own interface where somebody can copy one.
func TestBranchingARawEngineSnapshotIsRefusedAsUnverified(t *testing.T) {
	raw := Snapshot{ID: "dblab_pool/dataset_1/main/20260101@20260101", Message: "initial commit"}
	p := testProvider(t, snapshotsHandler([]Snapshot{raw}, nil))

	_, err := p.Branch(context.Background(), raw.ID, "env_1")
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFMSK001),
		"a snapshot of unmasked production was branched")
}

// The metadata says verified and is read rather than assumed, so a snapshot
// written by a version of this code that published before verifying would be
// caught rather than trusted.
func TestAGoldenMarkedUnverifiedIsRefused(t *testing.T) {
	s := Snapshot{ID: "pool/x@1", Message: encodeMeta(meta{Version: "gv_x", Verified: false})}
	p := testProvider(t, snapshotsHandler([]Snapshot{s}, nil))

	_, err := p.Branch(context.Background(), "gv_x", "env_1")
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFMSK001))
}

func TestCollectingAGoldenABranchCameFromIsRefused(t *testing.T) {
	g := goldenSnapshot("pool/x@1", "gv_x")
	live := Clone{ID: PrefixEnv + "env_1", Snapshot: &Snapshot{ID: g.ID}, Status: Status{Code: StatusOK}}
	p := testProvider(t, snapshotsHandler([]Snapshot{g}, []Clone{live}))

	err := p.DestroyGolden(context.Background(), "gv_x")
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFDB005))
}

// A clone the engine is already deleting is not a reason to refuse a
// collection: the operator would be told the version is referenced by
// something that is on its way out, and have no way to act on it.
func TestACloneBeingDeletedDoesNotHoldAGoldenHostage(t *testing.T) {
	g := goldenSnapshot("pool/x@1", "gv_x")
	going := Clone{ID: PrefixEnv + "env_1", Snapshot: &Snapshot{ID: g.ID}, Status: Status{Code: StatusDeleting}}
	p := testProvider(t, snapshotsHandler([]Snapshot{g}, []Clone{going}))

	n, err := p.cloneCount(context.Background(), g.ID)
	require.NoError(t, err)
	require.Zero(t, n)
}

func TestCollectingAGoldenThatIsAlreadyGoneSucceeds(t *testing.T) {
	p := testProvider(t, snapshotsHandler(nil, nil))
	require.NoError(t, p.DestroyGolden(context.Background(), "gv_missing"))
}

func TestRefreshingAnUnsupportedPostgresVersionIsAFDB003(t *testing.T) {
	p := testProvider(t, snapshotsHandler(nil, nil))
	_, err := p.RefreshGolden(context.Background(), provider.GoldenSpec{Version: 11})
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFDB003))
}

// A freshly started engine has not finished its first data retrieval, so this
// is reachable in the first minute of every install.
func TestRefreshingWithNoBaseSnapshotIsAFDB009(t *testing.T) {
	p := testProvider(t, snapshotsHandler(nil, nil))
	_, err := p.RefreshGolden(context.Background(), provider.GoldenSpec{Version: 17})
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFDB009))
}

// The second refresh must not start from the first refresh's golden. If it
// did, masking would run over already masked data, the seed would collide with
// the schema it created last time, and every golden after the first would be a
// descendant of one rather than an independent copy of production.
func TestARefreshNeverUsesAGoldenAsItsBase(t *testing.T) {
	older := Snapshot{
		ID: "pool/raw@1", Message: "retrieved",
		CreatedAt: Time{time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)},
	}
	newerGolden := goldenSnapshot("pool/gv@2", "gv_x")
	newerGolden.CreatedAt = Time{time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)}

	p := testProvider(t, snapshotsHandler([]Snapshot{newerGolden, older}, nil))
	base, err := p.baseSnapshot(context.Background())
	require.NoError(t, err)
	require.Equal(t, older.ID, base.ID,
		"the refresh would have cloned its own previous golden")
}

func TestAnEngineHoldingOnlyGoldensSaysSoRatherThanCloningOne(t *testing.T) {
	p := testProvider(t, snapshotsHandler([]Snapshot{goldenSnapshot("pool/gv@1", "gv_x")}, nil))
	_, err := p.baseSnapshot(context.Background())
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFDB009))
	require.ErrorContains(t, err, "Antifailure goldens")
}

// A wrong token is one of the two most likely first-run failures, and a bare
// 401 from the engine says nothing about which value to fix.
func TestARejectedTokenBecomesAFDB008(t *testing.T) {
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":"UNAUTHORIZED","message":"Check your verification token."}`))
	}))
	_, err := p.ListGoldens(context.Background())
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFDB008))
}

// The listing is what af golden list reads and what retention acts on.
func TestListGoldensReportsOnlyOursAndNewestFirst(t *testing.T) {
	p := testProvider(t, snapshotsHandler([]Snapshot{
		goldenSnapshot("pool/a@1", "gv_20260101000000_aaaaaaaa"),
		{ID: "pool/raw@2", Message: "somebody's commit"},
		goldenSnapshot("pool/c@3", "gv_20260827000000_cccccccc"),
	}, nil))

	got, err := p.ListGoldens(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 2, "a snapshot that is not a golden appeared in the golden listing")
	require.Equal(t, "gv_20260827000000_cccccccc", got[0].ID)
	require.Equal(t, "gv_20260101000000_aaaaaaaa", got[1].ID)
	require.Equal(t, "pool/c@3", got[0].ProviderRef)
}

// A Database Lab Engine is shared. Reporting a colleague's clone as a leak is
// how a leak report becomes something people learn to ignore.
func TestInventoryReportsOnlyWhatThisProviderMade(t *testing.T) {
	p := testProvider(t, snapshotsHandler(
		[]Snapshot{goldenSnapshot("pool/a@1", "gv_a"), {ID: "pool/raw@2", Message: "-"}},
		[]Clone{
			{ID: PrefixEnv + "env_1", Status: Status{Code: StatusOK}},
			{ID: PrefixCandidate + "gv_a", Status: Status{Code: StatusOK}},
			{ID: "nikolays-experiment", Status: Status{Code: StatusOK}},
		}))

	got, err := p.Inventory(context.Background())
	require.NoError(t, err)

	kinds := map[string]string{}
	for _, r := range got {
		kinds[r.ID] = r.Kind
		require.NotEmpty(t, r.Kind, "an inventory entry with no kind cannot describe a leak")
	}
	require.Equal(t, map[string]string{
		PrefixEnv + "env_1":      "clone/branch",
		PrefixCandidate + "gv_a": "clone/candidate",
		"pool/a@1":               "snapshot/golden",
	}, kinds)
}

// Health is checked during teardown. Erroring on a branch that is genuinely
// gone would make a successful teardown look like a failure.
func TestHealthReportsAMissingCloneAsUnreachableRatherThanErroring(t *testing.T) {
	p := testProvider(t, snapshotsHandler(nil, nil))
	got, err := p.Health(context.Background(), provider.Branch{EnvID: "env_gone", From: "gv_x"})
	require.NoError(t, err)
	require.False(t, got.Reachable)
	require.NotEmpty(t, got.Detail)
}

// Teardown retries after a crash and after a partial failure.
func TestDestroyingABranchThatIsAlreadyGoneSucceeds(t *testing.T) {
	p := testProvider(t, snapshotsHandler(nil, nil))
	require.NoError(t, p.Destroy(context.Background(),
		provider.Branch{EnvID: "env_1", ProviderRef: PrefixEnv + "env_1"}))
}

func TestDestroyRefusesABranchItCannotName(t *testing.T) {
	p := testProvider(t, snapshotsHandler(nil, nil))
	require.ErrorContains(t, p.Destroy(context.Background(), provider.Branch{}),
		"neither an identifier nor an environment")
}

// A clone leaves the engine's API before its storage is released: the engine
// forgets the clone, then destroys the ZFS dataset. A snapshot cannot be
// destroyed while a dataset cloned from it survives, so for the seconds in
// between, collecting a golden fails with "dependent datasets".
//
// This is not hypothetical. It made every golden the conformance suite created
// outlive the behaviour that created it, and the leak check at the end
// reported snapshots the suite had genuinely asked to have removed. The
// engine's own log named the dependent: a clone that had already 404'd on the
// API a minute earlier.
func TestCollectingAGoldenWaitsOutADeletedClonesStorage(t *testing.T) {
	var deletes int32
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/snapshots":
			_ = json.NewEncoder(w).Encode([]Snapshot{goldenSnapshot("pool/x@1", "gv_x")})
		case r.URL.Path == "/clones":
			// The API has already forgotten the clone, which is exactly the
			// state that makes this a race rather than a refusal.
			_ = json.NewEncoder(w).Encode([]Clone{})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/snapshot/"):
			if atomic.AddInt32(&deletes, 1) < 3 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"code":"BAD_REQUEST","message":"cannot delete snapshot pool/x@1 ` +
					`because it has dependent datasets: dblab_pool/branch/main/af-env-env_1/r0"}`))
				return
			}
			_, _ = w.Write([]byte(`{"status":"OK","message":"Deleted snapshot"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	p.dependentTeardownWait = 5 * time.Second

	require.NoError(t, p.DestroyGolden(context.Background(), "gv_x"))
	require.GreaterOrEqual(t, atomic.LoadInt32(&deletes), int32(3),
		"the first refusal was taken as final, so the golden would have leaked")
}

// A dependent that never goes away is a real refusal and has to be reported as
// one rather than waited on forever.
func TestAGoldenWithAPermanentDependentIsRefusedRatherThanHungOn(t *testing.T) {
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/snapshots":
			_ = json.NewEncoder(w).Encode([]Snapshot{goldenSnapshot("pool/x@1", "gv_x")})
		case r.URL.Path == "/clones":
			_ = json.NewEncoder(w).Encode([]Clone{})
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":"BAD_REQUEST","message":"cannot delete snapshot ` +
				`because it has dependent datasets: somebody-elses-clone"}`))
		}
	}))
	p.dependentTeardownWait = 100 * time.Millisecond

	err := p.DestroyGolden(context.Background(), "gv_x")
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFDB005))
}

// Forcing would delete the dependent datasets along with the snapshot, which
// on a shared engine is somebody else's environment.
func TestCollectingAGoldenNeverForces(t *testing.T) {
	var forced bool
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/snapshots":
			_ = json.NewEncoder(w).Encode([]Snapshot{goldenSnapshot("pool/x@1", "gv_x")})
		case r.URL.Path == "/clones":
			_ = json.NewEncoder(w).Encode([]Clone{})
		default:
			if r.URL.Query().Get("force") != "" {
				forced = true
			}
			_, _ = w.Write([]byte(`{"status":"OK"}`))
		}
	}))
	require.NoError(t, p.DestroyGolden(context.Background(), "gv_x"))
	require.False(t, forced, "the golden was collected with force, which takes dependent datasets with it")
}

// Reset names the golden the branch came from, never "latest".
//
// The ordering this guards is real and quiet: a golden refresh that happens
// while an environment is up makes a newer snapshot the latest one, and a
// reset to "latest" would move that environment onto data it never branched
// from. Nothing would report it; the environment would simply start disagreeing
// with the test run that created it.
func TestResetTargetsTheGoldenTheBranchCameFromAndNotTheLatest(t *testing.T) {
	older := goldenSnapshot("pool/older@1", "gv_older")
	older.CreatedAt = Time{time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)}
	newer := goldenSnapshot("pool/newer@2", "gv_newer")
	newer.CreatedAt = Time{time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)}

	var body map[string]any
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/snapshots":
			_ = json.NewEncoder(w).Encode([]Snapshot{newer, older})
		case strings.HasSuffix(r.URL.Path, "/reset"):
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.WriteHeader(http.StatusOK)
		default:
			_, _ = w.Write([]byte(`{"id":"af-env-env_1","status":{"code":"OK"},"db":{"port":"6000"}}`))
		}
	}))

	require.NoError(t, p.Reset(context.Background(),
		provider.Branch{EnvID: "env_1", From: "gv_older", ProviderRef: PrefixEnv + "env_1"}))

	require.Equal(t, older.ID, body["snapshotID"],
		"the reset targeted %v, so an environment would land on a golden it never branched from", body["snapshotID"])
	require.Equal(t, false, body["latest"])
}

// Resetting to a golden the engine no longer holds is a missing version, not a
// reset to whatever happens to be newest.
func TestResettingToACollectedGoldenIsAFDB004(t *testing.T) {
	p := testProvider(t, snapshotsHandler(nil, nil))
	err := p.Reset(context.Background(), provider.Branch{EnvID: "env_1", From: "gv_gone"})
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFDB004))
}
