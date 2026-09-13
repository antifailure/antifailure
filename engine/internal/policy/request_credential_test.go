package policy

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// ParseRequest's errors are passed on by af net explain and by the MCP server,
// and the URL in a request somebody asks about is where a token turns up.

func TestParseRequestRefusesAURLThatDoesNotParseWithoutItsCredential(t *testing.T) {
	_, err := ParseRequest("GET", "https://me:Pr5Pass@api.example.test:4x3/")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "Pr5Pass")
	require.Contains(t, err.Error(), "invalid port", "the refusal no longer says what is wrong")
}

func TestParseRequestRefusesAURLWithNoHostWithoutItsToken(t *testing.T) {
	_, err := ParseRequest("GET", "https:///v1?key=Ph3Tok")
	require.Error(t, err)
	require.Contains(t, err.Error(), "names no host")
	require.NotContains(t, err.Error(), "Ph3Tok")
}
