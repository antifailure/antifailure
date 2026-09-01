package cli

import (
	"context"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/antifailure/antifailure/engine/internal/change"
	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/report"
)

// af change is the sentence that makes the rest of this product's cost
// defensible.
//
// Everything else here runs the same checks on every pull request, which is
// safe and wasteful in the same breath: a change to a README does not need a
// database, and a change that adds a column and edits the billing service
// deserves more attention than the default. This command reads the diff and
// says which checks will exercise what it touched, naming the file and the
// rule behind every line of it.
//
// What it deliberately does not do is grade the change. There is no score and
// no risk word anywhere in the output, because a tool that says a change is
// safe is making a promise this product's own terms refuse to make, and it
// would be making it from a file listing.

func newChangeCommand(e *Env) *cobra.Command {
	var branch, base, head, diff, output string
	cmd := &cobra.Command{
		Use:   "change",
		Short: "Read the diff and say which checks will exercise what it touched",
		Long: strings.TrimSpace(`
What this change touches, and which checks cover it.

Every changed path is classified by a rule that names it, and every check is
reported as selected or not, together with whether the manifest configures it
at all. A check that is selected and unavailable is the line worth reading:
something changed and nothing is going to look at it.

Two things it will not do. It never says a change is safe or risky; it says
which checks exercise which files, and what it cannot see. And a path no rule
recognises selects every check rather than none, because the cost of the two
mistakes is not the same.

In a GitHub Actions job it writes one output per check, so a later step can
skip work this change does not need.

This is the one command that does not need antifailure.yaml. Without one it
still says what the diff touches, and reports every check as unavailable
because nothing is configured to run it.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			profile, err := changeProfile(cmd.Context(), e, branch,
				env.ChangeOptions{Base: base, Head: head, DiffPath: diff, Getenv: e.Getenv})
			if err != nil {
				return err
			}

			if output != "" {
				// The same marker af ci's report carries, so a workflow that
				// writes this one and then overwrites it with the full report
				// updates one comment rather than leaving two.
				body := report.Marker + "\n" + profile.Markdown()
				if wErr := os.WriteFile(output, []byte(body), 0o644); wErr != nil {
					e.Out.Printf("  could not write the section to %s: %v\n", output, wErr)
				}
			}
			writeChangeOutputs(e, profile)
			// Beside the plan, because this step is the one whose file gets
			// posted when nothing else runs, so its own comment step needs the
			// same answer af ci gives.
			announceComment(e)

			if e.Out.Format == FormatJSON {
				return e.Out.JSON(profile)
			}
			e.Out.Section("What this change touches")
			e.Out.Raw(profile.Explain())
			return nil
		},
	}
	cmd.Flags().StringVar(&base, "base", "", "Ref to measure against, defaulting to this job's base branch")
	cmd.Flags().StringVar(&head, "head", "", "Ref to measure, defaulting to HEAD")
	cmd.Flags().StringVar(&diff, "diff", "", "Read a unified diff from this file instead of asking git")
	cmd.Flags().StringVarP(&output, "write", "w", "", "Write the report section here as markdown")
	cmd.Flags().StringVar(&branch, "branch", "", "Branch to read the manifest for, defaulting to the checked out one")
	return cmd
}

// changeProfile reads and classifies the diff, with or without a manifest.
//
// Every other command in this product needs antifailure.yaml, because every
// other command builds something out of it. This one does not: the diff is in
// git, and what it touches is a fact about the repository rather than about
// the configuration. So a missing manifest is not an error here, it is the
// answer to a different question, and the report already has a sentence for
// it against every check: nothing is configured, so nothing will look.
//
// That matters most in the place somebody meets this product first. Running af
// change in an unconfigured repository and being shown what the tool can see,
// next to seven checks that all say no manifest was loaded, is a better
// argument for writing one than AF-MAN-001 is.
//
// Any other failure loading the manifest is still an error. A manifest that
// exists and does not parse must not quietly downgrade to none, because the
// output would then say no checks are configured to somebody who configured
// them and has a typo.
func changeProfile(ctx context.Context, e *Env, branch string, opts env.ChangeOptions) (*change.Profile, error) {
	o, err := orchestrator(e, branch, false)
	if err == nil {
		return o.Change(ctx, opts)
	}

	var coded *aferrors.Error
	if !aferrors.As(err, &coded) || coded.Entry.Code != aferrors.AFMAN001 {
		return nil, err
	}
	return change.ForRepo(ctx, nil, change.Source{
		Root:     gitRoot(e.WorkDir),
		Base:     opts.Base,
		Head:     opts.Head,
		DiffPath: opts.DiffPath,
		Getenv:   opts.Getenv,
	})
}

// gitRoot asks git where the repository starts, since without a manifest
// there is no other landmark to measure paths from. It falls back to the
// working directory, which is right for a diff handed in with --diff and
// wrong in a way that shows up immediately for one read out of git.
func gitRoot(dir string) string {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return dir
	}
	if root := strings.TrimSpace(string(out)); root != "" {
		return root
	}
	return dir
}

// writeChangeOutputs makes the plan actionable inside a GitHub Actions job.
//
// Printing a plan a workflow cannot read would leave this command as a comment
// on the run rather than an input to it, and the whole argument for reading a
// diff is that a step can then be skipped. One boolean per check, plus the
// selected list, so a job writes `if: steps.change.outputs.environment ==
// 'true'` and nothing has to parse prose.
//
// The value is selected AND available, because a step asking whether to do
// work needs both: a check the change selects and the manifest has turned off
// is a sentence for the report, not an instruction to a runner.
//
// The keys are the check names, which are stable, and every check is written
// on every run including the ones that are false. A key that appears only when
// it is true reads as an empty string in a workflow expression, and an empty
// string is not false to somebody debugging at eleven at night.
func writeChangeOutputs(e *Env, p *change.Profile) {
	path := e.Getenv("GITHUB_OUTPUT")
	if path == "" {
		return
	}
	var b strings.Builder
	for _, s := range p.Plan {
		value := "false"
		if s.Run() {
			value = "true"
		}
		b.WriteString(string(s.Check) + "=" + value + "\n")
	}

	// Runnable and not Selected, because a step reads this to decide whether
	// to do work. The two differ exactly when the manifest has a check turned
	// off, and that is the case where naming it here would tell a runner to
	// run something nothing is configured for.
	var selected []string
	for _, c := range p.Runnable() {
		selected = append(selected, string(c))
	}
	sort.Strings(selected)
	b.WriteString("selected=" + strings.Join(selected, ",") + "\n")

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		e.Out.Printf("  could not write the plan to GITHUB_OUTPUT: %v\n", err)
		return
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(b.String()); err != nil {
		e.Out.Printf("  could not write the plan to GITHUB_OUTPUT: %v\n", err)
	}
}
