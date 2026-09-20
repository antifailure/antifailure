package env_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Where a knob's value comes from, and in which order.
//
// The precedence is the caller, then the manifest, then the engine's default,
// and it is a function rather than three if statements at each call site
// because this repository already shipped the other version. `af load run`
// passed cobra's flag DEFAULT down as if it were a choice, so every run arrived
// carrying 60s and scale 1.0, which are not zero, and the fallback that would
// have read the manifest never fired. A repository that wrote `scale: 0.05`
// because it was aiming this at something fragile got production's full arrival
// rate. See loadRate in engine/internal/cli/load.go for the whole story.

func TestResolveSQLLoad(t *testing.T) {
	t.Parallel()

	t.Run("nothing set anywhere is the engine's own default", func(t *testing.T) {
		plan := env.ResolveSQLLoad(nil, env.SQLLoadOptions{})
		require.Equal(t, 8, plan.Clients)
		require.Equal(t, 60*time.Second, plan.Duration)
		require.Zero(t, plan.Transactions)
		require.Zero(t, plan.ThinkTime)
	})

	t.Run("the manifest beats the default", func(t *testing.T) {
		plan := env.ResolveSQLLoad(&schema.LoadSQL{
			Clients: 24, Duration: "5m", ThinkTime: "40ms",
		}, env.SQLLoadOptions{})
		require.Equal(t, 24, plan.Clients)
		require.Equal(t, 5*time.Minute, plan.Duration)
		require.Equal(t, 40*time.Millisecond, plan.ThinkTime)
	})

	t.Run("the caller beats the manifest", func(t *testing.T) {
		plan := env.ResolveSQLLoad(&schema.LoadSQL{
			Clients: 24, Duration: "5m", ThinkTime: "40ms",
		}, env.SQLLoadOptions{
			Clients: 4, Duration: 10 * time.Second, ThinkTime: time.Millisecond,
		})
		require.Equal(t, 4, plan.Clients)
		require.Equal(t, 10*time.Second, plan.Duration)
		require.Equal(t, time.Millisecond, plan.ThinkTime)
	})

	t.Run("a knob the caller left alone still reads the manifest", func(t *testing.T) {
		// The half the flag default defect destroyed. A command that sets one
		// knob must not silently overwrite the other two.
		plan := env.ResolveSQLLoad(&schema.LoadSQL{
			Clients: 24, Duration: "5m", ThinkTime: "40ms",
		}, env.SQLLoadOptions{Clients: 4})
		require.Equal(t, 4, plan.Clients)
		require.Equal(t, 5*time.Minute, plan.Duration)
		require.Equal(t, 40*time.Millisecond, plan.ThinkTime)
	})

	t.Run("a workload sized in transactions gets no time bound", func(t *testing.T) {
		// Leaving the default sixty seconds in place would cap a run its author
		// sized in work, and the report would say it ran fewer transactions
		// than it asked for with nothing saying why.
		plan := env.ResolveSQLLoad(&schema.LoadSQL{Transactions: 500}, env.SQLLoadOptions{})
		require.Equal(t, 500, plan.Transactions)
		require.Zero(t, plan.Duration)
	})

	t.Run("a workload that declares both keeps both", func(t *testing.T) {
		// Whichever comes first ends the run, which is what pgbench does with
		// -t and -T together and what a person asking for both means.
		plan := env.ResolveSQLLoad(&schema.LoadSQL{
			Transactions: 500, Duration: "90s",
		}, env.SQLLoadOptions{})
		require.Equal(t, 500, plan.Transactions)
		require.Equal(t, 90*time.Second, plan.Duration)
	})

	t.Run("a think time of zero is a choice the manifest can make", func(t *testing.T) {
		// Zero measures the server at saturation, which is a real thing to ask
		// for, and the normalizer writes "0ms" rather than leaving the key
		// empty so that the two are the same answer here.
		plan := env.ResolveSQLLoad(&schema.LoadSQL{ThinkTime: "0ms"}, env.SQLLoadOptions{})
		require.Zero(t, plan.ThinkTime)
	})

	t.Run("a duration the manifest could not have produced is ignored", func(t *testing.T) {
		// The validator refuses this before a run, so reaching here means the
		// manifest was never validated. Falling back to the default is the
		// right answer: a run at a duration nobody can parse is worse than a
		// run at the default, and the validator is where it is reported.
		plan := env.ResolveSQLLoad(&schema.LoadSQL{Duration: "a fortnight"}, env.SQLLoadOptions{})
		require.Equal(t, 60*time.Second, plan.Duration)
	})
}
