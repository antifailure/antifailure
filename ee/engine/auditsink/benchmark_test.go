// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package auditsink

// The number this lane owes: seconds from a privileged action to it appearing
// in your SIEM.
//
// No number is quotable unless the harness that produced it is in this
// repository, the methodology is published beside it, and a customer can run it
// against their own stack and get their own number. So this is a test rather
// than a spreadsheet, `just benchmark` runs it, and every run writes a dated
// report naming what it measured and what it did not.
//
// # What is being timed, exactly
//
// From the instant the action happened, which is the `OccurredAt` the engine
// stamps in engine/internal/env/audit.go, to the instant the RECEIVER has the
// entry. Not to the instant the sink finished writing: a write that returns
// while bytes are still in a kernel buffer has not put anything in anybody's
// SIEM, and measuring to the end of Write would flatter the product by exactly
// the part a customer cares about. Every destination here signals from inside
// its own handler, after it has the body.
//
// # What this number is NOT
//
// With no receiver configured this measures loopback, so it is the latency the
// PRODUCT is responsible for and nothing else. The wide area time to a hosted
// SIEM is the customer's own network and is not ours to quote. That is why the
// harness reads an address: point AF_AUDIT_BENCHMARK_SYSLOG_ADDRESS at a real
// collector and the number becomes the whole path, on their network, measured
// by them. Publishing the loopback figure alone and calling it "to your SIEM"
// would be the kind of unsourced round number this repository bans by name.
//
// # The second number, which is the one that survives an outage
//
// A forwarder is only as good as what it does when the receiver is down. So the
// report also carries how long an undeliverable entry takes to become durable
// on disk, through the full retry and into the dead letter file, because that
// is the delay a customer actually experiences during a SIEM restart and it is
// the number that decides whether a team leaves this turned on.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// benchmarkRuns is how many entries each destination is timed over.
//
// Two hundred, because the interesting figure here is the tail rather than the
// mean: an audit entry that is usually fast and occasionally seconds late is a
// stream somebody cannot alert on. Two hundred is enough for a p95 to mean
// something and small enough that the whole benchmark is a few seconds.
const benchmarkRuns = 200

// The variables that point this at a real receiver, which is the whole reason
// the harness is shipped rather than the number.
const (
	benchSyslogEnv  = "AF_AUDIT_BENCHMARK_SYSLOG_ADDRESS"
	benchSyslogCA   = "AF_AUDIT_BENCHMARK_SYSLOG_CA_FILE"
	benchWebhookEnv = "AF_AUDIT_BENCHMARK_WEBHOOK_URL"
	benchOutEnv     = "AF_AUDIT_BENCHMARK_OUT"
)

// latency is what one destination measured.
type latency struct {
	name string
	// where says whether this was a local stand-in or a real receiver, and it
	// is printed in the report next to every figure. A number whose destination
	// is not named is a number nobody can check.
	where   string
	samples []time.Duration
}

func (l latency) at(q float64) time.Duration {
	if len(l.samples) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), l.samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(float64(len(sorted)-1) * q)
	return sorted[idx]
}

func (l latency) max() time.Duration { return l.at(1) }

// TestBenchmarkTheDelayFromAnActionToItsArrival measures and writes the report.
//
// Guarded by AF_BENCHMARK like the engine's own benchmarks, so that `go test
// ./...` does not spend seconds on it, and so that the number in the report is
// always one somebody asked for.
func TestBenchmarkTheDelayFromAnActionToItsArrival(t *testing.T) {
	if os.Getenv("AF_BENCHMARK") == "" && os.Getenv(benchOutEnv) == "" {
		t.Skip("skipped: set AF_BENCHMARK=1, or run `just benchmark`")
	}
	ctx := licensed(t)

	results := []latency{
		benchmarkSyslog(t, ctx),
		benchmarkWebhook(t, ctx),
		benchmarkObjectStore(t, ctx),
	}
	spooled := benchmarkTheDeadLetterPath(t, ctx)

	report := renderAuditBenchmark(results, spooled)
	t.Log("\n" + report)

	out := os.Getenv(benchOutEnv)
	if out == "" {
		return
	}
	require.NoError(t, os.MkdirAll(filepath.Dir(out), 0o755))
	require.NoError(t, os.WriteFile(out, []byte(report), 0o644))
}

