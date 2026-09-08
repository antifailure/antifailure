package env

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// schemaSource declares every name this build answers to.
const schemaSource = "../../pkg/schema/manifest.go"

// TestReservedProviderNamesAreEveryNameThisBuildAnswersTo is the check that
// would have caught pgurl.
//
// reservedProviderNames is what stops a registration from shadowing a built in
// provider. The registry is consulted AFTER the engine's own switch, so a
// registration under a built in name is never used, and refusing it at
// validation is the only thing standing between that and a build somebody
// believes overrides the Docker provider and which silently does not.
//
// Its own comment said it was "built from the schema's constants rather than
// written out again, so a provider added to the engine cannot be left off this
// list". It was written out again, and pgurl HAD been left off: five providers
// in the switch, four in the reserved list, so a registration named pgurl
// validated cleanly and could never run. The comment described the property
// somebody wanted rather than the one the code had.
//
// This reads the constants instead of trusting the slice, so the property is
// now checked rather than asserted. Parsed rather than grepped, for the reason
// tools/licensegen's equivalent gives: a constant inside a comment or a string
// cannot be mistaken for a declaration, and renaming the TYPE fails here as an
// empty list rather than silently passing, which is why each case demands a
// non-empty result before comparing.
func TestReservedProviderNamesAreEveryNameThisBuildAnswersTo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		socket   string
		typeName string
	}{
		{extension.SocketDatabaseProvider, "DBProvider"},
		{extension.SocketRuntimeProvider, "RuntimeProvider"},
		{extension.SocketGoldenStore, "GoldenStorage"},
	}

	reserved := reservedProviderNames()
	for _, c := range cases {
		t.Run(c.typeName, func(t *testing.T) {
			want := constantValuesOfType(t, schemaSource, c.typeName)
			require.NotEmptyf(t, want,
				"%s declared no %s constants, so this test is reading the wrong file or "+
					"the type was renamed. Point it at the file that declares them",
				schemaSource, c.typeName)

			got := append([]string(nil), reserved[c.socket]...)
			sort.Strings(got)
			sort.Strings(want)
			require.Equalf(t, want, got,
				"the reserved names for the %s socket are not the %s constants this build "+
					"has. A name in the constants and not in the list is a name a build can "+
					"register and never use; a name in the list and not in the constants is a "+
					"registration refused for a provider that does not exist",
				c.socket, c.typeName)
		})
	}

	// The two sockets with no schema constants are named rather than left out,
	// so that this test's silence about them is a decision somebody made and
	// not a case that was forgotten. A datastore engine is an open string in
	// the schema on purpose, and no emulator is built in at all.
	require.NotContains(t, reserved, extension.SocketDatastoreProvider,
		"a datastore engine is an open string in the manifest and the provider lookup "+
			"refuses an engine by name, so there is no closed set to reserve")
	require.NotContains(t, reserved, extension.SocketEmulator,
		"no emulator is built into this binary, so a registration can shadow nothing")
}

// constantValuesOfType returns the string values of the constants declared
// with a given named type in one file.
func constantValuesOfType(t *testing.T, path, typeName string) []string {
	t.Helper()
	if _, err := os.Stat(filepath.Clean(path)); err != nil {
		t.Fatalf("the schema's source is not where this test expects it: %v", err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	require.NoError(t, err)

	var out []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		// The type is written once on the first spec of a const block and
		// carried by the rest, so it is remembered across the block the way
		// the compiler does.
		carried := ""
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if ident, ok := value.Type.(*ast.Ident); ok {
				carried = ident.Name
			} else if value.Type != nil {
				carried = ""
			}
			if carried != typeName {
				continue
			}
			for _, expr := range value.Values {
				lit, ok := expr.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				unquoted, err := strconv.Unquote(lit.Value)
				require.NoError(t, err)
				out = append(out, unquoted)
			}
		}
	}
	// Sorted and deduplicated, because the caller compares sets and a
	// duplicate constant is a compile error rather than this test's problem.
	sort.Strings(out)
	return dedupe(out)
}

func dedupe(in []string) []string {
	var out []string
	for i, v := range in {
		if i > 0 && v == in[i-1] {
			continue
		}
		out = append(out, v)
	}
	return out
}
