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

// Request is what to resolve.
type Request struct {
	// Declared are the variables the manifest's services ask for, with the
	// manifest's own semantics: a literal value, a rename, whether it is
	// required, and whether it is a sandbox credential.
	Declared []schema.EnvVar
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
	// Service is the environment handed to every service. Values are real
	// except for sandbox credentials, which are markers.
	Service map[string]Value
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

// Resolve looks up every declared variable.
//
// It never stops at the first missing one. Somebody who has three variables to
// set wants to be told all three, not to run the command three times.
func Resolve(ctx context.Context, chain *Chain, req Request) (*Resolved, error) {
	out := &Resolved{Service: map[string]Value{}, Sidecar: map[string]Value{}}
	searched := chain.Sources(ctx)

	sandbox := make(map[string]bool, len(req.Sandbox))
	for _, name := range req.Sandbox {
		sandbox[name] = true
	}

	// One entry per name, so two services declaring the same variable produce
	// one lookup and one audit record. Where they disagree the stricter wins:
	// required beats optional, and sandbox beats not, because a variable that
	// is a credential in one service's view is a credential.
	declared := map[string]schema.EnvVar{}
	var order []string
	for _, v := range req.Declared {
		if v.Name == "" {
			continue
		}
		existing, seen := declared[v.Name]
		if !seen {
			declared[v.Name] = v
			order = append(order, v.Name)
			continue
		}
		merged := existing
		if v.IsRequired() {
			merged.Required = nil // nil means required
		}
		if v.Sandbox {
			merged.Sandbox = true
		}
		if merged.Value == "" {
			merged.Value = v.Value
		}
		if merged.From == "" {
			merged.From = v.From
		}
		declared[v.Name] = merged
	}
	// Every sandbox rule's credential is resolved even when no service declared
	// it, because the sidecar needs it whether or not the application does.
	for _, name := range req.Sandbox {
		if _, seen := declared[name]; !seen {
			declared[name] = schema.EnvVar{Name: name, Sandbox: true}
			order = append(order, name)
		}
	}
	sort.Strings(order)

	for _, name := range order {
		spec := declared[name]
		isSandbox := spec.Sandbox || sandbox[name]

		// A literal in the manifest is not a secret and is not looked up. It is
		// in the repository, so treating it as one would put a value nobody
		// considered private into the redactor and the audit trail.
		if spec.Value != "" {
			out.Service[name] = NewFrom(spec.Value, "the manifest")
			out.Resolutions = append(out.Resolutions, Resolution{
				Name: name, Source: "the manifest",
			})
			continue
		}

		// From renames: the service wants DATABASE_URL and the value is stored
		// under PROD_DATABASE_URL. Looked up under the stored name and
		// delivered under the declared one.
		lookupName := name
		if spec.From != "" {
			lookupName = spec.From
		}

		value, resolution, found, err := chain.Lookup(ctx, lookupName)
		if err != nil {
			return nil, err
		}
		if !found {
			miss := Missing{Name: lookupName, Searched: searched}
			// A sandbox credential is always required. The rule that names it
			// says requests to that provider get substituted, and there is
			// nothing to substitute.
			if isSandbox || spec.IsRequired() {
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
		out.Resolutions = append(out.Resolutions, resolution)

		if isSandbox {
			// The one check that has to happen before the environment exists. A
			// live key handed to the sidecar would be substituted into every
			// sandbox request, which is the opposite of what sandbox mode is
			// for and would charge real cards.
			if hits := livekey.Scan(value.Reveal(), lookupName); len(hits) > 0 {
				return nil, &LiveCredentialError{Name: lookupName, Source: resolution.Source}
			}
			out.Sidecar[name] = value
			// The service gets a marker rather than the credential, and rather
			// than nothing: an application reading an unset variable usually
			// crashes on startup with a message about configuration, which
			// looks like a bug in the tool.
			out.Service[name] = New(SandboxMarker(req.EnvID, name))
			continue
		}
		out.Service[name] = value
	}

	SortResolutions(out.Resolutions)
	sort.Slice(out.Missing, func(i, j int) bool { return out.Missing[i].Name < out.Missing[j].Name })
	sort.Slice(out.Optional, func(i, j int) bool { return out.Optional[i].Name < out.Optional[j].Name })
	return out, nil
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

// DeclaredVars collects every variable a manifest's services ask for.
func DeclaredVars(m *schema.Manifest) []schema.EnvVar {
	if m == nil {
		return nil
	}
	var out []schema.EnvVar
	for _, svc := range m.Services {
		out = append(out, svc.Env...)
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
		out = append(out, map[string]string{
			"name": r.Name, "source": r.Source, "fingerprint": r.Fingerprint,
		})
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