// benchmarkSyslog times the TLS syslog sink.
//
// Against a real collector when one is configured, and against the in-process
// one otherwise. The in-process collector de-frames by octet count before it
// signals, so the clock stops when a receiver could actually have parsed the
// entry rather than when bytes arrived.
func benchmarkSyslog(t *testing.T, ctx context.Context) latency {
	t.Helper()
	if address := os.Getenv(benchSyslogEnv); address != "" {
		s, err := NewSyslog(SyslogConfig{Address: address, CAFile: os.Getenv(benchSyslogCA)})
		require.NoError(t, err)
		defer func() { _ = s.Close() }()

		// A real collector cannot tell us it has the entry, so this half stops
		// the clock when the write returns and SAYS SO in the report. It is the
		// weaker measurement and pretending otherwise would be the flattering
		// mistake this file exists to avoid.
		out := latency{name: "syslog over TLS", where: address + " (write returned, not receipt)"}
		for range benchmarkRuns {
			e := entry()
			e.OccurredAt = time.Now()
			require.NoError(t, s.Write(ctx, e))
			out.samples = append(out.samples, time.Since(e.OccurredAt))
		}
		return out
	}

	c := recordingSyslog(t)
	arrived := make(chan time.Time, benchmarkRuns)
	c.onMessage = func() { arrived <- time.Now() }

	s, err := NewSyslog(SyslogConfig{Address: "siem.local:6514", dial: c.dial})
	require.NoError(t, err)
	defer func() { _ = s.Close() }()

	out := latency{name: "syslog over TLS", where: "an in-process collector on loopback"}
	for range benchmarkRuns {
		e := entry()
		e.OccurredAt = time.Now()
		require.NoError(t, s.Write(ctx, e))
		out.samples = append(out.samples, waitArrival(t, arrived).Sub(e.OccurredAt))
	}
	return out
}

func benchmarkWebhook(t *testing.T, ctx context.Context) latency {
	t.Helper()
	arrived := make(chan time.Time, benchmarkRuns)

	url := os.Getenv(benchWebhookEnv)
	client := (*http.Client)(nil)
	where := url + " (write returned, not receipt)"
	if url == "" {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = readAll(r)
			arrived <- time.Now()
			w.WriteHeader(http.StatusNoContent)
		}))
		t.Cleanup(srv.Close)
		url, client, where = srv.URL, srv.Client(), "an in-process HTTPS receiver on loopback"
	}

	w, err := NewWebhook(WebhookConfig{
		URL: url, Secret: "benchmark", Client: client,
		DeadLetterFile: filepath.Join(t.TempDir(), "dead.jsonl"),
	})
	require.NoError(t, err)

	out := latency{name: "HTTPS webhook", where: where}
	for range benchmarkRuns {
		e := entry()
		e.OccurredAt = time.Now()
		require.NoError(t, w.Write(ctx, e))
		if client == nil {
			out.samples = append(out.samples, time.Since(e.OccurredAt))
			continue
		}
		out.samples = append(out.samples, waitArrival(t, arrived).Sub(e.OccurredAt))
	}
	return out
}

func benchmarkObjectStore(t *testing.T, ctx context.Context) latency {
	t.Helper()
	arrived := make(chan time.Time, benchmarkRuns)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = readAll(r)
		arrived <- time.Now()
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)

	s, err := NewObjectStore(ObjectStoreConfig{
		URL: srv.URL + "/audit/entries",
		Getenv: envMap(map[string]string{
			"AWS_ACCESS_KEY_ID": "AKIABENCHMARK", "AWS_SECRET_ACCESS_KEY": "benchmark",
		}),
	})
	require.NoError(t, err)

	out := latency{name: "object store drop", where: "an in-process S3 API on loopback"}
	for range benchmarkRuns {
		e := entry()
		e.OccurredAt = time.Now()
		require.NoError(t, s.Write(ctx, e))
		out.samples = append(out.samples, waitArrival(t, arrived).Sub(e.OccurredAt))
	}
	return out
}

