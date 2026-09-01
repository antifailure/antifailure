package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/load"
)

// LoadJSON is the machine readable result of a load run.
type LoadJSON struct {
	Source     string             `json:"source,omitempty"`
	TargetRate float64            `json:"target_rate,omitempty"`
	Sent       int                `json:"sent"`
	Rate       float64            `json:"rate"`
	Duration   string             `json:"duration"`
	ErrorRate  float64            `json:"error_rate"`
	Overall    load.Latency       `json:"overall"`
	Routes     []load.RouteResult `json:"routes"`
	Errors     map[string]int     `json:"errors,omitempty"`
	Refused    []string           `json:"refused_as_unsafe,omitempty"`
	Breaches   []load.Breach      `json:"breaches,omitempty"`
	// InertP95 says a p95_increase threshold was in force and no route
	// carried a baseline for it to be measured against, so it was listed and
	// evaluated nothing. A consumer that reported breaches as the whole
	// verdict would otherwise read that run as a clean p95.
	InertP95 bool `json:"inert_p95_increase,omitempty"`
}

func newLoadCommand(e *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "load",
		Short: "Send traffic shaped like production's at the environment",
		Long: strings.TrimSpace(`
A weighted mix rather than one endpoint at a fixed rate. Hammering one endpoint
proves that endpoint is fast, which nobody doubted; what breaks under real
traffic is the mix, and the page nobody thinks about that is nine percent of
requests.

Every route is treated as unsafe until the manifest names it safe. A generator
that finds POST /checkout in an access log and exercises it four hundred times
is a generator that charges four hundred cards.`),
	}
	cmd.AddCommand(newLoadRunCommand(e, false))
	cmd.AddCommand(newLoadRunCommand(e, true))
	cmd.AddCommand(newLoadScenarioCommand(e))
	return cmd
}

