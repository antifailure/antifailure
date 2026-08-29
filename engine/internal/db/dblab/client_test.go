package dblab

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// newTestClient builds a client against a handler, with polling fast enough
// that a test proving polling works does not spend real seconds doing it.
func newTestClient(t *testing.T, h http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Client{
		BaseURL:      srv.URL,
		Token:        secrets.New("test-token"),
		PollInterval: time.Millisecond,
		PollTimeout:  2 * time.Second,
		Sleep: func(ctx context.Context, d time.Duration) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d):
				return nil
			}
		},
	}, srv
}

// The engine marshals a zero timestamp as an empty string rather than omitting
// the field. A plain time.Time cannot parse that, and because snapshots arrive
// as an array, one such element would discard the whole listing: an engine
// holding twenty goldens would report none, and look exactly like an engine
// holding none.
func TestTimeAcceptsEveryFormTheEngineEmits(t *testing.T) {
	cases := []struct {
		name string
		json string
		want time.Time
	}{
		{"empty string", `""`, time.Time{}},
		{"null", `null`, time.Time{}},
		{"rfc3339 utc", `"2026-08-27T01:02:03Z"`, time.Date(2026, 8, 27, 1, 2, 3, 0, time.UTC)},
		{"rfc3339 with offset", `"2026-08-27T01:02:03+02:00"`,
			time.Date(2026, 8, 27, 1, 2, 3, 0, time.FixedZone("", 2*3600))},
		{"legacy", `"2026-08-27 01:02:03 UTC"`, time.Date(2026, 8, 27, 1, 2, 3, 0, time.UTC)},
		{"unreadable", `"tuesday"`, time.Time{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got Time
			require.NoError(t, json.Unmarshal([]byte(tc.json), &got))
			require.True(t, got.Equal(tc.want), "got %v, want %v", got.Time, tc.want)
		})
	}
}

// The property that matters is not that one odd element parses; it is that one
// odd element does not take the other nineteen with it.
func TestOneUnreadableTimestampDoesNotEmptyTheListing(t *testing.T) {
	body := `[
	  {"id":"pool/a@1","createdAt":"2026-08-27T01:00:00Z","message":"{\"antifailure\":1,\"version\":\"gv_a\"}"},
	  {"id":"pool/b@2","createdAt":"","message":"{\"antifailure\":1,\"version\":\"gv_b\"}"},
	  {"id":"pool/c@3","createdAt":"2026-08-27T03:00:00Z","message":"{\"antifailure\":1,\"version\":\"gv_c\"}"}
	]`
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	got, err := c.ListSnapshots(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 3, "one snapshot with no creation time discarded the whole listing")
	require.True(t, got[1].CreatedAt.IsZero())
}

// The engine answers a delete with 200 and no body at all. Decoding that as
// JSON is how "destroying something already gone succeeds" stops being true,
// which is the bug the Neon provider shipped first.
func TestAnEmptyTwoHundredBodyIsNotADecodeError(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	var out map[string]any
	require.NoError(t, c.do(context.Background(), http.MethodDelete, "/snapshot/x", nil, &out))
}

// The engine reports a missing clone as 404 NOT_FOUND and a missing snapshot
// as 400 BAD_REQUEST with prose. Treating the second as a real failure made
// destroying a golden twice fail while destroying a branch twice succeeded.
func TestNotFoundRecognisesBothShapes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"404", &APIError{Status: 404, Code: "NOT_FOUND", Message: "clone not found"}, true},
		{"400 does not exist", &APIError{Status: 400, Code: "BAD_REQUEST",
			Message: `snapshot "pool/x@1" does not exist`}, true},
		{"400 no such", &APIError{Status: 400, Message: "no such snapshot"}, true},
		{"400 something else", &APIError{Status: 400, Message: "clone ID must start with a letter"}, false},
		{"401", &APIError{Status: 401, Code: "UNAUTHORIZED"}, false},
		{"not an api error", errors.New("connection refused"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, NotFound(tc.err))
		})
	}
}

func TestUnauthorizedRecognisesTheEnginesRefusal(t *testing.T) {
	require.True(t, Unauthorized(&APIError{Status: 401, Code: "UNAUTHORIZED"}))
	require.False(t, Unauthorized(&APIError{Status: 403}))
	require.False(t, Unauthorized(errors.New("nope")))
}

// A snapshot identifier is pool/dataset/branch/timestamp@timestamp. The route
// is registered as {id:.*}, so the slashes have to survive and the at sign has
// to be escaped. url.PathEscape on the whole string encodes the slashes and
// routes the request to nothing.
func TestSnapshotIdentifiersKeepTheirSlashes(t *testing.T) {
	// The at sign is legal in a path segment and is left alone, which is what
	// the engine's own examples look like on the wire.
	id := "dblab_pool/dataset_1/main/20260101000000@20260101000000"
	require.Equal(t, id, escapeID(id))
	require.Equal(t, 3, strings.Count(escapeID(id), "/"))

	// What has to be escaped is anything that would end the path and turn the
	// rest of the identifier into a query string or a fragment.
	got := escapeID("pool/odd name?x#y@1")
	require.NotContains(t, got, "?")
	require.NotContains(t, got, "#")
	require.NotContains(t, got, " ")
	require.Equal(t, 1, strings.Count(got, "/"))
}