// benchmarkTheDeadLetterPath times an entry the receiver refused, all the way
// to durable on disk.
//
// The real backoff, not the injected one. This figure is the delay a customer
// experiences during a SIEM restart, so measuring it with the pauses removed
// would measure something nobody ever waits through.
func benchmarkTheDeadLetterPath(t *testing.T, ctx context.Context) latency {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	spool := filepath.Join(t.TempDir(), "dead.jsonl")
	w, err := NewWebhook(WebhookConfig{
		URL: srv.URL, Client: srv.Client(), DeadLetterFile: spool,
	})
	require.NoError(t, err)

	out := latency{name: "webhook, receiver down, into the dead letter file",
		where: "an in-process HTTPS receiver answering 503"}
	// Ten rather than two hundred, because each one spends the whole real
	// backoff and the figure barely moves: what is being measured is a fixed
	// pause plus three round trips.
	for range 10 {
		e := entry()
		e.OccurredAt = time.Now()
		require.Error(t, w.Write(ctx, e), "the receiver answered 503 and the sink reported success")
		out.samples = append(out.samples, time.Since(e.OccurredAt))
	}
	stat, err := os.Stat(spool)
	require.NoError(t, err, "nothing was spooled, so this measured a hole rather than a fallback")
	require.NotZero(t, stat.Size())
	return out
}

func waitArrival(t *testing.T, ch <-chan time.Time) time.Time {
	t.Helper()
	select {
	case at := <-ch:
		return at
	case <-time.After(10 * time.Second):
		require.FailNow(t, "the receiver never got the entry")
		return time.Time{}
	}
}

func readAll(r *http.Request) (int64, error) {
	defer func() { _ = r.Body.Close() }()
	var n int64
	buf := make([]byte, 4096)
	for {
		read, err := r.Body.Read(buf)
		n += int64(read)
		if err != nil {
			return n, nil
		}
	}
}

// renderAuditBenchmark writes the report, with its own methodology in it.
func renderAuditBenchmark(results []latency, spooled latency) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# From a privileged action to your SIEM\n\n")
	fmt.Fprintf(&b, "Measured %s on %s/%s, over %d entries per destination.\n\n",
		time.Now().UTC().Format("2006-01-02"), runtime.GOOS, runtime.GOARCH, benchmarkRuns)

	fmt.Fprintf(&b, "| Destination | Measured against | Median | p95 | Worst |\n")
	fmt.Fprintf(&b, "| --- | --- | --- | --- | --- |\n")
	for _, r := range results {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n",
			r.name, r.where, round(r.at(0.5)), round(r.at(0.95)), round(r.max()))
	}
	fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n",
		spooled.name, spooled.where,
		round(spooled.at(0.5)), round(spooled.at(0.95)), round(spooled.max()))

	fmt.Fprintf(&b, `
## What this measures

The clock starts at the instant the action happened, which is the OccurredAt
the engine stamps on the entry in engine/internal/env/audit.go, and stops when
the RECEIVER has the entry rather than when the sink finished writing. A write
that returns while bytes are still in a kernel buffer has not put anything in
anybody's SIEM.

## What it does not measure, and how to get that number

With no receiver configured every figure above is loopback, so it is the delay
this product is responsible for and nothing else. The wide area time to a
hosted SIEM is your network and is not ours to quote. Point the harness at your
own collector and the number becomes the whole path, measured by you:

    AF_AUDIT_BENCHMARK_SYSLOG_ADDRESS=collector.internal:6514 \
      AF_AUDIT_BENCHMARK_SYSLOG_CA_FILE=/etc/ssl/collector-ca.pem \
      AF_AUDIT_BENCHMARK_WEBHOOK_URL=https://siem.example/ingest \
      just benchmark

Against a real receiver the syslog and webhook rows stop the clock when the
write returns rather than when the receiver has it, because a receiver we do
not control cannot tell us. Those two rows say so in the "measured against"
column. They are the weaker measurement and they are labelled rather than
quietly mixed in with the others.

## The row that matters during an outage

The last row is an entry the receiver refused, timed through the full retry and
into the dead letter file, with the real backoff rather than an injected one.
That is the delay somebody actually waits through while their SIEM restarts,
and it is the figure that decides whether a team leaves this turned on. The
entry is not lost during that window: it is on disk, in the same JSON the
receiver would have been given.
`)
	return b.String()
}

func round(d time.Duration) string {
	switch {
	case d >= time.Second:
		return d.Round(10 * time.Millisecond).String()
	case d >= time.Millisecond:
		return d.Round(10 * time.Microsecond).String()
	default:
		return d.Round(time.Microsecond).String()
	}
}
