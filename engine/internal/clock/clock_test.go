package clock_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/antifailure/antifailure/engine/internal/clock"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestFake_Now_DoesNotMoveOnItsOwn(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(epoch)
	first := c.Now()
	for i := 0; i < 1000; i++ {
		require.Equal(t, first, c.Now())
	}
}

func TestFake_Advance_MovesNowByExactlyTheDuration(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(epoch)
	c.Advance(90 * time.Minute)
	require.Equal(t, epoch.Add(90*time.Minute), c.Now())
}

func TestFake_After_FiresOnlyOnceTheDeadlineIsPassed(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(epoch)
	ch := c.After(time.Minute)

	c.Advance(59 * time.Second)
	select {
	case <-ch:
		t.Fatal("After fired before its deadline")
	default:
	}

	c.Advance(time.Second)
	select {
	case at := <-ch:
		require.Equal(t, epoch.Add(time.Minute), at)
	default:
		t.Fatal("After did not fire at its deadline")
	}
}

func TestFake_After_WithNonPositiveDelayFiresImmediately(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(epoch)
	select {
	case at := <-c.After(0):
		require.Equal(t, epoch, at)
	default:
		t.Fatal("a zero delay must be already due")
	}
}

func TestFake_Advance_ReleasesWaitersInDeadlineOrder(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(epoch)
	third := c.After(3 * time.Second)
	first := c.After(1 * time.Second)
	second := c.After(2 * time.Second)

	c.Advance(5 * time.Second)

	require.Equal(t, epoch.Add(1*time.Second), <-first)
	require.Equal(t, epoch.Add(2*time.Second), <-second)
	require.Equal(t, epoch.Add(3*time.Second), <-third)
}

func TestFake_Ticker_FiresOncePerElapsedPeriod(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(epoch)
	tk := c.NewTicker(time.Second)
	defer tk.Stop()

	done := make(chan int, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		n := 0
		for range tk.C() {
			n++
			if n == 5 {
				done <- n
				return
			}
		}
	}()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case n := <-done:
			require.Equal(t, 5, n)
			wg.Wait()
			return
		case <-deadline:
			t.Fatal("ticker did not deliver five ticks")
		default:
			c.Advance(time.Second)
		}
	}
}

func TestFake_Sleep_ReturnsNilWhenTheClockPassesTheDeadline(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(epoch)
	ctx := context.Background()

	errc := make(chan error, 1)
	go func() { errc <- c.Sleep(ctx, time.Minute) }()

	require.NoError(t, c.BlockUntil(ctx, 1))
	c.Advance(time.Minute)
	require.NoError(t, <-errc)
	require.Equal(t, 0, c.WaiterCount(), "Sleep must not leave a waiter behind")
}

func TestFake_Sleep_ReturnsContextErrorWhenCancelledFirst(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(epoch)
	ctx, cancel := context.WithCancel(context.Background())

	errc := make(chan error, 1)
	go func() { errc <- c.Sleep(ctx, time.Hour) }()

	require.NoError(t, c.BlockUntil(context.Background(), 1))
	cancel()
	require.ErrorIs(t, <-errc, context.Canceled)
	require.Equal(t, 0, c.WaiterCount(), "a cancelled Sleep must drop its waiter")
}

func TestFake_Timer_StopPreventsTheFire(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(epoch)
	tm := c.NewTimer(time.Second)
	require.True(t, tm.Stop(), "Stop on a live timer reports true")
	require.False(t, tm.Stop(), "Stop on a stopped timer reports false")

	c.Advance(time.Hour)
	select {
	case <-tm.C():
		t.Fatal("a stopped timer must not fire")
	default:
	}
}

func TestFake_Timer_ResetKeepsTheChannelIdentity(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(epoch)
	tm := c.NewTimer(time.Hour)
	ch := tm.C()
	tm.Reset(time.Second)

	c.Advance(time.Second)
	select {
	case at := <-ch:
		require.Equal(t, epoch.Add(time.Second), at)
	default:
		t.Fatal("the channel held before Reset must still receive")
	}
}

func TestFake_Set_MovingBackwardsDoesNotReleaseWaiters(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(epoch)
	ch := c.After(time.Hour)
	c.Set(epoch.Add(-24 * time.Hour))
	require.Equal(t, epoch.Add(-24*time.Hour), c.Now())
	select {
	case <-ch:
		t.Fatal("a backwards clock move must not fire a waiter")
	default:
	}
}

func TestFake_Since_UsesTheFakeNow(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(epoch)
	c.Advance(3 * time.Hour)
	require.Equal(t, 3*time.Hour, c.Since(epoch))
}

func TestFake_Advance_WithNegativeDurationPanics(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(epoch)
	require.Panics(t, func() { c.Advance(-time.Second) })
}

func TestFake_ConcurrentWaitersAreAllReleased(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(epoch)
	const n = 64
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			<-c.After(time.Second)
		}()
	}
	require.NoError(t, c.BlockUntil(context.Background(), n))
	c.Advance(time.Second)
	wg.Wait()
}

func TestReal_Sleep_HonorsCancellation(t *testing.T) {
	t.Parallel()
	c := clock.New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, c.Sleep(ctx, time.Hour), context.Canceled)
}

func TestReal_NowAdvances(t *testing.T) {
	t.Parallel()
	c := clock.New()
	before := c.Now()
	require.NoError(t, c.Sleep(context.Background(), time.Millisecond))
	require.True(t, c.Now().After(before) || c.Now().Equal(before))
	require.GreaterOrEqual(t, c.Since(before), time.Duration(0))
}

func TestReal_TimerAndTickerWork(t *testing.T) {
	t.Parallel()
	c := clock.New()
	tm := c.NewTimer(time.Millisecond)
	<-tm.C()
	require.False(t, tm.Stop(), "a timer that already fired is not active")
	// Reset reports whether the timer was active, which a fired timer is not.
	require.False(t, tm.Reset(time.Millisecond))
	<-tm.C()

	tk := c.NewTicker(time.Millisecond)
	<-tk.C()
	tk.Reset(time.Millisecond)
	<-tk.C()
	tk.Stop()

	<-c.After(time.Millisecond)
}
