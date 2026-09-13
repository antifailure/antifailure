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

// #383 refuses a star pattern used as a request host, and the refusal quoted
// the request it refused in its request field, user information and query
// included.
func TestNetExplainRefusesAPatternHostWithoutTheCredentialInItsURL(t *testing.T) {
	_, err := parseRequest("GET", "https://me:Ns6Pass@*.zapier.com/hooks?key=Nq9Tok")
	require.Error(t, err)
	require.Contains(t, err.Error(), "is a pattern", "the refusal this test is about did not fire")
	require.NotContains(t, err.Error(), "Ns6Pass")
	require.NotContains(t, err.Error(), "Nq9Tok")
}
