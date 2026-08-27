// Package afcli is how a build outside this module runs the engine.
//
// MIT, like the rest of the engine. Nothing here is enterprise code and nothing
// here imports any.
//
// It exists because engine/pkg/extension was only half a socket. A registry you
// can add hooks to is no use to code that cannot also run the thing those hooks
// plug into, and the CLI is engine/internal/cli, which Go's internal rule makes
// unimportable from outside engine/... . That is deliberate and correct for the
// CLI's own surface, which is not an API and changes freely. But it meant the
// enterprise module, which is a separate module precisely so that the community
// binary cannot import it, could register a policy hook and had no way to reach
// a binary that would consult it. Everything under ee/engine compiled, was
// tested, and could never be run.
//
// So this is one function and a struct, and both are deliberately narrow. It is
// not a general purpose embedding API: there is no way to add a command, change
// a behaviour, or reach inside the engine. It runs the same command tree the af
// binary runs, in a process the caller has already set up, which is the whole
// requirement. Anything wider would be a second public surface to keep stable.
package afcli

import (
	"context"
	"io"

	"github.com/antifailure/antifailure/engine/internal/cli"
)

// Options are the process facilities the CLI should use.
//
// A struct of interfaces rather than the process's own, so that a caller
// wrapping the engine can capture its output, and so that this package's own
// test can run a command without a terminal. Every field may be left zero,
// which means the real one.
type Options struct {
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader
	// Getenv reads the environment. Zero means the process's own.
	Getenv func(string) string
	// WorkDir is the directory the manifest is resolved against. Zero means the
	// process's own working directory.
	WorkDir string
}

// Run executes one command and returns the exit code.
//
// The code is returned rather than the process being exited, so that a caller's
// deferred work runs and so that a wrapping binary decides its own exit. That
// is the same reason the af binary itself calls a function that returns a code.
//
// The context carries whatever the caller attached to it, which is how an
// enterprise build's licence status reaches a hook deep inside a command
// without a licence argument threaded through every signature between them.
func Run(ctx context.Context, args []string, opts Options) int {
	return cli.Execute(ctx, args, cli.Options{
		Stdout:  opts.Stdout,
		Stderr:  opts.Stderr,
		Stdin:   opts.Stdin,
		Getenv:  opts.Getenv,
		WorkDir: opts.WorkDir,
	})
}

// WithSignals returns a context cancelled by an interrupt, the way the af
// binary sets one up.
//
// Exported because a wrapping binary needs the same behaviour and should not
// have to reimplement it: the first interrupt cancels so that in flight work
// rolls back and teardown runs, and the second exits immediately with the
// journal intact. A wrapper that handled signals its own way would be a wrapper
// where control C means something different, which is worse than no wrapper.
//
// The second return value reports whether an interrupt has been seen. The third
// stops handling and must be deferred.
func WithSignals(ctx context.Context) (context.Context, func() bool, func()) {
	return cli.WithSignals(ctx)
}
