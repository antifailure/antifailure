package oracle_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/oracle"
)

// The whole comparison, on an application that writes to a database.
//
// Everything above this file tests one half at a time: a response against a
// response, a snapshot against a snapshot. This tests the thing itself, and it
// exists because the two halves together answer a question neither can answer
// alone. The change it catches is the one this feature is for: an endpoint that
// returns exactly what it always returned and no longer writes the row.
//
// Real servers and two real Postgres databases branched from one template. Not
// a fake: the row that goes missing goes missing because a transaction is never
// committed, which is a fact about Postgres rather than about a stub.

// ordersAPI is a small orders API, in three versions.
//
// The three differ the way three commits differ. Baseline is the reference,
// harmless changes how the response is written without changing what it says,
// and broken wraps the insert in a transaction it never commits, which is a bug
// somebody has shipped in every language there is.
type ordersAPI struct {
	conn *pgx.Conn
	// indent renders the JSON with whitespace, which changes every byte of
	// every response and none of its meaning.
	indent bool
	// echoRequestID adds a tracing header, which a real proxy does and which
	// no two responses can agree on.
	echoRequestID bool
	// forgetCommit is the change that matters.
	forgetCommit bool
	// clock is injected, so the served_at field below is this test's clock and
	// not the wall one. The point of the field is that it differs between the
	// two sides, which a fake clock that both sides share would not do, so each
	// side gets its own and they are started a few hundred milliseconds apart.
	now func() time.Time
	// nextIntent stands in for the egress sidecar's mock pack, which gives each
	// environment its own counter starting at one. Two sides therefore see the
	// same identifier for the same call, which is what makes an opaque token
	// comparable at all.
	nextIntent int
	// idSeed makes the request identifier below differ between the two sides.
	// It has to: a fake clock does not advance, so an identifier derived from
	// the clock would be the same on both sides and the UUID normaliser would
	// never be asked to do anything.
	idSeed int
	// responses counts what this side has written, so every identifier it
	// produces is a different one.
	responses int
}

