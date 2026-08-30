package manifest

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/antifailure/antifailure/engine/internal/oracle"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// validate applies every semantic rule the JSON Schema cannot express: cross
// references between sections, path confinement, port collisions, dependency
// cycles, and the rules that keep a read only statement read only.
//
// Problems are collected rather than returned one at a time, because fixing a
// manifest by rerunning the command eight times is an experience a validator
// can trivially avoid.
func validate(m *schema.Manifest, doc *yaml.Node, root string) []Problem {
	v := &validator{doc: doc, root: root}

	v.services(m)
	v.database(m)
	v.egress(m)
	v.personas(m)
	v.auth(m)
	v.workflows(m)
	v.invariants(m)
	v.oracle(m)
	v.load(m)
	v.runtime(m)

	if v.suppressed > 0 {
		v.problems = append(v.problems, Problem{
			Message: fmt.Sprintf("There are %d more problems, not listed.", v.suppressed),
			Hint:    "Fix these first; the rest are often the same mistake repeated.",
		})
	}
	return v.problems
}

type validator struct {
	doc      *yaml.Node
	root     string
	problems []Problem
	// suppressed counts problems past the cap, so that the report can say how
	// many it is not showing rather than pretending there are none.
	suppressed int
}

func (v *validator) add(path, msg, hint string) {
	// Past the cap, stop locating problems as well as reporting them. Locating
	// walks the document, so an unbounded report is quadratic in the size of a
	// hostile manifest.
	if len(v.problems) >= maxProblems {
		v.suppressed++
		return
	}
	p := Problem{Path: path, Message: msg, Hint: hint}
	if n := nodeAt(v.doc, path); n != nil {
		p.Line, p.Column = n.Line, n.Column
	}
	v.problems = append(v.problems, p)
}

// pathExists reports whether a repository relative path exists. With no root,
// which the schema example tests use, everything is treated as present.
func (v *validator) pathExists(p string) bool {
	if v.root == "" || p == "" {
		return true
	}
	_, err := os.Stat(filepath.Join(v.root, filepath.FromSlash(p)))
	return err == nil
}

func (v *validator) services(m *schema.Manifest) {
	if len(m.Services) == 0 {
		v.add("services",
			"The manifest declares no services.",
			"Run 'af init' to detect them, or add at least one service by hand.")
		return
	}

	names := map[string]int{}
	ports := map[int]string{}
	for i := range m.Services {
		s := &m.Services[i]
		base := fmt.Sprintf("services[%d]", i)

		if prev, dup := names[s.Name]; dup {
			v.add(base+".name",
				fmt.Sprintf("Two services are both named %q.", s.Name),
				fmt.Sprintf("The first is services[%d]. Names appear in hostnames and container names, so they must be unique.", prev))
		}
		names[s.Name] = i

		if _, ok := confine(s.Path); !ok {
			v.add(base+".path",
				fmt.Sprintf("The path %q resolves outside the repository.", s.Path),
				"Use a path relative to the repository root, with no leading slash and no parent segments.")
		} else if !v.pathExists(s.Path) {
			v.add(base+".path",
				fmt.Sprintf("The directory %q does not exist.", s.Path),
				"Check the spelling, or remove the key to use the repository root.")
		}

		switch s.Kind {
		case schema.ServiceWeb:
			if s.Port == 0 {
				v.add(base+".port",
					fmt.Sprintf("Service %q is a web service and declares no port.", s.Name),
					"Add the port it listens on. Detection fills this in when it can recognise the framework.")
			}
			if s.Schedule != "" {
				v.add(base+".schedule",
					fmt.Sprintf("Service %q is a web service and declares a schedule.", s.Name),
					"A schedule belongs on a service of kind cron.")
			}
		case schema.ServiceCron:
			if s.Schedule == "" {
				v.add(base+".schedule",
					fmt.Sprintf("Service %q is a cron service and declares no schedule.", s.Name),
					"Add a cron expression, optionally prefixed with CRON_TZ=Area/City.")
			} else if err := validateCron(s.Schedule); err != nil {
				v.add(base+".schedule",
					fmt.Sprintf("The schedule for %q is not valid: %s", s.Name, err),
					"A cron expression has five fields: minute, hour, day of month, month, day of week.")
			}
			if s.Command == "" && s.HealthPath == "" {
				v.add(base+".command",
					fmt.Sprintf("Service %q is a cron service and declares neither a command nor a path.", s.Name),
					"A cron service either runs a command or calls an HTTP path on the schedule. Platform crons, such as the ones declared in vercel.json, are the second kind.")
			}
		case schema.ServiceWorker:
			if s.Port != 0 {
				v.add(base+".port",
					fmt.Sprintf("Service %q is a worker and declares a port.", s.Name),
					"A worker receives no traffic. Change kind to web if it should.")
			}
		}

		if s.Port != 0 {
			if prev, taken := ports[s.Port]; taken {
				v.add(base+".port",
					fmt.Sprintf("Services %q and %q both claim port %d.", prev, s.Name, s.Port),
					"Give one of them a different port.")
			}
			ports[s.Port] = s.Name
		}

		v.build(base, s)
		v.env(base, s)

		if _, err := ParseDuration(s.HealthTimeout); err != nil {
			v.add(base+".health_timeout",
				fmt.Sprintf("The health timeout %q is not a duration.", s.HealthTimeout),
				"Use a number with a unit, for example 180s or 3m.")
		}
	}

	v.dependencies(m, names)
}

