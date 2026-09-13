package insights_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/insights"
	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// The branch is the only database a rehearsal may reach, whatever the service
// declares.
//
// The container used to be started with the branch's address first and the
// service's variables appended after it, and Docker keeps the last of two
// entries with one name. So the variable a service declared under the name the
// branch is delivered as decided which database the migrations ran against.
// Each cell is one thing a service can declare under that name.
func TestContainerApplier_TheBranchIsTheOnlyDatabaseTheMigrationSees(t *testing.T) {
	const branch = "postgres://rehearsal@af-rehearsal-db:5432/app"
	cells := []struct {
		name   string
		urlVar string
		env    map[string]secrets.Value
	}{
		{
			name: "a resolved secret named DATABASE_URL",
			env: map[string]secrets.Value{
				"DATABASE_URL":    secrets.New("postgres://app@production.invalid:5432/app"),
				"SECRET_KEY_BASE": secrets.New("fixture-key-base"),
			},
		},
		{
			name: "an unresolved DATABASE_URL",
			env: map[string]secrets.Value{
				"DATABASE_URL":    secrets.New(""),
				"SECRET_KEY_BASE": secrets.New("fixture-key-base"),
			},
		},
		{
			name:   "a secret under the variable the manifest names",
			urlVar: "MY_DB_URL",
			env: map[string]secrets.Value{
				"MY_DB_URL":       secrets.New("postgres://app@production.invalid:5432/app"),
				"SECRET_KEY_BASE": secrets.New("fixture-key-base"),
			},
		},
		{
			name: "no database variable declared",
			env: map[string]secrets.Value{
				"SECRET_KEY_BASE": secrets.New("fixture-key-base"),
			},
		},
	}
	for _, c := range cells {
		t.Run(c.name, func(t *testing.T) {
			applier := &insights.ContainerApplier{
				Image: "postgres:17-alpine", Command: "true", URLVar: c.urlVar, Env: c.env,
			}
			variable := c.urlVar
			if variable == "" {
				variable = "DATABASE_URL"
			}

			got := map[string][]string{}
			for _, kv := range applier.Environment(secrets.New(branch)) {
				k, v, _ := strings.Cut(kv, "=")
				got[k] = append(got[k], v)
			}
			// One entry per name, so nothing is left to Docker's order.
			for k, vs := range got {
				require.Len(t, vs, 1, "%s reaches the container %d times", k, len(vs))
			}
			require.Equal(t, []string{branch}, got[variable],
				"the migration would run against the database %s names, not the branch", variable)
			// And the service's other variables arrive as they were declared.
			require.Equal(t, []string{"fixture-key-base"}, got["SECRET_KEY_BASE"])
		})
	}
}
