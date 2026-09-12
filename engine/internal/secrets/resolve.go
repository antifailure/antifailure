package secrets

// Resolving a manifest's declared variables into an environment for the runtime.
//
// This is the layer that decides what a service actually receives, and there
// are three rules in it that are worth more than the code.
//
// A service receives what the manifest declares and nothing else. The engine's
// own environment is not passed through. A preview environment that inherited
// the shell it was started from would inherit AWS credentials, a production
// database URL, and whatever else is exported on a developer's laptop, which is
// the exact opposite of an isolated environment.
//
// A sandbox credential is never given to a service. The whole point of
// substituting it at the boundary is that the application never holds one, so
// it goes to the sidecar and the service gets a marker that is obviously not
// real. An application that logs its own configuration then logs the marker.
//
// A missing variable stops the environment before anything is created. Starting
// an environment with a variable absent produces a failure ten seconds later
// inside a container, in a log nobody is watching, and it looks like the
// application is broken rather than the configuration.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/pkg/livekey"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// ServiceVars is what one service declares, with the service's name attached.
//
// The name is the whole point. A lookup used to be keyed by the variable alone,
// so when Supabase's storage and supavisor each read DATABASE_URL and each
// renamed it to a different stored credential, the second rename was merged
// away and both services were handed the first one. Nothing said so. Keeping
// the service beside its declarations is what lets two of them be told apart.
type ServiceVars struct {
	Service string
	Vars    []schema.EnvVar
}

// Request is what to resolve.
type Request struct {
	// Services are the variables each service asks for, with the manifest's
	// own semantics: a literal value, a rename, a scope, whether it is
	// required, and whether it is a sandbox credential.
	Services []ServiceVars
	// Sandbox are variables named by sandbox-mode egress rules. A rule naming a
	// credential makes it a sandbox credential whether or not the service that
	// declares it says so, because the rule is what decides that the value
	// never leaves the boundary.
	Sandbox []string
	// EnvID appears in the substitution marker, so a value found in a log can
	// be traced to the environment that produced it.
	EnvID string
}

// Resolved is everything an environment needs, and the record of where it came
// from.
type Resolved struct {
	// Service is the environment-wide half: every variable that resolves the
	// same way for every service that declares it. Values are real except for
	// sandbox credentials, which are markers.
	Service map[string]Value
	// Scoped is the per service half, Scoped[service][name]. A variable lands
	// here when it is declared with scope: service, or when the services that
	// declare it name different places for it. A value here is absent from
	// Service, so no other service can reach it by declaring the same name.
	Scoped map[string]map[string]Value
	// Sidecar is the set of real sandbox credentials, for the egress proxy.
	Sidecar map[string]Value
	// Resolutions records which source answered for each name. Names only.
	Resolutions []Resolution
	// Missing lists what nothing could supply and that something needed.
	Missing []Missing
	// Optional lists what nothing could supply and nothing required. Reported
	// separately, because it is worth mentioning and is not a failure, and
	// mixing the two would make a warning look like an error.
	Optional []Missing
}

// Lookup returns what one service receives for one name: its own value when
// it has one, and the environment's otherwise.
func (r *Resolved) Lookup(service, name string) (Value, bool) {
	if v, ok := r.Scoped[service][name]; ok {
		return v, true
	}
	v, ok := r.Service[name]
	return v, ok
}

// Values returns every value this resolution holds, for the redactor. Both
// halves and the sidecar's, because a value that reached one service is as
// secret as one that reached all of them.
func (r *Resolved) Values() []Value {
	out := make([]Value, 0, len(r.Service)+len(r.Sidecar))
	for _, v := range r.Service {
		out = append(out, v)
	}
	for _, own := range r.Scoped {
		for _, v := range own {
			out = append(out, v)
		}
	}
	for _, v := range r.Sidecar {
		out = append(out, v)
	}
	return out
}

func (r *Resolved) put(name string, services []string, scoped bool, v Value) {
	if !scoped {
		r.Service[name] = v
		return
	}
	for _, s := range services {
		if r.Scoped[s] == nil {
			r.Scoped[s] = map[string]Value{}
		}
		r.Scoped[s][name] = v
	}
}

// origin is where one declaration's value comes from.
type origin struct {
	literal bool
	value   string // the literal, which is in the repository and is not a secret
	lookup  string // the name asked of the chain, otherwise
	scoped  bool
}

func originOf(service string, v schema.EnvVar) origin {
	if v.Value != "" {
		return origin{literal: true, value: v.Value}
	}
	// From renames: the service wants DATABASE_URL and the value is stored
	// under PROD_DATABASE_URL. Looked up under the stored name and delivered
	// under the declared one. A scope applies to the stored name, so a scoped
	// rename is still the service's own.
	return origin{lookup: v.StoredName(service), scoped: v.Scope == schema.ScopeService}
}

