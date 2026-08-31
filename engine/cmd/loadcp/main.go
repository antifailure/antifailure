// Command loadcp points engine/internal/load at the hosted control plane's
// own API, which this product has never done to itself.
//
// Every other caller of internal/load reaches it through `af load`, which
// asks an af-managed environment where it is running and sends traffic there.
// The control plane is not one of those: it is the thing customers'
// environments talk TO, so it has no branch, no orchestrator, and nothing for
// af load's "bring it up with 'af up' first" to bring up. This is a second,
// thinner caller of the same package: a base URL and a shape, no environment
// in between.
//
//	go run ./cmd/loadcp -url https://app.dev.antifailure.dev
//	go run ./cmd/loadcp -url http://127.0.0.1:8091 -duration 1m -scale 0.5
//
// The bundled profile.json is not measured production traffic, because none
// has ever been captured; the control plane has no access log source wired
// into internal/load and this is the first time anything has pointed load at
// it. Each weight is instead the route's own declared ceiling from
// web/apps/api/src/limits.ts: what the rate limiter is already willing to let
// one caller sustain. That is a real number checked into the API rather than
// a guess, and it is honestly labelled "declared_limits" rather than
// "production" in the shape, the same way internal/load labels a shape nobody
// supplied as "default" rather than pretending it came from somewhere.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/antifailure/antifailure/engine/internal/load"
)

//go:embed profile.json
var bundledProfile []byte

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "loadcp:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("loadcp", flag.ContinueOnError)
	url := fs.String("url", "", "base URL of the control plane to send at (required)")
	profilePath := fs.String("profile", "", "path to a load.Shape JSON file (default: the bundled control plane profile)")
	duration := fs.Duration("duration", 30*time.Second, "how long to send for")
	scale := fs.Float64("scale", 0.5, "multiplier on the profile's requests_per_second")
	seed := fs.Int64("seed", 1, "makes two runs send the same sequence")
	concurrency := fs.Int("concurrency", 20, "requests in flight at once")
	errorRate := fs.Float64("error-rate", 0, "fail the run if more than this share of requests errors (0 disables the check)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *url == "" {
		fs.Usage()
		return fmt.Errorf("-url is required")
	}

	body := bundledProfile
	if *profilePath != "" {
		b, err := os.ReadFile(*profilePath)
		if err != nil {
			return fmt.Errorf("reading %s: %w", *profilePath, err)
		}
		body = b
	}

	var shape load.Shape
	if err := json.Unmarshal(body, &shape); err != nil {
		return fmt.Errorf("the profile is not a valid load.Shape: %w", err)
	}

	// GET only, belt and suspenders. Every route in profile.json is already a
	// read, and internal/load itself refuses to send anything that has not
	// been named safe, for exactly the reason its own package doc gives: a
	// generator that finds a write in a shape and sends it a thousand times is
	// a generator that charges a thousand cards. Nothing here reads from a
	// customer's traffic, but the control plane is a shared multi-tenant
	// service and there is no reason to be the exception to a rule the rest of
	// this project holds everywhere else.
	sendable, refused := shape.Safe([]string{"GET /**"}, nil)
	if len(sendable.Routes) == 0 {
		return fmt.Errorf("every route in the profile is unsafe to send; nothing to do")
	}
	for _, r := range refused {
		fmt.Fprintf(os.Stderr, "loadcp: refusing %s, not a GET\n", r)
	}

	fmt.Printf("shape source: %s (%d routes, declared rate %.0f/s, sending at %.0f/s)\n",
		shape.Source, len(sendable.Routes), shape.RequestsPerSecond, shape.RequestsPerSecond*(*scale))
	fmt.Printf("target: %s, duration %s, seed %d\n\n", *url, duration.String(), *seed)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	res, err := load.Run(ctx, load.Options{
		BaseURL:     *url,
		Shape:       sendable,
		Scale:       *scale,
		Duration:    *duration,
		Seed:        *seed,
		Concurrency: *concurrency,
		Progress: func(p load.Progress) {
			fmt.Printf("  %6s  %5d sent  %4d errors  p95 %6.0fms  %3d in flight\n",
				p.Elapsed, p.Sent, p.Errors, p.P95Ms, p.Inflight)
		},
	})
	if err != nil {
		return err
	}

	fmt.Printf("\n%d requests in %s at %.0f/s, %.1f%% failed\n",
		res.Sent, res.Duration.Round(time.Second), res.Rate, res.ErrorRate*100)
	fmt.Printf("overall p50 %.0fms  p90 %.0fms  p95 %.0fms  p99 %.0fms  max %.0fms\n\n",
		res.Overall.P50Ms, res.Overall.P90Ms, res.Overall.P95Ms, res.Overall.P99Ms, res.Overall.MaxMs)

	fmt.Printf("%-42s %8s %8s %8s %8s\n", "ROUTE", "SENT", "ERRORS", "P95", "MAX")
	for _, r := range res.Routes {
		fmt.Printf("%-42s %8d %8d %7.0fms %7.0fms\n",
			r.Route, r.Sent, r.Errors, r.Latency.P95Ms, r.Latency.MaxMs)
	}
	if len(res.Errors) > 0 {
		fmt.Println()
		for reason, n := range res.Errors {
			fmt.Printf("  %d responses: %s\n", n, reason)
		}
	}

	if *errorRate > 0 && res.ErrorRate > *errorRate {
		return fmt.Errorf("error rate %.1f%% exceeds the %.1f%% threshold", res.ErrorRate*100, *errorRate*100)
	}
	return nil
}
