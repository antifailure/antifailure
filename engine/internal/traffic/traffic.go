// Package traffic answers the question a load test could not ask: of
// everything production serves, how much of it does this run actually send.
//
// THE FAILURE IT WAS WRITTEN FOR, measured on this repository on 2026-09-06. A
// migration was made to hold AccessExclusiveLock on events and its partitions,
// and pg_locks confirmed nine relations locked at once for the whole window.
// af load smoke ran straight through it and reported 0.0 percent failed, with
// p95 improving from 41ms to 17ms. The four safe_routes in this repository's
// own manifest were written by hand and none of them reads that table, so the
// load test was blind to a table being unavailable for thirty seconds. It was
// not a weak result. It was a green one.
//
// A hand written route list cannot know which routes touch which tables, and
// nothing in the engine had ever been told what production serves, so nothing
// could say the list was thin. The fidelity report said worse than nothing: it
// called those four routes a reproduction of production's traffic.
//
// So: a profile is production's own endpoint mix, its arrival rate, its per
// route p95 and its peak concurrency, read once from telemetry a team already
// has, written down as a dated artifact and committed. Nothing in it is a
// request body, a header, a query string or an identifier. It is a count per
// route.
//
// Where each part lives:
//
//   - This file: the artifact, the comparison against what a run sends, and
//     the arithmetic that turns two route sets into a sentence.
//   - record.go: building one out of an OpenTelemetry export or an access log,
//     through the readers the load generator already uses, and saying what the
//     source could not answer.
//   - artifact.go: writing it, reading it back, and refusing a stale one.
//
// What this package deliberately is not. There is no agent, no SDK and no
// collector here, and nothing in it opens a socket. A profile is produced from
// a file a team already has on disk, once, by a command somebody runs. An
// agent inside production is the largest trust ask this product makes, and it
// is the one most likely to fail a security review; the file is the version of
// this that needs no application change at all.
package traffic

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/load"
	"github.com/antifailure/antifailure/engine/internal/volume"
)

// Percent and Count are volume's, deliberately, rather than a second pair
// here. A fidelity report prints a share of production's rows and a share of
// production's requests in the same paragraph, and two renderers that round
// differently would put two percentages of the same magnitude on one page
// disagreeing in the last digit. volume.Percent truncates toward zero, so a
// copy holding 99.9 percent never prints as 100, and that reasoning is worth
// exactly as much here.
// Percent renders a share as a percentage that is still a number at the small
// end, truncated toward zero so a run covering 99.9 percent never prints as
// 100.
func Percent(share float64) string { return volume.Percent(share) }

// Count renders a whole number with separators and no unit.
func Count(n int64) string { return volume.Count(n) }

// Profile is what production served when it was measured.
//
// It carries no data and no credential, which is what makes it committable:
// every field is a count, a duration or a route template that is already in
// the application's own router. The profile has to live in the tree beside the
// manifest for a check running on a pull request to have a denominator, and an
// artifact somebody cannot commit is an artifact nobody has.
type Profile struct {
	// CollectedAt is when it was recorded. A profile with no time on it is
	// refused rather than trusted, because the age is the only thing that says
	// whether the mix is still production's.
	CollectedAt time.Time `json:"collected_at"`
	// Source describes what it was read from, in words. Never a credential and
	// never a URL that would have to be reachable to make sense.
	Source string `json:"source,omitempty"`
	// From and To are the window the traffic was observed over. Both are
	// carried rather than a duration, because the question somebody asks of a
	// mix is which week it was, and a duration cannot answer it.
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	// Requests is how many the window held, over every route below.
	Requests int64 `json:"requests"`
	// PeakConcurrency is the largest number of requests production had in
	// flight at once, and zero means the source could not say. An average rate
	// is what a load generator aims at and a peak is what fills a connection
	// pool, so the two are recorded separately and neither is derived from the
	// other.
	PeakConcurrency int `json:"peak_concurrency,omitempty"`
	// Routes are the endpoints production served, most requests first.
	Routes []Route `json:"routes"`
	// Missing names what the source could not answer, and why. A profile that
	// silently omits the p95 reads exactly like a production that serves
	// everything instantly.
	Missing []string `json:"missing,omitempty"`
}