// SandboxMarker is what a service receives in place of a sandbox credential.
//
// Deliberately unmistakable. An application that sends this to a provider gets
// a clear rejection rather than a confusing one, and a value that turns up in a
// log is obviously a placeholder rather than something somebody has to check.
func SandboxMarker(envID, name string) string {
	return fmt.Sprintf("af_sandbox_%s_%s_not_a_real_credential", shortEnv(envID), strings.ToLower(name))
}

func shortEnv(envID string) string {
	if len(envID) <= 12 {
		return envID
	}
	return envID[:12]
}

// group is every declaration of one name that reads from one place.
type group struct {
	from     origin
	spec     schema.EnvVar
	services []string
}

// Resolve looks up every declared variable.
//
// It never stops at the first missing one. Somebody who has three variables to
// set wants to be told all three, not to run the command three times.
func Resolve(ctx context.Context, chain *Chain, req Request) (*Resolved, error) {
	out := &Resolved{
		Service: map[string]Value{}, Scoped: map[string]map[string]Value{},
		Sidecar: map[string]Value{},
	}
	searched := chain.Sources(ctx)

	sandbox := make(map[string]bool, len(req.Sandbox))
	for _, name := range req.Sandbox {
		sandbox[name] = true
	}

	// Grouped by name, then by where the value comes from. Services that ask
	// for one name from one place are one lookup and one audit record, and
	// where they disagree about how strict to be the stricter wins: required
	// beats optional, and sandbox beats not, because a variable that is a
	// credential in one service's view is a credential. Services that ask for
	// one name from DIFFERENT places are separate lookups, and each receives
	// its own answer. That second rule is the one that was missing. The places
	// were merged, the first kept, and the rest dropped without a word, so two
	// services needing two credentials under one name both got the first.
	groups := map[string][]*group{}
	var order []string
	for _, sv := range req.Services {
		for _, v := range sv.Vars {
			if v.Name == "" {
				continue
			}
			from := originOf(sv.Service, v)
			existing, seen := groups[v.Name]
			if !seen {
				order = append(order, v.Name)
			}
			var g *group
			for _, candidate := range existing {
				if candidate.from == from {
					g = candidate
					break
				}
			}
			if g == nil {
				g = &group{from: from, spec: v}
				groups[v.Name] = append(existing, g)
			} else {
				if v.IsRequired() {
					g.spec.Required = nil // nil means required
				}
				if v.Sandbox {
					g.spec.Sandbox = true
				}
			}
			if !containsString(g.services, sv.Service) {
				g.services = append(g.services, sv.Service)
			}
		}
	}
	// Every sandbox rule's credential is resolved even when no service declared
	// it, because the sidecar needs it whether or not the application does.
	for _, name := range req.Sandbox {
		if _, seen := groups[name]; !seen {
			groups[name] = []*group{{
				from: origin{lookup: name}, spec: schema.EnvVar{Name: name, Sandbox: true},
			}}
			order = append(order, name)
		}
	}
	sort.Strings(order)

	for _, name := range order {
		gs := groups[name]
		isSandbox := sandbox[name]
		for _, g := range gs {
			isSandbox = isSandbox || g.spec.Sandbox
		}
		// The proxy is one per environment and substitutes one value per
		// credential into every request to that provider, whichever service
		// sent it. A sandbox credential that would need a value per service
		// cannot be honoured, and picking one would hand a service another's
		// credential, which is the silent wrong answer this exists to refuse.
		if isSandbox && (len(gs) > 1 || gs[0].from.scoped) {
			return nil, &SandboxConflictError{
				Name: name, Services: servicesOf(gs), Scoped: gs[0].from.scoped && len(gs) == 1,
			}
		}
		perService := len(gs) > 1

		for _, g := range gs {
			scoped := perService || g.from.scoped
			who := ""
			if scoped {
				who = strings.Join(g.services, ", ")
			}

			// A literal in the manifest is not a secret and is not looked up.
			// It is in the repository, so treating it as one would put a value
			// nobody considered private into the redactor and the audit trail.
			if g.from.literal {
				out.put(name, g.services, scoped, NewFrom(g.from.value, "the manifest"))
				out.Resolutions = append(out.Resolutions, Resolution{
					Name: name, Service: who, Source: "the manifest",
				})
				continue
			}

			lookupName := g.from.lookup
			value, resolution, found, err := chain.Lookup(ctx, lookupName)
			if err != nil {
				return nil, err
			}
			if !found {
				miss := Missing{Name: lookupName, Searched: searched}
				// A sandbox credential is always required. The rule that names
				// it says requests to that provider get substituted, and there
				// is nothing to substitute.
				if isSandbox || g.spec.IsRequired() {
					out.Missing = append(out.Missing, miss)
				} else {
					out.Optional = append(out.Optional, miss)
				}
				continue
			}
			// Recorded under the declared name, since that is what the service
			// sees, with the rename noted so the trail is followable.
			if lookupName != name {
				resolution.Name = fmt.Sprintf("%s (from %s)", name, lookupName)
			}
			resolution.Service = who
			out.Resolutions = append(out.Resolutions, resolution)

			if isSandbox {
				// The one check that has to happen before the environment
				// exists. A live key handed to the sidecar would be substituted
				// into every sandbox request, which is the opposite of what
				// sandbox mode is for and would charge real cards.
				if hits := livekey.Scan(value.Reveal(), lookupName); len(hits) > 0 {
					return nil, &LiveCredentialError{Name: lookupName, Source: resolution.Source}
				}
				out.Sidecar[name] = value
				// The service gets a marker rather than the credential, and
				// rather than nothing: an application reading an unset variable
				// usually crashes on startup with a message about
				// configuration, which looks like a bug in the tool.
				out.put(name, g.services, scoped, New(SandboxMarker(req.EnvID, name)))
				continue
			}
			out.put(name, g.services, scoped, value)
		}
	}

	SortResolutions(out.Resolutions)
	sort.Slice(out.Missing, func(i, j int) bool { return out.Missing[i].Name < out.Missing[j].Name })
	sort.Slice(out.Optional, func(i, j int) bool { return out.Optional[i].Name < out.Optional[j].Name })
	return out, nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func servicesOf(gs []*group) []string {
	var out []string
	for _, g := range gs {
		for _, s := range g.services {
			if !containsString(out, s) {
				out = append(out, s)
			}
		}
	}
	sort.Strings(out)
	return out
}

// SandboxConflictError reports a sandbox credential that would need more than
// one value. It names the variable and the services and never a value.
type SandboxConflictError struct {
	Name     string
	Services []string
	// Scoped is true when one service asked for its own value, rather than two
	// services asking for different ones.
	Scoped bool
}

func (e *SandboxConflictError) Error() string {
	who := joinServices(e.Services)
	if e.Scoped {
		return fmt.Sprintf(
			"%s is a sandbox credential and %s declares it with scope: service. The proxy holds one "+
				"value per credential for the whole environment and substitutes it whichever service "+
				"sends the request, so a value that belongs to one service cannot be kept to it.",
			e.Name, who)
	}
	return fmt.Sprintf(
		"%s is a sandbox credential and %s read it from different places. The proxy holds one "+
			"value per credential for the whole environment, so it cannot give each service its own, "+
			"and choosing one would hand a service another's credential.",
		e.Name, who)
}

// joinServices renders a list as prose: "storage", "storage and supavisor",
// "a, b and c".
func joinServices(s []string) string {
	switch len(s) {
	case 0:
		return "no service"
	case 1:
		return s[0]
	default:
		return strings.Join(s[:len(s)-1], ", ") + " and " + s[len(s)-1]
	}
}

// LiveCredentialError reports a real credential in a sandbox slot.
type LiveCredentialError struct {
	Name   string
	Source string
}

func (e *LiveCredentialError) Error() string {
	return fmt.Sprintf(
		"%s came from %s and looks like a live credential. It is named by a sandbox rule, "+
			"so it would be substituted into every request to that provider. Use the provider's "+
			"test credential instead.",
		e.Name, e.Source)
}

// DeclaredFor collects every variable each of a manifest's services asks for,
// keeping the service it came from.
func DeclaredFor(m *schema.Manifest) []ServiceVars {
	if m == nil {
		return nil
	}
	out := make([]ServiceVars, 0, len(m.Services))
	for _, svc := range m.Services {
		if len(svc.Env) == 0 {
			continue
		}
		out = append(out, ServiceVars{Service: svc.Name, Vars: svc.Env})
	}
	return out
}

// SandboxNames collects every variable a sandbox rule names.
func SandboxNames(m *schema.Manifest) []string {
	if m == nil || m.Egress == nil {
		return nil
	}
	var names []string
	for _, rule := range m.Egress.Rules {
		if rule.Mode == schema.ModeSandbox && rule.Credential != "" {
			names = append(names, rule.Credential)
		}
	}
	return unique(names)
}

// AuditFields renders the resolutions for an audit event.
//
// Names and sources, never values. This is what goes into the event log, into
// af explain, and into a support bundle, so it has to be safe to show to
// somebody who should not see the secrets.
func AuditFields(rs []Resolution) []map[string]string {
	out := make([]map[string]string, 0, len(rs))
	for _, r := range rs {
		row := map[string]string{
			"name": r.Name, "source": r.Source, "fingerprint": r.Fingerprint,
		}
		// Only when there is one, so the record of an environment that uses
		// no per service value is byte for byte what it was.
		if r.Service != "" {
			row["service"] = r.Service
		}
		out = append(out, row)
	}
	return out
}

func unique(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
