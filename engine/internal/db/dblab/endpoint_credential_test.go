package dblab

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnEndpointThatDoesNotParseIsRefusedWithoutItsCredential(t *testing.T) {
	_, err := New(Options{Endpoint: "https://dle:Db9Pass@dle.example.test:2x5"})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "Db9Pass")
	require.Contains(t, err.Error(), "invalid port", "the refusal no longer says what is wrong")
}