func (v *validator) build(base string, s *schema.Service) {
	b := s.Build
	if b == nil {
		return
	}
	if b.Strategy == schema.BuildImage && b.Image == "" {
		v.add(base+".build.image",
			fmt.Sprintf("Service %q uses the image strategy and names no image.", s.Name),
			"Add the image reference, pinned by digest if you can.")
	}
	if b.Strategy != schema.BuildImage && b.Image != "" {
		v.add(base+".build.image",
			fmt.Sprintf("Service %q names an image but does not use the image strategy.", s.Name),
			"Set build.strategy to image, or remove build.image.")
	}
	if b.Dockerfile != "" {
		if _, ok := confine(b.Dockerfile); !ok {
			v.add(base+".build.dockerfile",
				fmt.Sprintf("The Dockerfile path %q resolves outside the repository.", b.Dockerfile), "")
		} else if !v.pathExists(b.Dockerfile) {
			v.add(base+".build.dockerfile",
				fmt.Sprintf("The Dockerfile %q does not exist.", b.Dockerfile), "")
		}
		if b.Strategy == schema.BuildBuildpack {
			v.add(base+".build.dockerfile",
				fmt.Sprintf("Service %q names a Dockerfile and uses the buildpack strategy.", s.Name),
				"Set build.strategy to dockerfile, or remove build.dockerfile.")
		}
	}
	if b.Context != "" {
		if _, ok := confine(b.Context); !ok {
			v.add(base+".build.context",
				fmt.Sprintf("The build context %q resolves outside the repository.", b.Context), "")
		} else if !v.pathExists(b.Context) {
			v.add(base+".build.context",
				fmt.Sprintf("The build context %q does not exist.", b.Context), "")
		}
	}
	// A build argument is recorded in image metadata and is readable by anyone
	// who can pull the image. Naming one like a secret is almost always a
	// mistake, and it is a mistake with a real cost.
	for k, val := range b.Args {
		if looksLikeSecretName(k) {
			v.add(base+".build.args",
				fmt.Sprintf("The build argument %q is named like a secret.", k),
				"Build arguments are recorded in image metadata and readable by anyone who can pull the image. Declare it under env instead, where it is mounted rather than baked in.")
		}
		if looksLikeCredentialValue(val) {
			v.add(base+".build.args",
				fmt.Sprintf("The build argument %q holds a value shaped like a credential.", k),
				"Move it to env so that it is mounted at run time and redacted from logs.")
		}
	}
	for _, h := range b.AllowHosts {
		if !validHostPattern(h) {
			v.add(base+".build.allow_hosts",
				fmt.Sprintf("The host %q is not a valid hostname or wildcard.", h),
				"Use a hostname, or a wildcard such as *.example.com.")
		}
	}
}

func (v *validator) env(base string, s *schema.Service) {
	seen := map[string]bool{}
	for i, e := range s.Env {
		p := fmt.Sprintf("%s.env[%d]", base, i)
		if seen[e.Name] {
			v.add(p+".name", fmt.Sprintf("The variable %q is declared twice.", e.Name), "")
		}
		seen[e.Name] = true

		if e.Value != "" && looksLikeCredentialValue(e.Value) {
			v.add(p+".value",
				fmt.Sprintf("The variable %q has a literal value shaped like a credential.", e.Name),
				"A manifest is committed. Remove the value and let the secrets subsystem supply it.")
		}
		if e.Value != "" && e.From != "" {
			v.add(p, fmt.Sprintf("The variable %q sets both value and from.", e.Name),
				"Use one or the other.")
		}
		if e.Sandbox && e.Value != "" {
			v.add(p, fmt.Sprintf("The variable %q is marked sandbox and has a literal value.", e.Name),
				"A sandbox credential comes from the secrets subsystem so that it can be checked against the live key formats.")
		}
	}
}

func (v *validator) dependencies(m *schema.Manifest, names map[string]int) {
	graph := map[string][]string{}
	for i := range m.Services {
		s := &m.Services[i]
		base := fmt.Sprintf("services[%d]", i)
		for _, dep := range s.DependsOn {
			if _, ok := names[dep]; !ok {
				v.add(base+".depends_on",
					fmt.Sprintf("Service %q depends on %q, which is not declared.", s.Name, dep),
					"Check the spelling against the service names above.")
				continue
			}
			if dep == s.Name {
				v.add(base+".depends_on",
					fmt.Sprintf("Service %q depends on itself.", s.Name), "")
				continue
			}
			graph[s.Name] = append(graph[s.Name], dep)
		}
	}
	if cycle := findCycle(graph); len(cycle) > 0 {
		v.add("services",
			fmt.Sprintf("The services %s depend on each other in a cycle.", strings.Join(cycle, " to ")),
			"Nothing could start first. Break the cycle by removing one dependency.")
	}
}

