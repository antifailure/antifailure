package mcp

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/policy"
)

// A probe that does not parse is answered with the request echoed back beside
// the complaint, and the echo was the URL exactly as given.
func TestAProbeThatDoesNotParseIsEchoedWithoutItsCredential(t *testing.T) {
	eng, err := policy.New(nil)
	require.NoError(t, err)

	out, fault := runProbes(eng, map[string]any{"probe": []any{
		map[string]any{"method": "GET", "url": "https://me:Mc5Pass@api.example.test:4x3/"},
	}})
	require.Nil(t, fault)
	require.Len(t, out, 1)
	require.NotEmpty(t, out[0].Error, "the probe parsed, so this case tests nothing")
	require.NotContains(t, out[0].Request, "Mc5Pass")
	require.Contains(t, out[0].Request, "api.example.test", "the echo no longer says which request it was")
}
