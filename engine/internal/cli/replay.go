package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/replay"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/spf13/cobra"
)

// Replay commands do not look up the signed-in account or a production secret.
func replayEngine(e *Env) (*env.Orchestrator, error) {
	path, err := manifest.Find(e.WorkDir)
	if err != nil {
		return nil, err
	}
	m, err := manifest.Load(path)
	if err != nil {
		return nil, err
	}
	return env.New(env.Options{Root: repoRoot(path), Manifest: m, Branch: "replay", Clock: e.Clock, Redactor: e.Redactor, Secrets: secrets.NewChain(), Getenv: func(string) string { return "" }, Progress: func(line string) {
		if e.Out.Format != FormatJSON {
			e.Out.Printf("  %s\n", line)
		}
	}})
}
func replayError(err error) error {
	if err == nil {
		return nil
	}
	return aferrors.Coded(aferrors.AFRPL001, "detail", err.Error())
}

func readReplayFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read the supplied artifact")
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(io.LimitReader(f, replay.MaxBytes+1))
	if len(body) > replay.MaxBytes {
		return nil, fmt.Errorf("artifact exceeds the byte limit")
	}
	return body, err
}

func newIncidentCommand(e *Env) *cobra.Command {
	root := &cobra.Command{Use: "incident", Short: "Inspect captured agent evidence and save an immutable replay scenario"}
	root.AddCommand(&cobra.Command{Use: "import <capture.json>", Short: "Import an SDK capture into this project's local evidence store", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		o, err := replayEngine(e)
		if err != nil {
			return err
		}
		body, err := readReplayFile(args[0])
		if err != nil {
			return replayError(err)
		}
		var incident replay.Incident
		if err = replay.Decode(body, &incident); err != nil {
			return replayError(err)
		}
		if err = incident.Validate(); err != nil {
			return replayError(err)
		}
		if err = o.ReplayStore().ImportIncident(cmd.Context(), incident); err != nil {
			return replayError(err)
		}
		return printReplayValue(e, map[string]any{"runId": incident.RunID, "missing": incident.Missing()})
	}})
	root.AddCommand(&cobra.Command{Use: "list", Short: "List incidents without hiding malformed records", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		o, err := replayEngine(e)
		if err != nil {
			return err
		}
		entries, err := o.ReplayStore().List("incidents")
		if err != nil {
			return replayError(err)
		}
		return printReplayValue(e, entries)
	}})
	root.AddCommand(&cobra.Command{Use: "inspect <id>", Short: "Read retained incident content and missing dependencies", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		o, err := replayEngine(e)
		if err != nil {
			return err
		}
		body, err := o.ReplayStore().Read("incidents", args[0])
		if err != nil {
			return replayError(err)
		}
		var incident replay.Incident
		if err = replay.Decode(body, &incident); err != nil {
			return replayError(err)
		}
		return printReplayValue(e, map[string]any{"incident": incident, "missing": incident.Missing()})
	}})
	var id, golden, endpoint, pointer, original, expected, owner string
	var tables []string
	save := &cobra.Command{Use: "save <id>", Short: "Freeze an incident, a verified golden and distinct failure/fix assertions", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		o, err := replayEngine(e)
		if err != nil {
			return err
		}
		body, err := o.ReplayStore().Read("incidents", args[0])
		if err != nil {
			return replayError(err)
		}
		var incident replay.Incident
		if err = replay.Decode(body, &incident); err != nil {
			return replayError(err)
		}
		if id == "" {
			id = args[0]
		}
		scenario, err := o.SaveIncident(cmd.Context(), incident, id, golden, endpoint, owner, replay.Assertion{Pointer: pointer, Baseline: json.RawMessage(original), Expected: json.RawMessage(expected)}, tables)
		if err != nil {
			return replayError(err)
		}
		return printReplayValue(e, scenario)
	}}
	save.Flags().StringVar(&id, "scenario", "", "Name the immutable scenario")
	save.Flags().StringVar(&golden, "golden", "", "Pin a verified golden; defaults to the capture reference")
	save.Flags().StringVar(&endpoint, "endpoint", "/af-replay", "Explicitly enabled application replay endpoint")
	save.Flags().StringVar(&pointer, "pointer", "", "JSON pointer into the agent outcome")
	save.Flags().StringVar(&original, "original", "", "JSON value that identifies the original failure")
	save.Flags().StringVar(&expected, "expected", "", "JSON value the fix must produce")
	save.Flags().StringSliceVar(&tables, "table", nil, "Relevant database tables to compare")
	save.Flags().StringVar(&owner, "owner", "local", "Owner of the regression case")
	root.AddCommand(save)
	return root
}

