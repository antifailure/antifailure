package cli

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/oracle"
)

// af oracle is the only way to reach the comparison, and that is the point.
//
// A capability nothing invokes is a shippable gap that looks like a feature,
// and this repository has shipped that four times. The comparison engine, the
// second environment and this command land together, and the walkthrough in the
// documentation runs this exact command against examples/go-api.

func newOracleCommand(e *Env) *cobra.Command {
	var branch, baseRef, output, failOn string
	var keep bool
	cmd := &cobra.Command{
		Use:   "oracle",
		Short: "Run this change beside the version it is replacing and diff what they did",
		Long: strings.TrimSpace(`
Brings a second environment up from a baseline revision, branches the same
golden for both so they start from identical rows, sends both the same requests
in the same order, and reports every difference in what came back and in what
ended up in the database.

Responses and database contents are compared. Events, outbound effects, traces
and query plans are not: two comparisons done completely are worth more than six
done shallowly, because the first one that cries wolf is the last one anybody
looks at.

Values that no two runs can agree on are normalised before they are compared:
two timestamps within an hour, two UUIDs, two numbers within a relative
tolerance. Everything the comparison declined to look at is printed, defaults
included, because an oracle that silently ignores a field is worse than one that
reports it.

The candidate environment is left running whether or not this command brought it
up. The baseline is torn down unless --keep says otherwise.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, m, err := orchestratorWithManifest(e, branch)
			if err != nil {
				return err
			}
			if m.Oracle == nil {
				return aferrors.Coded(aferrors.AFORC001)
			}
			// A block that is present and off is a different answer from no
			// block at all: it is somebody who wrote a probe plan and turned
			// the check off, and failing their pipeline for it would be wrong.
			if m.Oracle.Enabled != nil && !*m.Oracle.Enabled {
				e.Out.Status(e.Out.S(StyleDim, SymbolSkip), "the manifest turns the oracle off",
					"set oracle.enabled to true to run it")
				return nil
			}

			threshold, ok := oracle.ParseSeverity(orDefaultSeverity(failOn, m.Oracle.FailOn))
			if !ok {
				return aferrors.Coded(aferrors.AFMAN002,
					"path", "oracle.fail_on",
					"detail", "use none, minor, major, or critical")
			}

			e.Out.Section("Comparing against the baseline")
			res, err := o.Oracle(cmd.Context(), env.OracleOptions{
				BaseRef: baseRef, Keep: keep,
				Progress: func(name string, index, total int) {
					e.Out.Printf("  %d/%d %s\n", index, total, name)
				},
			})
			// The report is printed before any error is returned. A comparison
			// that got as far as the probes and then failed to read a branch
			// has still found everything the responses had to say, and throwing
			// that away because a later step failed would discard the more
			// expensive half of the run.
			if res != nil && res.Result != nil {
				writeOracleReport(e, res.Result, output)
			}
			if err != nil {
				if res != nil && !res.BaselineTornDown && !keep {
					e.Out.Printf("  the baseline environment may still be up. Remove it with: "+
						"af down --branch %q\n", res.BaselineBranch)
				}
				return err
			}

			if oracle.AtLeast(res.Findings, threshold) {
				return silent(aferrors.Coded(aferrors.AFORC010, "detail",
					strings.Join(oracle.KindCounts(res.Findings), ", ")))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "Branch to compare, defaulting to the checked out one")
	cmd.Flags().StringVar(&baseRef, "baseline", "", "Revision to compare against, overriding oracle.base_ref")
	cmd.Flags().StringVarP(&output, "output", "o", "", "Write the report here as well as to the terminal")
	cmd.Flags().StringVar(&failOn, "fail-on", "",
		"Lowest severity that fails the command: none, minor, major, or critical")
	cmd.Flags().BoolVar(&keep, "keep", false, "Leave the baseline environment up, for looking at a difference")
	return cmd
}

// orDefaultSeverity prefers the flag over the manifest.
func orDefaultSeverity(flag, manifest string) string {
	if flag != "" {
		return flag
	}
	return manifest
}

// writeOracleReport prints the comparison and writes it where a workflow can
// post it.
//
// The same three destinations af ci writes to, because a comparison belongs on
// the pull request beside everything else the run found. The markdown carries
// the marker so an update replaces the previous comment rather than adding a
// thirteenth.
func writeOracleReport(e *Env, res *oracle.Result, output string) {
	if output != "" {
		if err := os.WriteFile(output, []byte(res.Comment()), 0o644); err != nil {
			e.Out.Printf("  could not write the report to %s: %v\n", output, err)
		}
	}
	if summary := e.Getenv("GITHUB_STEP_SUMMARY"); summary != "" {
		if f, err := os.OpenFile(summary, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644); err == nil {
			_, _ = f.WriteString(res.Markdown())
			_ = f.Close()
		}
	}
	if e.Out.Format == FormatJSON {
		_ = e.Out.JSON(res)
		return
	}
	e.Out.Println("")
	e.Out.Raw(res.Text())
}
