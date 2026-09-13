package env

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// pgx reports a URL that does not parse with the inner half of net/url's error,
// and for a password holding a slash that half quotes the start of the
// password. The whole value is registered with the redactor, and a piece of it
// is not the whole value, so the piece reached every message that carried the
// driver's error. It is refused here, before anything connects.
func TestSourceURL_AURLThatDoesNotParseIsRefusedWithoutItsPassword(t *testing.T) {
	o := chainOrchestrator(t, t.TempDir(),
		map[string]string{"PRODUCTION_DATABASE_URL": "postgres://reader:Sr7Pass/q@db.internal:5432/app"})

	value, err := o.sourceURL(t.Context())
	require.Error(t, err)
	require.True(t, errors.Is(err, aferrors.Coded(aferrors.AFDB024)), "got %v", err)
	require.NotContains(t, err.Error(), "Sr7Pass")
	require.False(t, value.IsZero(),
		"the fidelity report ignores this error and asks only whether production is named, and it is")
}
