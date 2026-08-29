package supabase

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// These are the properties a fake can prove. Everything about whether this
// provider agrees with Supabase is decided by the conformance suite against the
// real API, because a fake would only prove that this provider agrees with our
// idea of Supabase. What is here instead is the logic that decides which
// resources are ours, the two step delete, and the handling of responses that
// are awkward to provoke on demand from a real service.

func TestTheProductionBranchIsNeverOurs(t *testing.T) {
	// The first branch anybody creates on a project also registers a row for
	// the production project itself, is_default true, and it appears in every
	// listing from then on. A sweep that iterated branches and deleted would
	// delete it. This is the guard, so this is the test.
	require.False(t, Branch{Name: "main", IsDefault: true}.IsOurs())

	// Even wearing one of our prefixes. Somebody renaming the default branch
	// must not be able to talk this provider into destroying production.
	require.False(t, Branch{Name: PrefixEnv + "anything", IsDefault: true}.IsOurs())

	// Somebody else's branch, made by hand or by a pull request.
	require.False(t, Branch{Name: "feature-login"}.IsOurs())
	require.False(t, Branch{Name: "staging"}.IsOurs())

	require.True(t, Branch{Name: PrefixEnv + "env_1"}.IsOurs())
	require.True(t, Branch{Name: PrefixGolden + "gv_20260101000000_abcd1234"}.IsOurs())
	require.True(t, Branch{Name: PrefixCandidate + "gv_20260101000000_abcd1234"}.IsOurs())
}

func TestAGoldenVersionSurvivesTheRoundTripThroughABranchName(t *testing.T) {
	// Supabase has no annotations, so a listing recovers a version by trimming
	// the prefix off a name. That only works while names keep underscores, which
	// is why this asserts the exact identifier rather than a sanitised one.
	version := provider.NewGoldenVersionID(time.Unix(1767225600, 0), "abcdef1234567890")
	name := PrefixGolden + version
	require.Equal(t, version, strings.TrimPrefix(name, PrefixGolden))
	require.Equal(t, "gv_20260101000000_abcdef12", version)
}

