package neon_test

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
	"github.com/antifailure/antifailure/engine/internal/db/neon"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// These test the parts that do not need a database: how the client treats
// Neon's asynchronous operations, and how the provider decides what it is
// looking at. Everything that needs a real Postgres is in the conformance run,
// against the real service.

func TestClientWaitsForOperationsToFinish(t *testing.T) {
	// The failure this prevents: returning as soon as the HTTP call does hands
	// back a connection string to a compute that is not running, and the error
	// surfaces somewhere else entirely, usually inside a migration.
	var polls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/branches"):
			writeJSON(w, map[string]any{
				"branch":     map[string]any{"id": "br-1", "name": "af-env-x"},
				"operations": []map[string]any{{"id": "op-1", "action": "create_branch", "status": "running"}},
			})
		case strings.Contains(r.URL.Path, "/operations/op-1"):
			n := polls.Add(1)
			status := "running"
			if n >= 3 {
				status = "finished"
			}
			writeJSON(w, map[string]any{
				"operation": map[string]any{"id": "op-1", "action": "create_branch", "status": status},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := testClient(srv.URL)
	_, _, err := c.CreateBranch(context.Background(), neon.CreateBranchRequest{Name: "af-env-x"})
	require.NoError(t, err)
	require.GreaterOrEqual(t, polls.Load(), int32(3), "the client stopped polling before the operation finished")
}

func TestClientReportsAFailedOperation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/branches"):
			writeJSON(w, map[string]any{
				"branch":     map[string]any{"id": "br-1"},
				"operations": []map[string]any{{"id": "op-1", "action": "start_compute", "status": "running"}},
			})
		case strings.Contains(r.URL.Path, "/operations/op-1"):
			writeJSON(w, map[string]any{"operation": map[string]any{
				"id": "op-1", "action": "start_compute", "status": "failed", "error": "no capacity",
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	_, _, err := testClient(srv.URL).CreateBranch(context.Background(), neon.CreateBranchRequest{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "start_compute")
	require.Contains(t, err.Error(), "no capacity", "the reason Neon gave was dropped")
}

func TestClientTreatsAForgottenOperationAsFinished(t *testing.T) {
	// Neon prunes old operations. A caller slow enough to poll after that must
	// not fail for having been slow.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/branches"):
			writeJSON(w, map[string]any{
				"branch":     map[string]any{"id": "br-1"},
				"operations": []map[string]any{{"id": "op-gone", "action": "create_branch", "status": "running"}},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"not_found","message":"operation not found"}`))
		}
	}))
	defer srv.Close()

	_, _, err := testClient(srv.URL).CreateBranch(context.Background(), neon.CreateBranchRequest{})
	require.NoError(t, err)
}

func TestClientTreatsSkippedAsSuccess(t *testing.T) {
	// Neon skips work already in the state it was asked for. Treating that as
	// a failure would break every retry, which is the path this is on.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			writeJSON(w, map[string]any{
				"operations": []map[string]any{{"id": "op-1", "action": "delete_timeline", "status": "skipped"}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	require.NoError(t, testClient(srv.URL).DeleteBranch(context.Background(), "br-1"))
}

func TestDeletingABranchThatIsGoneSucceeds(t *testing.T) {
	// Teardown retries. A destroy that fails because the thing is already
	// destroyed turns a successful teardown into a reported failure.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"branches_not_found","message":"branch not found"}`))
	}))
	defer srv.Close()

	require.NoError(t, testClient(srv.URL).DeleteBranch(context.Background(), "br-gone"))
}

func TestTheAPIKeyNeverReachesTheURLOrAnError(t *testing.T) {
	// The key goes in a header, because a URL reaches access logs, proxies,
	// and error messages, and a header does not.
	var seenURL, seenAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenURL = r.URL.String()
		seenAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":"internal","message":"something broke"}`))
	}))
	defer srv.Close()

	c := testClient(srv.URL)
	_, err := c.ListBranches(context.Background())
	require.Error(t, err)
	require.NotContains(t, seenURL, "secret-key-value")
	require.Equal(t, "Bearer secret-key-value", seenAuth)
	require.NotContains(t, err.Error(), "secret-key-value", "the key reached an error message")
	require.Equal(t, secrets.Redacted, c.Key.String())
}