// Route is one endpoint and how much of production's traffic it carried.
type Route struct {
	// Method is the HTTP method, upper case.
	Method string `json:"method"`
	// Path is the route template, with identifiers already collapsed, so that
	// /users/4821 and /users/9130 are one route and not two.
	Path string `json:"path"`
	// Requests is how many of them the window held.
	Requests int64 `json:"requests"`
	// P95Ms is what production served it in, and zero means unknown rather
	// than instant. An access log line carries no duration, which is why a
	// profile read from one says so in Missing rather than reporting a page
	// full of zeroes.
	P95Ms float64 `json:"p95_ms,omitempty"`
}

// String renders a route the way a report does.
func (r Route) String() string { return r.Method + " " + r.Path }

// Endpoint is one route a run would actually send.
//
// The run's side of the comparison, and a separate type from Route because a
// run is asked what it sends and nothing else: a weight, a p95 and a request
// count on the generator's side answer no question this package exists to ask.
// It is the same separation volume draws between a Table and a TableRows.
type Endpoint struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// String renders an endpoint the way a report does.
func (e Endpoint) String() string { return e.Method + " " + e.Path }

// Rate is how many requests a second production served, and false when the
// window is too short to divide by.
func (p Profile) Rate() (float64, bool) {
	window := p.Window()
	if p.Requests <= 0 || window < time.Second {
		return 0, false
	}
	return float64(p.Requests) / window.Seconds(), true
}

// Window is how long the traffic was observed for.
func (p Profile) Window() time.Duration {
	if p.From.IsZero() || !p.To.After(p.From) {
		return 0
	}
	return p.To.Sub(p.From)
}

// Age is how old the profile is at now.
func (p Profile) Age(now time.Time) time.Duration {
	if p.CollectedAt.IsZero() {
		return 0
	}
	return now.Sub(p.CollectedAt)
}

// Find returns one route by method and path.
func (p Profile) Find(method, path string) (Route, bool) {
	key := canonical(method, path)
	for _, r := range p.Routes {
		if canonical(r.Method, r.Path) == key {
			return r, true
		}
	}
	return Route{}, false
}

// RouteShare is one route and whether the run sends it.
type RouteShare struct {
	Route Route `json:"route"`
	// Sent reports whether the run would send this route at all. Not how
	// often: a run that sends a route once has exercised it, and a run that
	// never sends it cannot fail on it however fast the rest of the mix goes.
	Sent bool `json:"sent"`
}

// Share is the fraction of production's requests this route carried.
func (s RouteShare) Share(total int64) (float64, bool) {
	if total <= 0 {
		return 0, false
	}
	return float64(s.Route.Requests) / float64(total), true
}

// Coverage is what a run sends, measured against what production served.
type Coverage struct {
	// CollectedAt and Source are carried through from the profile, so that
	// anything quoting the coverage can date it.
	CollectedAt time.Time `json:"collected_at"`
	Source      string    `json:"source,omitempty"`
	// Requests is how many production served in the window, and Covered how
	// many of them went to a route this run would send.
	Requests int64 `json:"requests"`
	Covered  int64 `json:"covered_requests"`
	// Routes is every route production served, most requests first, each
	// saying whether the run reaches it.
	Routes []RouteShare `json:"routes,omitempty"`
	// Invented names routes the run sends that production never served. It is
	// not a fault in the profile: it is traffic somebody wrote down from
	// memory, which is the other half of the same defect. A run spending its
	// budget on a route production does not have is a run not spending it on
	// one production does.
	Invented []Endpoint `json:"invented,omitempty"`
}

