package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/traffic"
)

// The traffic profile: what production serves, so that what a run sends is a
// fraction rather than a list somebody wrote from memory.
//
// Measured on this repository on 2026-09-06. A migration was made to hold
// AccessExclusiveLock on events and its partitions, and pg_locks confirmed
// nine relations locked at once for the whole window. af load smoke ran
// straight through it and reported 0.0 percent failed, with p95 improving from
// 41ms to 17ms, because none of the four hand written safe_routes reads that
// table. A hand written route list cannot know which routes touch which
// tables, and nothing in the engine had ever been told what production serves,
// so nothing could say the list was thin.
//
// Recording it needs no application change and no SDK. It reads a file a
// team's collector or reverse proxy already wrote, once, and writes down the
// counts. Nothing here opens a socket and nothing here reports from inside a
// running application.

// TrafficJSON is one profile and how a run measures against it.
type TrafficJSON struct {
	CollectedAt       string             `json:"collected_at"`
	Source            string             `json:"source,omitempty"`
	From              string             `json:"from,omitempty"`
	To                string             `json:"to,omitempty"`
	AgeHours          float64            `json:"age_hours"`
	MaxAgeHours       float64            `json:"max_age_hours,omitempty"`
	Stale             bool               `json:"stale"`
	Requests          int64              `json:"requests"`
	RequestsPerSecond float64            `json:"requests_per_second,omitempty"`
	PeakConcurrency   int                `json:"peak_concurrency,omitempty"`
	Routes            []TrafficRouteJSON `json:"routes"`
	Missing           []string           `json:"missing,omitempty"`
	// Coverage is what a load run would send of this, absent when the run's
	// shape could not be read.
	Coverage *TrafficCoverageJSON `json:"coverage,omitempty"`
	// CoverageReason says why there is no coverage, which is the only thing
	// that makes its absence useful.
	CoverageReason string `json:"coverage_reason,omitempty"`
}

// TrafficRouteJSON is one route production served.
type TrafficRouteJSON struct {
	Method   string  `json:"method"`
	Path     string  `json:"path"`
	Requests int64   `json:"requests"`
	Share    float64 `json:"share"`
	P95Ms    float64 `json:"p95_ms,omitempty"`
	// Sent reports whether a load run would send this route at all.
	Sent bool `json:"sent"`
}

// TrafficCoverageJSON is the comparison, for a caller that wants the number.
type TrafficCoverageJSON struct {
	Requests         int64    `json:"requests"`
	Covered          int64    `json:"covered_requests"`
	Share            float64  `json:"share"`
	Covers           bool     `json:"covers"`
	SentRoutes       int      `json:"sent_routes"`
	ProductionRoutes int      `json:"production_routes"`
	Uncovered        []string `json:"uncovered,omitempty"`
	Invented         []string `json:"invented,omitempty"`
}

func newTrafficCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "traffic",
		Short: "What production serves, and how much of it a load run actually sends",
		Long: strings.TrimSpace(`
A load run sends the routes safe_routes names. Without a traffic profile
nothing says how much of production that is, so four routes written by hand
report in the same words and with the same verdict as a mix read from a week of
production telemetry.

That is not a cosmetic gap. Measured on this repository on 2026-09-06: a
migration held an exclusive lock on nine relations for thirty seconds and the
load run over four hand written routes reported 0.0 percent failed, because
none of the four reads the locked table. A hand written route list cannot know
which routes touch which tables.

A profile is the endpoint mix, the arrival rate, the peak concurrency and the
per route p95, counted from telemetry a team already has. It carries no request
body, no header, no query string and no identifier. It is a count per route,
which is what makes it safe to commit beside the manifest, and committing it is
the point: the check running on a pull request cannot reach production.

Declare where it lives under load.traffic.profile, and how old it may be under
load.traffic.max_age. A profile past that age is refused rather than quoted.`),
	}
	cmd.AddCommand(newTrafficRecordCommand(env))
	cmd.AddCommand(newTrafficShowCommand(env))
	return cmd
}

