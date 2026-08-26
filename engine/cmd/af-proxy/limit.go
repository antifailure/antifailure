package main

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Rate limiting exists to protect the other side.
//
// A load run against an environment whose Stripe rule is sandbox sends every
// one of those requests to Stripe's real sandbox, and a sandbox that gets
// four hundred requests a second from a preview environment is a sandbox
// somebody rate limits or revokes. The limit is per rule, because that is
// where a user would think to write it.
//
// It shapes rather than refuses. A refused request looks to the application
// exactly like the host being down, and an application that handles a 429 by
// retrying turns a limit into a storm. Waiting is slower and truthful.

// limiter is a token bucket per rule.
type limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	// perSecond is the refill rate.
	perSecond float64
	// burst is how many can go at once, which is what makes a limit usable:
	// an application that opens six connections at startup should not have
	// five of them wait a second each.
	burst  float64
	tokens float64
	last   time.Time
}

func newLimiter() *limiter { return &limiter{buckets: map[string]*bucket{}} }

// wait blocks until the rule allows another request, and reports how long it
// waited so the decision log can show it.
//
// A zero or unparseable limit is no limit. Refusing to start over a malformed
// rate would take an environment down over a typo in a field that is an
// optimisation.
func (l *limiter) wait(rule, spec string) time.Duration {
	perSecond, burst, ok := parseRate(spec)
	if !ok {
		return 0
	}

	started := time.Now()
	for {
		delay := l.reserve(rule, perSecond, burst)
		if delay <= 0 {
			return time.Since(started)
		}
		// Capped, so a mistaken limit of one an hour does not hold a request
		// open for an hour. A slow request is better than a hung one.
		if delay > 5*time.Second {
			delay = 5 * time.Second
		}
		time.Sleep(delay)
	}
}

// reserve takes a token if there is one, and returns how long until there is.
func (l *limiter) reserve(rule string, perSecond, burst float64) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, ok := l.buckets[rule]
	if !ok {
		// Starts full, so the first request of a run is not made to wait for
		// a budget it has not spent.
		b = &bucket{perSecond: perSecond, burst: burst, tokens: burst, last: now}
		l.buckets[rule] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * b.perSecond
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
	b.last = now

	if b.tokens >= 1 {
		b.tokens--
		return 0
	}
	return time.Duration((1 - b.tokens) / b.perSecond * float64(time.Second))
}

// parseRate reads a limit written the way somebody would write it.
//
// "10/s", "600/m", "5000/h", or a bare number meaning per second. Burst
// defaults to the per second rate, or to one when that is below one, so a
// limit of "60/m" still lets a single request through immediately.
func parseRate(spec string) (perSecond, burst float64, ok bool) {
	spec = strings.TrimSpace(strings.ToLower(spec))
	if spec == "" {
		return 0, 0, false
	}
	count, unit, hasUnit := strings.Cut(spec, "/")
	n, err := strconv.ParseFloat(strings.TrimSpace(count), 64)
	if err != nil || n <= 0 {
		return 0, 0, false
	}
	per := n
	if hasUnit {
		switch strings.TrimSpace(unit) {
		case "s", "sec", "second":
		case "m", "min", "minute":
			per = n / 60
		case "h", "hr", "hour":
			per = n / 3600
		default:
			return 0, 0, false
		}
	}
	burst = per
	if burst < 1 {
		burst = 1
	}
	return per, burst, true
}

// describeRate renders a limit for a message.
func describeRate(spec string) string {
	per, burst, ok := parseRate(spec)
	if !ok {
		return "no limit"
	}
	return fmt.Sprintf("%.3g a second, bursting to %.0f", per, burst)
}