// findCycle returns a cycle as a readable chain, or nil.
func findCycle(graph map[string][]string) []string {
	const (
		white = 0
		grey  = 1
		black = 2
	)
	color := map[string]int{}
	var stack []string
	var result []string

	var visit func(n string) bool
	visit = func(n string) bool {
		color[n] = grey
		stack = append(stack, n)
		for _, next := range graph[n] {
			switch color[next] {
			case grey:
				// Found it. Report from the first appearance of next.
				for i, s := range stack {
					if s == next {
						result = append(append([]string{}, stack[i:]...), next)
						return true
					}
				}
				result = append(append([]string{}, stack...), next)
				return true
			case white:
				if visit(next) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[n] = black
		return false
	}

	// Sorted iteration so that the reported cycle is stable across runs.
	keys := make([]string, 0, len(graph))
	for k := range graph {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		if color[k] == white && visit(k) {
			return result
		}
	}
	return nil
}

func (v *validator) database(m *schema.Manifest) {
	d := m.Database
	if d == nil {
		return
	}
	if d.SourceURLEnv != "" && d.Seed != "" {
		v.add("database",
			"The database declares both a source and a seed command.",
			"A golden is built either from production or from a seed, not both. Remove one.")
	}
	if _, ok := confine(d.MaskingRules); !ok {
		v.add("database.masking_rules",
			fmt.Sprintf("The masking rules path %q resolves outside the repository.", d.MaskingRules), "")
	}
	if d.Golden != nil {
		if d.Golden.Schedule != "" {
			if err := validateCron(d.Golden.Schedule); err != nil {
				v.add("database.golden.schedule",
					fmt.Sprintf("The golden refresh schedule is not valid: %s", err), "")
			}
		}
		if _, err := ParseDuration(d.Golden.MaxAge); err != nil {
			v.add("database.golden.max_age",
				fmt.Sprintf("The maximum age %q is not a duration.", d.Golden.MaxAge),
				"Use a number of hours or days, for example 168h or 7d.")
		}
		if d.Golden.Storage != schema.StorageLocal && d.Golden.StorageURL == "" {
			v.add("database.golden.storage_url",
				fmt.Sprintf("Storage is set to %s and no URL is given.", d.Golden.Storage),
				"Add the container or bucket URL. Credentials come from the secrets subsystem, never from the URL.")
		}
	}
	if d.Subset != nil && d.Subset.Enabled {
		if d.Subset.SeedTable == "" {
			v.add("database.subset.seed_table",
				"Subsetting is enabled and no seed table is named.",
				"Name the table the selection starts from, usually the tenant or account table.")
		}
		for i, r := range d.Subset.VirtualRelationships {
			p := fmt.Sprintf("database.subset.virtual_relationships[%d]", i)
			for field, val := range map[string]string{"from": r.From, "to": r.To} {
				if !strings.Contains(val, ".") {
					v.add(p+"."+field,
						fmt.Sprintf("The reference %q is not in table.column form.", val), "")
				}
			}
		}
	}
}

func (v *validator) egress(m *schema.Manifest) {
	e := m.Egress
	if e == nil {
		return
	}
	if e.Default == schema.ModeAllow {
		v.add("egress.default",
			"The default egress mode is allow, so the environment can reach the whole internet.",
			"This is how a preview environment emails a real customer. Set it to block and add rules for the hosts you need.")
	}

	seen := map[string]int{}
	for i := range e.Rules {
		r := &e.Rules[i]
		base := fmt.Sprintf("egress.rules[%d]", i)

		if !validHostPattern(r.Host) {
			v.add(base+".host",
				fmt.Sprintf("The host %q is not a valid hostname, address, or wildcard.", r.Host),
				"Use a hostname, an IP address, or a wildcard such as *.example.com.")
		}
		if r.Host == "*" && r.Mode != schema.ModeBlock {
			v.add(base+".host",
				fmt.Sprintf("A rule matching every host is set to %s.", r.Mode),
				"Only block may match everything. Name the hosts you want to reach.")
		}
		key := r.Host + "|" + strings.Join(r.Paths, ",") + "|" + strings.Join(r.Methods, ",")
		if prev, dup := seen[key]; dup {
			v.add(base,
				fmt.Sprintf("This rule matches exactly what rule %d matches.", prev),
				"The earlier rule wins, so this one never applies. Remove it or make it more specific.")
		}
		seen[key] = i

		if r.Mode == schema.ModeSandbox && r.Credential == "" {
			v.add(base+".credential",
				fmt.Sprintf("The sandbox rule for %q names no credential.", r.Host),
				"Name the environment variable holding the sandbox key, so that the live key tripwire has something to compare against.")
		}
		if r.Mode == schema.ModeMock && r.Fixtures != "" {
			if _, ok := confine(r.Fixtures); !ok {
				v.add(base+".fixtures",
					fmt.Sprintf("The fixtures path %q resolves outside the repository.", r.Fixtures), "")
			} else if !v.pathExists(r.Fixtures) {
				v.add(base+".fixtures",
					fmt.Sprintf("The fixtures path %q does not exist.", r.Fixtures), "")
			}
		}
		if r.Fixtures != "" && r.Mode != schema.ModeMock {
			v.add(base+".fixtures",
				fmt.Sprintf("Fixtures are only used in mock mode, and this rule is %s.", r.Mode), "")
		}
		if r.Credential != "" && r.Mode != schema.ModeSandbox {
			v.add(base+".credential",
				fmt.Sprintf("A credential is only used in sandbox mode, and this rule is %s.", r.Mode), "")
		}
		if r.RateLimit != "" {
			if _, _, err := ParseRate(r.RateLimit); err != nil {
				v.add(base+".rate_limit",
					fmt.Sprintf("The rate limit %q is not valid.", r.RateLimit),
					"Use a count and a unit, for example 10/s or 600/m.")
			}
			if r.Mode != schema.ModeAllow && r.Mode != schema.ModeSandbox {
				v.add(base+".rate_limit",
					fmt.Sprintf("A rate limit applies to allow and sandbox, and this rule is %s.", r.Mode), "")
			}
		}
		if r.Mode == schema.ModeSynth {
			v.add(base+".mode",
				fmt.Sprintf("The rule for %q uses synth, so any workflow that touches it reports unverified rather than pass.", r.Host),
				"Synth is an escape hatch for exploration. Write a fixture before you rely on the result.")
		}
	}
}

func (v *validator) personas(m *schema.Manifest) {
	names := map[string]bool{}
	emails := map[string]int{}
	phones := map[string]int{}
	for i := range m.Personas {
		p := &m.Personas[i]
		base := fmt.Sprintf("personas[%d]", i)
		if names[p.Name] {
			v.add(base+".name", fmt.Sprintf("Two personas are both named %q.", p.Name), "")
		}
		names[p.Name] = true

		if prev, dup := emails[p.Email]; dup {
			v.add(base+".email",
				fmt.Sprintf("Personas %q and %q share the address %q.", m.Personas[prev].Name, p.Name, p.Email),
				"Each persona needs its own address, because the inbox routes messages by recipient.")
		}
		emails[p.Email] = i

		if !validEmail(p.Email) {
			v.add(base+".email", fmt.Sprintf("The address %q is not valid.", p.Email), "")
		}
		if p.Login == schema.LoginSMSCode {
			v.add(base+".login",
				fmt.Sprintf("Persona %q logs in with an SMS code.", p.Name),
				"The message provider must be set to capture mode for the runner to read it.")
		}
		if p.Phone != "" {
			if prev, dup := phones[p.Phone]; dup {
				// The inbox routes a text by recipient exactly as it routes
				// mail by recipient, so two personas on one number means each
				// can read the other's code.
				v.add(base+".phone",
					fmt.Sprintf("Personas %q and %q share the number %q.",
						m.Personas[prev].Name, p.Name, p.Phone),
					"Each persona needs its own number, because the inbox routes messages by recipient.")
			}
			phones[p.Phone] = i
		}
	}
}

// auth checks that the configured adapter has what it needs.
//
// Every rule here prevents a failure that would otherwise happen much later,
// after a golden refresh, when the branch is up and the agent cannot sign in.
// The cost of finding out at `af doctor` time rather than then is the whole
// reason this function exists.
func (v *validator) auth(m *schema.Manifest) {
	a := m.Auth
	if a == nil {
		return
	}

	hosted := map[schema.AuthAdapter]bool{
		schema.AuthSupabaseAPI: true, schema.AuthClerk: true,
		schema.AuthAuth0: true, schema.AuthWorkOS: true,
	}

	switch a.Adapter {
	case schema.AuthSeed:
		if strings.TrimSpace(a.Seed) == "" {
			v.add("auth.seed",
				"The seed adapter is selected and no command is configured.",
				"Set auth.seed to the command that creates a persona.")
		}
	case schema.AuthDirect:
		if a.Table == nil {
			// Not an error: detection can describe the table from the live
			// schema. Worth saying, because detection guesses and this does
			// not.
			v.add("auth.table",
				"The direct adapter is selected and no table is described.",
				"Add auth.table with the users table's name and columns, or leave "+
					"auth.adapter unset so detection reads them from the database.")
		} else if strings.TrimSpace(a.Table.Name) == "" {
			v.add("auth.table.name", "The users table has no name.", "")
		}
	case schema.AuthSupabaseAPI:
		if strings.TrimSpace(a.URL) == "" {
			v.add("auth.url",
				"The Supabase auth API adapter is selected and no project URL is set.",
				"Set auth.url to the project's API root, for example https://abc.supabase.co.")
		}
	case schema.AuthAuth0:
		if strings.TrimSpace(a.Domain) == "" {
			v.add("auth.domain",
				"The Auth0 adapter is selected and no tenant domain is set.",
				"Set auth.domain to the tenant, for example dev-abc123.us.auth0.com.")
		}
	}

	if hosted[a.Adapter] {
		if strings.TrimSpace(a.TokenEnv) == "" {
			v.add("auth.token_env",
				fmt.Sprintf("The %s adapter needs an admin token and none is named.", a.Adapter),
				"Set auth.token_env to the variable holding it. The variable name, never the token.")
		}
		if !a.Sandbox {
			// The same refusal AF-DB-020 makes at run time, made early. A
			// hosted adapter with no sandbox has nowhere to put a persona
			// except the production tenant.
			v.add("auth.sandbox",
				fmt.Sprintf("The %s adapter is selected and auth.sandbox is not set.", a.Adapter),
				"Point it at a sandbox, development or staging tenant and set "+
					"auth.sandbox: true. Without one the only tenant left is production.")
		}
	}

	if a.TokenEnv != "" && looksLikeACredential(a.TokenEnv) {
		v.add("auth.token_env",
			"auth.token_env holds something shaped like a credential rather than a variable name.",
			"Put the variable's NAME here and the credential in that variable.")
	}

	for i, name := range a.Sessions {
		if strings.Count(name, ".") > 1 || strings.TrimSpace(name) == "" {
			v.add(fmt.Sprintf("auth.sessions[%d]", i),
				fmt.Sprintf("%q is not a table name.", name),
				"Write it as table or schema.table.")
		}
	}

	if a.Password != nil && a.Password.MinLength > 128 {
		v.add("auth.password.min_length",
			"A minimum password length above 128 is longer than most applications accept.", "")
	}
}

// looksLikeACredential reports whether a value is a secret rather than the
// name of the variable holding one.
//
// A generated token is long, mixed case and often prefixed. A variable name is
// short, upper case and underscored. The test is deliberately loose, because
// the cost of a false positive is one confused message and the cost of a false
// negative is a credential committed to a repository.
func looksLikeACredential(value string) bool {
	if len(value) < 20 {
		return false
	}
	for _, prefix := range []string{"sk_", "sk-", "pk_", "eyJ", "ey", "whsec_", "Bearer "} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	upper := strings.ToUpper(value)
	return value != upper && !strings.Contains(value, " ")
}

func (v *validator) workflows(m *schema.Manifest) {
	personas := map[string]bool{}
	for _, p := range m.Personas {
		personas[p.Name] = true
	}
	names := map[string]bool{}
	for i := range m.Workflows {
		w := &m.Workflows[i]
		base := fmt.Sprintf("workflows[%d]", i)
		if names[w.Name] {
			v.add(base+".name", fmt.Sprintf("Two workflows are both named %q.", w.Name), "")
		}
		names[w.Name] = true

		if w.Persona == "" {
			v.add(base+".persona",
				fmt.Sprintf("Workflow %q names no persona and the manifest declares none.", w.Name),
				"Add a persona so that the agent has an account to log in as.")
		} else if !personas[w.Persona] {
			v.add(base+".persona",
				fmt.Sprintf("Workflow %q runs as %q, which is not a declared persona.", w.Name, w.Persona),
				"Check the spelling against the persona names.")
		}
		if w.Budget != nil {
			if _, err := ParseDuration(w.Budget.Duration); err != nil {
				v.add(base+".budget.duration",
					fmt.Sprintf("The budget duration %q is not a duration.", w.Budget.Duration), "")
			}
		}
		// The agent plans from this text, so a description that names no goal
		// produces a workflow that wanders. Ten characters is the schema's
		// floor; three words is the smallest phrase that can carry a verb, an
		// object, and an outcome.
		if len(strings.TrimSpace(w.Description)) < 10 || len(strings.Fields(w.Description)) < 4 {
			v.add(base+".description",
				fmt.Sprintf("The description of %q is too short to plan from.", w.Name),
				"Say what a person would do and what proves it worked. The agent plans from this text.")
		}
	}
}

func (v *validator) invariants(m *schema.Manifest) {
	names := map[string]bool{}
	for i := range m.Invariants {
		inv := &m.Invariants[i]
		base := fmt.Sprintf("invariants[%d]", i)
		if names[inv.Name] {
			v.add(base+".name", fmt.Sprintf("Two invariants are both named %q.", inv.Name), "")
		}
		names[inv.Name] = true

		if err := validateReadOnlySQL(inv.SQL); err != nil {
			v.add(base+".sql",
				fmt.Sprintf("The invariant %q is not read only: %s", inv.Name, err),
				"Write it as a single SELECT that returns the violating rows. It runs inside a read only transaction, so a write would be refused at run time as well.")
		}
		if alwaysReturnsARow(inv.SQL) {
			v.add(base+".sql",
				fmt.Sprintf("The invariant %q counts the violations instead of returning them, so it can never hold.", inv.Name),
				"An invariant holds when its statement returns no rows, and a bare count returns one row saying zero. Select the offending rows themselves: SELECT id FROM ... WHERE ... rather than SELECT count(*) FROM ... WHERE ....")
		}
	}
}

// oracle checks that a comparison somebody asked for can actually be made.
//
// Each rule here prevents a failure that would otherwise arrive after two
// environments have been built, which is minutes of Docker and two database
// branches spent to learn that a path was missing a slash.
func (v *validator) oracle(m *schema.Manifest) {
	o := m.Oracle
	if o == nil {
		return
	}

	if _, ok := oracle.ParseSeverity(o.FailOn); !ok {
		v.add("oracle.fail_on",
			fmt.Sprintf("%q is not a severity.", o.FailOn),
			"Use none, minor, major, or critical.")
	}

	if o.Baseline == schema.BaselineRef && o.BaseRef == "" {
		v.add("oracle.base_ref",
			"The baseline is an explicit ref and no ref is given.",
			"Name the branch, tag, or commit to compare against, or use baseline: merge_base.")
	}

	if len(o.Probes) == 0 && deref(o.Enabled) {
		v.add("oracle.probes",
			"The oracle is on and declares no requests to send.",
			"Add at least one probe. Both versions have to receive the same requests in the same "+
				"order, so the plan is written down rather than discovered.")
	}

	names := map[string]int{}
	for i := range o.Probes {
		p := &o.Probes[i]
		base := fmt.Sprintf("oracle.probes[%d]", i)

		if prev, dup := names[p.Name]; dup {
			v.add(base+".name",
				fmt.Sprintf("Two probes are both named %q.", p.Name),
				fmt.Sprintf("The first is oracle.probes[%d]. The name is how a difference is "+
					"reported, so two of them make a report nobody can act on.", prev))
		}
		names[p.Name] = i

		if !strings.HasPrefix(p.Path, "/") {
			v.add(base+".path",
				fmt.Sprintf("The path %q does not start with a slash.", p.Path),
				"A probe path is a path and a query, such as /orders?limit=10.")
		}
		if p.Body != "" && p.Method == http.MethodGet {
			v.add(base+".body",
				fmt.Sprintf("Probe %q is a GET and declares a body.", p.Name),
				"Most servers ignore a body on a GET, so this would be sent and have no effect. "+
					"Set a method, or move the values into the query.")
		}
		if p.Body != "" && jsonContentType(p.Headers) && !json.Valid([]byte(p.Body)) {
			v.add(base+".body",
				fmt.Sprintf("Probe %q declares a JSON content type and a body that is not JSON.", p.Name),
				"Both versions would refuse it identically, so the comparison would prove nothing.")
		}
		for name, value := range p.Headers {
			if looksLikeACredential(value) {
				v.add(base+".headers",
					fmt.Sprintf("The header %q on probe %q carries what looks like a credential.", name, p.Name),
					"A probe is committed to the repository. Credentials come from the secrets "+
						"subsystem; a probe that needs a session should sign in through one.")
			}
		}
	}

	if o.Ignore != nil {
		for i, f := range o.Ignore.Fields {
			if !oracle.ValidPattern(f) {
				v.add(fmt.Sprintf("oracle.ignore.fields[%d]", i),
					fmt.Sprintf("%q is not a field path.", f),
					"Write $.field, $.list[0].field, $.list[*].field, $..field, or $.object.*.")
			}
		}
	}

	if o.Database != nil {
		for i, t := range append(append([]string(nil), o.Database.Tables...), o.Database.Exclude...) {
			if strings.Count(t, ".") > 1 {
				key := "oracle.database.tables"
				if i >= len(o.Database.Tables) {
					key = "oracle.database.exclude"
				}
				v.add(key,
					fmt.Sprintf("%q is not a table pattern.", t),
					"Write a table name, or schema.table, with an asterisk for either half.")
			}
		}
	}
}

// jsonContentType reports whether a probe declares a JSON body.
func jsonContentType(headers map[string]string) bool {
	for k, v := range headers {
		if strings.EqualFold(k, "content-type") {
			lower := strings.ToLower(v)
			return strings.Contains(lower, "json")
		}
	}
	return false
}

func (v *validator) load(m *schema.Manifest) {
	l := m.Load
	if l == nil || !l.Enabled {
		return
	}
	d, err := ParseDuration(l.Duration)
	if err != nil {
		v.add("load.duration", fmt.Sprintf("The load duration %q is not a duration.", l.Duration), "")
		return
	}
	if d > 15*60*1e9 {
		v.add("load.duration",
			fmt.Sprintf("The load duration %s is above the fifteen minute cap.", l.Duration),
			"Load runs are a comparison, not a soak test. Lower it or raise the scale instead.")
	}
	overlap := map[string]bool{}
	for _, r := range l.SafeRoutes {
		overlap[r] = true
	}
	for _, r := range l.UnsafeRoutes {
		if overlap[r] {
			v.add("load.unsafe_routes",
				fmt.Sprintf("The route %q is listed as both safe and unsafe.", r),
				"Unsafe wins, and the ambiguity will confuse whoever reads this next. Remove one.")
		}
	}
}

func (v *validator) runtime(m *schema.Manifest) {
	r := m.Runtime
	if r == nil {
		return
	}
	for field, val := range map[string]string{"ttl": r.TTL, "idle_sleep": r.IdleSleep} {
		if _, err := ParseDuration(val); err != nil {
			v.add("runtime."+field, fmt.Sprintf("The value %q is not a duration.", val), "")
		}
	}
	if r.Provider == schema.RuntimeKubernetes && r.Domain == DefaultDomain {
		v.add("runtime.domain",
			"The runtime is Kubernetes and the domain is still localhost.",
			"Set a wildcard domain that resolves to the cluster's ingress, so preview URLs work from a browser.")
	}
}

// ParseRate parses a token bucket rate such as 10/s.
func ParseRate(s string) (count int, unit string, err error) {
	i := strings.IndexByte(s, '/')
	if i <= 0 || i == len(s)-1 {
		return 0, "", fmt.Errorf("expected a count and a unit, such as 10/s")
	}
	n, err := strconv.Atoi(s[:i])
	if err != nil || n <= 0 {
		return 0, "", fmt.Errorf("the count must be a positive number")
	}
	unit = s[i+1:]
	switch unit {
	case "s", "m", "h":
	default:
		return 0, "", fmt.Errorf("the unit must be s, m, or h")
	}
	return n, unit, nil
}

// validHostPattern accepts a hostname, an IP address, or a leading wildcard.
func validHostPattern(h string) bool {
	if h == "" {
		return false
	}
	if h == "*" {
		return true
	}
	if strings.HasPrefix(h, "*.") {
		h = h[2:]
		if h == "" {
			return false
		}
	}
	// A port may be attached, and is matched separately by the policy engine.
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	if ip := net.ParseIP(h); ip != nil {
		return true
	}
	if len(h) > 253 || strings.HasPrefix(h, ".") || strings.HasSuffix(h, ".") {
		return false
	}
	for _, label := range strings.Split(h, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
				(r >= '0' && r <= '9') || r == '-' || r == '_'
			if !ok {
				return false
			}
		}
	}
	return true
}

func validEmail(s string) bool {
	at := strings.LastIndexByte(s, '@')
	if at <= 0 || at == len(s)-1 || len(s) > 254 {
		return false
	}
	local, domain := s[:at], s[at+1:]
	if strings.ContainsAny(local, " \t\r\n") {
		return false
	}
	return validHostPattern(domain) && !strings.HasPrefix(domain, "*")
}

// looksLikeSecretName reports whether a name suggests it holds a credential.
func looksLikeSecretName(k string) bool {
	l := strings.ToLower(k)
	for _, m := range []string{"secret", "password", "passwd", "token", "apikey",
		"api_key", "access_key", "private_key", "credential", "client_secret"} {
		if strings.Contains(l, m) {
			return true
		}
	}
	return false
}

// looksLikeCredentialValue reports whether a literal value carries a known
// credential prefix. It is deliberately narrow: a false positive here rejects
// a valid manifest, so only unambiguous provider prefixes count.
func looksLikeCredentialValue(v string) bool {
	if len(v) < 20 {
		return false
	}
	prefixes := []string{
		"sk" + "_live_", "sk" + "_test_", "rk" + "_live_", "pk" + "_live_",
		"wh" + "sec_", "gh" + "p_", "gh" + "o_", "gh" + "s_", "gh" + "u_", "gh" + "r_",
		"github" + "_pat_", "AK" + "IA", "AS" + "IA", "xo" + "xb-", "xo" + "xp-",
		"S" + "G.", "sk" + "-ant-", "sb" + "p_", "na" + "pi_", "np" + "m_",
		"ey" + "J", "-----BEGIN",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(v, p) {
			return true
		}
	}
	// A connection string carrying a password.
	if strings.Contains(v, "://") && strings.Contains(v, "@") {
		if i := strings.Index(v, "://"); i >= 0 {
			rest := v[i+3:]
			if at := strings.IndexByte(rest, '@'); at > 0 && strings.Contains(rest[:at], ":") {
				return true
			}
		}
	}
	return false
}

// validateReadOnlySQL rejects a statement that could write.
//
// This is belt and braces: invariants also run inside a read only transaction,
// so Postgres refuses a write regardless. Catching it at validation means the
// author finds out when they write the manifest rather than when the first
// workflow finishes.
func validateReadOnlySQL(sql string) error {
	trimmed := strings.TrimSpace(stripSQLComments(sql))
	if trimmed == "" {
		return fmt.Errorf("the statement is empty")
	}
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, "select") && !strings.HasPrefix(lower, "with") &&
		!strings.HasPrefix(lower, "table") && !strings.HasPrefix(lower, "values") {
		return fmt.Errorf("it must begin with SELECT, WITH, TABLE, or VALUES")
	}
	// A semicolon inside the statement means more than one statement, which is
	// how a read only check gets a write appended to it.
	body := strings.TrimSuffix(trimmed, ";")
	if strings.Contains(body, ";") {
		return fmt.Errorf("it contains more than one statement")
	}
	for _, kw := range []string{
		"insert ", "update ", "delete ", "drop ", "alter ", "truncate ",
		"create ", "grant ", "revoke ", "copy ", "call ", "do ",
		"vacuum", "reindex", "cluster ", "refresh materialized",
		"set ", "reset ", "lock ", "listen ", "notify ",
		"pg_read_file", "pg_ls_dir", "dblink", "pg_sleep",
	} {
		if containsKeyword(lower, kw) {
			return fmt.Errorf("it uses %s", strings.ToUpper(strings.TrimSpace(kw)))
		}
	}
	return nil
}

