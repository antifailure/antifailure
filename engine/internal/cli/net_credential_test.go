package cli

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Every refusal in parseRequest quotes the request it refused, and the request
// is a URL somebody typed, which is where people put a token. Each case breaks
// the request a different way, so each quoting site is reached.

func TestNetExplainRefusesAURLThatDoesNotParseWithoutItsCredential(t *testing.T) {
	_, err := parseRequest("GET", "https://me:Nt4Pass@api.example.test:4x3/v1")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "Nt4Pass")
	require.Contains(t, err.Error(), "invalid port", "the refusal no longer says what is wrong")
}

func TestNetExplainRefusesABadMethodWithoutTheTokenInItsURL(t *testing.T) {
	_, err := parseRequest("GE T", "https://api.example.test/v1?key=Nk8Tok")
	require.Error(t, err)
	require.Contains(t, err.Error(), "is not an HTTP method")
	require.NotContains(t, err.Error(), "Nk8Tok")
}

func TestNetExplainRefusesAURLWithNoHostWithoutItsToken(t *testing.T) {
	_, err := parseRequest("GET", "https:///v1?key=Nh2Tok")
	require.Error(t, err)
	require.Contains(t, err.Error(), "names no host")
	require.NotContains(t, err.Error(), "Nh2Tok")
}
