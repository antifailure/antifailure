// Package clock provides the only source of time the engine is allowed to use.
//
// Library code never calls time.Now or time.Sleep. Both are forbidden by the
// linter outside main and tests. Everything that needs the current instant, a
// delay, or a ticker takes a Clock, which has a real implementation for
// production and a fake implementation that tests advance explicitly. That is
// what makes scheduling, retries, TTLs, and idle sleep testable without
// waiting for wall time, and what keeps the suite free of the flakes that
// time-based waits produce.
package clock

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Clock is the engine's view of time.
type Clock interface {
	// Now returns the current instant.
	Now() time.Time
	// Since returns the time elapsed since t.
	Since(t time.Time) time.Duration
	// After returns a channel that receives once, after d has elapsed.
	After(d time.Duration) <-chan time.Time
	// NewTimer returns a timer that fires once after d.
	NewTimer(d time.Duration) Timer
	// NewTicker returns a ticker that fires every d.
	NewTicker(d time.Duration) Ticker
	// Sleep blocks until d has elapsed or ctx is done, whichever is first.
	// It returns ctx.Err() when the context ended first and nil otherwise.
	Sleep(ctx context.Context, d time.Duration) error
}

// Timer fires once.
type Timer interface {
	C() <-chan time.Time
	Stop() bool
	Reset(d time.Duration) bool
}

// Ticker fires repeatedly.
type Ticker interface {
	C() <-chan time.Time
	Stop()
	Reset(d time.Duration)
}

// Real is the wall clock. It is the only place in the engine that may call
// into the time package directly.
type Real struct{}

// New returns the wall clock.
func New() Clock { return Real{} }

// Now returns the current instant.
func (Real) Now() time.Time { return time.Now() }

// Since returns the time elapsed since t.
func (Real) Since(t time.Time) time.Duration { return time.Since(t) }

// After returns a channel that receives once, after d has elapsed.
func (Real) After(d time.Duration) <-chan time.Time { return time.After(d) }

// NewTimer returns a timer that fires once after d.
func (Real) NewTimer(d time.Duration) Timer { return &realTimer{t: time.NewTimer(d)} }

// NewTicker returns a ticker that fires every d.
func (Real) NewTicker(d time.Duration) Ticker { return &realTicker{t: time.NewTicker(d)} }

// Sleep blocks until d has elapsed or ctx is done.
func (Real) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type realTimer struct{ t *time.Timer }

func (r *realTimer) C() <-chan time.Time        { return r.t.C }
func (r *realTimer) Stop() bool                 { return r.t.Stop() }
func (r *realTimer) Reset(d time.Duration) bool { return r.t.Reset(d) }

type realTicker struct{ t *time.Ticker }

func (r *realTicker) C() <-chan time.Time   { return r.t.C }
func (r *realTicker) Stop()                 { r.t.Stop() }
func (r *realTicker) Reset(d time.Duration) { r.t.Reset(d) }

// Fake is a clock that only moves when a test advances it.
//
// Every waiter registered through After, NewTimer, NewTicker, or Sleep is
// released deterministically by Advance, in deadline order, so a test can
// prove what happens at a TTL boundary or a daylight-saving transition without
// waiting for one.
type Fake struct {
	mu      sync.Mutex
	now     time.Time
	waiters []*waiter
	nextID  int64
}

type waiter struct {
	id       int64
	deadline time.Time
	ch       chan time.Time
	period   time.Duration // zero for one-shot waiters
	stopped  bool
}

// NewFake returns a fake clock set to start.
func NewFake(start time.Time) *Fake {
	return &Fake{now: start}
}

// Now returns the fake clock's current instant.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Since returns the elapsed time according to the fake clock.
func (f *Fake) Since(t time.Time) time.Duration { return f.Now().Sub(t) }

// After returns a channel released when the fake clock passes d.
func (f *Fake) After(d time.Duration) <-chan time.Time {
	return f.addWaiter(d, 0).ch
}

// NewTimer returns a timer released when the fake clock passes d.
func (f *Fake) NewTimer(d time.Duration) Timer {
	return &fakeTimer{f: f, w: f.addWaiter(d, 0)}
}

// NewTicker returns a ticker released every d of fake time.
func (f *Fake) NewTicker(d time.Duration) Ticker {
	if d <= 0 {
		panic("clock: non-positive interval for NewTicker")
	}
	return &fakeTicker{f: f, w: f.addWaiter(d, d)}
}

// Sleep blocks until the fake clock passes d or ctx is done.
func (f *Fake) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	w := f.addWaiter(d, 0)
	select {
	case <-ctx.Done():
		f.removeWaiter(w.id)
		return ctx.Err()
	case <-w.ch:
		return nil
	}
}

