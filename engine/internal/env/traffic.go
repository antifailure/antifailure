package env

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/load"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/traffic"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The traffic profile: reading the committed one, and recording a new one.
//
// Neither half opens a socket. The reading half is a file the manifest names,
// and the recording half is a file somebody's collector or reverse proxy
// already wrote. That is the whole trust ask: no agent inside production, no
// endpoint to allow, and nothing that calls home. It is also why the profile
// is committed, because the machine that most needs the denominator is a pull
// request check that can reach nothing at all.

// trafficProfile reads the committed profile, or says why there is none.
//
// A reason rather than an error, because every caller is a report, and a
// report that refuses to say anything because one input was stale tells the
// reader less than one that says which input was stale.
func (o *Orchestrator) trafficProfile() (*traffic.Profile, string) {
	l := o.opts.Manifest.Load
	if l == nil || l.Traffic == nil || strings.TrimSpace(l.Traffic.Profile) == "" {
		return nil, "no traffic profile says what production serves, so whether this run " +
			"exercises it is unknown. Declare one under load.traffic and record it with " +
			"af traffic record"
	}
	maxAge, err := manifest.ParseDuration(l.Traffic.MaxAge)
	if err != nil {
		return nil, fmt.Sprintf(
			"load.traffic.max_age is %q, which is not a duration, so nothing could decide "+
				"whether the profile is still production's", l.Traffic.MaxAge)
	}
	profile, why := traffic.Load(
		filepath.Join(o.opts.Root, l.Traffic.Profile), maxAge, o.opts.Clock.Now())
	if why != "" {
		return nil, why
	}
	return &profile, ""
}

// TrafficProfile is the committed profile, and the reason there is none.
func (o *Orchestrator) TrafficProfile() (*traffic.Profile, string) { return o.trafficProfile() }

// TrafficProfilePath is where the manifest says the profile lives, and false
// when it declares none.
func (o *Orchestrator) TrafficProfilePath() (string, bool) {
	l := o.opts.Manifest.Load
	if l == nil || l.Traffic == nil || strings.TrimSpace(l.Traffic.Profile) == "" {
		return "", false
	}
	return filepath.Join(o.opts.Root, l.Traffic.Profile), true
}

// TrafficMaxAge is how old the manifest lets a profile get, and zero when it
// declares no traffic block or an age nothing can parse.
//
// Zero is not "never stale" by accident: manifest.ParseDuration only fails on
// a value the validator already refused, and trafficProfile turns that same
// value into a reason of its own rather than reading the profile at all.
func (o *Orchestrator) TrafficMaxAge() time.Duration {
	l := o.opts.Manifest.Load
	if l == nil || l.Traffic == nil {
		return 0
	}
	d, err := manifest.ParseDuration(l.Traffic.MaxAge)
	if err != nil {
		return 0
	}
	return d
}

// RecordTraffic reads a telemetry file and returns the profile.
//
// from overrides the file, and an empty from falls back to the one the
// manifest's load source already names, because a project with source: otel
// has already said where its traffic is and asking for it twice is how the two
// drift apart. A project with no source configured has to say, and is told so
// by name rather than given an empty profile: a profile of nothing would be a
// denominator of zero, and a denominator of zero is how a report ends up
// claiming a run covers everything.
func (o *Orchestrator) RecordTraffic(from string) (traffic.Profile, error) {
	path, format, err := o.trafficSource(from)
	if err != nil {
		return traffic.Profile{}, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return traffic.Profile{}, fmt.Errorf(
			"the traffic at %s could not be read: %w", o.relative(path), err)
	}
	// The path relative to the repository, never an absolute one. The profile
	// is committed, and an absolute path in it names somebody's home directory
	// to everyone who reads the file afterwards.
	source := describeFormat(format) + " " + o.relative(path)
	switch format {
	case traffic.FormatOTel:
		p, err := traffic.FromOTLP(body, source, o.opts.Clock.Now())
		if err != nil {
			return traffic.Profile{}, fmt.Errorf("%w in %s", err, o.relative(path))
		}
		return p, nil
	default:
		p, err := traffic.FromAccessLog(
			strings.Split(string(body), "\n"), source, o.opts.Clock.Now())
		if err != nil {
			return traffic.Profile{}, fmt.Errorf("%w in %s", err, o.relative(path))
		}
		return p, nil
	}
}

