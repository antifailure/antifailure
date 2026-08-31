package manifest

import (
	"fmt"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/textwrap"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Explain renders the effective configuration: what the manifest says after
// every default has been applied.
//
// It exists because the most common configuration bug is a default the user
// did not know about. Printing the resolved value beside the one they wrote
// turns "why is it blocking that host" into a one line answer.
//
// width is how many columns the reader has. Most of what this prints is
// bounded by the schema, a provider name or a duration, and fits anywhere. A
// handful of values are not bounded by anything: a migrate command, a seed
// command, a list of environment variables, a rule's note, an invariant's
// description. Those are wrapped under their own column, because the one in
// examples/next-app is already ninety six characters and a reader on an eighty
// column terminal was getting it hard wrapped mid word by the terminal.
func Explain(m *schema.Manifest, width int) string {
	// Zero means the caller has no terminal to measure, as the support bundle
	// does not: it is read in an editor somewhere else, and a bundle whose
	// line lengths depend on the sender's window diffs against itself.
	if width <= 0 {
		width = textwrap.DefaultWidth
	}
	var b strings.Builder

	fmt.Fprintf(&b, "Application  %s\n", m.Name)
	fmt.Fprintf(&b, "Manifest     version %d\n", m.Version)
	b.WriteString("\n")

	b.WriteString("Services\n")
	if len(m.Services) == 0 {
		b.WriteString("  none\n")
	}
	// A service's facts hang under a gutter as wide as the longest name it
	// leaves room for, which reads well at eighty columns and leaves nothing
	// at forty: twenty three columns of gutter against a forty column terminal
	// is a margin with a fragment in it. Narrow it rather than letting every
	// line overflow.
	gut := 20
	if width < 60 {
		gut = 2
	}
	for _, s := range m.Services {
		port := "no port"
		if s.Port != 0 {
			port = fmt.Sprintf("port %d", s.Port)
		}
		where := s.Path
		if where == "" {
			where = "."
		}
		fmt.Fprintf(&b, "  %-*s %-7s %-12s %s\n", gut, s.Name, s.Kind, port, where)
		fmt.Fprintf(&b, "  %-*s build %s", gut, "", s.Build.Strategy)
		if s.Build.Dockerfile != "" {
			fmt.Fprintf(&b, " (%s)", s.Build.Dockerfile)
		}
		b.WriteString("\n")
		if s.Kind == schema.ServiceWeb {
			fmt.Fprintf(&b, "  %-*s health %s within %s\n", gut, "", s.HealthPath, s.HealthTimeout)
		}
		if s.Schedule != "" {
			fmt.Fprintf(&b, "  %-*s schedule %s\n", gut, "", s.Schedule)
		}
		if s.Migrate != "" {
			fmt.Fprintf(&b, "  %-*s migrate %s\n", gut, "", value(s.Migrate, gut+11, width))
		}
		if len(s.Env) > 0 {
			names := make([]string, 0, len(s.Env))
			for _, e := range s.Env {
				n := e.Name
				if e.Sandbox {
					n += " (sandbox)"
				}
				if !e.IsRequired() {
					n += " (optional)"
				}
				names = append(names, n)
			}
			fmt.Fprintf(&b, "  %-*s env %s\n", gut, "", value(strings.Join(names, ", "), gut+7, width))
		}
		if len(s.DependsOn) > 0 {
			fmt.Fprintf(&b, "  %-*s after %s\n", gut, "",
				value(strings.Join(s.DependsOn, ", "), gut+9, width))
		}
	}
	b.WriteString("\n")

	d := m.Database
	b.WriteString("Database\n")
	fmt.Fprintf(&b, "  provider     %s, Postgres %d\n", d.Provider, d.Version)
	fmt.Fprintf(&b, "  injected as  %s\n", value(d.URLEnv, 15, width))
	if d.SourceURLEnv != "" {
		fmt.Fprintf(&b, "  source from  %s\n", value(
			d.SourceURLEnv+" (read once during a golden refresh, never stored)", 15, width))
	} else if d.Seed != "" {
		fmt.Fprintf(&b, "  seeded by    %s\n", value(d.Seed, 15, width))
	} else {
		fmt.Fprintf(&b, "  source       %s\n",
			value("none declared, so branches start from an empty database", 15, width))
	}
	fmt.Fprintf(&b, "  masking      %s\n", value(d.MaskingRules, 15, width))
	fmt.Fprintf(&b, "  golden       %s\n", value(fmt.Sprintf("refresh %s, keep %d, storage %s",
		orNone(d.Golden.Schedule, "on demand"), d.Golden.Retain, d.Golden.Storage), 15, width))
	if d.Subset.Enabled {
		fmt.Fprintf(&b, "  subset       %s\n", value(fmt.Sprintf(
			"from %s, up to %d rows per table, %d level(s) of dependents",
			d.Subset.SeedTable, d.Subset.MaxRows, *d.Subset.FollowDependents), 15, width))
	} else {
		fmt.Fprintf(&b, "  subset       %s\n",
			value("off, the whole database is masked", 15, width))
	}
	b.WriteString("\n")

	b.WriteString("Egress\n")
	fmt.Fprintf(&b, "  default      %s\n", value(string(m.Egress.Default), 15, width))
	fmt.Fprintf(&b, "  IPv6         %s\n", value(enabledWord(m.Egress.AllowIPv6), 15, width))
	if len(m.Egress.Rules) == 0 {
		fmt.Fprintf(&b, "  %s\n",
			value("no rules, so every outbound request is refused", 2, width))
	}
	for _, r := range m.Egress.Rules {
		detail := ""
		switch {
		case len(r.Paths) > 0:
			detail = " paths " + strings.Join(r.Paths, ", ")
		case len(r.Methods) > 0:
			detail = " methods " + strings.Join(r.Methods, ", ")
		}
		if r.RateLimit != "" {
			detail += " at " + r.RateLimit
		}
		fmt.Fprintf(&b, "  %-30s %-8s%s\n", r.Host, r.Mode, detail)
		if r.Note != "" {
			fmt.Fprintf(&b, "  %-30s %s\n", "", value(r.Note, 33, width))
		}
	}
	b.WriteString("\n")

	if len(m.Personas) > 0 {
		b.WriteString("Personas\n")
		for _, p := range m.Personas {
			mfa := ""
			if p.MFA {
				mfa = " with MFA"
			}
			fmt.Fprintf(&b, "  %-*s %-28s %s%s\n", gut, p.Name, p.Email, p.Login, mfa)
		}
		if m.Auth != nil && m.Auth.Adapter != "" {
			how := string(m.Auth.Adapter)
			if m.Auth.Adapter == schema.AuthAuto {
				how = "auto (chosen from the dependencies and the schema)"
			}
			fmt.Fprintf(&b, "  %-*s %s\n", gut, "created by", how)
			if m.Auth.Adapter == schema.AuthSeed && m.Auth.Seed != "" {
				fmt.Fprintf(&b, "  %-*s %s\n", gut, "", value(m.Auth.Seed, gut+3, width))
			}
			if m.Auth.Sandbox {
				fmt.Fprintf(&b, "  %-*s %s\n", gut, "", "in a sandbox tenant")
			}
		}
		b.WriteString("\n")
	}

	if len(m.Workflows) > 0 {
		b.WriteString("Workflows\n")
		for _, w := range m.Workflows {
			mode := "serial"
			if w.Independent {
				mode = "parallel"
			}
			fmt.Fprintf(&b, "  %-24s as %-14s %s, up to %d steps and %s\n",
				w.Name, w.Persona, mode, w.Budget.Steps, formatUSD(w.Budget.USD))
		}
		b.WriteString("\n")
	}

	if len(m.Invariants) > 0 {
		b.WriteString("Invariants\n")
		for _, inv := range m.Invariants {
			fmt.Fprintf(&b, "  %-24s %s\n", inv.Name,
				value(orNone(inv.Description, "no description"), 27, width))
		}
		b.WriteString("\n")
	}

	b.WriteString("Insights\n")
	fmt.Fprintf(&b, "  rehearsal    %s\n", value(enabledWord(deref(m.Insights.MigrationRehearsal)), 15, width))
	fmt.Fprintf(&b, "  regression   %s, above %.1fx and %.0f ms\n",
		enabledWord(deref(m.Insights.QueryRegression)), m.Insights.RegressionFactor, m.Insights.RegressionMinMS)
	fmt.Fprintf(&b, "  plan diff    %s\n", value(enabledWord(deref(m.Insights.PlanDiff)), 15, width))
	if r := m.Insights.RollingCompatibility; r != nil {
		fmt.Fprintf(&b, "  rolling      %s, against %s\n", r.When, r.Against)
	}
	b.WriteString("\n")

	// Only the keys that stop a merge, and the lock thresholds. The full
	// block is ten lines of "warn" and this section exists so that somebody
	// can see what will block them without reading the schema.
	b.WriteString("Policy\n")
	fmt.Fprintf(&b, "  locks        %s\n", value(fmt.Sprintf("warn at %.0f ms, fail at %.0f ms",
		m.Policy.MigrationLock.WarnMS, m.Policy.MigrationLock.FailMS), 15, width))
	fmt.Fprintf(&b, "  fails on     %s\n", value(failingPolicies(m.Policy), 15, width))
	b.WriteString("\n")

	b.WriteString("Fidelity\n")
	fmt.Fprintf(&b, "  inventory    %s\n", value(enabledWord(deref(m.Fidelity.Enabled)), 15, width))
	// The list of required dimensions grows with the schema and is the one
	// value in this section nothing bounds, so it wraps under its own column
	// like every other unbounded value on the page.
	fmt.Fprintf(&b, "  required     %s\n",
		value(orNone(joinDimensions(m.Fidelity.Require), "nothing"), 15, width))
	b.WriteString("\n")

	b.WriteString("Runtime\n")
	fmt.Fprintf(&b, "  provider     %s\n", value(string(m.Runtime.Provider), 15, width))
	fmt.Fprintf(&b, "  hostnames    *.%s\n", m.Runtime.Domain)
	fmt.Fprintf(&b, "  lifetime     %s\n", value(fmt.Sprintf("%s, sleeping after %s idle",
		m.Runtime.TTL, m.Runtime.IdleSleep), 15, width))
	b.WriteString("\n")

	b.WriteString("GitHub\n")
	fmt.Fprintf(&b, "  mode         %s\n", value(string(m.GitHub.Mode), 15, width))
	fmt.Fprintf(&b, "  comment      %s\n", value(enabledWord(deref(m.GitHub.Comment)), 15, width))
	fmt.Fprintf(&b, "  forks        %s\n", value(forkWord(m.GitHub.ForkPolicy), 15, width))
	fmt.Fprintf(&b, "  teardown on  %s\n", value(strings.Join(m.GitHub.TeardownOn, ", "), 15, width))

	// Printed only when the block is there, because the oracle is the one
	// subsystem that does not run unless a manifest asks for it. A section
	// saying "off" on every manifest in the world would be noise in the one
	// command whose job is to show what is actually in force.
	if o := m.Oracle; o != nil {
		b.WriteString("\nOracle\n")
		fmt.Fprintf(&b, "  comparison   %s\n", value(enabledWord(deref(o.Enabled)), 15, width))
		fmt.Fprintf(&b, "  baseline     %s\n", value(baselineWord(o), 15, width))
		fmt.Fprintf(&b, "  fails on     %s\n", value(string(o.FailOn), 15, width))
		fmt.Fprintf(&b, "  requests     %d %s\n", len(o.Probes), plural("probe", len(o.Probes)))
		for _, p := range o.Probes {
			fmt.Fprintf(&b, "    %-20s %s %s\n", p.Name, p.Method, p.Path)
		}
		fmt.Fprintf(&b, "  database     %s, up to %d rows a table\n",
			enabledWord(deref(o.Database.Enabled)), o.Database.MaxRows)
		fmt.Fprintf(&b, "  timestamps   %s\n", value(normalisedWord(o.CompareTimestamps), 15, width))
		fmt.Fprintf(&b, "  identifiers  %s\n", value(normalisedWord(o.CompareUUIDs), 15, width))
		if len(o.Ignore.Headers) > 0 {
			fmt.Fprintf(&b, "  also ignores %s\n",
				value(strings.Join(o.Ignore.Headers, ", "), 15, width))
		}
		if len(o.Ignore.Fields) > 0 {
			fmt.Fprintf(&b, "  ignores      %s\n",
				value(strings.Join(o.Ignore.Fields, ", "), 15, width))
		}
	}

	if m.Explore != nil && m.Explore.Enabled && len(m.Explore.Goals) > 0 {
		b.WriteString("\nExplore\n")
		for _, g := range m.Explore.Goals {
			fmt.Fprintf(&b, "  %-24s as %-14s seed %s, up to %d steps\n",
				g.Name, g.Persona, g.Seed, g.Budget.Steps)
		}
	}

	if m.Load.Enabled {
		b.WriteString("\nLoad\n")
		fmt.Fprintf(&b, "  source       %s at %.0f%% of production rate for %s\n",
			m.Load.Source, m.Load.Scale*100, m.Load.Duration)
	}
	return b.String()
}

// value wraps an unbounded value under the column it starts in.
func value(s string, indent, width int) string {
	return textwrap.Wrap(s, indent, width)
}

// joinDimensions renders fidelity.require, which is a list of named
// dimensions rather than the single threshold a percentage would invite.
func joinDimensions(ds []schema.FidelityDimension) string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = string(d)
	}
	return strings.Join(out, ", ")
}