func (f *Fake) addWaiter(d time.Duration, period time.Duration) *waiter {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	w := &waiter{
		id:       f.nextID,
		deadline: f.now.Add(d),
		ch:       make(chan time.Time, 1),
		period:   period,
	}
	f.waiters = append(f.waiters, w)
	// A waiter with a non-positive delay is already due. Release it now so
	// that callers do not block until the next Advance.
	if d <= 0 && period == 0 {
		w.ch <- f.now
		w.stopped = true
	}
	return w
}

func (f *Fake) removeWaiter(id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, w := range f.waiters {
		if w.id == id {
			f.waiters = append(f.waiters[:i], f.waiters[i+1:]...)
			return
		}
	}
}

// Advance moves the fake clock forward by d, releasing every waiter whose
// deadline the move passes, in deadline order. Tickers are rescheduled and
// fire once per elapsed period, so advancing by ten intervals delivers ten
// ticks rather than one.
func (f *Fake) Advance(d time.Duration) {
	if d < 0 {
		panic("clock: Advance with a negative duration")
	}
	f.mu.Lock()
	target := f.now.Add(d)
	for {
		due := f.dueLocked(target)
		if len(due) == 0 {
			break
		}
		w := due[0]
		f.now = w.deadline
		if w.period > 0 {
			w.deadline = w.deadline.Add(w.period)
		} else {
			w.stopped = true
			f.dropLocked(w.id)
		}
		fireAt := f.now
		ch := w.ch
		f.mu.Unlock()
		// A buffered send that would block means the receiver has not drained
		// the previous tick. Dropping matches the standard library's ticker
		// behavior and keeps Advance from deadlocking.
		select {
		case ch <- fireAt:
		default:
		}
		f.mu.Lock()
	}
	f.now = target
	f.mu.Unlock()
}

// Set moves the fake clock to t. Moving backwards is allowed so that clock
// rollback can be tested; waiters are not released by a backwards move.
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	if !t.After(f.now) {
		f.now = t
		f.mu.Unlock()
		return
	}
	d := t.Sub(f.now)
	f.mu.Unlock()
	f.Advance(d)
}

// BlockUntil waits until n waiters are registered. It lets a test synchronize
// with a goroutine that is about to sleep, without a sleep of its own.
func (f *Fake) BlockUntil(ctx context.Context, n int) error {
	for {
		f.mu.Lock()
		count := 0
		for _, w := range f.waiters {
			if !w.stopped {
				count++
			}
		}
		f.mu.Unlock()
		if count >= n {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		// Yield rather than sleep so the fake clock never depends on wall time.
		runtimeGosched()
	}
}

// WaiterCount reports how many live waiters the fake clock holds. Tests use it
// to assert that a component cleaned up its timers.
func (f *Fake) WaiterCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, w := range f.waiters {
		if !w.stopped {
			n++
		}
	}
	return n
}

func (f *Fake) dueLocked(target time.Time) []*waiter {
	var due []*waiter
	for _, w := range f.waiters {
		if w.stopped {
			continue
		}
		if !w.deadline.After(target) {
			due = append(due, w)
		}
	}
	sort.SliceStable(due, func(i, j int) bool {
		if due[i].deadline.Equal(due[j].deadline) {
			return due[i].id < due[j].id
		}
		return due[i].deadline.Before(due[j].deadline)
	})
	return due
}

func (f *Fake) dropLocked(id int64) {
	for i, w := range f.waiters {
		if w.id == id {
			f.waiters = append(f.waiters[:i], f.waiters[i+1:]...)
			return
		}
	}
}

type fakeTimer struct {
	f *Fake
	w *waiter
}

func (t *fakeTimer) C() <-chan time.Time { return t.w.ch }

func (t *fakeTimer) Stop() bool {
	t.f.mu.Lock()
	active := !t.w.stopped
	t.w.stopped = true
	t.f.mu.Unlock()
	t.f.removeWaiter(t.w.id)
	return active
}

func (t *fakeTimer) Reset(d time.Duration) bool {
	active := t.Stop()
	nw := t.f.addWaiter(d, 0)
	// Preserve the channel identity so a caller holding C() still receives.
	t.f.mu.Lock()
	nw.ch = t.w.ch
	t.f.mu.Unlock()
	t.w = nw
	return active
}

type fakeTicker struct {
	f *Fake
	w *waiter
}

func (t *fakeTicker) C() <-chan time.Time { return t.w.ch }

func (t *fakeTicker) Stop() {
	t.f.mu.Lock()
	t.w.stopped = true
	t.f.mu.Unlock()
	t.f.removeWaiter(t.w.id)
}

func (t *fakeTicker) Reset(d time.Duration) {
	if d <= 0 {
		panic("clock: non-positive interval for Reset")
	}
	t.f.mu.Lock()
	t.w.deadline = t.f.now.Add(d)
	t.w.period = d
	t.w.stopped = false
	t.f.mu.Unlock()
}
