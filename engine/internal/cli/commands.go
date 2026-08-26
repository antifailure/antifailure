package cli

import (
	"fmt"
	"runtime"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/manifest"
)

// The command tree is declared in full from the first release, including
// commands whose engines have not landed. A command that exists and says "not
// yet available in this version" is honest; a command that is missing makes a
// user think they have the wrong version, and a command that silently does
// nothing is the failure this product exists to prevent.

func newLoadCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "load",
		Short: "Generate traffic shaped like production and compare against the base branch",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return notYetAvailable("af load")
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "smoke",
		Short: "Run a short load check with the safety caps at their defaults",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return notYetAvailable("af load smoke")
		},
	})
	return cmd
}

func newInsightsCommand(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "insights",
		Short: "Rehearse migrations and compare query behavior against the base branch",
		Long: strings.TrimSpace(`
Pending migrations are applied to a fresh branch with per statement timing and
the strongest lock held per table, pg_stat_statements is diffed between
branches to catch a query loop, and query plans are compared to catch an index
that stopped being used.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return notYetAvailable("af insights")
		},
	}
}

func newEnvCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Inspect the variables an environment resolves",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List the variables each service needs and where each resolves from",
		Long: strings.TrimSpace(`
Names and sources only. A value is never printed, because a command that can
print a secret becomes the command someone runs with output redirected to a
file.`),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return notYetAvailable("af env list")
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "pull",
		Short: "Fetch variable names from a configured secrets adapter",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return notYetAvailable("af env pull")
		},
	})
	return cmd
}

// newExplainCommand renders the effective manifest. It works today, because
// everything it needs is the manifest package.
func newExplainCommand(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "explain",
		Short: "Show the effective configuration, with every default filled in",
		Long: strings.TrimSpace(`
The most common configuration bug is a default nobody knew about. This prints
the resolved value of every setting, so "why is it blocking that host" has a
one line answer.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := manifest.Find(env.WorkDir)
			if err != nil {
				return err
			}
			m, err := manifest.Load(path)
			if err != nil {
				return err
			}
			if env.Out.Format == FormatJSON {
				return env.Out.JSON(m)
			}
			env.Out.Raw(manifest.Explain(m))
			return nil
		},
	}
}

func newSupportCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "support",
		Short: "Collect a redacted diagnostic bundle",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "bundle",
		Short: "Write logs, events, the manifest, and doctor output, with the redactor applied",
		Long: strings.TrimSpace(`
The bundle carries a manifest of exactly what it included, so you can see what
you are about to send before you send it.`),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return notYetAvailable("af support bundle")
		},
	})
	return cmd
}

// VersionInfo is the JSON form of af version.
type VersionInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	Edition   string `json:"edition"`
	Go        string `json:"go"`
	Platform  string `json:"platform"`
}

func newVersionCommand(env *Env) *cobra.Command {
	var short bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version, commit, and edition",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info := VersionInfo{
				Version: Version, Commit: Commit, BuildDate: BuildDate, Edition: Edition,
				Go:       runtime.Version(),
				Platform: runtime.GOOS + "/" + runtime.GOARCH,
			}
			if env.Out.Format == FormatJSON {
				return env.Out.JSON(info)
			}
			if short {
				env.Out.Raw(info.Version + "\n")
				return nil
			}
			env.Out.Raw(fmt.Sprintf("antifailure %s (%s edition)\n", info.Version, info.Edition))
			env.Out.Raw(fmt.Sprintf("  commit    %s\n", info.Commit))
			env.Out.Raw(fmt.Sprintf("  built     %s\n", info.BuildDate))
			env.Out.Raw(fmt.Sprintf("  go        %s\n", info.Go))
			env.Out.Raw(fmt.Sprintf("  platform  %s\n", info.Platform))
			return nil
		},
	}
	cmd.Flags().BoolVar(&short, "short", false, "Print only the version number")
	return cmd
}

// commandNames returns every command path in the tree, sorted. The reference
// generator and the completeness test both use it.
func commandNames(root *cobra.Command) []string {
	var out []string
	var walk func(c *cobra.Command, prefix string)
	walk = func(c *cobra.Command, prefix string) {
		name := strings.TrimSpace(prefix + " " + c.Name())
		if c.Name() != "af" {
			out = append(out, name)
		} else {
			name = "af"
		}
		for _, sub := range c.Commands() {
			if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" {
				continue
			}
			walk(sub, name)
		}
	}
	walk(root, "")
	sort.Strings(out)
	return out
}

// ensure the errors import stays used as commands land.
var _ = aferrors.ExitSuccess
