package cli

import (
	"context"
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

Client setup differs by host. https://antifailure.dev/docs/reference/mcp has
the current command or configuration for each supported local client. This
release provides no hosted MCP URL. A browser client requires a separately
operated and authenticated Streamable HTTP bridge.`),
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
				// The two machine checks are handed in rather than called from
				// inside the server, because they live here and this package
				// imports that one. Reimplementing either over there would
				// give two instruments that can disagree about the same
				// machine, which is the failure this repository keeps finding
				// in its own gates. Left nil, the tool reports NOT CHECKED
				// rather than a pass.
				Diagnose:    func(ctx context.Context) (mcp.Diagnosis, error) { return diagnose(ctx, env) },
				RunnerReady: func(ctx context.Context) (mcp.RunnerReadiness, error) { return runnerReady(ctx, env) },
			})
		},
	}
}

// diagnose runs the machine checks for the MCP server.
//
// The same RunDoctor af doctor and af support bundle call, so a tool call and
// a terminal cannot disagree about whether this machine can run anything.
func diagnose(ctx context.Context, env *Env) (mcp.Diagnosis, error) {
	report := RunDoctor(ctx, env, systemProber{getenv: env.Getenv})
	out := mcp.Diagnosis{OK: report.OK, Platform: report.Platform}
	for _, c := range report.Checks {
		out.Checks = append(out.Checks, mcp.DiagnosticCheck{
			Name: c.Name, Status: string(c.Status),
			Detail: c.Detail, Remediation: c.Remediation,
		})
	}
	return out, nil
}

// runnerReady inspects the browser agent runner for the MCP server.
//
// The same checks and the same three way verdict af runner check reports,
// including the runners it went past, because a report about a directory the
// reader did not mean is how this check came to say a runner was ready while
// the run took a different copy.
func runnerReady(ctx context.Context, env *Env) (mcp.RunnerReadiness, error) {
	target, passedOver, err := runnerToCheck(env.WorkDir)
	if err != nil {
		return mcp.RunnerReadiness{}, err
	}
	results := append(passedOverChecks(passedOver), checkRunner(ctx, target)...)

	out := mcp.RunnerReadiness{
		Verdict:    string(runnerVerdict(results)),
		Path:       target,
		Unanswered: unanswered(results),
	}
	for _, r := range results {
		if r.label == "node" && r.symbol != SymbolFail {
			out.Node = strings.SplitN(r.detail, ",", 2)[0]
		}
		out.Checks = append(out.Checks, mcp.DiagnosticCheck{
			Name: r.label, Status: r.symbol, Detail: r.detail, Remediation: r.remedy,
		})
	}
	return out, nil
}