// Compare measures what a run sends against what production served.
//
// Both sides are canonicalised the same way, through the same path normaliser
// the readers use, so a manifest that names GET /runs/4821 and a profile that
// holds GET /runs/{id} are one route rather than a false absence. A route the
// run sends and production never served is named rather than folded into
// either total, for the reason volume names a table only one side has: folding
// it in would let a route somebody invented move the number that is supposed
// to measure the invention.
func Compare(sent []Endpoint, p Profile) Coverage {
	c := Coverage{CollectedAt: p.CollectedAt, Source: p.Source, Requests: p.Requests}
	sends := map[string]bool{}
	for _, e := range sent {
		sends[canonical(e.Method, e.Path)] = true
	}
	served := map[string]bool{}
	for _, r := range p.Routes {
		key := canonical(r.Method, r.Path)
		served[key] = true
		share := RouteShare{Route: r, Sent: sends[key]}
		if share.Sent && r.Requests > 0 {
			c.Covered += r.Requests
		}
		c.Routes = append(c.Routes, share)
	}
	for _, e := range sent {
		if !served[canonical(e.Method, e.Path)] {
			c.Invented = append(c.Invented, Endpoint{
				Method: strings.ToUpper(strings.TrimSpace(e.Method)),
				Path:   load.NormalisePath(strings.TrimSpace(e.Path)),
			})
		}
	}
	sort.SliceStable(c.Routes, func(i, j int) bool {
		if c.Routes[i].Route.Requests != c.Routes[j].Route.Requests {
			return c.Routes[i].Route.Requests > c.Routes[j].Route.Requests
		}
		return c.Routes[i].Route.String() < c.Routes[j].Route.String()
	})
	sort.Slice(c.Invented, func(i, j int) bool { return c.Invented[i].String() < c.Invented[j].String() })
	return c
}

// Share is the fraction of production's requests the run's routes carried.
//
// False when the profile counted no request, which is not zero: a comparison
// against nothing has not shown the run to be blind.
func (c Coverage) Share() (float64, bool) {
	if c.Requests <= 0 || len(c.Routes) == 0 {
		return 0, false
	}
	return float64(c.Covered) / float64(c.Requests), true
}

// Uncovered returns the routes production served and this run does not send,
// heaviest first.
func (c Coverage) Uncovered() []RouteShare {
	var out []RouteShare
	for _, r := range c.Routes {
		if !r.Sent {
			out = append(out, r)
		}
	}
	return out
}

// MinShare is how much of production's traffic a route has to carry before its
// absence from a run is a finding.
//
// One request in a thousand, and it is a floor on noise rather than a
// tolerance. Every production of any age serves a tail of routes that exist
// only because a crawler asked for them, a health checker polls them, or
// somebody ran a script once, and a rule that demanded every one of them be
// exercised would fail every real profile forever. A check that can never pass
// teaches people to widen it until it can never fail, which is the failure
// this whole lane exists to correct. A route at one in a thousand of a million
// requests is still a thousand requests, and it is still named in the report;
// what MinShare decides is only whether its absence changes the verdict.
const MinShare = 0.001

// Covers reports whether the run reaches everything production actually
// leans on.
//
// Every route above MinShare has to be sent. Not the headline share alone: a
// run can carry ninety nine percent of production's requests and still never
// touch the one endpoint that reads the table a migration locks, which is
// exactly what happened here on 2026-09-06. The heavy routes are the ones
// somebody would have written down from memory anyway, and the value of a
// measured profile is entirely in the ones they would not have.
func (c Coverage) Covers() bool {
	if len(c.Routes) == 0 || c.Requests <= 0 {
		return false
	}
	for _, r := range c.Routes {
		if r.Sent {
			continue
		}
		if share, ok := r.Share(c.Requests); ok && share >= MinShare {
			return false
		}
	}
	return true
}