func TestBranchingAMissingGoldenSaysMissingAndAnUnfinishedOneSaysUnverified(t *testing.T) {
	// Two different problems with two different fixes. A refresh that did not
	// finish leaves a candidate, and telling the operator "no such version"
	// would send them looking for the wrong thing.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"branches": []map[string]any{
				{"id": "br-cand", "name": "af-cand-gv-20260101000000-abcd1234"},
			},
			"annotations": map[string]any{
				"br-cand": map[string]any{"value": map[string]string{
					"antifailure-version": "gv_20260101000000_abcd1234",
				}},
			},
		})
	}))
	defer srv.Close()

	p := testProvider(t, srv.URL)
	_, err := p.Branch(context.Background(), "gv_20260101000000_abcd1234", "env-1")
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFMSK001))

	_, err = p.Branch(context.Background(), "gv_19700101000000_deadbeef", "env-1")
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFDB004))
}

func TestBranchingIsIdempotentByEnvironment(t *testing.T) {
	var creates atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			creates.Add(1)
		}
		writeJSON(w, map[string]any{
			"branches": []map[string]any{
				{"id": "br-gold", "name": "af-gv-gv-20260101000000-abcd1234"},
				{"id": "br-env", "name": "af-env-env-1", "parent_id": "br-gold"},
			},
			"annotations": map[string]any{
				"br-gold": map[string]any{"value": map[string]string{
					"antifailure-version": "gv_20260101000000_abcd1234",
				}},
				"br-env": map[string]any{"value": map[string]string{
					"antifailure-env":  "env-1",
					"antifailure-from": "gv_20260101000000_abcd1234",
				}},
			},
		})
	}))
	defer srv.Close()

	b, err := testProvider(t, srv.URL).Branch(context.Background(), "gv_20260101000000_abcd1234", "env-1")
	require.NoError(t, err)
	require.Equal(t, "br-env", b.ProviderRef)
	require.Equal(t, int32(0), creates.Load(), "a second branch was created for an environment that had one")
}

func TestTheConcurrencyLimitFailsFastRatherThanHanging(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"branches": []map[string]any{
				{"id": "br-gold", "name": "af-gv-g"},
				{"id": "br-a", "name": "af-env-a", "parent_id": "br-gold"},
				{"id": "br-b", "name": "af-env-b", "parent_id": "br-gold"},
			},
			"annotations": map[string]any{
				"br-gold": map[string]any{"value": map[string]string{"antifailure-version": "g"}},
				"br-a":    map[string]any{"value": map[string]string{"antifailure-env": "a"}},
				"br-b":    map[string]any{"value": map[string]string{"antifailure-env": "b"}},
			},
		})
	}))
	defer srv.Close()

	p, err := neon.New(neon.Options{
		APIKey: secrets.New("k"), ProjectID: "p", BaseURL: srv.URL, MaxBranches: 2,
		Clock: clock.New(),
	})
	require.NoError(t, err)
	_, err = p.Branch(context.Background(), "g", "c")
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFDB006))
}

func TestDestroyingAReferencedGoldenIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NotEqual(t, http.MethodDelete, r.Method, "the golden was deleted despite being referenced")
		writeJSON(w, map[string]any{
			"branches": []map[string]any{
				{"id": "br-gold", "name": "af-gv-g"},
				{"id": "br-a", "name": "af-env-a", "parent_id": "br-gold"},
			},
			"annotations": map[string]any{
				"br-gold": map[string]any{"value": map[string]string{"antifailure-version": "g"}},
				"br-a":    map[string]any{"value": map[string]string{"antifailure-env": "a"}},
			},
		})
	}))
	defer srv.Close()

	err := testProvider(t, srv.URL).DestroyGolden(context.Background(), "g")
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFDB005))
}

