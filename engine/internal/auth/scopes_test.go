package auth_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/auth"
)

// GrantableScopes says in its own comment that it mirrors GRANTABLE_SCOPES on
// the server, and nothing made that true.
//
// The list exists on this side so `af login --scope` can refuse a typo in the
// terminal rather than after somebody has approved a login in a browser. That
// courtesy inverts the moment the lists differ: a scope added on the server and
// not here is refused by checkScopes with "is not a scope", followed by this
// engine's own stale list of four, for a capability the server would have
// granted. The message is wrong, it comes from the client, and it reads as a
// server fault.
//
// The docs page is checked with them because it is a third copy of the same
// five names, written out in a sentence, and a reader who tries the scope it
// omits gets the same refusal.
//
// Read out of the other files rather than restated here, for the reason
// internal/controlplane/vocabulary_test.go gives: a copy of a list is one more
// thing to keep in step, and that is the problem.

const (
	serverPath   = "../../../web/apps/api/src/server.ts"
	signingInDoc = "../../../docs/src/content/docs/guides/signing-in.md"
)

var (
	grantableBlock = regexp.MustCompile(`(?s)export const GRANTABLE_SCOPES: readonly string\[\] = \[(.*?)\]`)
	cliBlock       = regexp.MustCompile(`export const CLI_SCOPES: readonly string\[\] = \[(.*?)\]`)
	quotedScope    = regexp.MustCompile(`'([^']+)'`)
)

// grantedByServer reads the two scope lists out of the control plane's source.
//
// GRANTABLE_SCOPES spreads CLI_SCOPES rather than repeating it, so the block
// itself yields only the two names beyond the default and the spread has to be
// followed for the full set.
func grantedByServer(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(serverPath))
	require.NoError(t, err, "read the control plane's scope list")

	m := grantableBlock.FindSubmatch(b)
	require.NotNil(t, m, "%s no longer declares GRANTABLE_SCOPES in a form this test can read. "+
		"Fix the test rather than deleting it: the drift it guards makes af login refuse a "+
		"scope the server would grant, and blame the server for it", serverPath)

	var out []string
	if strings.Contains(string(m[1]), "...CLI_SCOPES") {
		c := cliBlock.FindSubmatch(b)
		require.NotNil(t, c, "GRANTABLE_SCOPES spreads CLI_SCOPES and CLI_SCOPES could not be read")
		for _, q := range quotedScope.FindAllSubmatch(c[1], -1) {
			out = append(out, string(q[1]))
		}
	}
	for _, q := range quotedScope.FindAllSubmatch(m[1], -1) {
		out = append(out, string(q[1]))
	}
	require.NotEmpty(t, out, "the server's scope list parsed as empty, which would make every "+
		"assertion below vacuous")
	return out
}

func sortedCopy(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return out
}

func TestTheEngineAndTheServerNameTheSameScopes(t *testing.T) {
	require.Equal(t, sortedCopy(grantedByServer(t)), sortedCopy(auth.GrantableScopes),
		"the server grants one set of scopes and this engine offers another. A scope only the "+
			"server knows is refused in the terminal before the request is ever sent; a scope "+
			"only the engine knows is asked for and silently dropped by the intersection.")
}

// scopeSentence is the guide's own enumeration, up to the full stop.
//
// The sentence rather than the page, because providers.write is discussed
// further down and a page wide search would find it there while the list that
// claims to be exhaustive had quietly lost it. That is the exact drift being
// guarded, so the assertion has to be narrow enough to see it.
var scopeSentence = regexp.MustCompile(`(?s)The scopes that exist are(.*?)\.\s`)

func TestTheSigningInGuideNamesEveryScope(t *testing.T) {
	b, err := os.ReadFile(filepath.Clean(signingInDoc))
	require.NoError(t, err)

	m := scopeSentence.FindSubmatch(b)
	require.NotNil(t, m, "%s no longer says which scopes exist in a form this test can read. "+
		"Fix the test rather than deleting it: a reader who cannot see a scope named cannot "+
		"know to ask for it", signingInDoc)
	sentence := string(m[1])

	for _, scope := range auth.GrantableScopes {
		require.Contains(t, sentence, "`"+scope+"`",
			"%s lists the scopes that exist and does not name %s, so a reader has no way to "+
				"know they can ask for it", signingInDoc, scope)
	}
}

// A guard on the guard. A parse that silently returned the wrong thing would
// leave the test above comparing nothing, which is the failure this file exists
// to prevent.
func TestTheParsedScopesLookLikeScopes(t *testing.T) {
	scopes := grantedByServer(t)
	require.GreaterOrEqual(t, len(scopes), 3,
		"parsed only %d scopes (%v), which is too few to be the real list", len(scopes), scopes)
	for _, s := range scopes {
		require.Contains(t, s, ".", "%q does not look like a scope; the parse is picking up the "+
			"wrong quotes", s)
	}
	require.Contains(t, scopes, "environments.view",
		"the parsed list does not contain environments.view, so it is not the list")
}
