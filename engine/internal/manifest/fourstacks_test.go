package manifest_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/manifest"
)

// The four stacks are a standing question put to the validator.
//
// docs/plan/four-stacks holds three manifests converted mechanically from
// PostHog's, ClickHouse's and Supabase's own published compose files, and one
// that is a shape rather than a twin. They are evidence rather than examples,
// and the reason they are worth a test is that they are the only manifests in
// this repository that somebody else's application asked for. Everything under
// examples/ was written here and validates because it was written to.
//
// A schema change that refuses one of these is a schema change that refuses a
// real published stack. That may still be the right change, and this test does
// not argue against it: it makes the cost visible in the same commit rather
// than in a customer's terminal.
//
// The counts are asserted because the point of the row was how many services
// each published file declares. A conversion that silently lost a service
// would still parse.
func TestTheFourStacksStillParse(t *testing.T) {
	t.Parallel()

	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)

	for _, tc := range []struct {
		dir      string
		services int
		why      string
	}{
		{"clickhouse-1s-1k", 2,
			"ClickHouse/examples, docker-compose-recipes/recipes/ch-1S_1K"},
		{"supabase-selfhost", 11,
			"supabase/supabase, docker/docker-compose.yml"},
		{"posthog-hobby", 37,
			"PostHog/posthog, docker-compose.hobby.yml over docker-compose.base.yml"},
		{"goliath-shape", 4,
			"a shape proved instead of a twin, because nothing is published"},
	} {
		t.Run(tc.dir, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(root, "docs", "plan", "four-stacks", tc.dir, "antifailure.yaml")
			if _, statErr := os.Stat(path); statErr != nil {
				t.Fatalf("%s is missing: %v", path, statErr)
			}
			m, loadErr := manifest.Load(path)
			require.NoError(t, loadErr, "%s (%s) no longer validates", tc.dir, tc.why)
			require.Len(t, m.Services, tc.services,
				"%s declares %d services in its published file", tc.dir, tc.services)
		})
	}
}

// TestTheGoliathShapeNamesEveryThirdPartyHost is the assertion the shape exists
// to carry.
//
// A rule that covers a domain rather than naming a host is refused by the
// sidecar rather than answered, so a delivery path written as *.zapier.com gets
// a 403 at run time while af net explain reports it as CAPTURE. The manifest
// beside this test names each host, and this is what keeps it that way.
func TestTheGoliathShapeNamesEveryThirdPartyHost(t *testing.T) {
	t.Parallel()

	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)
	m, err := manifest.Load(filepath.Join(root,
		"docs", "plan", "four-stacks", "goliath-shape", "antifailure.yaml"))
	require.NoError(t, err)
	require.NotNil(t, m.Egress)
	require.NotEmpty(t, m.Egress.Rules, "the shape is about the egress block")

	for _, r := range m.Egress.Rules {
		require.NotContains(t, r.Host, "*",
			"the rule for %q covers a domain rather than naming a host, which is the "+
				"case the sidecar refuses and af net explain cannot distinguish", r.Host)
	}
}