func newTrafficRecordCommand(env *Env) *cobra.Command {
	var branch, from, out string
	cmd := &cobra.Command{
		Use:   "record",
		Short: "Count what production served from a trace export or an access log",
		Long: strings.TrimSpace(`
Reads the file load.source_config.path names, which --from overrides, and
writes the profile to the path load.traffic.profile names, which --out
overrides.

Two sources, both of them a file. An OpenTelemetry trace export in OTLP/JSON
answers every question the profile asks, because a span carries a start and an
end: the mix, the rate, the per route p95 a threshold compares against, and the
peak concurrency. A combined format access log answers the mix and the rate,
and says in the profile that it could answer neither of the others.

Nothing here opens a socket, and there is no agent to install. The file is one
a collector or a reverse proxy already wrote.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := orchestrator(env, branch, false)
			if err != nil {
				return err
			}
			path := out
			switch {
			case path == "":
				declared, ok := o.TrafficProfilePath()
				if !ok {
					return errors.New(
						"the manifest declares no load.traffic.profile, so there is nowhere " +
							"to write this. Add the path, or pass --out")
				}
				path = declared
			case !filepath.IsAbs(path):
				// Against the working directory the command was told to run
				// as, not the process's own.
				path = filepath.Join(env.WorkDir, path)
			}

			env.Out.Section("Counting what production served")
			profile, err := o.RecordTraffic(from)
			if err != nil {
				return err
			}
			if err := traffic.Write(path, profile); err != nil {
				return err
			}

			if env.Out.Format == FormatJSON {
				return env.Out.JSON(trafficJSON(profile, 0, env.Clock.Now(), nil, ""))
			}
			env.Out.Println("")
			env.Out.Status(SymbolOK, path, fmt.Sprintf("%d routes, %s requests over %s",
				len(profile.Routes), trafficCount(profile.Requests), trafficWindow(profile)))
			printTrafficMissing(env, profile)
			env.Out.Hint("Commit it, so the check running on a pull request can read it. Then",
				"af traffic show")
			return nil
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "",
		"Branch context to use, defaulting to the checked out one")
	cmd.Flags().StringVar(&from, "from", "",
		"Read this file instead of the one load.source_config.path names")
	cmd.Flags().StringVar(&out, "out", "",
		"Write the profile here instead of where the manifest says")
	return cmd
}

func newTrafficShowCommand(env *Env) *cobra.Command {
	var branch string
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print what production serves and which of it this run sends",
		Long: strings.TrimSpace(`
Reads the profile the manifest names and prints it, busiest route first, with a
mark against every route a load run would actually send.

The routes with no mark are the finding. They are what production serves and
this run never touches, so they are what a green run says nothing about, and
the safe_routes lines that would cover them are printed at the end for somebody
to read and paste. Nothing is written for you: this measures and states, and
the manifest confirms it.

A profile older than load.traffic.max_age is REFUSED rather than printed with a
warning beside it. A stale denominator is not a smaller number, it is an
unknown one.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := orchestrator(env, branch, false)
			if err != nil {
				return err
			}
			profile, why := o.TrafficProfile()
			if why != "" {
				if env.Out.Format == FormatJSON {
					return env.Out.JSON(map[string]string{"missing": why})
				}
				env.Out.Empty(env.Out.Wrap(why, 0), "Record one with", "af traffic record")
				return nil
			}

			// What the run sends, and the reason it could not be worked out.
			// A profile printed beside no comparison is a table of numbers,
			// and the comparison is the whole point of having recorded it.
			sendable, sendErr := o.SendableRoutes()
			reason := ""
			if sendErr != nil {
				reason = sendErr.Error()
			}
			var cov *traffic.Coverage
			if sendErr == nil {
				c := traffic.Compare(sendable, *profile)
				cov = &c
			}

			maxAge := o.TrafficMaxAge()
			if env.Out.Format == FormatJSON {
				return env.Out.JSON(trafficJSON(*profile, maxAge, env.Clock.Now(), cov, reason))
			}
			env.Out.Section("What production serves")
			env.Out.Println("")
			env.Out.Printf("  Collected %s from %s.\n",
				profile.CollectedAt.UTC().Format(time.RFC3339), orUnknownSource(profile.Source))
			env.Out.Printf("  %d routes, %s requests over %s.\n",
				len(profile.Routes), trafficCount(profile.Requests), trafficWindow(*profile))
			if rate, ok := profile.Rate(); ok {
				line := fmt.Sprintf("  %s.", trafficRate(rate))
				if profile.PeakConcurrency > 0 {
					line = fmt.Sprintf("  %s, %d in flight at once at its peak.",
						trafficRate(rate), profile.PeakConcurrency)
				}
				env.Out.Println(line)
			}
			env.Out.Println("")

			sent := sentRoutes(cov)
			rows := make([][]string, 0, len(profile.Routes))
			for _, r := range profile.Routes {
				share := ""
				if profile.Requests > 0 {
					share = traffic.Percent(float64(r.Requests) / float64(profile.Requests))
				}
				rows = append(rows, []string{
					r.String(), trafficCount(r.Requests), share, p95Cell(r.P95Ms),
					sentCell(sent, r),
				})
			}
			env.Out.Table([]Column{
				Col("ROUTE"), Num("REQUESTS"), Num("SHARE"), Num("P95"), Col("THIS RUN"),
			}, rows)
			printTrafficCoverage(env, cov, reason)
			printTrafficRate(env, o, *profile)
			printTrafficMissing(env, *profile)
			return nil
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "",
		"Branch context to use, defaulting to the checked out one")
	return cmd
}

// sentRoutes is the set of routes a run reaches, by the name a report renders.
//
// Built once rather than scanned per row. A profile of a busy production holds
// hundreds of routes and the table prints every one of them, so a scan per
// cell is quadratic in a place with no reason to be.
func sentRoutes(cov *traffic.Coverage) map[string]bool {
	if cov == nil {
		return nil
	}
	out := make(map[string]bool, len(cov.Routes))
	for _, got := range cov.Routes {
		out[got.Route.String()] = got.Sent
	}
	return out
}

// sentCell says whether a run reaches this route.
//
// "never sent" rather than a blank, because a blank under a heading reads as a
// value somebody is not sure about, and this column is the finding.
func sentCell(sent map[string]bool, r traffic.Route) string {
	if sent == nil {
		return "unknown"
	}
	if sent[r.String()] {
		return "sent"
	}
	return "never sent"
}

// p95Cell renders a route's p95, or says the source could not carry one.
func p95Cell(ms float64) string {
	if ms <= 0 {
		return "unknown"
	}
	return fmt.Sprintf("%.0f ms", ms)
}

// printTrafficCoverage prints the number and the lines that would close it.
func printTrafficCoverage(env *Env, cov *traffic.Coverage, reason string) {
	env.Out.Println("")
	if cov == nil {
		env.Out.Println(env.Out.Wrap(
			"What this run sends of it could not be worked out: "+reason, 0))
		return
	}
	env.Out.Println(env.Out.Wrap("What this run sends of it: "+cov.Describe()+".", 0))
	missed := cov.Uncovered()
	if len(missed) == 0 {
		return
	}
	env.Out.Println("")
	env.Out.Println(env.Out.Wrap(
		"The safe_routes lines that would cover the routes above this run never sends. "+
			"Read them before pasting them: a route being served in production is not a "+
			"promise that sending it a thousand times is safe.", 0))
	env.Out.Println("")
	const most = 12
	for i, r := range missed {
		if i == most {
			env.Out.Printf("    # and %d more, in af traffic show --output json\n", len(missed)-most)
			break
		}
		env.Out.Printf("    - %s\n", r.Route.String())
	}
}

// printTrafficRate says how fast this run sends against how fast production
// serves.
//
// The other half of what a run reproduces, and the half a coverage percentage
// hides completely: a run that reaches every route production serves and sends
// them at two percent of its rate has exercised the code and not the
// contention. It states and does not adjust. Nothing here changes load.scale.
func printTrafficRate(out *Env, o *env.Orchestrator, p traffic.Profile) {
	production, ok := p.Rate()
	if !ok {
		return
	}
	run, err := o.TrafficRate()
	if err != nil {
		out.Out.Println("")
		out.Out.Println(out.Out.Wrap(
			"How fast this run sends could not be worked out: "+err.Error(), 0))
		return
	}
	cmp := traffic.RateComparison{
		Run: run, Production: production, PeakConcurrency: p.PeakConcurrency,
	}
	out.Out.Println("")
	out.Out.Println(out.Out.Wrap("How fast it sends: "+cmp.Describe()+".", 0))
}

func printTrafficMissing(env *Env, p traffic.Profile) {
	if len(p.Missing) == 0 {
		return
	}
	env.Out.Println("")
	env.Out.Println(env.Out.Wrap("What this profile could not read:", 0))
	for _, m := range p.Missing {
		env.Out.Printf("  %s %s\n", env.Out.S(StyleWarn, SymbolWarn), env.Out.Wrap(m, 7))
	}
}

// trafficWindow renders the period a profile covers.
func trafficWindow(p traffic.Profile) string {
	w := p.Window()
	if w <= 0 {
		return "a window nothing recorded"
	}
	switch {
	case w >= 48*time.Hour:
		return fmt.Sprintf("%d days", int(w.Hours()/24))
	case w >= 2*time.Hour:
		return fmt.Sprintf("%d hours", int(w.Hours()))
	case w >= 2*time.Minute:
		return fmt.Sprintf("%d minutes", int(w.Minutes()))
	}
	return w.Round(time.Second).String()
}

// trafficRate renders an arrival rate the way somebody says it.
func trafficRate(perSecond float64) string {
	switch {
	case perSecond < 1:
		return fmt.Sprintf("%.2f requests a second", perSecond)
	case perSecond < 10:
		return fmt.Sprintf("%.1f requests a second", perSecond)
	}
	return traffic.Count(int64(perSecond)) + " requests a second"
}

func trafficCount(n int64) string { return traffic.Count(n) }

func trafficJSON(
	p traffic.Profile, maxAge time.Duration, now time.Time,
	cov *traffic.Coverage, reason string,
) TrafficJSON {
	doc := TrafficJSON{
		Source: p.Source, Requests: p.Requests, Missing: p.Missing,
		PeakConcurrency: p.PeakConcurrency,
		AgeHours:        p.Age(now).Hours(),
		Stale:           traffic.Stale(p.CollectedAt, maxAge, now),
		CoverageReason:  reason,
	}
	if !p.CollectedAt.IsZero() {
		doc.CollectedAt = p.CollectedAt.UTC().Format(time.RFC3339)
	}
	if !p.From.IsZero() {
		doc.From = p.From.UTC().Format(time.RFC3339)
	}
	if !p.To.IsZero() {
		doc.To = p.To.UTC().Format(time.RFC3339)
	}
	if rate, ok := p.Rate(); ok {
		doc.RequestsPerSecond = rate
	}
	if maxAge > 0 {
		doc.MaxAgeHours = maxAge.Hours()
	}
	sent := sentRoutes(cov)
	for _, r := range p.Routes {
		row := TrafficRouteJSON{
			Method: r.Method, Path: r.Path, Requests: r.Requests, P95Ms: r.P95Ms,
			Sent: sent[r.String()],
		}
		if p.Requests > 0 {
			row.Share = float64(r.Requests) / float64(p.Requests)
		}
		doc.Routes = append(doc.Routes, row)
	}
	if cov != nil {
		share, _ := cov.Share()
		out := TrafficCoverageJSON{
			Requests: cov.Requests, Covered: cov.Covered, Share: share,
			Covers: cov.Covers(), ProductionRoutes: len(cov.Routes),
			SentRoutes: len(cov.Routes) - len(cov.Uncovered()),
		}
		for _, r := range cov.Uncovered() {
			out.Uncovered = append(out.Uncovered, r.Route.String())
		}
		for _, e := range cov.Invented {
			out.Invented = append(out.Invented, e.String())
		}
		doc.Coverage = &out
	}
	return doc
}
