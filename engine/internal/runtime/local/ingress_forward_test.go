package local

// The ingress forwarder is the sidecar binary in forward mode, so the runtime
// and the sidecar have to agree on a command line across a container boundary
// that no compiler checks. These read the sidecar's flags out of the source
// this binary carries, which is the source the image it runs is built from.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/proxyimage"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

func TestIngressCommand_RelaysTheServicesPortToItsName(t *testing.T) {
	got := ingressCommand(provider.ServiceSpec{Name: "web", Port: 8000})
	require.Equal(t, []string{"-forward-listen", ":8000", "-forward-to", "web:8000"}, got)
}

// sidecarFlags is every flag name the carried sidecar's main declares.
func sidecarFlags(t *testing.T) map[string]bool {
	t.Helper()
	body, ok := proxyimage.Sources["cmd/af-proxy/main.go"]
	require.True(t, ok, "the sidecar's main is not among the carried sources, so NOTHING was checked")
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", body, 0)
	require.NoError(t, err, "the carried sidecar main does not parse, so NOTHING was checked")

	declared := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "flag" {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if name, err := strconv.Unquote(lit.Value); err == nil {
			declared[name] = true
		}
		return true
	})
	// The sidecar has declared -config since it existed. Not finding it means
	// this reader stopped reading, not that every flag disappeared.
	require.True(t, declared["config"], "no -config flag was read from the carried sidecar, so this "+
		"reader is not finding flags at all")
	return declared
}

func TestIngressCommand_NamesOnlyFlagsTheCarriedSidecarDeclares(t *testing.T) {
	declared := sidecarFlags(t)
	named := 0
	for _, arg := range ingressCommand(provider.ServiceSpec{Name: "web", Port: 8000}) {
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		named++
		require.Truef(t, declared[strings.TrimPrefix(arg, "-")],
			"the forwarder is started with %s and the sidecar it runs declares no such flag, so every "+
				"published port would fail to start", arg)
	}
	require.Equal(t, 2, named, "the forwarder's command line named no flags to check")
}
