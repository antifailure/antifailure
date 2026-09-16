package security

import "github.com/antifailure/antifailure/engine/internal/change"

// Selection is one family the change selected and the targets routed to it.
//
// The router turns a change profile and the registry into a slice of these, and
// the collector runs each family's Probe against its Targets. A family with an
// empty Targets was still selected, because a family that reads a surface which
// names no driven unit (a dependency change, a config header) runs against the
// run's captured artifacts rather than a routed endpoint. Selected-with-no-target
// and not-selected are different facts, which is why the empty slice is kept
// rather than the family dropped.
type Selection struct {
	// Family is the selected family.
	Family Family
	// Targets are the routed units this family exercises, in the profile's
	// deterministic order. Each carries the checks routed to it in Families.
	Targets []change.Target
}

// Select selects the families a change touched and gives each the targets routed
// to it, filling every target's Families from the registry.
//
// Pure and deterministic: the same profile and registry produce the same
// selections forever, so a pull request comment listing what ran can be
// reviewed and reproduced. A family is selected when its declared surfaces
// intersect the surfaces the diff actually touched, so a docs-only diff routes
// no security family and stays as fast as it is today. When the profile could
// not narrow the plan (an unknown path, a truncated or empty diff), the fail
// safe holds: every registered family is selected, the same way the built-in
// plan selects every check.
//
// Routing is derived here from the registry rather than from a second table in
// the change package, so a family's surfaces are the one place its routing is
// decided and the two cannot drift apart.
func Select(reg *Registry, p *change.Profile) []Selection {
	if reg == nil || p == nil {
		return nil
	}
	families := reg.Families()
	if len(families) == 0 {
		return nil
	}

	targets := p.Targets()
	// Fill each target's Families from the registry: the checks of every family
	// routed to that target's kind. Done once, over the shared targets, before
	// they are sliced per family, so a target names every check that reaches it
	// and not merely the one family currently being routed.
	for i := range targets {
		targets[i].Families = checksForTarget(reg, targets[i].Kind)
	}

	everything := p.Everything
	touched := touchedSurfaces(p)

	var out []Selection
	for _, fam := range families {
		if !everything && !intersects(fam.Surfaces(), touched) {
			continue
		}
		out = append(out, Selection{Family: fam, Targets: targetsFor(fam, targets)})
	}
	return out
}

// touchedSurfaces is the set of surfaces the diff assigned, which is what the
// change actually touched. It reads the facts the analyser already produced, so
// a family's selection rests on the same classification the built-in plan does.
func touchedSurfaces(p *change.Profile) map[change.Surface]bool {
	touched := map[change.Surface]bool{}
	for _, f := range p.Facts {
		touched[f.Surface] = true
	}
	return touched
}

// targetsFor returns the targets routed to a family: those whose kind maps to
// one of the family's surfaces. A family that consumes only a surface which
// names no driven unit gets an empty slice, which the collector hands it as is.
func targetsFor(fam Family, targets []change.Target) []change.Target {
	surfaces := fam.Surfaces()
	var out []change.Target
	for _, t := range targets {
		if sharesSurface(surfaces, surfacesForKind(t.Kind)) {
			out = append(out, t)
		}
	}
	return out
}

// checksForTarget collects the checks of every registered family routed to a
// target kind, in registration order, de-duplicated. It is what fills a
// target's Families, so the target names the security checks that reach it.
func checksForTarget(reg *Registry, kind change.TargetKind) []change.Check {
	surfaces := surfacesForKind(kind)
	seen := map[change.Check]bool{}
	var out []change.Check
	for _, fam := range reg.Families() {
		if !sharesSurface(fam.Surfaces(), surfaces) {
			continue
		}
		for _, c := range fam.Checks() {
			if seen[c] {
				continue
			}
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// surfacesForKind is the inverse of the change router's targetOf: the surfaces
// that can produce a target of this kind. It is how a family, which declares
// surfaces, is matched to a target, which carries a kind. Kept beside Route so
// the mapping lives in one place and next to the family selection it serves.
func surfacesForKind(kind change.TargetKind) []change.Surface {
	switch kind {
	case change.TargetAuthBoundary:
		return []change.Surface{change.SurfaceAuth}
	case change.TargetDatabase:
		return []change.Surface{change.SurfaceSchema}
	case change.TargetEndpoint, change.TargetScreen:
		return []change.Surface{change.SurfaceCode, change.SurfaceService, change.SurfaceAsset}
	default:
		return nil
	}
}

// BaselineReader is implemented by a family whose Probe diffs its candidate run
// against a base twin: side_effect counts the outbound effects each side made
// and reports the ones this change added. Building a base twin is the run's most
// expensive artifact, a second environment brought up from the base revision, so
// the collector builds one ONLY when a selected family reads it. A family that
// ignores the baseline does not implement this, its Input.Baseline stays
// ok=false, and Detect leaves the comparison unmade rather than diffing against a
// base of zero, which every family already handles.
//
// A marker rather than a method that returns a value: its presence is the whole
// signal, so there is nothing for a family to get wrong by returning the wrong
// bool. side_effect implements it; authz's increment will when the runner emits
// the per-persona base observations it needs.
type BaselineReader interface {
	// ReadsBaseline marks a family as a base-twin consumer. It takes and returns
	// nothing; the collector checks for it with a type assertion.
	ReadsBaseline()
}

// SelectionsWantBaseline reports whether any selected family reads a base twin,
// so the collector knows whether a second environment is worth building for this
// change. A change that routes no baseline reader, a docs edit, a config change,
// a code change that touched no baseline-reading surface, never pays for one.
func SelectionsWantBaseline(sels []Selection) bool {
	for _, s := range sels {
		if _, ok := s.Family.(BaselineReader); ok {
			return true
		}
	}
	return false
}

// intersects reports whether a surface slice shares a member with a surface
// set. It is how a family's surfaces are tested against the diff's touched set.
func intersects(a []change.Surface, b map[change.Surface]bool) bool {
	for _, s := range a {
		if b[s] {
			return true
		}
	}
	return false
}

// sharesSurface reports whether two surface slices share a member. Both are
// tiny (a family declares a handful of surfaces, a kind maps to at most three),
// so a nested loop is clearer than a map and faster for the sizes involved.
func sharesSurface(a, b []change.Surface) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}
