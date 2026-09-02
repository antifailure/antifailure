// Command af is the Antifailure engine.
//
// It builds an environment from the shape of production for every branch: a
// masked and verified copy of the database, the services running in a sandbox
// whose only egress path is a proxy you configure, inbound webhooks simulated
// so flows finish, and agents that use the application the way people do.
//
// Everything it creates is journaled before it is made and compensated on
// teardown, so nothing outlives the environment.
package main

import (
	"context"
	"os"

	"github.com/antifailure/antifailure/engine/internal/cli"
)

func main() {
	// The first interrupt cancels the root context so that in flight work
	// rolls back and teardown runs. The second forces an exit with the journal
	// intact, because a user pressing control C twice means "stop now", and
	// the journal is what makes stopping now safe. Run is what honours the
	// second one: it returns the exit code out from under a command that has
	// not noticed the first.
	ctx, forced, stop := cli.WithSignals(context.Background())
	defer stop()

	// Execute returns the code rather than exiting, so that deferred cleanup
	// runs and every command stays testable without a process.
	os.Exit(cli.Run(ctx, forced, os.Args[1:], cli.Options{}))
}
