package pgcrash

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// Availability is what a probe running beside the fault saw of the database:
// whether it stopped answering at all, and for how long.
//
// It exists because the number it replaces was not a measurement of the
// database. It was the time from the fault to the first query answered AFTER
// the proof had waited out its settle, undone the fault and stopped its
// writers, and the first attempt was made only then. It could never read below
// the settle: a database measured ready 1.66s after its checkpointer was killed
// was reported unreachable for 3.1s, and a film and an MCP answer quoted it.
type Availability struct {
	// Unreachable is whether any probe attempt went unanswered. False means
	// every attempt, from the moment the fault was injected, got an answer,
	// and then there is no outage to put a number on.
	Unreachable bool `json:"unreachable"`
	// For is from the start of the first attempt that went unanswered to the
	// finish of the first answered attempt started after it. Attempts start
	// every Interval, so the outage is known to Interval.
	For time.Duration `json:"for"`
	// Recovered is whether an answered attempt followed the first unanswered
	// one before the probe stopped. When it is false, For is how long the
	// probe watched the database not answer, a floor rather than the outage.
	Recovered bool `json:"recovered"`
	// Interval is the probe's resolution.
	Interval time.Duration `json:"interval"`
}

// probeInterval is how often an attempt starts.
//
// A tenth of a second resolves a crash restart, which on a small cluster is
// under a second, and it costs a recovering database ten short connections a
// second, which a postmaster that is refusing them answers from its own
// process in microseconds.
const probeInterval = 100 * time.Millisecond

// probeTimeout is how long one attempt may take before it counts as
// unanswered. A frozen database accepts the TCP connection in the kernel and
// then says nothing, so without a limit an attempt against it never ends, and
// an attempt that never ends is a probe that never reports.
const probeTimeout = time.Second

// probeAttempt is one connection and one trivial query.
type probeAttempt func(ctx context.Context) error

// pgAttempt is the attempt against a real database.
func pgAttempt(url string) probeAttempt {
	return func(ctx context.Context) error {
		conn, err := pgx.Connect(ctx, url)
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
		var one int
		return conn.QueryRow(ctx, "SELECT 1").Scan(&one)
	}
}

// probeSample is one attempt: when it started, when it finished, and whether
// it was answered within the timeout.
type probeSample struct {
	at, done time.Time
	ok       bool
}

// probe runs attempts on a fixed interval, each in its own goroutine so that
// one hanging against a frozen database does not delay the next, and keeps
// what each one found.
type probe struct {
	interval, timeout time.Duration
	attempt           probeAttempt

	stopTicking context.CancelFunc
	ticking     sync.WaitGroup
	running     sync.WaitGroup

	mu      sync.Mutex
	samples []probeSample
}

// startProbe begins probing now. Attempts run under their own timeout and not
// under ctx, so a run being cancelled cannot turn attempts it cut short into
// an outage the database never had.
func startProbe(ctx context.Context, attempt probeAttempt, interval, timeout time.Duration) *probe {
	actx := context.WithoutCancel(ctx)
	tctx, stopTicking := context.WithCancel(actx)
	p := &probe{interval: interval, timeout: timeout, attempt: attempt, stopTicking: stopTicking}
	p.ticking.Add(1)
	go func() {
		defer p.ticking.Done()
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			p.launch(actx)
			select {
			case <-tctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	return p
}

func (p *probe) launch(ctx context.Context) {
	at := time.Now()
	p.running.Add(1)
	go func() {
		defer p.running.Done()
		actx, cancel := context.WithTimeout(ctx, p.timeout)
		defer cancel()
		err := p.attempt(actx)
		done := time.Now()
		p.mu.Lock()
		p.samples = append(p.samples, probeSample{at: at, done: done, ok: err == nil})
		p.mu.Unlock()
	}()
}

// settle is called once the proof knows the database answers again. It lets
// the probe run two more intervals, so an attempt starts after that moment,
// and then waits, up to within, for an answered attempt to follow the first
// unanswered one. An attempt still in flight is waited for by stop.
func (p *probe) settle(within time.Duration) {
	time.Sleep(2 * p.interval)
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if a := p.read(); !a.Unreachable || a.Recovered {
			return
		}
		time.Sleep(p.interval / 2)
	}
}

// stop ends the probe, waits for every attempt it started to finish or time
// out, and says what it saw.
func (p *probe) stop() Availability {
	p.stopTicking()
	p.ticking.Wait()
	p.running.Wait()
	return p.read()
}

func (p *probe) read() Availability {
	p.mu.Lock()
	samples := append([]probeSample(nil), p.samples...)
	p.mu.Unlock()
	return availabilityOf(samples, p.interval)
}

// availabilityOf reads an outage out of what the attempts found.
//
// It starts at the START of the first attempt that went unanswered, since the
// database was not answering from then. It ends at the FINISH of the first
// answered attempt started after that, since the database was answering by
// then. The finish and not the start, because against a frozen database an
// attempt started during the freeze is answered the moment it thaws, and its
// start would end the outage before the thaw. Attempts are read in the order
// they started, not the order they reported, because one that hung for its
// whole timeout reports after attempts that started later.
func availabilityOf(samples []probeSample, interval time.Duration) Availability {
	sort.Slice(samples, func(i, j int) bool { return samples[i].at.Before(samples[j].at) })
	a := Availability{Interval: interval}
	first := -1
	for i, s := range samples {
		if !s.ok {
			first = i
			break
		}
	}
	if first < 0 {
		return a
	}
	a.Unreachable = true
	from := samples[first].at
	var until time.Time
	for _, s := range samples[first+1:] {
		if s.ok && (until.IsZero() || s.done.Before(until)) {
			until = s.done
		}
	}
	if until.IsZero() {
		a.For = samples[len(samples)-1].at.Sub(from)
		return a
	}
	a.For, a.Recovered = until.Sub(from), true
	return a
}
