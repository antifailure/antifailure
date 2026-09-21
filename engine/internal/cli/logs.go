package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// LogLineJSON is one line of service output.
type LogLineJSON struct {
	Service string `json:"service"`
	Text    string `json:"text"`
}

func newLogsCommand(env *Env) *cobra.Command {
	var branch string
	var tail int
	cmd := &cobra.Command{
		Use:   "logs [service]",
		Short: "Show what the environment's services have written",
		Long: strings.TrimSpace(`
Output from every service, or from one if you name it.

Everything here goes through the redactor on the way out. A service's own log
is the second likeliest place for a secret to surface after a build log, and
this is the command people paste into issues.`),
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			service := ""
			if len(args) == 1 {
				service = args[0]
			}
			o, err := orchestrator(env, branch, false)
			if err != nil {
				return err
			}
			return showLogs(cmd.Context(), env.Out, o, service, tail)
		},
	}
	cmd.Flags().IntVar(&tail, "tail", 200, "How many lines to show per service")
	cmd.Flags().StringVar(&branch, "branch", "", "Branch to read, defaulting to the checked out one")
	return cmd
}

// logSource is what af logs reads from. The orchestrator in the command, and a
// fake in the tests, so the decision about what an empty answer means is
// exercised through the same function the command runs.
type logSource interface {
	Logs(ctx context.Context, service string, tail int) ([]provider.LogLine, error)
	Status(ctx context.Context) (*env.Result, error)
}

// showLogs reads and prints one answer to af logs.
func showLogs(ctx context.Context, out *Output, src logSource, service string, tail int) error {
	lines, err := src.Logs(ctx, service, tail)
	if err != nil {
		return err
	}

	if out.Format == FormatJSON {
		docs := make([]LogLineJSON, 0, len(lines))
		for _, l := range lines {
			docs = append(docs, LogLineJSON{Service: l.Service, Text: l.Text})
		}
		return out.JSON(docs)
	}
	if len(lines) == 0 {
		// Only asked when there is nothing to show, because it is the
		// only case where the answer changes what is printed.
		res, statusErr := src.Status(ctx)
		emptyLogs(out, service, res, statusErr)
		return nil
	}

	// The service name is printed only when there is more than one, so
	// reading one service's output is not a column of the same word.
	names := map[string]bool{}
	for _, l := range lines {
		names[l.Service] = true
	}
	width := 0
	if len(names) > 1 {
		for n := range names {
			if len(n) > width {
				width = len(n)
			}
		}
	}
	for _, l := range lines {
		if width == 0 {
			out.Printf("%s\n", l.Text)
			continue
		}
		out.Printf("%s  %s\n", out.S(StyleDim, pad(l.Service, width)), l.Text)
	}
	return nil
}

// emptyLogs says why there is no output, and offers a remedy only when running
// it would change the answer.
//
// This printed "Bring the environment up with af up" whatever the reason. On
// 2026-09-21 it said so to `af logs database` with the environment up and
// serving, where af up would have changed nothing. An empty log has at least
// three causes and only one of them is fixed by bringing something up.
func emptyLogs(out *Output, service string, res *env.Result, statusErr error) {
	if statusErr != nil {
		// No remedy. Which one is right depends on the answer that could not
		// be read.
		out.Empty(fmt.Sprintf("Nothing was returned, and whether the environment is "+
			"running could not be checked: %v", statusErr), "", "")
		return
	}
	if res == nil || len(res.Services) == 0 {
		out.Empty("Nothing is running for this branch, so nothing has been written.",
			"Bring it up with", "af up")
		return
	}
	if service == "" {
		out.Empty("The environment is running and none of its services has written anything yet.", "", "")
		return
	}
	if service == local.ProxyAlias {
		out.Empty("The environment is running and its egress sidecar has written nothing yet.", "", "")
		return
	}
	running := make([]string, 0, len(res.Services))
	for _, s := range res.Services {
		if s.Name != service {
			running = append(running, s.Name)
			continue
		}
		if s.State == "" || s.State == "running" {
			out.Empty(fmt.Sprintf("%s is running and has written nothing yet.", service), "", "")
			return
		}
		what := fmt.Sprintf("%s has written nothing, and the runtime reports it as %s", service, s.State)
		if s.Detail != "" {
			what += ": " + s.Detail
		}
		out.Empty(what+".", "", "")
		return
	}
	// Declared, because Logs refuses a name the manifest does not declare,
	// and absent from a running environment. A service the environment
	// provides as a datastore is one way to get here. af up is not offered,
	// because nothing says it would start it.
	out.Empty(fmt.Sprintf("The environment is running, but nothing called %s is running in it, "+
		"so it has no output. What is running: %s.", service, strings.Join(running, ", ")), "", "")
}
