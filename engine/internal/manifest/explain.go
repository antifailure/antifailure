package manifest

import (
	"fmt"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Explain renders the effective configuration: what the manifest says after
// every default has been applied.
//
// It exists because the most common configuration bug is a default the user
// did not know about. Printing the resolved value beside the one they wrote
// turns "why is it blocking that host" into a one line answer.
func Explain(m *schema.Manifest) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Application  %s\n", m.Name)
	fmt.Fprintf(&b, "Manifest     version %d\n", m.Version)
	b.WriteString("\n")

	b.WriteString("Services\n")
	if len(m.Services) == 0 {
		b.WriteString("  none\n")
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
		fmt.Fprintf(&b, "  %-20s %-7s %-12s %s\n", s.Name, s.Kind, port, where)
		fmt.Fprintf(&b, "  %-20s build %s", "", s.Build.Strategy)
		if s.Build.Dockerfile != "" {
			fmt.Fprintf(&b, " (%s)", s.Build.Dockerfile)
		}
		b.WriteString("\n")
		if s.Kind == schema.ServiceWeb {
			fmt.Fprintf(&b, "  %-20s health %s within %s\n", "", s.HealthPath, s.HealthTimeout)
		}
		if s.Schedule != "" {
			fmt.Fprintf(&b, "  %-20s schedule %s\n", "", s.Schedule)
		}
		if s.Migrate != "" {
			fmt.Fprintf(&b, "  %-20s migrate %s\n", "", s.Migrate)
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
			fmt.Fprintf(&b, "  %-20s env %s\n", "", strings.Join(names, ", "))
		}
		if len(s.DependsOn) > 0 {
			fmt.Fprintf(&b, "  %-20s after %s\n", "", strings.Join(s.DependsOn, ", "))
		}
	}
	b.WriteString("\n")

	d := m.Database
	b.WriteString("Database\n")
	fmt.Fprintf(&b, "  provider     %s, Postgres %d\n", d.Provider, d.Version)
	fmt.Fprintf(&b, "  injected as  %s\n", d.URLEnv)
	if d.SourceURLEnv != "" {
		fmt.Fprintf(&b, "  source from  %s (read once during a golden refresh, never stored)\n", d.SourceURLEnv)
	} else if d.Seed != "" {
		fmt.Fprintf(&b, "  seeded by    %s\n", d.Seed)
	} else {
		b.WriteString("  source       none declared, so branches start from an empty database\n")
	}
	fmt.Fprintf(&b, "  masking      %s\n", d.MaskingRules)
	fmt.Fprintf(&b, "  golden       refresh %s, keep %d, storage %s\n",
		orNone(d.Golden.Schedule, "on demand"), d.Golden.Retain, d.Golden.Storage)
	if d.Subset.Enabled {
		fmt.Fprintf(&b, "  subset       from %s, up to %d rows per table, %d level(s) of dependents\n",
			d.Subset.SeedTable, d.Subset.MaxRows, *d.Subset.FollowDependents)
	} else {
		b.WriteString("  subset       off, the whole database is masked\n")
	}
	b.WriteString("\n")

	b.WriteString("Egress\n")
	fmt.Fprintf(&b, "  default      %s\n", m.Egress.Default)
	fmt.Fprintf(&b, "  IPv6         %s\n", enabledWord(m.Egress.AllowIPv6))
	if len(m.Egress.Rules) == 0 {
		b.WriteString("  no rules, so every outbound request is refused\n")
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
			fmt.Fprintf(&b, "  %-30s %s\n", "", r.Note)
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
			fmt.Fprintf(&b, "  %-20s %-28s %s%s\n", p.Name, p.Email, p.Login, mfa)
		}
		if m.Auth != nil && m.Auth.Adapter != "" {
			how := string(m.Auth.Adapter)
			if m.Auth.Adapter == schema.AuthAuto {
				how = "auto (chosen from the dependencies and the schema)"
			}
			fmt.Fprintf(&b, "  %-20s %s\n", "created by", how)
			if m.Auth.Adapter == schema.AuthSeed && m.Auth.Seed != "" {
				fmt.Fprintf(&b, "  %-20s %s\n", "", m.Auth.Seed)
			}
			if m.Auth.Sandbox {
				fmt.Fprintf(&b, "  %-20s %s\n", "", "in a sandbox tenant")
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
			fmt.Fprintf(&b, "  %-24s %s\n", inv.Name, orNone(inv.Description, "no description"))
		}
		b.WriteString("\n")
	}

	b.WriteString("Insights\n")
	fmt.Fprintf(&b, "  rehearsal    %s\n", enabledWord(deref(m.Insights.MigrationRehearsal)))
	fmt.Fprintf(&b, "  regression   %s, above %.1fx and %.0f ms\n",
		enabledWord(deref(m.Insights.QueryRegression)), m.Insights.RegressionFactor, m.Insights.RegressionMinMS)
	fmt.Fprintf(&b, "  plan diff    %s\n", enabledWord(deref(m.Insights.PlanDiff)))
	if r := m.Insights.RollingCompatibility; r != nil {
		fmt.Fprintf(&b, "  rolling      %s, against %s\n", r.When, r.Against)
	}
	b.WriteString("\n")

	// Only the keys that stop a merge, and the lock thresholds. The full
	// block is ten lines of "warn" and this section exists so that somebody
	// can see what will block them without reading the schema.
	b.WriteString("Policy\n")
	fmt.Fprintf(&b, "  locks        warn at %.0f ms, fail at %.0f ms\n",
		m.Policy.MigrationLock.WarnMS, m.Policy.MigrationLock.FailMS)
	fmt.Fprintf(&b, "  fails on     %s\n", failingPolicies(m.Policy))
	b.WriteString("\n")

	b.WriteString("Runtime\n")
	fmt.Fprintf(&b, "  provider     %s\n", m.Runtime.Provider)
	fmt.Fprintf(&b, "  hostnames    *.%s\n", m.Runtime.Domain)
	fmt.Fprintf(&b, "  lifetime     %s, sleeping after %s idle\n", m.Runtime.TTL, m.Runtime.IdleSleep)
	// The ceiling is printed next to the lifetime rather than left to the
	// manifest reference, because the two numbers only mean anything together:
	// the first is what an environment gets, the second is the most af env
	// extend can ever give it.
	fmt.Fprintf(&b, "  extend to    %s at the most, from when it was created\n", m.Runtime.MaxTTL)
	b.WriteString("\n")

	b.WriteString("GitHub\n")
	fmt.Fprintf(&b, "  mode         %s\n", m.GitHub.Mode)
	fmt.Fprintf(&b, "  comment      %s\n", enabledWord(deref(m.GitHub.Comment)))
	fmt.Fprintf(&b, "  forks        %s\n", forkWord(m.GitHub.ForkPolicy))
	fmt.Fprintf(&b, "  teardown on  %s\n", strings.Join(m.GitHub.TeardownOn, ", "))

	// Printed only when the block is there, because the oracle is the one
	// subsystem that does not run unless a manifest asks for it. A section
	// saying "off" on every manifest in the world would be noise in the one
	// command whose job is to show what is actually in force.
	if o := m.Oracle; o != nil {
		b.WriteString("\nOracle\n")
		fmt.Fprintf(&b, "  comparison   %s\n", enabledWord(deref(o.Enabled)))
		fmt.Fprintf(&b, "  baseline     %s\n", baselineWord(o))
		fmt.Fprintf(&b, "  fails on     %s\n", o.FailOn)
		fmt.Fprintf(&b, "  requests     %d %s\n", len(o.Probes), plural("probe", len(o.Probes)))
		for _, p := range o.Probes {
			fmt.Fprintf(&b, "    %-20s %s %s\n", p.Name, p.Method, p.Path)
		}
		fmt.Fprintf(&b, "  database     %s, up to %d rows a table\n",
			enabledWord(deref(o.Database.Enabled)), o.Database.MaxRows)
		fmt.Fprintf(&b, "  timestamps   %s\n", normalisedWord(o.CompareTimestamps))
		fmt.Fprintf(&b, "  identifiers  %s\n", normalisedWord(o.CompareUUIDs))
		if len(o.Ignore.Headers) > 0 {
			fmt.Fprintf(&b, "  also ignores %s\n", strings.Join(o.Ignore.Headers, ", "))
		}
		if len(o.Ignore.Fields) > 0 {
			fmt.Fprintf(&b, "  ignores      %s\n", strings.Join(o.Ignore.Fields, ", "))
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
