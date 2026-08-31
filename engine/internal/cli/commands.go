package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/extension"
	"github.com/antifailure/antifailure/engine/pkg/schema"
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
			env.Out.Raw(explainSecrets(cmd.Context(), env, m, filepath.Dir(path)))
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

// ensure the errors import stays used as commands land.
var _ = aferrors.ExitSuccess

// explainSecrets says where each declared variable would come from.
//
// The section exists because of one question: somebody sees AF-SEC-001 and
// needs to know where to put the value. Listing the variables a service wants,
// which this already did through the manifest, does not answer that. Saying
// which source would answer for each one, and which would not, does.
//
// Names, sources, and a fingerprint. Never a value. This is printed on a
// terminal somebody may be sharing and goes into support bundles, and a
// fingerprint is enough to answer the other common question, which is whether
// two machines have the same value without either person reading theirs out.
func explainSecrets(ctx context.Context, e *Env, m *schema.Manifest, root string) string {
	declared := secrets.DeclaredVars(m)
	sandbox := secrets.SandboxNames(m)
	if len(declared) == 0 && len(sandbox) == 0 {
		return ""
	}

	// The same chain af up resolves against, including anything an enterprise
	// build registered. Explain has to build the identical chain or it answers
	// a question about a different lookup than the one that will actually
	// happen, which is worse than not answering. Built by one constructor for
	// exactly that reason.
	chain := secrets.LocalChain(root, e.Getenv, extension.Default, e.Keyring())

	resolved, err := secrets.Resolve(ctx, chain, secrets.Request{
		Declared: declared, Sandbox: sandbox, EnvID: "explain",
	})
	if err != nil {
		// A live credential in a sandbox slot stops af up, and explain is
		// exactly where somebody would look to find out why, so it is reported
		// here rather than swallowed.
		return fmt.Sprintf("\nSecrets\n  %v\n", err)
	}

	var b strings.Builder
	b.WriteString("\nSecrets\n")

	isSandbox := map[string]bool{}
	for _, name := range sandbox {
		isSandbox[name] = true
	}

	for _, r := range resolved.Resolutions {
		note := ""
		if isSandbox[strings.Fields(r.Name)[0]] {
			// Worth saying every time. The value going to the sidecar and a
			// marker going to the service is the single most surprising thing
			// about how this works, and somebody who does not know it will
			// spend an hour wondering why their application sees a placeholder.
			note = "  (to the proxy; the service gets a marker)"
		}
		fmt.Fprintf(&b, "  %-28s %s%s\n", r.Name, r.Source, note)
	}
	for _, mi := range resolved.Optional {
		fmt.Fprintf(&b, "  %-28s not set, and not required\n", mi.Name)
	}
	for _, mi := range resolved.Missing {
		fmt.Fprintf(&b, "  %-28s not found\n", mi.Name)
	}

	if len(resolved.Missing) > 0 {
		fmt.Fprintf(&b, "\n  Looked in: %s\n", strings.Join(chain.Considered(ctx), ", "))
	}
	return b.String()
}