func TestTheTokenTravelsInItsOwnHeader(t *testing.T) {
	var seen string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get(TokenHeader)
		require.Empty(t, r.Header.Get("Authorization"),
			"the engine is not a bearer API and sending one fails with an unhelpful 401")
		_, _ = w.Write([]byte(`[]`))
	}))
	_, err := c.ListSnapshots(context.Background())
	require.NoError(t, err)
	require.Equal(t, "test-token", seen)
}

// A create that timed out may have reached the engine, and sending it again
// would either make a second clone or fail as a duplicate. Idempotency for
// creates lives in the caller, so the transport must not retry them.
func TestOnlyReadsAreRetried(t *testing.T) {
	for _, tc := range []struct {
		method string
		want   int32
	}{
		{http.MethodGet, 4},
		{http.MethodPost, 1},
		{http.MethodDelete, 1},
	} {
		t.Run(tc.method, func(t *testing.T) {
			var calls int32
			c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				atomic.AddInt32(&calls, 1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			_ = c.do(context.Background(), tc.method, "/x", map[string]string{"a": "b"}, nil)
			require.Equal(t, tc.want, atomic.LoadInt32(&calls))
		})
	}
}

func TestABadRequestIsNotRetried(t *testing.T) {
	var calls int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"BAD_REQUEST","message":"nope"}`))
	}))
	err := c.do(context.Background(), http.MethodGet, "/x", nil, nil)
	require.Error(t, err)
	require.Equal(t, int32(1), atomic.LoadInt32(&calls))
}

// Creating a clone returns immediately with CREATING. A client that returns
// then hands back a connection string to a Postgres that is not listening.
func TestAwaitClonePollsUntilTheCloneSettles(t *testing.T) {
	var calls int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		code := StatusCreating
		if n >= 3 {
			code = StatusOK
		}
		_, _ = w.Write([]byte(`{"id":"af-env-x","status":{"code":"` + code + `"},"db":{"port":"6000"}}`))
	}))
	got, err := c.AwaitClone(context.Background(), "af-env-x")
	require.NoError(t, err)
	require.Equal(t, StatusOK, got.Status.Code)
	require.GreaterOrEqual(t, atomic.LoadInt32(&calls), int32(3))
}

// A clone that reaches FATAL is an error rather than a value every caller has
// to remember to check, because every caller wants the same thing: a clone it
// can connect to.
func TestAwaitCloneReportsAFatalClone(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"af-env-x","status":{"code":"FATAL","message":"no space left"}}`))
	}))
	_, err := c.AwaitClone(context.Background(), "af-env-x")
	require.ErrorContains(t, err, "no space left")
}

// The engine answers a delete before the container is stopped and the dataset
// destroyed, and the conformance suite reads the inventory immediately
// afterwards. Returning early reports a branch as destroyed while it is still
// there, which is the leak the journal exists to catch.
func TestDeleteCloneWaitsUntilTheCloneIsGone(t *testing.T) {
	var gets int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			return
		}
		if atomic.AddInt32(&gets, 1) < 3 {
			_, _ = w.Write([]byte(`{"id":"af-env-x","status":{"code":"DELETING"}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"NOT_FOUND","message":"clone not found"}`))
	}))
	require.NoError(t, c.DeleteClone(context.Background(), "af-env-x"))
	require.GreaterOrEqual(t, atomic.LoadInt32(&gets), int32(3))
}

// Teardown retries, so deleting something already gone is at least as common
// as deleting something that exists.
func TestDeletingACloneThatIsAlreadyGoneSucceeds(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"NOT_FOUND","message":"clone not found"}`))
	}))
	require.NoError(t, c.DeleteClone(context.Background(), "af-env-x"))
}

// The engine answers a bare array here and an object elsewhere. A version that
// changes its mind about the envelope must not empty the inventory, because an
// empty inventory is what the leak detector reads as "nothing was left behind".
func TestListClonesAcceptsBothEnvelopes(t *testing.T) {
	for name, body := range map[string]string{
		"bare array": `[{"id":"af-env-a"},{"id":"af-env-b"}]`,
		"wrapped":    `{"clones":[{"id":"af-env-a"},{"id":"af-env-b"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			got, err := c.ListClones(context.Background())
			require.NoError(t, err)
			require.Len(t, got, 2)
		})
	}
}

func TestSnapshotCloneRefusesAnAnswerWithNoIdentifier(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	_, err := c.SnapshotClone(context.Background(), "af-cand-x", "msg")
	require.ErrorContains(t, err, "returned no identifier")
}

// A clone that has no snapshot on the wire is a clone that failed to create.
// Every read of the field goes through SnapshotID so that a nil pointer is a
// missing identifier rather than a panic in the middle of a teardown.
func TestACloneWithNoSnapshotDoesNotPanic(t *testing.T) {
	var c Clone
	require.NoError(t, json.Unmarshal([]byte(`{"id":"af-env-x","snapshot":null}`), &c))
	require.Equal(t, "", c.SnapshotID())
}