// trafficSource decides which file to read and how to read it.
//
// The format comes from the manifest's load source when the file does, because
// that is the one place a project has already said what shape its telemetry
// is. When --from names a file instead, the extension decides, and a name that
// says nothing is refused rather than guessed at: reading an OTLP export as an
// access log produces zero routes and blames the user for it.
func (o *Orchestrator) trafficSource(from string) (path string, format traffic.Format, err error) {
	cfg := o.opts.Manifest.Load
	if strings.TrimSpace(from) == "" {
		switch {
		case cfg == nil || cfg.SourceConfig["path"] == "":
			return "", "", errors.New(
				"no file was given and the manifest names none, so there is no traffic to " +
					"record. Pass --from, or set load.source and load.source_config.path")
		case cfg.Source == schema.LoadOTel:
			return filepath.Join(o.opts.Root, filepath.FromSlash(cfg.SourceConfig["path"])),
				traffic.FormatOTel, nil
		case cfg.Source == schema.LoadAccessLog:
			return filepath.Join(o.opts.Root, filepath.FromSlash(cfg.SourceConfig["path"])),
				traffic.FormatAccessLog, nil
		default:
			return "", "", fmt.Errorf(
				"the load source is %q, which reads no traffic, so there is nothing to record "+
					"from. Pass --from, or set load.source to otel or access_log", cfg.Source)
		}
	}
	if !filepath.IsAbs(from) {
		from = filepath.Join(o.opts.Root, filepath.FromSlash(from))
	}
	switch strings.ToLower(filepath.Ext(from)) {
	case ".json", ".jsonl", ".ndjson", ".otlp":
		return from, traffic.FormatOTel, nil
	case ".log", ".txt":
		return from, traffic.FormatAccessLog, nil
	default:
		return "", "", errors.New(
			"nothing in the name of " + filepath.Base(from) + " says whether it is an " +
				"OpenTelemetry export or an access log, and reading one as the other finds no " +
				"traffic at all. Name it .json for an OTLP export or .log for an access log")
	}
}

// describeFormat names a source in the words somebody uses for it, because the
// profile's source line is read by whoever reviews the committed file.
func describeFormat(f traffic.Format) string {
	if f == traffic.FormatOTel {
		return "otel export"
	}
	return "access log"
}

