// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package cloudauth

// The lane's number, measured rather than asserted: three clouds, one auth
// implementation, no vendor SDK in the credential path.
//
// Two of those three are claims about the whole enterprise module rather than
// about this package, which is why they are checked by reading the tree instead
// of by reading this file. A package can say "no SDK" in a comment for years
// after somebody added one, and "one implementation" stops being true the first
// time a lane copies a signer rather than importing it. Both of those are how
// this repository has been wrong before, so both get an instrument that fails.
//
// What this cannot see: a transitive dependency that pulls an SDK in without a
// direct import, and a second implementation written without any of the three
// protocol strings below. The first is covered by the module graph having no
// SDK in it at all, which the go.mod half checks. The second is not covered and
// is named here rather than pretended away.

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTheCredentialPathCarriesNoVendorSDK(t *testing.T) {
	// Every import this package makes, read off the source rather than off a
	// list somebody maintains. A standard library path has no dot in its first
	// segment, because a module path begins with a domain and a standard
	// library path does not.
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	fset := token.NewFileSet()
	files, imports := 0, 0
	var foreign []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files++
		parsed, parseErr := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		// A file that cannot be parsed is not a file with no imports, and
		// counting it as one is how a check reports clean because it could not
		// look.
		require.NoError(t, parseErr, "%s could not be parsed, so its imports were not read", name)
		for _, spec := range parsed.Imports {
			path := strings.Trim(spec.Path.Value, `"`)
			imports++
			if strings.Contains(strings.Split(path, "/")[0], ".") {
				foreign = append(foreign, name+" imports "+path)
			}
		}
	}

	require.NotZero(t, files, "no source files were read, so nothing was checked")
	require.Empty(t, foreign, "the credential path must be standard library only")
	t.Logf("credential path: %d files, %d imports, %d outside the standard library",
		files, imports, len(foreign))
}

func TestTheEnterpriseModuleDeclaresNoCloudSDK(t *testing.T) {
	// The module graph, not just this package. An SDK reached through another
	// package is still an SDK in the module that holds the credentials.
	raw, err := os.ReadFile(filepath.Join("..", "go.mod"))
	require.NoError(t, err)

	sdks := []string{
		"github.com/aws/aws-sdk-go",
		"cloud.google.com/go",
		"google.golang.org/api",
		"github.com/Azure/azure-sdk-for-go",
		"github.com/AzureAD/microsoft-authentication-library-for-go",
	}
	var found []string
	for _, sdk := range sdks {
		if strings.Contains(string(raw), sdk) {
			found = append(found, sdk)
		}
	}
	require.Empty(t, found, "a cloud SDK entered the module that holds the credentials")
	t.Logf("ee/engine go.mod: 0 of %d cloud SDKs present", len(sdks))
}

// theThreeProtocols are the strings only an implementation of each cloud's
// authentication can contain. One per cloud, and each is the part that cannot
// be written any other way.
var theThreeProtocols = map[string]string{
	"AWS Signature Version 4":    "AWS4-HMAC-SHA256",
	"Google JWT bearer exchange": "urn:ietf:params:oauth:grant-type:jwt-bearer",
	"Entra client credentials":   "client_credentials",
}

func TestThreeCloudsAreSignedByOneImplementation(t *testing.T) {
	// Here, and nowhere else in the enterprise module. The day a database
	// provider copies the signer instead of importing it, this fails and names
	// the file, which is the only moment anybody would notice: two signers that
	// agree today look exactly like one signer, right up until a fix lands in
	// one of them.
	for cloud, marker := range theThreeProtocols {
		require.True(t, markerIsInThisPackage(t, marker),
			"%s: the protocol string is not in cloudauth, so this check is watching nothing", cloud)
	}

	elsewhere := map[string][]string{}
	err := filepath.Walk("..", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == "cloudauth" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for cloud, marker := range theThreeProtocols {
			if strings.Contains(string(raw), marker) {
				elsewhere[cloud] = append(elsewhere[cloud], path)
			}
		}
		return nil
	})
	require.NoError(t, err)
	require.Empty(t, elsewhere, "a second implementation of a cloud's authentication exists")
	t.Logf("%d clouds, 1 auth implementation, 0 copies elsewhere in ee/engine",
		len(theThreeProtocols))
}

func markerIsInThisPackage(t *testing.T, marker string) bool {
	t.Helper()
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") ||
			strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		raw, readErr := os.ReadFile(entry.Name())
		require.NoError(t, readErr)
		if strings.Contains(string(raw), marker) {
			return true
		}
	}
	return false
}
