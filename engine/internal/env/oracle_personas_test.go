package env

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// What the oracle is told names each persona is every address and every phone
// number the manifest declares, and nothing for a persona that declares
// neither. A persona left out here is a persona whose rows the comparison
// reports as missing on one side and extra on the other, on a build that
// changed nothing about it.
func TestTheOracleIsToldEveryIdentityAPersonaDeclares(t *testing.T) {
	got := personaIdentities([]schema.Persona{
		{Name: "owner", Email: "owner@antifailure.test"},
		{Name: "texter", Phone: "+15555550100"},
		{Name: "both", Email: "both@antifailure.test", Phone: "+15555550101"},
		{Name: "visitor", Login: schema.LoginNone},
	})
	require.Equal(t, []string{
		"owner@antifailure.test",
		"+15555550100",
		"both@antifailure.test", "+15555550101",
	}, got)
}
