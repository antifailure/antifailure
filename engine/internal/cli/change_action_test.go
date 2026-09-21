package cli_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/antifailure/antifailure/engine/internal/change"
)

// The published action's outputs and the checks the engine has are one list.
//
// writeChangeOutputs walks p.Plan, so it writes a key per check automatically
// and can never fall behind. action.yml cannot: a composite action has to name
// every output it re-exports, by hand, in a file no compiler reads. So adding a
// check to Checks() would add a GITHUB_OUTPUT line that reaches the step and
// stops at the action's boundary, and a caller of the action would read an
// empty string for it. An empty string is not false to a workflow expression
// and is not false to somebody reading the run at eleven at night.
//
// A second opinion about the same fact is always the bug, and this is the one
// place in this feature where the second opinion could not be deleted. So it is
// compared instead.
func TestChange_TheActionExportsAnOutputForEveryCheck(t *testing.T) {
	t.Parallel()

	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)
	body, err := os.ReadFile(filepath.Join(root, "action.yml"))
	require.NoError(t, err, "the published action could not be read, so this test cannot say "+
		"whether it agrees with the engine")

	var action struct {
		Outputs map[string]struct {
			Description string `yaml:"description"`
			Value       string `yaml:"value"`
		} `yaml:"outputs"`
	}
	require.NoError(t, yaml.Unmarshal(body, &action))
	require.NotEmpty(t, action.Outputs,
		"no outputs were read out of action.yml, and a test that quietly found none would "+
			"report agreement having compared nothing")

	for _, c := range change.Checks() {
		out, ok := action.Outputs[string(c)]
		if !assert.Truef(t, ok, "af change writes the output %q on every run and action.yml "+
			"re-exports %v, so a caller of the action reads an empty string for it",
			c, sortedKeys(action.Outputs)) {
			continue
		}
		assert.Equalf(t, "${{ steps.change.outputs."+string(c)+" }}", out.Value,
			"the action's %q output does not carry the value af change writes for it", c)
		assert.NotEmptyf(t, out.Description, "the action's %q output has no description", c)
	}

	// The converse. An output named for a check the engine does not have would
	// be a promise of a value nothing ever writes, which renders as an empty
	// string rather than as an error.
	known := map[string]bool{"command": true, "selected": true, "handled": true}
	for _, c := range change.Checks() {
		known[string(c)] = true
	}
	for name := range action.Outputs {
		assert.Truef(t, known[name],
			"action.yml exports %q, which is neither a check the engine has nor one of the "+
				"action's own outputs, so nothing ever writes it", name)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
