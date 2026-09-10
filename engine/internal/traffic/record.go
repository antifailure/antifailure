package traffic

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/load"
)

// Recording a profile out of telemetry a team already has.
//
// Through the readers the load generator already uses, rather than a second
// parser beside them. Two implementations of "what did production serve" that
// could disagree is the defect this file would exist to create: the fidelity
// report would then be comparing a run against a profile read one way, having
// generated the run from the same file read another, and the difference
// between the two would look like a finding about the environment.
//
// There is no third source and no agent. An SDK that reports from inside a
// running application is the largest trust ask this product makes and it is
// deliberately not what this lane shipped; L6.3 owns the install flow, and
// what a team needs today is a file they already have.

// Format names a source a profile can be recorded from.
type Format string

const (
	// FormatOTel is an OpenTelemetry trace export in OTLP/JSON, which is what
	// a collector's file exporter writes.
	FormatOTel Format = "otel"
	// FormatAccessLog is a combined format access log, which every reverse
	// proxy writes and nobody has to install anything to get.
	FormatAccessLog Format = "access_log"
)

// FromOTLP records a profile from an OpenTelemetry trace export.
//
// A trace carries a start and an end per request, so this is the source that
// can answer every question the profile asks: the mix, the rate, the per route
// p95 that a threshold compares against, and the peak concurrency.
func FromOTLP(data []byte, source string, now time.Time) (Profile, error) {
	read, err := load.FromOTLP(data)
	if err != nil {
		return Profile{}, fmt.Errorf("%w%s", err, describeSkipped(read.Skipped))
	}
	p := Profile{
		CollectedAt:     now.UTC(),
		Source:          source,
		From:            read.Start,
		To:              read.End,
		PeakConcurrency: read.PeakConcurrency,
	}
	for _, r := range read.Shape.Routes {
		n := int64(read.Requests[r.String()])
		p.Requests += n
		p.Routes = append(p.Routes, Route{
			Method: r.Method, Path: r.Path, Requests: n, P95Ms: r.P95Ms,
		})
	}
	p.Missing = append(p.Missing, skippedReasons(read.Skipped)...)
	if thin := routesWithoutBaseline(p.Routes); len(thin) > 0 {
		// Named rather than left as a zero. A route with too few samples has
		// no p95 in the export, and a zero in that field would read as a route
		// production serves instantly, which is the most flattering possible
		// way to be wrong about a latency.
		p.Missing = append(p.Missing, fmt.Sprintf(
			"%s carried too few requests in this window for a p95, so no threshold can compare against %s: %s",
			plural(int64(len(thin)), "route", "routes"), oneOrThem(len(thin)), list(thin)))
	}
	return finish(p)
}

// FromAccessLog records a profile from a combined format access log.
//
// Everything but the timings. A combined format line carries no duration, so
// there is no p95 and no concurrency to be had from one, and the profile says
// so rather than writing zeroes into those fields. That is the same reason
// load.FromAccessLog leaves every route without a baseline: a threshold
// comparing against a zero is a threshold that fires on everything.
func FromAccessLog(lines []string, source string, now time.Time) (Profile, error) {
	read := load.ReadAccessLog(lines)
	p := Profile{
		CollectedAt: now.UTC(),
		Source:      source,
		From:        read.First.UTC(),
		To:          read.Last.UTC(),
	}
	if read.First.IsZero() {
		p.From, p.To = time.Time{}, time.Time{}
	}
	for _, r := range read.Shape.Routes {
		n := int64(read.Requests[r.String()])
		p.Requests += n
		p.Routes = append(p.Routes, Route{Method: r.Method, Path: r.Path, Requests: n})
	}
	p.Missing = append(p.Missing,
		"a combined format access log carries no request duration, so this profile has no p95 "+
			"for any route and nothing here can say what production's latency was")
	p.Missing = append(p.Missing,
		"a combined format access log carries no concurrency, so nothing here says how many "+
			"requests production had in flight at once")
	if read.Unreadable > 0 {
		p.Missing = append(p.Missing, fmt.Sprintf(
			"%s not counted: could not be read as a request line",
			plural(int64(read.Unreadable), "line", "lines")))
	}
	if read.First.IsZero() {
		p.Missing = append(p.Missing,
			"no line carried a timestamp this reader could parse, so the window is unknown and "+
				"there is no rate to compare a run's against")
	}
	return finish(p)
}

// finish sorts a recorded profile and refuses an empty one.
//
// Refused rather than written. A profile naming no route is a denominator of
// zero, and a denominator of zero is how a report ends up reporting that a run
// covers everything production serves. The command that would have written it
// says what it read instead.
func finish(p Profile) (Profile, error) {
	if len(p.Routes) == 0 {
		return Profile{}, fmt.Errorf(
			"no request could be read from this source, so there is no traffic profile to write")
	}
	sort.SliceStable(p.Routes, func(i, j int) bool {
		if p.Routes[i].Requests != p.Routes[j].Requests {
			return p.Routes[i].Requests > p.Routes[j].Requests
		}
		return p.Routes[i].String() < p.Routes[j].String()
	})
	return p, nil
}

// routesWithoutBaseline names the routes the export could not give a p95 for.
func routesWithoutBaseline(routes []Route) []string {
	var out []string
	for _, r := range routes {
		if r.P95Ms == 0 {
			out = append(out, r.String())
		}
	}
	sort.Strings(out)
	const most = 5
	if len(out) > most {
		return append(out[:most:most], fmt.Sprintf("and %d more", len(out)-most))
	}
	return out
}

// skippedReasons turns the reader's skip counts into lines of a profile.
//
// The count and then the reason, rather than a sentence built around the
// reason, because the reasons come from the reader and are phrases of
// different shapes: "not a server span" and "no HTTP method or path on the
// span" cannot both be dropped into one template and read as English.
func skippedReasons(skipped map[string]int) []string {
	var out []string
	for reason, n := range skipped {
		out = append(out, fmt.Sprintf("%s not counted: %s",
			plural(int64(n), "span", "spans"), reason))
	}
	sort.Strings(out)
	return out
}

// describeSkipped puts the skip counts on the end of a failed read.
//
// "No requests were found" is a much harder message to act on than the reason
// nothing was found, which is nearly always an export full of client spans or
// one whose exporter wrote the old attribute names.
func describeSkipped(skipped map[string]int) string {
	reasons := skippedReasons(skipped)
	if len(reasons) == 0 {
		return ""
	}
	return ". " + strings.Join(reasons, ", ")
}

func oneOrThem(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}
