package controlplane_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/controlplane"
	"github.com/antifailure/antifailure/engine/internal/redact"
)

func TestAControlPlaneAddressThatDoesNotParseIsRefusedWithoutItsCredential(t *testing.T) {
	_, err := controlplane.New(controlplane.Options{
		BaseURL: "https://ci:Cp3Pass@cp.example.test:4x3", Token: "t", Redactor: redact.New(),
	})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "Cp3Pass")
	require.Contains(t, err.Error(), "invalid port", "the refusal no longer says what is wrong")
}

// The address parses, and is refused for being plain HTTP, and the refusal
// quoted it: the credential the refusal exists to keep off the wire went into
// the error instead.
func TestAPlainHTTPControlPlaneAddressIsRefusedWithoutItsCredential(t *testing.T) {
	_, err := controlplane.New(controlplane.Options{
		BaseURL: "http://ci:Cp4Pass@cp.example.test", Token: "t", Redactor: redact.New(),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "is not https")
	require.NotContains(t, err.Error(), "Cp4Pass")
}