func TestDestroyingAGoldenThatIsGoneSucceeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"branches": []map[string]any{}})
	}))
	defer srv.Close()
	require.NoError(t, testProvider(t, srv.URL).DestroyGolden(context.Background(), "nothing"))
}

func TestInventoryIgnoresBranchesThisDidNotCreate(t *testing.T) {
	// A Neon project holds branches people made by hand. Reporting those as
	// leaks is how a leak report becomes something people learn to ignore.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"branches": []map[string]any{
				{"id": "br-main", "name": "main", "default": true},
				{"id": "br-mine", "name": "my-experiment"},
				{"id": "br-gold", "name": "af-gv-g"},
				{"id": "br-env", "name": "af-env-e"},
				{"id": "br-cand", "name": "af-cand-x"},
			},
			"annotations": map[string]any{
				"br-env": map[string]any{"value": map[string]string{"antifailure-env": "e"}},
			},
		})
	}))
	defer srv.Close()

	got, err := testProvider(t, srv.URL).Inventory(context.Background())
	require.NoError(t, err)
	kinds := map[string]string{}
	for _, r := range got {
		kinds[r.ID] = r.Kind
	}
	require.Equal(t, map[string]string{
		"br-cand": "candidate", "br-env": "branch", "br-gold": "golden",
	}, kinds)
}

func TestHealthReportsAnUnreachableBranchRatherThanErroring(t *testing.T) {
	// Teardown checks health. Erroring on a branch that is gone would make a
	// successful teardown look like a failure.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"not_found","message":"gone"}`))
	}))
	defer srv.Close()

	h, err := testProvider(t, srv.URL).Health(context.Background(), provider.Branch{ProviderRef: "br-gone"})
	require.NoError(t, err)
	require.False(t, h.Reachable)
	require.NotEmpty(t, h.Detail, "an unreachable branch was reported with no reason")
}

func TestCapabilitiesDoNotContradictThemselves(t *testing.T) {
	caps := testProvider(t, "http://example.invalid").Capabilities()
	require.True(t, caps.Branching)
	require.True(t, caps.CopyOnWrite, "Neon's branches share storage; without this there is no reason to use it")
	require.True(t, caps.Reset)
	require.True(t, caps.PooledEndpoints)
	require.False(t, caps.ProviderMasking,
		"the engine's rules are the single implementation of masking")
	require.True(t, caps.Supports(17))
	require.False(t, caps.Supports(13))
}

func TestARefreshOnAnUnsupportedVersionIsRefusedBeforeAnythingIsCreated(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeJSON(w, map[string]any{"branches": []map[string]any{}})
	}))
	defer srv.Close()

	_, err := testProvider(t, srv.URL).RefreshGolden(context.Background(), provider.GoldenSpec{Version: 12})
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFDB003))
	require.Equal(t, int32(0), calls.Load(), "Neon was called before the version was checked")
}

func TestPooledIsSentExplicitlyInBothDirections(t *testing.T) {
	// Found by running the conformance suite against the real service: Neon
	// treats a missing pooled parameter as pooled, so omitting it when false
	// hands a pooled connection to pg_restore, which needs session level
	// features a transaction pooler does not have. Both directions are stated.
	seen := map[bool]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		host := "direct.example"
		if q.Get("pooled") == "true" {
			host = "pooled.example"
		}
		seen[q.Get("pooled") == "true"] = q.Get("pooled")
		writeJSON(w, map[string]any{"uri": "postgresql://u:p@" + host + "/db"})
	}))
	defer srv.Close()

	c := testClient(srv.URL)
	direct, err := c.ConnectionURI(context.Background(), "br-1", "db", "role", false)
	require.NoError(t, err)
	pooled, err := c.ConnectionURI(context.Background(), "br-1", "db", "role", true)
	require.NoError(t, err)

	require.Equal(t, "false", seen[false], "pooled was omitted rather than sent as false")
	require.Equal(t, "true", seen[true])
	require.False(t, pooled.Equal(direct), "the two modes produced the same connection string")
}

