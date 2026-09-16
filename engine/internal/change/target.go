package change

import (
	"path"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// TargetKind is the kind of unit a security family and a browser personality
// run against. A target is a LOCATION the diff touched, never a value: a route,
// a screen path, a table. It is the difference between running a check across
// the whole application and running it at exactly the endpoint that changed,
// which is what keeps the security layer as fast as the rest of the plan.
type TargetKind string

const (
	// TargetEndpoint is an API route: something a client calls and reads a
	// response from.
	TargetEndpoint TargetKind = "endpoint"
	// TargetScreen is a UI route a persona drives in a browser.
	TargetScreen TargetKind = "screen"
	// TargetAuthBoundary is a middleware, a guard, a session or token seam, or
	// a policy definition: the edge that decides who may do what.
	TargetAuthBoundary TargetKind = "auth"
	// TargetDatabase is a schema or row level security object: a migration, a
	// policy, a grant.
	TargetDatabase TargetKind = "database"
)

// Target is one routed unit: the location a family and a persona exercise,
// with the facts that produced it and the personalities to drive it.
//
// It is derived purely from the profile's facts and the manifest, so the same
// diff and manifest produce the same targets forever, which is what lets a
// pull request comment be reviewed and reproduced.
type Target struct {
	// Kind is what sort of unit this is.
	Kind TargetKind `json:"kind"`
	// Ref locates it: a route path, a screen path, a table or a file. It is a
	// LOCATION and never a value, a body or a row, so that a target can cross
	// the control plane's data boundary unchanged.
	Ref string `json:"ref"`
	// Because names the facts that produced this target, each carrying the
	// path it rests on, in the profile's own "path: evidence" form. A target
	// whose reasoning cannot be audited is astrology, the same rule the rest of
	// this package holds itself to.
	Because []string `json:"because"`
	// Personas are the browser personalities to drive this target, by name,
	// matching the persona names the manifest declares. An EMPTY slice means
	// the no-session probe: an unauthenticated reach, which is a state no
	// persona can express (an empty workflow persona is silently filled with
	// the first declared one), so it is defined here in the router contract
	// rather than left to the normaliser. The adversarial relationship "another
	// tenant" is not a persona but an edge between two, so it lives on the
	// family that reads this target, not on the persona list.
	Personas []string `json:"personas,omitempty"`
	// Families are the reserved security checks routed to this target. It is
	// filled by the security router from the family registry at the point the
	// findings are collected, NOT here: routing is derived from which families
	// registered for this target's surface, so it stays a fact about the
	// registry rather than a second hand-written table in this package. With no
	// family registered it is empty, which is why a spine with no families adds
	// no security check to any plan.
	Families []Check `json:"families,omitempty"`
}

// Targets derives the routed units from the profile's facts and the manifest.
//
// Pure: no I/O, no clock, deterministic order. It reads the facts the analyser
// already produced and groups them into the units a security family and a
// persona run against. It fills Kind, Ref, Because and Personas; Families is
// left for the security router to fill from the registry, so that a check is
// routed to a target only once its family is registered and this package never
// grows a second routing table to drift from the first.
//
// When runEverything holds, the fail safe is the same as the plan's: every
// target still gets every declared persona plus the no-session probe, because
// an incomplete classification narrows nothing.
func (p *Profile) Targets() []Target {
	personas := declaredPersonas(p.manifest())

	// Group facts by the target they imply, keyed by kind and ref so two facts
	// about one route become one target carrying both reasons.
	index := map[string]*Target{}
	var order []string
	add := func(kind TargetKind, ref, because string) {
		key := string(kind) + "\x00" + ref
		t, ok := index[key]
		if !ok {
			t = &Target{Kind: kind, Ref: ref}
			index[key] = t
			order = append(order, key)
		}
		for _, existing := range t.Because {
			if existing == because {
				return
			}
		}
		t.Because = append(t.Because, because)
	}

	for _, f := range p.Facts {
		kind, ref, ok := targetOf(f)
		if !ok {
			continue
		}
		add(kind, ref, f.Path+": "+f.Evidence)
	}

	out := make([]Target, 0, len(order))
	for _, key := range order {
		t := index[key]
		// A database target is exercised by SQL and row level security probes
		// rather than by a browser, so it carries no personas. Everything a
		// persona can drive gets the declared set; the no-session probe is the
		// empty slice, which the families read as an unauthenticated reach.
		if t.Kind != TargetDatabase {
			t.Personas = append([]string(nil), personas...)
		}
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Ref < out[j].Ref
	})
	return out
}

// manifest is the manifest this profile was analysed against, when one was
// carried. It is nil for a profile analysed without one, in which case there
// are no declared personas and every persona-driven target gets the no-session
// probe alone.
func (p *Profile) manifest() *schema.Manifest { return p.mani }

// targetOf maps one fact to the unit it implies, or reports that the fact does
// not name a driven unit. Config, dependency, build, egress and the rest are
// real facts about the change and route their own built-in checks; they are
// not units a persona or a database probe drives, so they produce no target.
func targetOf(f Fact) (TargetKind, string, bool) {
	switch f.Surface {
	case SurfaceAuth:
		return TargetAuthBoundary, f.Path, true
	case SurfaceSchema:
		return TargetDatabase, f.Path, true
	case SurfaceCode, SurfaceService, SurfaceAsset:
		if isEndpointPath(f.Path) {
			return TargetEndpoint, f.Path, true
		}
		return TargetScreen, f.Path, true
	}
	return "", "", false
}

// isEndpointPath reports whether a path reads as an API route rather than a
// screen. It is a convention, like every other rule in this package, and a
// project that breaks it is why a target names its file rather than pretending
// to know the served route: the ref is a location a reader can open, not a
// guessed URL.
func isEndpointPath(p string) bool {
	if segment(p, "api", "routes", "route", "controllers", "controller",
		"handlers", "handler", "endpoints", "resolvers", "graphql", "trpc") {
		return true
	}
	base := strings.ToLower(path.Base(p))
	return base == "route.ts" || base == "route.js" || base == "route.go" ||
		strings.HasSuffix(base, "_controller.rb") ||
		strings.HasSuffix(base, ".controller.ts")
}

// declaredPersonas is the sorted, de-duplicated set of persona names the
// manifest declares, which is who is available to drive a target. A family
// narrows this adversarially at run time; the router hands it the full set.
func declaredPersonas(m *schema.Manifest) []string {
	if m == nil {
		return nil
	}
	seen := map[string]bool{}
	var names []string
	for _, persona := range m.Personas {
		if persona.Name == "" || seen[persona.Name] {
			continue
		}
		seen[persona.Name] = true
		names = append(names, persona.Name)
	}
	sort.Strings(names)
	return names
}