// relative renders a path inside the repository as a repository path.
func (o *Orchestrator) relative(path string) string {
	if rel, err := filepath.Rel(o.opts.Root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.Base(path)
}

// SafeRoutes is the allow list a run obeys, with the default applied.
//
// One place rather than three. The mix, the scenarios and the fidelity report
// all have to agree on which routes may be sent, and they used to each apply
// the default themselves. A report measuring coverage against a fourth copy of
// that rule would be measuring a run nobody runs.
func (o *Orchestrator) SafeRoutes() (safe, unsafe []string) {
	if cfg := o.opts.Manifest.Load; cfg != nil {
		safe, unsafe = cfg.SafeRoutes, cfg.UnsafeRoutes
	}
	if len(safe) == 0 {
		// Reads under the root, which is what a smoke test wants and is the
		// only thing that can be assumed safe without being told.
		safe = []string{"GET /**"}
	}
	return safe, unsafe
}

// SendableRoutes is what a load run would actually send at this environment.
//
// The shape the manifest's source produces, filtered by the safe list, which
// is the only set of routes that can fail. Not the shape and not the safe
// list: a route in the shape that the safe list refuses is never sent, and a
// pattern in the safe list that no source produces sends nothing. The gap
// between what somebody wrote down and what actually leaves the generator is
// where the 2026-09-06 failure lived.
func (o *Orchestrator) SendableRoutes() ([]traffic.Endpoint, error) {
	shape, err := o.trafficShape()
	if err != nil {
		return nil, err
	}
	safe, unsafe := o.SafeRoutes()
	sendable, _ := shape.Safe(safe, unsafe)
	out := make([]traffic.Endpoint, 0, len(sendable.Routes))
	for _, r := range sendable.Routes {
		out = append(out, traffic.Endpoint{Method: r.Method, Path: r.Path})
	}
	return out, nil
}

// TrafficRate is how many requests a second a run would send.
//
// The shape's own rate multiplied by the manifest's scale, which is exactly
// what load.Run sends at. It is measured here rather than assumed, because the
// number this is compared against is production's and a comparison between a
// real rate and a configured one is the report saying something it did not
// check.
func (o *Orchestrator) TrafficRate() (float64, error) {
	shape, err := o.trafficShape()
	if err != nil {
		return 0, err
	}
	_, scale := ResolveLoadRate(LoadOptions{}, o.opts.Manifest.Load)
	return shape.RequestsPerSecond * scale, nil
}

// withProfileBaselines fills in the per route p95 a shape does not carry.
//
// The threshold this repository's own manifest documents as never having been
// able to fire is load.thresholds.p95_increase, and the reason is that a
// baseline has to come from somewhere. An access log carries no duration and a
// literal safe list carries nothing at all, so every route arrived with
// HasBaseline false and the comparison was skipped for all of them.
//
// A committed traffic profile carries production's p95 per route, so it can
// supply exactly the routes the shape could not. It never overwrites one the
// shape already has: a baseline measured in the same file the traffic came
// from is closer to the run than one recorded on another day, and a profile
// silently replacing it would move a threshold's meaning without saying so.
func withProfileBaselines(shape load.Shape, p *traffic.Profile) (load.Shape, int) {
	if p == nil {
		return shape, 0
	}
	filled := 0
	for i, r := range shape.Routes {
		if r.P95Ms > 0 {
			continue
		}
		if got, ok := p.Find(r.Method, r.Path); ok && got.P95Ms > 0 {
			shape.Routes[i].P95Ms = got.P95Ms
			filled++
		}
	}
	return shape, filled
}

// baselineNote says where a run's p95 comparisons came from, or that it has
// none.
//
// The silence is what this replaces. A run whose every route has no baseline
// evaluates p95_increase against nothing and prints no breach, which reads
// exactly like a run that compared and found no regression. This repository's
// own manifest documents that state in a comment beside the threshold it
// removed, and a comment in one manifest is not an instrument.
func baselineNote(shape load.Shape, p *traffic.Profile, filled int, why string) string {
	switch {
	case filled > 0:
		return fmt.Sprintf(
			"%d of %d routes take their p95 baseline from the traffic profile collected on %s",
			filled, len(shape.Routes), p.CollectedAt.UTC().Format("2006-01-02"))
	case shapeHasBaseline(shape):
		// The shape carried its own, which is the case an otel source has
		// always had. Nothing to say that the source line does not already.
		return ""
	case p != nil:
		return "no route has a p95 baseline: the traffic profile collected on " +
			p.CollectedAt.UTC().Format("2006-01-02") +
			" names none of the routes this run sends, so p95_increase cannot fire"
	default:
		return "no route has a p95 baseline, so p95_increase cannot fire: " + why
	}
}

// shapeHasBaseline reports whether any route in a shape carries a p95.
//
// Any, rather than all. A shape where one route has a baseline can still fire
// p95_increase on that route, so it is not the case the note above is about.
func shapeHasBaseline(shape load.Shape) bool {
	for _, r := range shape.Routes {
		if r.P95Ms > 0 {
			return true
		}
	}
	return false
}