func TestAnEmptyBodyOnASuccessIsNotADecodeFailure(t *testing.T) {
	// Also found by running it. Neon answers some deletes with 200 and no body,
	// and decoding that as JSON is how "destroying something already gone
	// succeeds" quietly stopped being true.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	require.NoError(t, testClient(srv.URL).DeleteBranch(context.Background(), "br-1"))
}

func TestNeonsOwnBranchLimitBecomesTheCodedError(t *testing.T) {
	// The configured limit is what this provider was told; the plan is what
	// actually decides. An operator who hits the second one should get the same
	// answer as the first, not an unexplained 422.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"code":"BRANCHES_LIMIT_EXCEEDED","message":"branches limit exceeded"}`))
			return
		}
		writeJSON(w, map[string]any{
			"branches": []map[string]any{{"id": "br-gold", "name": "af-gv-g"}},
			"annotations": map[string]any{
				"br-gold": map[string]any{"value": map[string]string{"antifailure-version": "g"}},
			},
		})
	}))
	defer srv.Close()

	// No limit configured here, so the only source of the refusal is Neon.
	p, err := neon.New(neon.Options{
		APIKey: secrets.New("k"), ProjectID: "p", BaseURL: srv.URL, Clock: clock.New(),
		PollInterval: time.Millisecond,
	})
	require.NoError(t, err)
	_, err = p.Branch(context.Background(), "g", "env-1")
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFDB006))
	require.True(t, neon.LimitExceeded(err), "the original refusal was lost in the wrapping")
}

func TestAnIdempotentRequestSurvivesATransientFailure(t *testing.T) {
	// A run against the real service died on a DNS blip. A client that crosses
	// the public internet to a cloud API and gives up on the first transport
	// error is not finished.
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		writeJSON(w, map[string]any{"branches": []map[string]any{{"id": "br-1", "name": "af-gv-g"}}})
	}))
	defer srv.Close()

	got, err := testClient(srv.URL).ListBranches(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int32(3), calls.Load())
}

func TestACreateIsNeverRetried(t *testing.T) {
	// The asymmetry that matters. A create that timed out may have reached
	// Neon, and sending it again makes a second branch, which is exactly the
	// orphan this provider exists to avoid. Idempotency for creates lives in
	// the caller, which looks before it makes.
	var creates atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		creates.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	_, _, err := testClient(srv.URL).CreateBranch(context.Background(), neon.CreateBranchRequest{Name: "x"})
	require.Error(t, err)
	require.Equal(t, int32(1), creates.Load(), "a branch creation was sent more than once")
}

func TestARefusalIsNotRetried(t *testing.T) {
	// A 404 is an answer. Retrying it turns an immediate, correct result into
	// four times the latency for the same result.
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"not_found","message":"no"}`))
	}))
	defer srv.Close()

	_, err := testClient(srv.URL).GetBranch(context.Background(), "br-1")
	require.Error(t, err)
	require.Equal(t, int32(1), calls.Load())
}

func TestGivingUpReportsTheLastFailureRatherThanNothing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"code":"unavailable","message":"try later"}`))
	}))
	defer srv.Close()

	_, err := testClient(srv.URL).ListBranches(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "try later", "the reason was lost when the retries ran out")
}

// ---------------------------------------------------------------------------

func testClient(base string) *neon.Client {
	return &neon.Client{
		BaseURL:      base,
		Key:          secrets.New("secret-key-value"),
		ProjectID:    "proj",
		PollInterval: time.Millisecond,
		PollTimeout:  5 * time.Second,
		Retries:      4,
	}
}

func testProvider(t *testing.T, base string) *neon.Provider {
	t.Helper()
	p, err := neon.New(neon.Options{
		APIKey: secrets.New("secret-key-value"), ProjectID: "proj",
		BaseURL: base, Clock: clock.New(),
		PollInterval: time.Millisecond, PollTimeout: 5 * time.Second,
	})
	require.NoError(t, err)
	return p
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
