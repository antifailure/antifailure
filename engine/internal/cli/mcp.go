package cli

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/antifailure/antifailure/engine/internal/mcp"
)

// newMCPCommand serves the rehearsal tools to a model over the Model Context
// Protocol.
//
// It is started by an MCP client rather than typed by a person, and it speaks
// on standard input and output, so it is the one command in this program whose
// output is not for a human at all. Everything the rest of the CLI would print
// goes to standard error here, including the progress lines the engine emits
// while an environment comes up: a single line of prose on standard output
// corrupts the session, and the client reports it as a parse error somewhere
// unrelated.
func newMCPCommand(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Serve the rehearsal tools to a model over the Model Context Protocol",
		Long: strings.TrimSpace(`
Serve this repository's rehearsal tools to an MCP client on standard input and
output.

The agent on the other end chooses what to rehearse. It does not choose how
safely the rehearsal runs: there is no argument on any tool that can disable
sanitization, widen the egress policy, lower a threshold or name a database.
Thresholds come from this project's manifest, and the verdict is decided by the
same evaluator af ci uses, so a tool call and a pull request check cannot
disagree about the same change.

The server serves exactly this checkout. A tool call may state which project it
believes it is talking to, and a call naming a different one is refused rather
than followed.

Standard output carries the protocol and nothing else. Progress, warnings and
errors go to standard error, where the client's log will show them.

A client configured by JSON runs af with ["-C", "/absolute/path", "mcp"],
because most of them have nowhere to set a working directory.
https://antifailure.dev/docs/reference/mcp has the entry for each client, and
says why there is no hosted endpoint to point one at.`),
		Args: cobra.NoArgs,
		// The protocol owns standard output, so cobra must not write a usage
		// block onto it when something fails. Errors reach standard error
		// through the root command's own handling.
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return mcp.Serve(cmd.Context(), mcp.Config{
				WorkDir: env.WorkDir,
				In:      env.Stdin,
				Out:     os.Stdout,
				Log:     os.Stderr,
				Clock:   env.Clock,
				Getenv:  env.Getenv,
				Version: Version,
			})
		},
	}
}