// alwaysReturnsARow reports whether the statement is a bare aggregate, which
// returns exactly one row whatever the data says and so describes an invariant
// that can never hold.
//
// This is the mistake everybody makes first, including this repository's own
// example, which said `SELECT count(*) AS violations FROM orders o LEFT JOIN
// ...` and would have been red on every run from the day invariants started
// running. Counting the violations feels like the natural way to ask, and the
// contract is that the rows ARE the answer.
//
// Deliberately narrow. It fires only when the select list opens with an
// aggregate call and the statement has no GROUP BY or HAVING to make the row
// count depend on the data. That leaves `SELECT id FROM t WHERE x > (SELECT
// avg(y) FROM t)` alone, which mentions an aggregate and is a perfectly good
// invariant, because refusing a correct manifest is worse than missing a
// wrong one.
func alwaysReturnsARow(sql string) bool {
	lower := strings.ToLower(strings.TrimSpace(stripSQLComments(sql)))
	if !strings.HasPrefix(lower, "select") {
		return false
	}
	// A grouped or filtered aggregate returns a row per group, and no groups
	// when nothing matches, which is exactly the shape that works.
	if containsKeyword(lower, "group by") || containsKeyword(lower, "having ") {
		return false
	}
	list := strings.TrimSpace(strings.TrimPrefix(lower, "select"))
	list = strings.TrimPrefix(list, "distinct ")
	for _, fn := range []string{"count(", "sum(", "avg(", "min(", "max(",
		"bool_and(", "bool_or(", "every("} {
		if strings.HasPrefix(strings.TrimSpace(list), fn) {
			return true
		}
	}
	return false
}

