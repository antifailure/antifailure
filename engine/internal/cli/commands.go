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