// Describe renders the coverage as the sentence a fidelity report carries.
func (c Coverage) Describe() string {
	share, ok := c.Share()
	if !ok {
		return "the traffic profile counted no request, so there is nothing to compare against"
	}
	out := fmt.Sprintf("this run sends %s of the %s production served, carrying %s of its requests",
		plural(int64(len(c.Routes)-len(c.Uncovered())), "route", "routes"),
		plural(int64(len(c.Routes)), "route", "routes"), Percent(share))
	if missed := c.Uncovered(); len(missed) > 0 {
		heaviest := missed[0]
		if s, okShare := heaviest.Share(c.Requests); okShare {
			out += fmt.Sprintf(". The heaviest it never sends is %s, which is %s of production's traffic over %s requests",
				heaviest.Route, Percent(s), Count(heaviest.Route.Requests))
		} else {
			out += ". The heaviest it never sends is " + heaviest.Route.String()
		}
	}
	if len(c.Invented) > 0 {
		names := make([]string, 0, len(c.Invented))
		for _, e := range c.Invented {
			names = append(names, e.String())
		}
		out += ". It also sends " + list(names) + ", which production never served"
	}
	return out
}

// RateComparison is the rate a run sends at, measured against production's.
type RateComparison struct {
	// Run is what the run sends, in requests a second, after the manifest's
	// scale. Production is what production served over the profile's window.
	Run        float64 `json:"run"`
	Production float64 `json:"production"`
	// PeakConcurrency is what production had in flight at once, zero when the
	// source could not say.
	PeakConcurrency int `json:"peak_concurrency,omitempty"`
}

// Share is the fraction of production's rate the run sends at.
func (r RateComparison) Share() (float64, bool) {
	if r.Production <= 0 {
		return 0, false
	}
	return r.Run / r.Production, true
}

// Reaches reports whether the run sends at least as fast as production.
//
// At least, rather than within a band. A run sending faster than production is
// a harder test than production and its results still hold; a run sending
// slower is the one that reports green on contention production would have
// found, which is the direction this measurement exists to catch. There is no
// tolerance below one for the same reason: half of production's rate is half
// of its contention, and rounding that up to "close enough" is how a load test
// says a lock is fine.
func (r RateComparison) Reaches() bool {
	share, ok := r.Share()
	return ok && share >= 1
}

// Describe renders the rate comparison as one line.
func (r RateComparison) Describe() string {
	share, ok := r.Share()
	if !ok {
		return fmt.Sprintf("this run sends %s and the profile carries no rate to compare it against",
			rate(r.Run))
	}
	out := fmt.Sprintf("this run sends %s against production's %s, which is %s of it",
		rate(r.Run), rate(r.Production), Percent(share))
	if r.PeakConcurrency > 0 {
		out += fmt.Sprintf(". Production had %s in flight at once at its peak",
			plural(int64(r.PeakConcurrency), "request", "requests"))
	}
	return out
}

// canonical is the form both sides of a comparison are matched on.
//
// The method upper cased and the path put through the same normaliser the
// readers use, so a route written by hand in a manifest and a route read from
// production's telemetry meet in one spelling. Without it a manifest naming
// GET /runs/4821 would be reported as a route production never served, beside
// a GET /runs/{id} production serves constantly, and both halves of that
// report would be wrong.
func canonical(method, path string) string {
	m := strings.ToUpper(strings.TrimSpace(method))
	p := load.NormalisePath(strings.TrimSpace(path))
	if p != "/" {
		p = strings.TrimSuffix(p, "/")
	}
	return m + " " + p
}

// rate renders a request rate the way somebody says it.
func rate(perSecond float64) string {
	switch {
	case perSecond <= 0:
		return "no requests a second"
	case perSecond < 1:
		return fmt.Sprintf("%.2f requests a second", perSecond)
	case perSecond < 10:
		return fmt.Sprintf("%.1f requests a second", perSecond)
	}
	return Count(int64(perSecond)) + " requests a second"
}

// plural renders a count with the right noun.
func plural(n int64, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return Count(n) + " " + many
}

// list renders names for one line of prose.
func list(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}
