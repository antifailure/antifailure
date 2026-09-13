package pgurl

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// AF-DB-024 prints its detail, and the detail was url.Parse's error, which
// quotes the value it could not read. A slash in the password is the ordinary
// way a DATABASE_URL fails to parse, and it printed the password.
func TestNormalizeNeverQuotesThePasswordOfAValueThatDoesNotParse(t *testing.T) {
	_, _, err := normalize(secrets.New("postgres://reader:Pg7Pass/x2@db.example.test:5432/app"), "V")
	require.True(t, errors.Is(err, aferrors.Coded(aferrors.AFDB024)), "got %v", err)
	require.NotContains(t, err.Error(), "Pg7Pass")
	require.Contains(t, err.Error(), "not quoted", "the refusal no longer says what is wrong")
}