// containsKeyword looks for a keyword at a word boundary, so that a column
// named "created_at" does not trip the "create" rule.
func containsKeyword(haystack, kw string) bool {
	from := 0
	for {
		i := strings.Index(haystack[from:], kw)
		if i < 0 {
			return false
		}
		i += from
		if i == 0 || !isWordByte(haystack[i-1]) {
			return true
		}
		from = i + 1
	}
}

func isWordByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func stripSQLComments(s string) string {
	var b strings.Builder
	for _, lineText := range strings.Split(s, "\n") {
		if i := strings.Index(lineText, "--"); i >= 0 {
			lineText = lineText[:i]
		}
		b.WriteString(lineText)
		b.WriteString("\n")
	}
	out := b.String()
	for {
		start := strings.Index(out, "/*")
		if start < 0 {
			break
		}
		end := strings.Index(out[start:], "*/")
		if end < 0 {
			out = out[:start]
			break
		}
		out = out[:start] + " " + out[start+end+2:]
	}
	return out
}

// validateCron checks a five field expression, with an optional CRON_TZ prefix.
func validateCron(expr string) error {
	if rest, ok := strings.CutPrefix(expr, "CRON_TZ="); ok {
		i := strings.IndexByte(rest, ' ')
		if i < 0 {
			return fmt.Errorf("the time zone prefix is not followed by an expression")
		}
		expr = rest[i+1:]
	}
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return fmt.Errorf("it has %d fields, and a cron expression has 5", len(fields))
	}
	bounds := [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	for i, f := range fields {
		if err := validateCronField(f, bounds[i][0], bounds[i][1]); err != nil {
			return fmt.Errorf("field %d (%q) is not valid: %w", i+1, f, err)
		}
	}
	return nil
}