func TestAnAnnotationRoundTrips(t *testing.T) {
	in := annotation{
		From:      "gv_20260101000000_abcd1234",
		EnvID:     "env_conformance00001",
		Rules:     "sha256:deadbeef",
		CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	out := parseAnnotation(in.String())
	require.Equal(t, in.From, out.From)
	require.Equal(t, in.EnvID, out.EnvID)
	require.Equal(t, in.Rules, out.Rules)
	require.True(t, in.CreatedAt.Equal(out.CreatedAt))
}

func TestSomebodyElsesGitBranchIsNotMetadata(t *testing.T) {
	// git_branch is Supabase's field for the git branch a preview belongs to.
	// Reading a real one as our metadata would invent a relationship that does
	// not exist, and would make DestroyGolden believe an environment came from
	// a version nobody published.
	for _, raw := range []string{"", "main", "feature/login", "from=gv_1"} {
		require.Equal(t, annotation{}, parseAnnotation(raw), "%q was read as ours", raw)
	}
}

func TestAnAnnotationValueCannotForgeASecondField(t *testing.T) {
	in := annotation{From: "a;env=somebody-elses-environment", EnvID: "mine"}
	out := parseAnnotation(in.String())
	require.Equal(t, "a;env=somebody-elses-environment", out.From)
	require.Equal(t, "mine", out.EnvID)
}

func TestBranchSafeKeepsWhatSupabaseAccepts(t *testing.T) {
	require.Equal(t, "env_abc-123", branchSafe("env_abc-123"))
	require.Equal(t, "env_abc", branchSafe("ENV_ABC"))
	require.Equal(t, "env-abc", branchSafe("env/abc"))
	require.Equal(t, "abc", branchSafe("--abc--"))
	require.LessOrEqual(t, len(branchSafe(strings.Repeat("x", 200))), 64)
}

// ---------------------------------------------------------------------------
// The client, against a server that answers the way the real one does.
// ---------------------------------------------------------------------------

type fakeAPI struct {
	t *testing.T
	// calls records method and path in order, which is what the two step delete
	// has to be checked against: the assertion is about the sequence, not about
	// the final state.
	calls    []string
	persist  map[string]bool
	deleted  map[string]bool
	statuses map[string]int
	bodies   map[string]string
	// script answers the first calls to a path differently from the rest, which
	// is the only way to reproduce an API that acknowledges a write before it
	// is readable.
	script map[string][]scripted
	hits   atomic.Int32
}

type scripted struct {
	code int
	body string
}

func newFakeAPI(t *testing.T) *fakeAPI {
	return &fakeAPI{
		t:        t,
		persist:  map[string]bool{},
		deleted:  map[string]bool{},
		statuses: map[string]int{},
		bodies:   map[string]string{},
		script:   map[string][]scripted{},
	}
}

func (f *fakeAPI) serve() *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		key := r.Method + " " + r.URL.Path
		f.calls = append(f.calls, key)

		if steps, ok := f.script[key]; ok && len(steps) > 0 {
			step := steps[0]
			f.script[key] = steps[1:]
			w.WriteHeader(step.code)
			_, _ = w.Write([]byte(step.body))
			return
		}

		if code, ok := f.statuses[key]; ok {
			w.WriteHeader(code)
			_, _ = w.Write([]byte(f.bodies[key]))
			return
		}

		id := strings.TrimPrefix(r.URL.Path, "/branches/")
		switch r.Method {
		case http.MethodDelete:
			if f.deleted[id] {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"Not Found"}`))
				return
			}
			if f.persist[id] {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(`{"message":"Cannot delete persistent branch."}`))
				return
			}
			f.deleted[id] = true
			_, _ = w.Write([]byte(`{"message":"ok"}`))
		case http.MethodPatch:
			var body map[string]any
			require.NoError(f.t, json.NewDecoder(r.Body).Decode(&body))
			if v, ok := body["persistent"].(bool); ok {
				f.persist[id] = v
			}
			// Supabase answers a PATCH with the branch. An empty body is also
			// legal and is covered by its own test.
			_, _ = w.Write([]byte(`{"id":"` + id + `"}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	f.t.Cleanup(srv.Close)
	return srv
}

func (f *fakeAPI) client(srv *httptest.Server) *Client {
	return &Client{
		BaseURL:      srv.URL,
		Key:          secrets.New("sbp_test"),
		ProjectRef:   "proj",
		Sleep:        func(context.Context, time.Duration) error { return nil },
		PollInterval: time.Millisecond,

		// These budgets are generous on purpose. Sleeping is stubbed out, so
		// the only time a poll spends here is the real HTTP round trip through
		// httptest, and a test that proves the client KEEPS POLLING has to make
		// several of them. A 50ms budget made those tests a stopwatch race
		// against the machine rather than a check on the client, and it lost
		// under -race on a loaded one: a correct implementation reported that a
		// published golden never appeared. The test that wants a wait to GIVE
		// UP shortens the budget itself, which is the thing that test is about.
		PollTimeout:    10 * time.Second,
		VisibleTimeout: 10 * time.Second,
	}
}

func TestDeletingAPersistentBranchClearsPersistenceFirst(t *testing.T) {
	// The most expensive thing to get wrong on Supabase. A persistent branch
	// answers DELETE with 422 forever, and every branch this provider makes is
	// persistent, so a single DELETE leaves a running project billing by the
	// hour with nothing in any log to say so.
	f := newFakeAPI(t)
	f.persist["br1"] = true
	srv := f.serve()

	require.NoError(t, f.client(srv).DeleteBranch(context.Background(), "br1"))
	require.Equal(t, []string{
		"DELETE /branches/br1",
		"PATCH /branches/br1",
		"DELETE /branches/br1",
	}, f.calls)
	require.True(t, f.deleted["br1"])
}

func TestDeletingTwiceSucceeds(t *testing.T) {
	// Teardown retries, so deleting something already gone is the expected case
	// at least as often as deleting something that exists.
	f := newFakeAPI(t)
	f.persist["br1"] = true
	srv := f.serve()
	c := f.client(srv)

	require.NoError(t, c.DeleteBranch(context.Background(), "br1"))
	require.NoError(t, c.DeleteBranch(context.Background(), "br1"))
}

func TestAnUnrelatedRefusalIsNotSwallowed(t *testing.T) {
	// 422 is Supabase's general refusal. Reading every one of them as the
	// persistence case would turn a real failure into a silent success, and the
	// branch would live on believed destroyed.
	f := newFakeAPI(t)
	f.statuses["DELETE /branches/br1"] = http.StatusUnprocessableEntity
	f.bodies["DELETE /branches/br1"] = `{"message":"Branch has an in-flight migration"}`
	srv := f.serve()

	err := f.client(srv).DeleteBranch(context.Background(), "br1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "in-flight migration")
}

func TestA2xxWithNoBodyIsSuccessRatherThanADecodeError(t *testing.T) {
	// This bit the Neon provider first. An API that answers some calls with 200
	// and nothing at all makes "destroying something already gone succeeds"
	// stop being true the moment a decoder is pointed at the empty response.
	f := newFakeAPI(t)
	f.statuses["GET /projects/proj/branches"] = http.StatusOK
	f.bodies["GET /projects/proj/branches"] = ""
	srv := f.serve()

	got, err := f.client(srv).ListBranches(context.Background())
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestACreateIsNeverRetried(t *testing.T) {
	// A create that timed out may have reached Supabase, and sending it again
	// would make a second branch, which on this platform is a second running
	// project. Idempotency lives in the caller, which looks before it creates.
	f := newFakeAPI(t)
	f.statuses["POST /projects/proj/branches"] = http.StatusInternalServerError
	f.bodies["POST /projects/proj/branches"] = `{"message":"boom"}`
	srv := f.serve()

	_, err := f.client(srv).CreateBranch(context.Background(), CreateBranchRequest{Name: "af-env-x"})
	require.Error(t, err)
	require.Equal(t, int32(1), f.hits.Load())
}

func TestAReadIsRetried(t *testing.T) {
	f := newFakeAPI(t)
	f.statuses["GET /projects/proj/branches"] = http.StatusInternalServerError
	f.bodies["GET /projects/proj/branches"] = `{"message":"boom"}`
	srv := f.serve()
	c := f.client(srv)
	c.Retries = 3

	_, err := c.ListBranches(context.Background())
	require.Error(t, err)
	require.Equal(t, int32(3), f.hits.Load())
}

func TestARejectedTokenSaysWhichTokenAndWhereToFixIt(t *testing.T) {
	// The commonest way a first run fails, and until this was wired the answer
	// was "supabase: 401: Unauthorized" from whichever request happened to go
	// first, which names neither the credential nor anywhere to change it.
	f := newFakeAPI(t)
	f.statuses["GET /projects/proj/branches"] = http.StatusUnauthorized
	f.bodies["GET /projects/proj/branches"] = `{"message":"Unauthorized"}`
	srv := f.serve()
	c := f.client(srv)
	c.Retries = 3

	_, err := c.ListBranches(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "SUPABASE_ACCESS_TOKEN")
	require.Contains(t, err.Error(), "account/tokens")

	// A rejected token is not a transport failure, and asking again with the
	// same token cannot answer differently. Retrying it three times turns an
	// instant, clear failure into a slow one.
	require.Equal(t, int32(1), f.hits.Load())

	// The status stays askable. NotFound, Conflict and Unauthorized all read
	// through errors.As, so a wrapper that swallowed the APIError would trade
	// a better message for a worse error.
	require.True(t, Unauthorized(err), "the wrapped error no longer reads as unauthorized: %v", err)

	// And nothing about the token itself appears in the message. It is the
	// error most likely to be pasted into an issue.
	require.NotContains(t, err.Error(), "sbp_test")
}

func TestARefusalThatIsNotAboutTheTokenDoesNotBlameTheToken(t *testing.T) {
	// A token that is valid and cannot see the project answers 404 on the real
	// API, not 403, so nothing here should attach "rotate your token" to a
	// refusal that would send somebody to issue a new one for no reason.
	f := newFakeAPI(t)
	f.statuses["GET /projects/proj/branches"] = http.StatusForbidden
	f.bodies["GET /projects/proj/branches"] = `{"message":"Forbidden"}`
	srv := f.serve()

	_, err := f.client(srv).ListBranches(context.Background())
	require.Error(t, err)
	require.False(t, Unauthorized(err))
	require.NotContains(t, err.Error(), "SUPABASE_ACCESS_TOKEN")
}

func TestABranchThatWillNeverComeUpFailsRatherThanPolling(t *testing.T) {
	f := newFakeAPI(t)
	f.statuses["GET /branches/br1"] = http.StatusOK
	f.bodies["GET /branches/br1"] = `{"ref":"abc","status":"INIT_FAILED"}`
	srv := f.serve()

	_, err := f.client(srv).WaitReady(context.Background(), "br1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "INIT_FAILED")
	// One look, not a poll until the deadline. A branch in a terminal failure
	// state is not going to change, and waiting for it turns a clear error into
	// a timeout somewhere else.
	require.Equal(t, int32(1), f.hits.Load())
}

func TestAPooledStringCarriesThePasswordAndNotThePlaceholder(t *testing.T) {
	// Supabase returns connection_string with the literal text [YOUR-PASSWORD]
	// in it. A provider that passed it through would declare pooled endpoints,
	// satisfy any test that checks pooled differs from direct, and fail the
	// first time anything connected.
	pooler := Pooler{
		DatabaseType:     "PRIMARY",
		DBUser:           "postgres.abcdef",
		DBHost:           "aws-0-us-east-1.pooler.supabase.com",
		DBPort:           6543,
		DBName:           "postgres",
		PoolMode:         "transaction",
		ConnectionString: "postgresql://postgres.abcdef:[YOUR-PASSWORD]@aws-0-us-east-1.pooler.supabase.com:6543/postgres",
	}
	pass := secrets.New("s3cr3t/pa+ss")
	pooled := pooledConnString(pooler, pass)

	require.NotContains(t, pooled.Reveal(), "YOUR-PASSWORD")
	require.Contains(t, pooled.Reveal(), "aws-0-us-east-1.pooler.supabase.com:6543")
	// Escaped, because a Supabase password is base64ish and a stray slash or
	// plus would otherwise truncate the host.
	require.Contains(t, pooled.Reveal(), "s3cr3t%2Fpa+ss")
	require.Equal(t, secrets.Redacted, pooled.String())

	direct := connString(Detail{
		DBHost: "db.abcdef.supabase.co", DBPort: 5432, DBUser: "postgres", DBPass: pass,
	})
	require.False(t, direct.Equal(pooled))
	require.Contains(t, direct.Reveal(), ":5432/postgres")
}

func TestAReadReplicaIsNeverHandedOutAsThePool(t *testing.T) {
	// Handing an application a read replica is a bug that looks like a
	// permissions problem the first time it writes.
	entries := []Pooler{
		{DatabaseType: "READ_REPLICA", DBHost: "replica"},
		{DatabaseType: "PRIMARY", DBHost: "primary"},
	}
	got, ok := primaryPooler(entries)
	require.True(t, ok)
	require.Equal(t, "primary", got.DBHost)

	_, ok = primaryPooler([]Pooler{{DatabaseType: "READ_REPLICA"}, {DatabaseType: "READ_REPLICA"}})
	require.False(t, ok)
}

func TestAProviderNeedsATokenAndAProject(t *testing.T) {
	_, err := New(Options{ProjectRef: "abc"})
	require.ErrorContains(t, err, "token")
	_, err = New(Options{Token: secrets.New("sbp_x")})
	require.ErrorContains(t, err, "project")
}

func TestTheDeclaredCapabilitiesSayWhatThisProviderActuallyDoes(t *testing.T) {
	p, err := New(Options{Token: secrets.New("sbp_x"), ProjectRef: "abc", Clock: clock.New()})
	require.NoError(t, err)
	caps := p.Capabilities()

	require.True(t, caps.Branching)
	require.True(t, caps.Reset)
	// A branch is a separate project with its own storage and the golden's rows
	// are copied into it. Declaring copy on write because branching is quick
	// would be declaring the wrong reason, and the conformance suite would
	// believe branch time is independent of database size.
	require.False(t, caps.CopyOnWrite)
	require.False(t, caps.ProviderMasking)
	require.True(t, caps.PooledEndpoints)
	require.Equal(t, []int{15, 17}, caps.SupportedVersions)
	require.Positive(t, caps.ExpectedBranchLatency)
}

func TestABranchThatIsNotReadableYetIsWaitedForRatherThanFailed(t *testing.T) {
	// Creating a branch answers 201 with an identifier, and asking for that
	// identifier can answer 404 for the next few seconds. Reading that as "the
	// branch does not exist" fails a refresh four seconds in, which is what
	// happened against the real API before this.
	f := newFakeAPI(t)
	f.script["GET /branches/br1"] = []scripted{
		{http.StatusNotFound, `{"message":"Not Found"}`},
		{http.StatusNotFound, `{"message":"Not Found"}`},
		{http.StatusOK, `{"ref":"abc","status":"ACTIVE_HEALTHY","db_host":"h","db_port":5432,"db_user":"postgres","db_pass":"p"}`},
	}
	srv := f.serve()

	got, err := f.client(srv).WaitReady(context.Background(), "br1")
	require.NoError(t, err)
	require.Equal(t, "abc", got.Ref)
}

func TestABranchThatIsTrulyGoneStillFails(t *testing.T) {
	// The other half. Tolerating a 404 forever would turn asking about a branch
	// somebody deleted into a wait for the provisioning deadline, which on this
	// provider is minutes.
	f := newFakeAPI(t)
	f.statuses["GET /branches/br1"] = http.StatusNotFound
	f.bodies["GET /branches/br1"] = `{"message":"Not Found"}`
	srv := f.serve()

	// The grace has to END, so this is the one test that wants a short one.
	// It is a property of the client rather than of the machine, so shortening
	// it here cannot make the test flaky the way a shared short budget did.
	c := f.client(srv)
	c.VisibleTimeout = 50 * time.Millisecond

	_, err := c.WaitReady(context.Background(), "br1")
	require.Error(t, err)
	require.True(t, NotFound(err), "a branch that is gone must fail as not found, got %v", err)
}

func TestAPublishIsNotReportedUntilTheListingShowsIt(t *testing.T) {
	// Renaming answers 200 with the new name while the LISTING still carries
	// the old one. Publishing a golden is a rename, so a caller that branched
	// in that window was told its verified golden had failed verification.
	f := newFakeAPI(t)
	stale := `[{"id":"br1","name":"af-cand-gv_1","project_ref":"r","persistent":true}]`
	fresh := `[{"id":"br1","name":"af-gv-gv_1","project_ref":"r","persistent":true}]`
	f.script["GET /projects/proj/branches"] = []scripted{
		{http.StatusOK, stale},
		{http.StatusOK, stale},
		{http.StatusOK, fresh},
	}
	srv := f.serve()

	require.NoError(t, f.client(srv).WaitNamed(context.Background(), "af-gv-gv_1"))
	require.GreaterOrEqual(t, f.hits.Load(), int32(3))
}