func (a *ordersAPI) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /customers", func(w http.ResponseWriter, r *http.Request) {
		rows, err := a.conn.Query(r.Context(), `SELECT id, name FROM customers ORDER BY id`)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id int
			var name string
			if err := rows.Scan(&id, &name); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			out = append(out, map[string]any{"id": id, "name": name})
		}
		a.write(w, http.StatusOK, out)
	})

	mux.HandleFunc("POST /orders", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			CustomerID int `json:"customer_id"`
			TotalCents int `json:"total_cents"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "body is not JSON", http.StatusBadRequest)
			return
		}
		if in.CustomerID == 0 || in.TotalCents <= 0 {
			http.Error(w, "customer_id and a positive total_cents are required",
				http.StatusUnprocessableEntity)
			return
		}
		a.nextIntent++
		intent := fmt.Sprintf("pi_mock%014d", a.nextIntent)

		tx, err := a.conn.Begin(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer func() { _ = tx.Rollback(context.WithoutCancel(r.Context())) }()

		var id, customerID, total int
		var placedAt time.Time
		err = tx.QueryRow(r.Context(),
			`INSERT INTO orders (customer_id, total_cents)
			 VALUES ($1, $2) RETURNING id, customer_id, total_cents, placed_at`,
			in.CustomerID, in.TotalCents,
		).Scan(&id, &customerID, &total, &placedAt)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !a.forgetCommit {
			if err := tx.Commit(r.Context()); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		a.write(w, http.StatusCreated, map[string]any{
			"id": id, "customer_id": customerID, "total_cents": total,
			"placed_at":      placedAt.UTC().Format(time.RFC3339Nano),
			"payment_intent": intent,
		})
	})
	return mux
}

// write is where the harmless differences live.
func (a *ordersAPI) write(w http.ResponseWriter, status int, data any) {
	a.responses++
	body := map[string]any{
		// The two sources of non-determinism this test deliberately has. Both
		// appear on both sides and neither can ever agree.
		"served_at": a.now().UTC().Format(time.RFC3339Nano),
		"request_id": fmt.Sprintf("3f8a1c2e-0000-4000-8000-%012d",
			a.idSeed*1000+a.responses),
		"data": data,
	}
	if a.echoRequestID {
		w.Header().Set("X-Request-Id", body["request_id"].(string))
	}
	// The content type before the status. Setting it afterwards lands on a
	// response that has already been written, and the body is then sniffed as
	// text: that is a real defect this comparison found in examples/go-api.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	if a.indent {
		enc.SetIndent("", "  ")
	}
	_ = enc.Encode(body)
}

const endToEndSchema = `
CREATE TABLE customers (
  id    serial PRIMARY KEY,
  name  text NOT NULL,
  email text NOT NULL UNIQUE
);
CREATE TABLE orders (
  id          serial PRIMARY KEY,
  customer_id integer NOT NULL REFERENCES customers (id),
  total_cents integer NOT NULL,
  placed_at   timestamptz NOT NULL DEFAULT now()
);
INSERT INTO customers (name, email) VALUES
  ('Ada Lovelace', 'ada@example.test'),
  ('Grace Hopper', 'grace@example.test');
`

// endToEndProbes is the plan both sides receive, in this order.
var endToEndProbes = []oracle.Probe{
	{Name: "list-customers", Method: "GET", Path: "/customers"},
	{Name: "place-an-order", Method: "POST", Path: "/orders",
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    `{"customer_id": 1, "total_cents": 2599}`},
	{Name: "refuse-an-order-with-no-total", Method: "POST", Path: "/orders",
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    `{"customer_id": 1, "total_cents": 0}`},
}

// compareVersions runs the whole thing: two servers, two databases, one probe
// plan, four snapshots.
func compareVersions(t *testing.T, baseline, candidate *ordersAPI, cfg oracle.Config) *oracle.Result {
	t.Helper()
	b, c := twoBranches(t, endToEndSchema)
	baseline.conn, candidate.conn = b, c
	baseline.idSeed, candidate.idSeed = 1, 2

	// Separate clocks, started a third of a second apart, because the point of
	// served_at is that the two sides disagree about it. One shared clock would
	// make them agree and prove nothing.
	baseClock := clock.NewFake(epoch)
	candClock := clock.NewFake(epoch.Add(320 * time.Millisecond))
	baseline.now, candidate.now = baseClock.Now, candClock.Now

	bs := httptest.NewServer(baseline.handler())
	t.Cleanup(bs.Close)
	cs := httptest.NewServer(candidate.handler())
	t.Cleanup(cs.Close)

	opts := oracle.DatabaseOptions{}
	in := oracle.Input{Config: cfg, Database: opts}
	in.BaselineBefore, in.CandidateBefore = capture(t, b, opts), capture(t, c, opts)
	in.Probes = oracle.Drive(context.Background(),
		&oracle.Driver{Clock: clock.NewFake(epoch)}, bs.URL, cs.URL, endToEndProbes, nil)
	in.BaselineAfter, in.CandidateAfter = capture(t, b, opts), capture(t, c, opts)

	res := oracle.Compare(in)
	res.BaselineRef, res.CandidateRef = "baseline", "candidate"
	res.BaselineHow = "the version under test"
	return res
}

// The change that does not matter. Every difference here is one a byte
// comparison reports on every single request: indented JSON, a tracing header,
// a clock reading in the body, and a fresh identifier in the body.
func TestEndToEnd_AChangeThatDoesNotMatterReportsNothing(t *testing.T) {
	res := compareVersions(t,
		&ordersAPI{},
		&ordersAPI{indent: true, echoRequestID: true},
		oracle.Config{})

	require.Emptyf(t, res.Findings, "%s", res.Text())
	require.Equal(t, "identical", res.Verdict())

	// The responses really were different bytes, or this proves nothing.
	require.NotEqual(t,
		string(res.Probes[0].Baseline.Body), string(res.Probes[0].Candidate.Body),
		"the two servers produced identical bytes, so nothing was normalised")

	// And it says what it absorbed.
	describe := res.Ignored.Describe()
	require.Contains(t, describe, "timestamp normaliser")
	require.Contains(t, describe, "uuid normaliser")
	require.Contains(t, describe, "x-request-id")
}

// The change that matters, and the shape of it is the point. Every response is
// identical: the endpoint still answers 201 with the order in it. The row is
// gone, and the only thing that can say so is the database half.
func TestEndToEnd_AChangeThatMattersIsCriticalAndFoundInTheDatabase(t *testing.T) {
	res := compareVersions(t,
		&ordersAPI{},
		&ordersAPI{forgetCommit: true},
		oracle.Config{})

	for _, p := range res.Probes {
		require.Zerof(t, p.Findings,
			"%s differed in its response, and the point of this case is that none of them do:\n%s",
			p.Name, res.Text())
	}

	require.Len(t, res.Findings, 1)
	f := res.Findings[0]
	require.Equal(t, oracle.KindRowMissing, f.Kind)
	require.Equal(t, oracle.Critical, f.Severity)
	require.Equal(t, "public.orders", f.Where)
	require.Equal(t, "id=1", f.Path)
	require.Equal(t, oracle.PhaseTraffic, f.Phase,
		"the row was there before the traffic on neither side, so this is the application")
	require.Contains(t, f.Baseline, "total_cents=2599")
	require.Equal(t, "regressed", res.Verdict())
	require.True(t, oracle.AtLeast(res.Findings, oracle.Critical))
}

// The two cases apart. The same threshold that fails the second run passes the
// first, which is the property that decides whether anybody leaves this turned
// on.
func TestEndToEnd_TheDefaultThresholdSeparatesTheTwo(t *testing.T) {
	harmless := compareVersions(t,
		&ordersAPI{}, &ordersAPI{indent: true, echoRequestID: true}, oracle.Config{})
	breaking := compareVersions(t,
		&ordersAPI{}, &ordersAPI{forgetCommit: true}, oracle.Config{})

	require.False(t, oracle.AtLeast(harmless.Findings, oracle.Critical))
	require.True(t, oracle.AtLeast(breaking.Findings, oracle.Critical))
}

// The normalisation is what makes the first case quiet, and this is the proof
// rather than the claim. The same harmless change, compared with the timestamp
// normaliser turned off, reports a clock reading in every response and in the
// row the traffic wrote. That is the wall of noise the layer exists to prevent,
// and it is one manifest key away, which is the honest way to expose it.
func TestEndToEnd_WithoutNormalisationTheHarmlessChangeCriesWolf(t *testing.T) {
	res := compareVersions(t,
		&ordersAPI{}, &ordersAPI{indent: true, echoRequestID: true},
		oracle.Config{KeepTimestamps: true})

	require.NotEmpty(t, res.Findings)
	for _, f := range res.Findings {
		require.Truef(t,
			strings.Contains(f.Path+" "+f.Detail, "served_at") ||
				strings.Contains(f.Path+" "+f.Detail, "placed_at"),
			"every difference here should be a clock reading, and %q %q is not", f.Path, f.Detail)
	}
	for _, n := range res.Ignored.Normalisers {
		require.NotEqual(t, "timestamp", n.Name,
			"the timestamp normaliser is off and reports having absorbed something")
	}
	// The UUID one is still on and still working, which is what makes this a
	// test of one switch rather than of the whole layer.
	require.Contains(t, res.Ignored.Describe(), "uuid normaliser")
}