func validateCronField(f string, lo, hi int) error {
	for _, part := range strings.Split(f, ",") {
		step := part
		if i := strings.IndexByte(part, '/'); i >= 0 {
			step = part[:i]
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n <= 0 {
				return fmt.Errorf("the step must be a positive number")
			}
		}
		if step == "*" {
			continue
		}
		if i := strings.IndexByte(step, '-'); i > 0 {
			from, err1 := strconv.Atoi(step[:i])
			to, err2 := strconv.Atoi(step[i+1:])
			if err1 != nil || err2 != nil {
				return fmt.Errorf("the range must be two numbers")
			}
			if from < lo || to > hi || from > to {
				return fmt.Errorf("the range must be within %d to %d", lo, hi)
			}
			continue
		}
		n, err := strconv.Atoi(step)
		if err != nil {
			return fmt.Errorf("expected a number, a range, or *")
		}
		if n < lo || n > hi {
			return fmt.Errorf("the value must be within %d to %d", lo, hi)
		}
	}
	return nil
}

// nodeAt resolves a dotted path such as services[1].port to its YAML node, so
// that a semantic problem can name the line the user has to edit.
func nodeAt(doc *yaml.Node, dotted string) *yaml.Node {
	if doc == nil || dotted == "" {
		return nil
	}
	n := doc
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	for _, seg := range splitPath(dotted) {
		if n == nil {
			return nil
		}
		if seg.index >= 0 {
			n = mapValue(n, seg.key)
			if n == nil || n.Kind != yaml.SequenceNode || seg.index >= len(n.Content) {
				return n
			}
			n = n.Content[seg.index]
			continue
		}
		next := mapValue(n, seg.key)
		if next == nil {
			// The key is absent, which is often exactly the problem. Point at
			// the containing mapping rather than nothing.
			return n
		}
		n = next
	}
	return n
}

type pathSeg struct {
	key   string
	index int
}

func splitPath(dotted string) []pathSeg {
	var out []pathSeg
	for _, part := range strings.Split(dotted, ".") {
		if i := strings.IndexByte(part, '['); i >= 0 && strings.HasSuffix(part, "]") {
			idx, err := strconv.Atoi(part[i+1 : len(part)-1])
			if err != nil {
				idx = -1
			}
			out = append(out, pathSeg{key: part[:i], index: idx})
			continue
		}
		out = append(out, pathSeg{key: part, index: -1})
	}
	return out
}

func mapValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}