func orNone(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func enabledWord(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func deref(b *bool) bool { return b != nil && *b }

func formatUSD(v float64) string {
	return fmt.Sprintf("$%.2f", v)
}

func forkWord(p schema.ForkPolicy) string {
	switch p {
	case schema.ForkNever:
		return "never, no environment is created for a fork"
	case schema.ForkAlways:
		return "always, which lets a stranger's code run with this environment's credentials"
	default:
		return "only when a maintainer adds the antifailure:allow label"
	}
}

// Summary returns a one line description, used in the dashboard header and in
// the pull request comment.
func Summary(m *schema.Manifest) string {
	kinds := map[schema.ServiceKind]int{}
	for _, s := range m.Services {
		kinds[s.Kind]++
	}
	var parts []string
	for _, k := range []schema.ServiceKind{schema.ServiceWeb, schema.ServiceWorker, schema.ServiceCron} {
		if n := kinds[k]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, plural(string(k), n)))
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "no services")
	}
	out := fmt.Sprintf("%s: %s, %s database", m.Name, strings.Join(parts, ", "), m.Database.Provider)
	if n := len(m.Workflows); n > 0 {
		out += fmt.Sprintf(", %d %s", n, plural("workflow", n))
	}
	return out
}

func plural(s string, n int) string {
	if n == 1 {
		return s
	}
	return s + "s"
}

