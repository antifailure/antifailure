package manifest_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The engine's idea of which surfaces this build can drive, checked against
// the runner's registry, which is the thing that actually decides.
//
// THE DEFECT THIS EXISTS FOR, which is the one this lane was created to fix,
// recreated one layer up. A surface is reachable from a manifest because the
// ENGINE accepts it, and it is driveable because the RUNNER has a driver for
// it. Those are two files in two languages, and nothing connected them. A lane
// that finishes a driver and flips `available: true` in the registry, and
// forgets `schema.DriveableSurfaces`, ships a surface nothing can ask for: the
// driver works, every test of it passes, and the engine refuses every manifest
// that names it. A lane that does the opposite ships the worse half: the
// manifest is accepted, the job reaches the runner, and the run is refused
// there with a message about an unbuilt driver, after an environment has been
// built and paid for.
//
// So the two lists are compared rather than trusted. This is the same
// mechanism schema_drift_test.go uses for the schema and the Go mirror, and
// for the same reason: two readings that agree today are two readings that can
// disagree tomorrow.

// The registry is a Go-free file, so it is read as text. The shape it is read
// from is small and fixed: a key, `surface:` naming the same key, and an
// `available:` boolean. A registry that stops looking like this fails the
// parse below rather than being half read, because a parser that finds nothing
// and says nothing is how a gate comes to measure an empty set.
var registryEntry = regexp.MustCompile(
	`(?m)^  (\w+): \{\n\s*surface: '(\w+)',\n\s*available: (true|false),`)

func runnerRegistry(t *testing.T) map[string]bool {
	t.Helper()
	path := filepath.Join("..", "..", "..", "runner", "src", "drivers", "driver.ts")
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "the runner's driver registry is missing, so this checked nothing")

	matches := registryEntry.FindAllStringSubmatch(string(raw), -1)
	require.NotEmpty(t, matches,
		"no registry entry parsed out of %s. The shape this reads has changed, and a "+
			"gate that parses nothing reports agreement about an empty set, so fix the "+
			"pattern rather than deleting this test.", path)

	out := map[string]bool{}
	for _, m := range matches {
		require.Equalf(t, m[1], m[2],
			"the registry key %q and its surface field %q disagree", m[1], m[2])
		out[m[1]] = m[3] == "true"
	}
	return out
}

// Every surface the manifest may name has a row in the runner's registry, and
// every row is a surface the manifest may name. Neither side may grow alone:
// a surface in the schema and not the registry is one `driverFor` answers
// undefined for, and a surface in the registry and not the schema is one no
// manifest can reach.
func TestEverySurfaceTheManifestKnowsHasADriverRow(t *testing.T) {
	t.Parallel()
	registry := runnerRegistry(t)

	inRegistry := make([]string, 0, len(registry))
	for name := range registry {
		inRegistry = append(inRegistry, name)
	}
	sort.Strings(inRegistry)

	known := schema.SurfaceNames(schema.Surfaces)
	sort.Strings(known)

	require.Equal(t, known, inRegistry,
		"schema.Surfaces and the runner's driver registry name different sets. "+
			"A surface in one and not the other is either unreachable from a manifest "+
			"or reachable and undriveable, and both look like a working feature.")
}

// And the narrower claim, which is the one that decides what a person is
// allowed to write: the surfaces the engine says this build can drive are
// exactly the ones the registry marks available.
func TestTheDriveableSurfacesMatchTheRunnersRegistry(t *testing.T) {
	t.Parallel()
	registry := runnerRegistry(t)

	available := []string{}
	for name, ok := range registry {
		if ok {
			available = append(available, name)
		}
	}
	sort.Strings(available)

	driveable := schema.SurfaceNames(schema.DriveableSurfaces)
	sort.Strings(driveable)

	require.Equal(t, available, driveable,
		"schema.DriveableSurfaces and the runner's `available` flags disagree. "+
			"Finishing a driver means flipping the registry entry AND adding the surface "+
			"to DriveableSurfaces; doing one of the two ships a surface that is either "+
			"refused by the engine although it works, or accepted by the engine and "+
			"refused by the runner after an environment has been paid for.")
}

// The reading itself is checked, because the two tests above compare it
// against DriveableSurfaces and would BOTH pass against a parse that read
// every flag as false, or as true, or as the same value for everything.
//
// What is asserted here is deliberately the part that cannot go stale. An
// earlier version named desktop as the unbuilt one, and the desktop driver
// landed that afternoon and turned a control into a false alarm about a lane
// that had done nothing wrong. So: web is driveable, because a build with no
// browser driver is not a build; at least one surface is NOT driveable, which
// is what proves the parse distinguishes the flags at all rather than
// answering one value for everything; and android has a row, because a surface
// the manifest can name and the registry has never heard of is refused as a
// typo rather than as a surface waiting for its driver.
func TestTheRegistryReadingCanSayNo(t *testing.T) {
	t.Parallel()
	registry := runnerRegistry(t)
	require.Truef(t, registry["web"], "the registry says web is not driveable, which cannot be right")

	var yes, no []string
	for name, ok := range registry {
		if ok {
			yes = append(yes, name)
			continue
		}
		no = append(no, name)
	}
	require.NotEmptyf(t, yes, "the parse read every surface as undriveable, so it is reading nothing")
	require.NotEmptyf(t, no,
		"the parse read every surface as driveable. Either every driver is finished, in which "+
			"case delete this assertion and say so, or the parse is answering one value for "+
			"everything and the two comparisons above are measuring nothing.")

	require.Contains(t, registry, "android",
		"android is missing from the registry, so a manifest naming it is refused as a typo "+
			"rather than as a surface this build cannot drive yet")
}
