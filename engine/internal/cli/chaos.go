package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/gate"
	"github.com/antifailure/antifailure/engine/internal/report"
)

// ChaosJSON is the machine readable result of breaking the environment.
type ChaosJSON struct {
	Held     bool                `json:"held"`
	Verified bool                `json:"verified"`
	Faults   []report.ChaosFault `json:"faults"`
	Findings []report.Finding    `json:"findings,omitempty"`
	Skipped  string              `json:"skipped,omitempty"`
}

func newChaosCommand(e *Env) *cobra.Command {
	var branch string
	cmd := &cobra.Command{
		Use:   "chaos",
		Short: "Break this environment on purpose and prove the recovery",
		Long: strings.TrimSpace(`
Injects the faults the manifest's chaos block declares into the running
environment, one at a time, and reads what the system did about each one.

The faults are real. A process is killed with SIGKILL, a container is stopped,
a container is frozen, a container is detached from the network, a data
directory is made read only. Nothing is simulated, and nothing is aimed
anywhere but at the containers this environment created: a target is resolved
from the labels the runtime stamped at create time, the ownership is proved
again from the daemon at the instant of the act, and the egress sidecar is
refused whatever a fault asks for, because a fault that can stop the thing
deciding where the environment may connect is a way out rather than an outage.

Around a fault aimed at the database, the durability proof runs. Concurrent
writers commit into a schema of the engine's own while the fault lands, and
afterwards every commit the client was told was committed must still be there
and nothing may be there that no client ever wrote. That needs a record the
database cannot provide, because the claim is about what the database SAID,
and the write ahead log is then read for the evidence that it actually
replayed: the position recovery started from, against the one the control file
named before the crash, and the position it reached, against the last flush a
writer saw.

Anything that could not be established is reported as unverified rather than as
a pass. A fault that was applied and changed nothing is refused, because every
assertion after it would be measuring a system that never broke.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, m, err := orchestratorWithManifest(e, branch)
			if err != nil {
				return err
			}
			if m.Chaos == nil || !m.Chaos.Enabled {
				if e.Out.Format == FormatJSON {
					return e.Out.JSON(ChaosJSON{Held: true, Verified: true,
						Skipped: "this manifest declares no chaos block, or declares one that is off"})
				}
				e.Out.Println("")
				e.Out.Empty(
					"This manifest declares no faults. They are the failures a rehearsal injects "+
						"into its own environment so that a recovery can be proved rather than hoped for, "+
						"and they are described at https://antifailure.dev/docs/guides/chaos", "", "")
				return nil
			}

			// Named policy rather than gate, because engine/internal/gate is
			// the package that decides what the findings MEAN and the two
			// cannot share a name in one function.
			policy := report.Configure(m.Policy)
			run, err := o.RunChaos(cmd.Context(), policy)
			if err != nil {
				return err
			}
			if run == nil {
				return nil
			}

			held, verified := gate.ChaosHolds(run.Findings)
			if e.Out.Format == FormatJSON {
				if err := e.Out.JSON(ChaosJSON{
					Held: held, Verified: verified, Faults: run.Report.Faults,
					Findings: run.Findings, Skipped: run.Report.Skipped,
				}); err != nil {
					return err
				}
				if !held {
					return silent(chaosFailure(run.Findings))
				}
				return nil
			}

			e.Out.Section("Breaking it on purpose")
			printChaos(e, run)
			if !held {
				return silent(chaosFailure(run.Findings))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "Branch to break, defaulting to the checked out one")
	return cmd
}

// printChaos renders a run for a terminal.
func printChaos(e *Env, run *env.ChaosRun) {
	if run.Report.Skipped != "" {
		e.Out.Println("")
		e.Out.Note(StyleDim, "No fault was injected: "+run.Report.Skipped)
		return
	}
	for _, f := range run.Report.Faults {
		e.Out.Println("")
		switch {
		case f.Error != "":
			e.Out.Status(SymbolSkip, f.Name, f.Kind+" on "+f.Target)
			e.Out.Note(StyleDim, "Could not inject: "+f.Error)
			continue
		case !f.Injected:
			e.Out.Status(SymbolSkip, f.Name, "did not run")
			continue
		}
		e.Out.Status(SymbolOK, f.Name, f.Kind+" on "+f.Target)
		e.Out.Note(StyleDim, f.Evidence)
		rec := f.Recovery
		if rec == nil {
			continue
		}
		e.Out.Printf("      crash          %s\n", crashLine(rec))
		e.Out.Printf("      replay         %s\n", replayLine(rec))
		e.Out.Printf("      commits        %d acknowledged, %d lost, %d phantom, %d in flight landed\n",
			rec.Acknowledged, rec.Lost, rec.Phantom, rec.InFlightLanded)
		e.Out.Printf("      relations      heap %d, index %d\n", rec.HeapRows, rec.IndexRows)
		e.Out.Printf("      unreachable    %dms\n", rec.DowntimeMs)
	}

	e.Out.Println("")
	for _, f := range run.Findings {
		if f.Level == report.LevelIgnore {
			continue
		}
		symbol := SymbolWarn
		if f.Level == report.LevelFail {
			symbol = SymbolFail
		}
		e.Out.Status(symbol, f.Rule, f.Title)
		e.Out.Note(StyleDim, f.Detail)
	}
	if len(run.Findings) == 0 {
		e.Out.Println("  Nothing was lost and nothing was invented.")
	}
}

// crashLine says whether the database crashed.
func crashLine(rec *report.ChaosRecovery) string {
	if !rec.Crashed {
		return "no process was killed by a signal"
	}
	return fmt.Sprintf("a server process was killed by signal %d", rec.Signal)
}

// replayLine says how far replay reached.
func replayLine(rec *report.ChaosRecovery) string {
	if !rec.Replayed {
		return "the log records no replay"
	}
	return rec.RedoStart + " to " + rec.RedoEnd
}

// chaosFailure is the error a failing run exits with.
//
// The catalog code rather than a bare exit, because a script that branches on
// the code has to be able to tell a lost commit from a fault that would not go
// in, and those are two codes with two exits.
func chaosFailure(findings []report.Finding) error {
	for _, f := range findings {
		if f.Level != report.LevelFail {
			continue
		}
		return gateError(f)
	}
	return nil
}

// chaosPrefix is what every rule this feature raises starts with, the way
// every dynamic security rule starts with security.
const chaosPrefix = "chaos."