func printReplayValue(e *Env, value any) error {
	if e.Out.Format == FormatJSON {
		return e.Out.JSON(value)
	}
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	e.Out.Println(string(body))
	return nil
}
func replayExit(report *replay.Report) error {
	if report.Verdict == "PASS" {
		return nil
	}
	if report.Verdict == "FAIL" {
		return silent(aferrors.Coded(aferrors.AFRPL003))
	}
	return silent(aferrors.Coded(aferrors.AFRPL002, "detail", strings.Join(report.Issues, ", ")))
}
func renderReplay(e *Env, report *replay.Report) error {
	if e.Out.Format == FormatJSON {
		return e.Out.JSON(report)
	}
	e.Out.Printf("%s  %s\n", report.Verdict, report.Scenario)
	e.Out.Printf("  Attempt: %s\n  Fidelity: %s\n", report.ID, report.Fidelity)
	e.Out.Printf("  Baseline reproduced: %t\n  Candidate assertion: %t\n", report.Baseline.Assertion, report.Candidate.Assertion)
	e.Out.Printf("  Teardown: baseline %t, candidate %t\n", report.Baseline.TornDown, report.Candidate.TornDown)
	for _, issue := range report.Issues {
		e.Out.Printf("  %s\n", issue)
	}
	for _, note := range report.Notes {
		e.Out.Printf("  %s\n", note)
	}
	e.Out.Printf("  Evidence: af replay inspect %s --output json\n", report.ID)
	return nil
}
func newReplayCommand(e *Env) *cobra.Command {
	var candidate string
	var reason string
	var timeout time.Duration
	cmd := &cobra.Command{Use: "replay <scenario>", Short: "Reproduce an agent failure, then test a candidate in an independent branch", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		o, err := replayEngine(e)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
		defer cancel()
		report, err := o.Replay(ctx, args[0], candidate)
		if report != nil {
			if printErr := renderReplay(e, report); printErr != nil {
				return printErr
			}
		}
		if err != nil {
			return replayError(err)
		}
		return replayExit(report)
	}}
	cmd.Flags().StringVar(&candidate, "candidate", "HEAD", "Candidate Git revision")
	cmd.Flags().DurationVar(&timeout, "timeout", 20*time.Minute, "Bound setup and replay, excluding required cleanup")
	cmd.AddCommand(&cobra.Command{Use: "inspect <attempt>", Short: "Read a replay attempt and its retained evidence", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		o, err := replayEngine(e)
		if err != nil {
			return err
		}
		body, err := o.ReplayStore().Read("attempts", args[0])
		if err != nil {
			return replayError(err)
		}
		var report replay.Report
		if err = replay.Decode(body, &report); err != nil {
			return replayError(err)
		}
		return printReplayValue(e, report)
	}})
	cmd.AddCommand(&cobra.Command{Use: "recover <attempt>", Short: "Reconcile an interrupted attempt's two environments", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		o, err := replayEngine(e)
		if err != nil {
			return err
		}
		report, err := o.RecoverReplay(cmd.Context(), args[0])
		if err != nil {
			return replayError(err)
		}
		if err = renderReplay(e, report); err != nil {
			return err
		}
		if !report.Baseline.TornDown || !report.Candidate.TornDown {
			return replayExit(report)
		}
		return nil
	}})
	retire := &cobra.Command{Use: "retire <scenario>", Short: "Delete a scenario's unreferenced content and retain its retirement reason", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		o, err := replayEngine(e)
		if err != nil {
			return err
		}
		store := o.ReplayStore()
		if err = store.Retire(cmd.Context(), args[0], reason, e.Clock.Now()); err != nil {
			return replayError(err)
		}
		summary, err := store.RetentionSummary(args[0])
		if err != nil {
			return replayError(err)
		}
		return printReplayValue(e, summary)
	}}
	retire.Flags().StringVar(&reason, "reason", "", "Record why the regression case is retired")
	cmd.AddCommand(retire)
	return cmd
}

func newEvalCommand(e *Env) *cobra.Command {
	var candidate string
	cmd := &cobra.Command{Use: "eval", Short: "Run saved agent incidents as regression cases"}
	run := &cobra.Command{Use: "run <suite.json>", Short: "Run every named scenario and retain each verdict", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		body, err := readReplayFile(args[0])
		if err != nil {
			return replayError(err)
		}
		var suite struct {
			SchemaVersion int      `json:"schemaVersion"`
			Scenarios     []string `json:"scenarios"`
		}
		if err = replay.Decode(body, &suite); err != nil {
			return replayError(err)
		}
		if suite.SchemaVersion != 1 || len(suite.Scenarios) == 0 || len(suite.Scenarios) > 100 {
			return replayError(fmt.Errorf("suite must name between one and 100 scenarios"))
		}
		o, err := replayEngine(e)
		if err != nil {
			return err
		}
		reports := []*replay.Report{}
		var exit error
		for _, id := range suite.Scenarios {
			report, runErr := o.Replay(cmd.Context(), id, candidate)
			if runErr != nil {
				return replayError(runErr)
			}
			reports = append(reports, report)
			if report.Verdict != "PASS" {
				exit = replayExit(report)
			}
		}
		if err = printReplayValue(e, reports); err != nil {
			return err
		}
		return exit
	}}
	run.Flags().StringVar(&candidate, "candidate", "HEAD", "Candidate Git revision for every saved case")
	cmd.AddCommand(run)
	return cmd
}