func newLoadRunCommand(e *Env, smoke bool) *cobra.Command {
	use, short := "run", "Run the full load profile"
	defaultDuration := 60 * time.Second
	defaultScale := 1.0
	if smoke {
		use, short = "smoke", "Send a short burst, to check the environment answers under any load at all"
		defaultDuration = 10 * time.Second
		defaultScale = 0.1
	}

	var branch string
	var duration time.Duration
	var scale float64
	var seed int64
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Load is sent AT an environment rather than creating one, so this
			// is defence in depth rather than the gate that matters. It is
			// here because an environment left up from before the policy
			// existed, or from a run on the base branch, is still an
			// environment a fork's pull request can point traffic at.
			if fork := forkGate(e); fork.Refused {
				return refuseFork(fork)
			}
			o, err := orchestrator(e, branch, false)
			if err != nil {
				return err
			}
			e.Out.Section("Generating load")

			res, refused, err := o.Load(cmd.Context(), env.LoadOptions{
				Duration: duration, Scale: scale, Seed: seed,
				Progress: func(p load.Progress) {
					e.Out.Printf("  %s  %d sent, %d errors, p95 %.0fms, %d in flight\n",
						p.Elapsed, p.Sent, p.Errors, p.P95Ms, p.Inflight)
				},
			})
			if err != nil {
				return err
			}

			p95Increase, errorRate := o.Thresholds()
			breaches := res.Breaches(p95Increase, errorRate)
			// A threshold that was in force and measured nothing. It is not a
			// breach, because nothing was exceeded; it is the absence of the
			// check the manifest asked for, which is why it is reported
			// separately and why it still exits non-zero.
			inert := res.InertP95(p95Increase)
			verdict := loadExit(res, breaches, p95Increase)

			if e.Out.Format == FormatJSON {
				doc := LoadJSON{
					Source: res.Source, TargetRate: res.TargetRate,
					Sent: res.Sent, Rate: res.Rate,
					Duration:  res.Duration.Round(time.Millisecond).String(),
					ErrorRate: res.ErrorRate, Overall: res.Overall,
					Routes: res.Routes, Errors: res.Errors, Breaches: breaches,
					InertP95: inert,
				}
				for _, r := range refused {
					doc.Refused = append(doc.Refused, r.String())
				}
				if err := e.Out.JSON(doc); err != nil {
					return err
				}
				if verdict != nil {
					return silent(verdict)
				}
				return nil
			}

			e.Out.Println("")
			if res.Source != "" {
				// Where the mix came from, and what rate it asked for against
				// what it got. A run that says nothing about its source
				// invites a default shape to be read as production's.
				e.Out.Printf("  Shape from %s, %.1f requests a second asked for.\n",
					res.Source, res.TargetRate)
			}
			e.Out.Printf("  %d requests in %s at %.0f a second, %.1f percent failed.\n",
				res.Sent, res.Duration.Round(time.Second), res.Rate, res.ErrorRate*100)
			e.Out.Printf("  Overall p50 %.0fms, p95 %.0fms, p99 %.0fms.\n\n",
				res.Overall.P50Ms, res.Overall.P95Ms, res.Overall.P99Ms)

			rows := make([][]string, 0, len(res.Routes))
			for _, r := range res.Routes {
				change := e.Out.S(StyleDim, "no baseline")
				if r.HasBaseline {
					change = fmt.Sprintf("%+.0f%%", r.P95Increase*100)
					if r.P95Increase > 0.25 {
						change = e.Out.S(StyleWarn, change)
					}
				}
				rows = append(rows, []string{
					r.Route, fmt.Sprint(r.Sent), fmt.Sprintf("%.0fms", r.Latency.P95Ms),
					change, fmt.Sprint(r.Errors),
				})
			}
			e.Out.Table([]Column{
				Flex("ROUTE"), Num("SENT"), Num("P95"), Num("VS BASELINE"), Num("ERRORS"),
			}, rows)

			if len(refused) > 0 {
				e.Out.Println("")
				e.Out.Printf("  %d routes were not sent because nothing named them safe: %s\n",
					len(refused), describeRoutes(refused, 4))
			}
			for reason, n := range res.Errors {
				e.Out.Printf("  %s %d responses: %s\n", e.Out.S(StyleWarn, SymbolWarn), n, reason)
			}

			if len(breaches) > 0 {
				e.Out.Println("")
				e.Out.Section("Thresholds exceeded")
				for _, b := range breaches {
					e.Out.Printf("  %s %s: %s\n", e.Out.S(StyleBad, SymbolFail), b.What, b.Detail)
				}
			}
			if inert {
				e.Out.Println("")
				e.Out.Section("A threshold measured nothing")
				e.Out.Printf("  %s p95_increase %.2f: %s\n",
					e.Out.S(StyleBad, SymbolFail), p95Increase, inertDetail(res))
			}
			if verdict != nil {
				return silent(verdict)
			}
			return nil
		},
	}
	cmd.Flags().DurationVar(&duration, "duration", defaultDuration, "How long to send for")
	cmd.Flags().Float64Var(&scale, "scale", defaultScale, "Multiplier on production's rate")
	cmd.Flags().Int64Var(&seed, "seed", 1, "Makes two runs send the same sequence")
	cmd.Flags().StringVar(&branch, "branch", "", "Branch to send at, defaulting to the checked out one")
	return cmd
}

// loadExit is the verdict a load run exits with.
//
// Two ways to fail, and the second one used to be silence. A breach is a
// threshold that was exceeded. An inert threshold is one that was in force and
// evaluated nothing, and it exits non-zero for the reason the scenario runner
// already does: a check that ran nothing and reported green is a check
// everybody believes is running. A breach is reported first, because a run
// with both has a measured regression and that is the more actionable half.
func loadExit(res *load.Result, breaches []load.Breach, p95Increase float64) error {
	if len(breaches) > 0 {
		return aferrors.Coded(aferrors.AFLOD011, "count", fmt.Sprint(len(breaches)))
	}
	if res.InertP95(p95Increase) {
		return aferrors.Coded(aferrors.AFLOD016, "detail", inertDetail(res))
	}
	return nil
}

// inertDetail says how much was measured against nothing, in the run's own
// numbers.
//
// The count rather than a sentence about sources, because the reader already
// knows which source they configured and does not know that every one of their
// routes came back without a baseline. A route arrives without one when the
// trace export saw it fewer than twenty times, so a thin export produces this
// for every route at once and reads as a broken threshold until the number
// says otherwise.
func inertDetail(res *load.Result) string {
	return fmt.Sprintf("no baseline for any of the %s the run sent, so nothing was compared",
		plural(len(res.Routes), "route", "routes"))
}

func describeRoutes(routes []load.Route, limit int) string {
	names := make([]string, 0, len(routes))
	for _, r := range routes {
		names = append(names, r.String())
	}
	if len(names) <= limit {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:limit], ", "), len(names)-limit)
}
