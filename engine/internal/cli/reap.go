package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/lease"
)

// ReapJSON is one environment a sweep dealt with.
type ReapJSON struct {
	EnvID     string  `json:"env_id"`
	ExpiresAt string  `json:"expires_at"`
	OverdueH  float64 `json:"overdue_hours"`
	Resources int     `json:"resources"`
	Removed   int     `json:"removed"`
	// Outcome is one of removed, deferred, failed, or would-remove.
	Outcome string `json:"outcome"`
	// Detail is why, for anything that is not "removed".
	Detail string `json:"detail,omitempty"`
	// Extended reports that the expiry came from af env extend.
	Extended bool `json:"extended"`
}

// ReapSummaryJSON is the whole sweep.
type ReapSummaryJSON struct {
	Scanned      int        `json:"scanned"`
	Expired      int        `json:"expired"`
	Removed      int        `json:"removed"`
	Resources    int        `json:"resources_removed"`
	Deferred     int        `json:"deferred"`
	Failed       int        `json:"failed"`
	DryRun       bool       `json:"dry_run"`
	Environments []ReapJSON `json:"environments"`
}

// af env reap is the command that makes runtime.ttl mean something.
//
// It is separate from af env prune, and the difference is who chose the
// cutoff. prune takes one from the person running it and applies it to
// everything on the machine, which is the right shape for "this laptop is
// full". reap applies each environment's OWN stated lifetime, which is the
// only shape that is safe to run unattended and on a machine holding more than
// one project's environments.
func newEnvReapCommand(e *Env) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "reap",
		Short: "Remove the environments whose lifetime has ended",
		Long: strings.TrimSpace(`
Removes every environment on this machine that has passed the lifetime it was
created with, and nothing else.

The lifetime is read off each environment's own resources, stamped there when
it was created from that repository's runtime.ttl. It is never taken from the
manifest this command was run with, so a repository with a two hour lifetime
cannot remove another project's week long environment on the same machine.

Three things are never removed. An environment whose resources state no
lifetime, which is everything created before this feature existed: use
'af env prune --older-than' for those, where a person names the cutoff. An
environment something is running against, which is deferred to the next sweep
rather than pulled out from under a command. And anything that is not an
environment, such as the shared sidecar image.

An environment you are still using can be kept with 'af env extend'.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := orchestrator(e, "", false)
			if err != nil {
				return err
			}
			result, err := o.Reap(cmd.Context(), dryRun)
			if err != nil {
				return err
			}
			return reportReap(e, result, dryRun)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"Print what would be removed without removing it")
	return cmd
}

func reportReap(e *Env, result *env.ReapResult, dryRun bool) error {
	deferredBy := make(map[string]env.Deferred, len(result.Deferred))
	for _, d := range result.Deferred {
		deferredBy[d.EnvID] = d
	}

	docs := make([]ReapJSON, 0, len(result.Outcomes))
	removed, failed := 0, 0
	for _, out := range result.Outcomes {
		doc := ReapJSON{
			EnvID: out.EnvID, ExpiresAt: out.ExpiresAt.UTC().Format(time.RFC3339),
			OverdueH: out.Overdue.Hours(), Resources: out.Resources,
			Removed: out.Removed, Extended: out.Extended, Outcome: "removed",
		}
		switch {
		case dryRun:
			doc.Outcome = "would-remove"
		case errors.Is(out.Err, env.ErrInUse):
			doc.Outcome = "deferred"
			d := deferredBy[out.EnvID]
			doc.Detail = fmt.Sprintf("%s is running against it", holderLabel(d))
		case out.Err != nil:
			doc.Outcome, doc.Detail = "failed", out.Err.Error()
			failed++
		default:
			removed++
		}
		docs = append(docs, doc)
	}

	summary := ReapSummaryJSON{
		Scanned: result.Scanned, Expired: len(result.Outcomes), Removed: removed,
		Resources: result.Removed(), Deferred: len(result.Deferred), Failed: failed,
		DryRun: dryRun, Environments: docs,
	}
	if e.Out.Format == FormatJSON {
		if err := e.Out.JSON(summary); err != nil {
			return err
		}
		return reapExit(summary)
	}

	if len(docs) == 0 {
		e.Out.Printf("Nothing has expired. %d environments on this machine.\n", result.Scanned)
		return nil
	}
	rows := make([][]string, 0, len(docs))
	for _, d := range docs {
		note := d.Detail
		if note == "" && d.Extended {
			note = "extended"
		}
		rows = append(rows, []string{
			d.EnvID, humanAge(time.Duration(d.OverdueH * float64(time.Hour))),
			fmt.Sprint(d.Removed), d.Outcome, note,
		})
	}
	e.Out.Table([]Column{
		Col("ENVIRONMENT"), Num("OVERDUE"), Num("REMOVED"), Col("OUTCOME"), Flex("NOTE"),
	}, rows)
	e.Out.Println("")
	if dryRun {
		e.Out.Printf("  %d environments would be removed. Run without --dry-run to do it.\n",
			len(docs))
		return nil
	}
	e.Out.Printf("  %d of %d environments removed, %d resources.\n",
		removed, result.Scanned, result.Removed())
	if summary.Deferred > 0 {
		// Said out loud rather than left to be inferred from a table, because
		// "the reaper ran and the environment is still there" is otherwise a
		// bug report.
		e.Out.Printf("  %d were left alone because something is running against them. "+
			"They are still expired and the next sweep takes them.\n", summary.Deferred)
	}
	return reapExit(summary)
}

func holderLabel(d env.Deferred) string {
	if d.Holder == "" {
		return "another process"
	}
	if d.PID == 0 {
		return d.Holder
	}
	return fmt.Sprintf("%s (pid %d)", d.Holder, d.PID)
}

// reapExit turns a sweep that could not finish into a non-zero exit.
//
// A deferral is not a failure and does not appear here: nothing went wrong,
// the environment is still expired, and the next sweep takes it. A teardown
// that errored is a failure, because something on this machine is now neither
// removed nor accounted for.
func reapExit(s ReapSummaryJSON) error {
	if s.Failed > 0 {
		return aferrors.Coded(aferrors.AFRUN030, "count", fmt.Sprint(s.Failed))
	}
	return nil
}

// af env extend is the other half of the TTL: the way to say "I am using this"
// without the answer being "then it lives forever".
func newEnvExtendCommand(e *Env) *cobra.Command {
	var forDur time.Duration
	var reason string
	cmd := &cobra.Command{
		Use:   "extend <environment>",
		Short: "Keep an environment past its lifetime, up to its maximum",
		Long: strings.TrimSpace(`
Moves an environment's expiry, so a sweep does not take one you are still
using.

There is a bound, and it is the point. No extension may take an environment
past runtime.max_ttl measured from when it was CREATED, not from now, so
extending repeatedly cannot walk the limit forward. Asking for more than the
maximum grants the maximum and says so rather than failing, because being given
less time than you asked for silently is how you come back to an environment
that is gone.`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o, err := orchestrator(e, "", false)
			if err != nil {
				return err
			}
			until := e.Clock.Now().UTC().Add(forDur)
			got, err := o.Extend(cmd.Context(), args[0], until, reason)
			clamped := errors.Is(err, lease.ErrPastCeiling)
			if err != nil && !clamped {
				return err
			}

			if e.Out.Format == FormatJSON {
				return e.Out.JSON(map[string]any{
					"env_id":     got.EnvID,
					"expires_at": got.ExpiresAt.Format(time.RFC3339),
					"ceiling_at": got.CeilingAt.Format(time.RFC3339),
					"reason":     got.Reason,
					"at_ceiling": got.AtCeiling(),
					"clamped":    clamped,
				})
			}
			if clamped {
				e.Out.Printf("%s now expires at %s, which is its maximum lifetime.\n",
					got.EnvID, got.ExpiresAt.Format(time.RFC3339))
				e.Out.Println("  That is less than you asked for. Raise runtime.max_ttl " +
					"in the manifest if this environment genuinely needs longer.")
				return nil
			}
			e.Out.Printf("%s now expires at %s.\n",
				got.EnvID, got.ExpiresAt.Format(time.RFC3339))
			e.Out.Printf("  It cannot be extended past %s.\n",
				got.CeilingAt.Format(time.RFC3339))
			return nil
		},
	}
	cmd.Flags().DurationVar(&forDur, "for", 4*time.Hour,
		"How long from now the environment should live")
	cmd.Flags().StringVar(&reason, "reason", "",
		"Why, recorded with the extension")
	return cmd
}
