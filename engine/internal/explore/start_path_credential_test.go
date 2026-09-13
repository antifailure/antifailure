package explore_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/explore"
)

// af explore --start-path and the MCP explore tool both refuse a value that is
// not a path on the environment, and each refusal quoted the value it refused.
// The value refused is most often a whole URL somebody pasted, credential and
// all, and a start path may carry a query, which is where a signed token goes.

// The first refusal: a value that does not begin with a single slash.
func TestAStartPathThatIsAWholeURLIsRefusedWithoutItsCredential(t *testing.T) {
	_, err := explore.ParseStartPath("https://deploy:Xp4Pass@staging.example.com/billing")
	require.Error(t, err)
	require.Equal(t, aferrors.AFAGT023, codeOf(err))
	require.NotContains(t, err.Error(), "Xp4Pass")
	require.Contains(t, err.Error(), "staging.example.com", "the refusal no longer says what was given")
}

// The second refusal: a path that begins correctly and does not parse.
func TestAStartPathThatDoesNotParseIsRefusedWithoutItsQueryToken(t *testing.T) {
	_, err := explore.ParseStartPath("/a%zz?code=Xq8Tok")
	require.Error(t, err)
	require.Equal(t, aferrors.AFAGT023, codeOf(err))
	require.NotContains(t, err.Error(), "Xq8Tok")
	require.Contains(t, err.Error(), "/a%zz", "the refusal no longer names the path")
}
