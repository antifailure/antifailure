package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A field the sidecar spends a line recording has to reach a surface.
//
// This is an instrument rather than a review, because the failure it catches
// is invisible to both. The sidecar writes a decision, the engine decodes it
// into local.Decision, and af net log renders it, and every one of those three
// compiles perfectly when a field is missing from the middle or the end of the
// chain. json.Unmarshal drops what it has nowhere to put, silently and by
// design. So the fact simply never leaves the proxy, and the only evidence is
// a column that is always empty, which reads as "this did not happen".
//
// It has happened repeatedly and the struct comments in local.Decision are the
// record of it: waited_ms decoded into nothing, so a request the policy held
// for half a second looked exactly like a slow application; synthesized never
// arrived, so a response a model invented looked like an ordinary allowed one
// and the promise that it comes back unverified was made in five places and
// could not be kept by any of them; pack and fixture went the same way against
// the sidecar's own comment that a mock which cannot name its fixture is a
// mock nobody can debug.
//
// Every one of those was found by somebody reading three files side by side
// and noticing. This is that comparison, run by the test suite, so the next
// one is found by the next test run instead.

// tagsOf returns the json tag names of a struct declared in a file, in order.
func tagsOf(t *testing.T, path, name string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	require.NoError(t, err, "parsing %s", path)

	var out []string
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != name {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return true
		}
		found = true
		for _, f := range st.Fields.List {
			if f.Tag == nil {
				continue
			}
			tag := reflect.StructTag(strings.Trim(f.Tag.Value, "`")).Get("json")
			tag = strings.Split(tag, ",")[0]
			// "-" is a field deliberately not carried over the wire, which is
			// a decision already written down in the type.
			if tag == "" || tag == "-" {
				continue
			}
			out = append(out, tag)
		}
		return false
	})
	require.True(t, found, "no struct named %s in %s", name, path)
	return out
}

func has(set []string, want string) bool {
	for _, s := range set {
		if s == want {
			return true
		}
	}
	return false
}

// Fields the sidecar writes that are not part of a request's decision, with
// the reason each one stops where it does. A field that is neither carried
// forward nor listed here fails, which is the point: adding an entry is a
// decision somebody has to write down, and forgetting to is the bug.
var notADecisionField = map[string]string{
	"event":       "names which kind of line this is, and the reader has already selected on it",
	"env":         "identifies the environment, which the caller of af net log already chose",
	"rules":       "the rule count on the ready line, which is about the policy and not about a request",
	"default":     "the default mode on the ready line, and af net explain is where a policy is read",
	"credentials": "the count of sandbox values loaded, on the ready line, and never per request",
}

// Fields on a decision that af net log -o json does not carry under their own
// name, with the reason. Anything else missing is a fact recorded by the
// sidecar that no surface can show.
var notItsOwnJSONField = map[string]string{
	"method": "folded into request by decisionRequest, with the scheme, host, port and path",
	"host":   "folded into request",
	"port":   "folded into request, and omitted there when it is the default for the scheme",
	"path":   "folded into request",
	"tls":    "folded into request as the scheme",
	"event":  "always decision on these rows, because Decisions returns nothing else",
}

func TestEveryFieldTheSidecarRecordsSurvivesTheDecodeIntoADecision(t *testing.T) {
	t.Parallel()
	record := tagsOf(t, "../../cmd/af-proxy/main.go", "record")
	decision := tagsOf(t, "../runtime/local/proxy.go", "Decision")

	var missing []string
	for _, tag := range record {
		if has(decision, tag) || notADecisionField[tag] != "" {
			continue
		}
		missing = append(missing, tag)
	}
	sort.Strings(missing)
	require.Empty(t, missing,
		"the sidecar writes %v and local.Decision has nowhere to decode them, so json.Unmarshal "+
			"drops them silently and the fact never leaves the proxy. Add the field, or add it to "+
			"notADecisionField with the reason it stops there.", missing)
}

func TestEveryFieldADecisionCarriesReachesTheJSONSurface(t *testing.T) {
	t.Parallel()
	decision := tagsOf(t, "../runtime/local/proxy.go", "Decision")
	surface := tagsOf(t, "net.go", "DecisionJSON")

	var missing []string
	for _, tag := range decision {
		if has(surface, tag) || notItsOwnJSONField[tag] != "" {
			continue
		}
		missing = append(missing, tag)
	}
	sort.Strings(missing)
	require.Empty(t, missing,
		"af net log -o json drops %v, so a fact the sidecar recorded and the engine decoded cannot "+
			"be read by anything parsing the log. Add the field to DecisionJSON and assign it, or add "+
			"it to notItsOwnJSONField with the reason.", missing)
}

// The allowlists have to stay honest too. An entry naming a field that no
// longer exists is a reason nobody can check, and it would hide the next
// field that took that name.
func TestTheReasonsForNotCarryingAFieldStillNameRealFields(t *testing.T) {
	t.Parallel()
	record := tagsOf(t, "../../cmd/af-proxy/main.go", "record")
	decision := tagsOf(t, "../runtime/local/proxy.go", "Decision")

	for tag := range notADecisionField {
		require.True(t, has(record, tag),
			"notADecisionField explains %q, which the sidecar no longer writes", tag)
	}
	for tag := range notItsOwnJSONField {
		require.True(t, has(decision, tag),
			"notItsOwnJSONField explains %q, which is no longer on a Decision", tag)
	}
}