// Hosts returns every host the manifest mentions, sorted, which the network
// policy view and af net explain both list.
func Hosts(m *schema.Manifest) []string {
	seen := map[string]bool{}
	for _, r := range m.Egress.Rules {
		seen[r.Host] = true
	}
	for _, s := range m.Services {
		if s.Build == nil {
			continue
		}
		for _, h := range s.Build.AllowHosts {
			seen[h] = true
		}
	}
	out := make([]string, 0, len(seen))
	for h := range seen {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// baselineWord says which revision the oracle compares against, in words.
func baselineWord(o *schema.Oracle) string {
	ref := o.BaseRef
	if ref == "" {
		ref = "origin/HEAD, or main, or master"
	}
	if o.Baseline == schema.BaselineRef {
		return "the ref " + ref
	}
	return "the merge base with " + ref
}

// normalisedWord reports whether a class of value is compared or absorbed.
func normalisedWord(compared bool) string {
	if compared {
		return "compared exactly"
	}
	return "two well formed values are equal"
}

// failingPolicies names every class of finding that stops a merge.
//
// Named rather than counted, because "3 policies" tells a reader nothing and
// the whole point of the block is that the answer to "why did this fail" is a
// key they can go and read.
func failingPolicies(p *schema.Policy) string {
	pairs := []struct {
		name  string
		level schema.PolicyLevel
	}{
		{"migration_failed", p.MigrationFailed},
		{"migration_rewrite", p.MigrationRewrite},
		{"migration_lint", p.MigrationLint},
		{"plan_regression", p.PlanRegression},
		{"query_regression", p.QueryRegression},
		{"load_regression", p.LoadRegression},
		{"egress_surprise", p.EgressSurprise},
		{"masking", p.Masking},
		{"cleanup", p.Cleanup},
	}
	var names []string
	for _, pair := range pairs {
		if pair.level == schema.PolicyFail {
			names = append(names, pair.name)
		}
	}
	if len(names) == 0 {
		return "nothing; every finding is a warning"
	}
	return strings.Join(names, ", ")
}
