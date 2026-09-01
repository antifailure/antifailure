package manifest_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// explain renders the manifest the way af explain does.
func explain(m *schema.Manifest) string { return manifest.Explain(m, 0) }

// The github block had no validator at all, so none of its three enumerations
// was enforced by the engine. The JSON Schema has had them the whole time, and
// the reference page is generated from it, so the page told a reader the
// values and nothing held them to it: two documentation pages in this
// repository shipped `teardown_on: [closed, merged]` and loaded fine.
//
// fork_policy is the one worth a test of its own. normalize only fills an
// EMPTY policy, so a misspelling survives as itself, and everything downstream
// reads an unrecognised policy as label. Safe, and silent: `nevr` is a
// maintainer who believes forks are refused and whose forks run behind one
// label instead.
func TestGitHub_RefusesAValueTheSchemaDoesNotList(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ block, want string }{
		"a misspelt fork policy": {
			"  fork_policy: nevr", `github.fork_policy: The value "nevr" is not one of never, label or always`},
		"a mode nobody has": {
			"  mode: sideways", `github.mode: The value "sideways" is not one of actions, app or off`},
		"the teardown values two of our own pages used": {
			"  teardown_on: [closed, merged]", `github.teardown_on[0]: The value "closed" is not one of close, merge or ttl`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parse(t, "version: 1\nname: shop\nservices:\n  - name: web\n    port: 3000\ngithub:\n"+tc.block+"\n")
			require.Error(t, err, "an unrecognised value in a security control has to be an error, not a shrug")
			require.Contains(t, strings.Join(strings.Fields(err.Error()), " "), tc.want)
		})
	}
}

// And the values that are listed still load, so the test above is not passing
// because the block is refused outright.
func TestGitHub_AcceptsEveryValueTheSchemaLists(t *testing.T) {
	t.Parallel()
	for _, block := range []string{
		"  fork_policy: never", "  fork_policy: label", "  fork_policy: always",
		"  mode: actions", "  mode: app", "  mode: off",
		"  teardown_on: [close]", "  teardown_on: [close, merge, ttl]",
		"  comment: false",
	} {
		t.Run(strings.TrimSpace(block), func(t *testing.T) {
			_, err := parse(t, "version: 1\nname: shop\nservices:\n  - name: web\n    port: 3000\ngithub:\n"+block+"\n")
			require.NoError(t, err)
		})
	}
}

// af explain used to print the configured teardown list as though it were in
// force. Nothing reads github.teardown_on, so the line was a setting that was
// not one, and the honest version says what actually happens while still
// showing what was written.
func TestExplain_SaysTeardownIsNotASetting(t *testing.T) {
	t.Parallel()
	m := mustParse(t, "version: 1\nname: shop\nservices:\n  - name: web\n    port: 3000\n"+
		"github:\n  teardown_on: [close]\n")
	out := strings.Join(strings.Fields(explain(m)), " ")
	require.Contains(t, out, "always, whatever the outcome")
	require.Contains(t, out, "github.teardown_on (close) is accepted and not read")
	require.Contains(t, out, "runtime.max_ttl")
}
