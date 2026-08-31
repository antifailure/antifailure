package cli

import (
	"strings"

	"github.com/spf13/cobra"
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
			lines, err := o.Logs(cmd.Context(), service, tail)
			if err != nil {
				return err
			}

			if env.Out.Format == FormatJSON {
				docs := make([]LogLineJSON, 0, len(lines))
				for _, l := range lines {
					docs = append(docs, LogLineJSON{Service: l.Service, Text: l.Text})
				}
				return env.Out.JSON(docs)
			}
			if len(lines) == 0 {
				env.Out.Empty("Nothing has been written yet.", "Bring the environment up with", "af up")
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
					env.Out.Printf("%s\n", l.Text)
					continue
				}
				env.Out.Printf("%s  %s\n", env.Out.S(StyleDim, pad(l.Service, width)), l.Text)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&tail, "tail", 200, "How many lines to show per service")
	cmd.Flags().StringVar(&branch, "branch", "", "Branch to read, defaulting to the checked out one")
	return cmd
}
