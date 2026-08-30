package env

import (
	"context"
	"io"

	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// ServiceFieldForTest exposes serviceField to the package's external tests.
//
// The function decides which service a runtime progress line belongs to, and
// getting it wrong puts a row in the dashboard named after a word that is not
// a service. That is worth a test, and the function has no reason to be part
// of the package's real surface.
func ServiceFieldForTest(line string, names map[string]bool) []events.Field {
	return serviceField(line, names)
}

// NonEmptyForTest exposes nonEmpty to the package's external tests.
func NonEmptyForTest(kv ...string) []events.Field { return nonEmpty(kv...) }

// UntarForTest exposes untar to the package's external tests.
//
// It writes a tar into a directory and is the one place in the oracle's path
// that reads bytes from outside this process. A path that escapes the
// directory would write into the user's repository, and the escape is not
// hypothetical: git archive can carry a symlink, and a crafted commit can carry
// a name with parent segments in it. That is worth a test and the function has
// no reason to be part of the package's surface.
func UntarForTest(dir string, r io.Reader) error { return untar(dir, r) }

// ResolveBaselineForTest exposes resolveBaseline to the package's external
// tests, so the git resolution can be exercised against a real repository.
func ResolveBaselineForTest(root string, source schema.BaselineSource, ref string) (string, string, error) {
	return resolveBaseline(root, source, ref)
}

// BaselineTreeForTest exposes baselineTree to the package's external tests, so
// that the archive of a manifest in a subdirectory can be checked against a
// real repository rather than reasoned about.
func (o *Orchestrator) BaselineTreeForTest(ctx context.Context, rev string) (string, func(), error) {
	return o.baselineTree(ctx, rev)
}
