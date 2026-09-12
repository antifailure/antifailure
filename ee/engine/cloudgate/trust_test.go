package cloudgate

import (
	"context"
	"errors"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/stretchr/testify/require"
	"testing"
)

type trustedDatabase struct {
	provider.Database
	called bool
	err    error
}

func (d *trustedDatabase) TrustBundle(context.Context, provider.Branch) (string, error) {
	d.called = true
	return "public-ca", d.err
}
func TestDatabaseTrustSurvivesTheEntitlementWrapper(t *testing.T) {
	inner := &trustedDatabase{}
	wrapped := &gatedDatabase{inner: inner}
	bundle, err := wrapped.TrustBundle(context.Background(), provider.Branch{})
	require.NoError(t, err)
	require.Equal(t, "public-ca", bundle)
	require.True(t, inner.called)
	inner.err = errors.New("trust failed")
	_, err = wrapped.TrustBundle(context.Background(), provider.Branch{})
	require.ErrorIs(t, err, inner.err)
}
