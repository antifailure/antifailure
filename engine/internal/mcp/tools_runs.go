package mcp

import (
	"context"
)

// runIDSchema is shared, so that both tools describe the identifier the same
// way and neither can describe it as something it is not.
//
// The pattern is what this server mints and nothing else. It is a cheap
// rejection of a caller that passes a path, an environment name or a SQL
// fragment where a run id belongs, before any of it reaches the store.
func runIDSchema() *Schema {
	return &Schema{
		Type: "string", MaxLength: 64, MinLength: 8, Pattern: `run_[0-9a-f]{32}`,
		Description: "The run_id returned when the rehearsal was submitted. " +
			"It identifies a run and authorises nothing: a run belonging to another " +
			"project or client reads as not found.",
	}
}

// idempotencyKeySchema is shared by every submitting tool.
func idempotencyKeySchema() *Schema {
	return &Schema{
		Type: "string", MaxLength: 200, MinLength: 1,
		Description: "Optional. Repeat a submission safely: the same key with the same " +
			"arguments returns the run already started rather than starting a second " +
			"one. The same key with different arguments is refused, so use a new key " +
			"for a new experiment.",
	}
}

// newGetRunTool builds get_rehearsal_run.
func newGetRunTool(p *Project, store *Store) *Tool {
	return &Tool{
		Name:     "get_rehearsal_run",
		Title:    "Read a rehearsal",
		ReadOnly: true,
		Description: "Read the status and, once it has finished, the verdict of a rehearsal " +
			"submitted earlier. Poll this after submitting one. " +
			"The verdict is PASS, FAIL or INCONCLUSIVE, and INCONCLUSIVE means the " +
			"experiment did not finish, so it says nothing about the change: it is never " +
			"a weaker PASS. Evidence references are paginated; pass the next_cursor from " +
			"one response as evidence_cursor to read the next page.",
		Input: &Schema{
			Type:     "object",
			Required: []string{"run_id"},
			Properties: map[string]*Schema{
				"run_id":     runIDSchema(),
				"project_id": projectIDSchema(),
				"evidence_cursor": {
					Type: "string", MaxLength: 256,
					Description: "Optional. The next_cursor from a previous response, to read " +
						"the next page of evidence references.",
				},
			},
		},
		Handler: func(ctx context.Context, call *Call, args map[string]any) (any, *Fault) {
			if fault := p.checkAssertion(args); fault != nil {
				return nil, fault
			}
			id, _ := args["run_id"].(string)
			cursor, _ := args["evidence_cursor"].(string)

			run, fault := store.Get(ctx, call.Caller, p.ID, id)
			if fault != nil {
				return nil, fault
			}
			return view(run, cursor)
		},
	}
}

// newCancelRunTool builds cancel_rehearsal_run.
func newCancelRunTool(p *Project, store *Store) *Tool {
	return &Tool{
		Name:  "cancel_rehearsal_run",
		Title: "Cancel a rehearsal",
		// Not read only: it changes what the server is doing. It is not
		// destructive either, because stopping an experiment removes the
		// environment it made and touches nothing the caller owns.
		ReadOnly: false,
		Description: "Ask a running rehearsal to stop. The experiment stops at the next " +
			"point it can do so safely and tears down the environment it created, so " +
			"cancelling is a request rather than an immediate kill. A cancelled run is " +
			"reported INCONCLUSIVE, because an experiment that did not finish says " +
			"nothing about the change. A run that has already finished cannot be " +
			"cancelled and keeps the verdict it reached.",
		Input: &Schema{
			Type:     "object",
			Required: []string{"run_id"},
			Properties: map[string]*Schema{
				"run_id":     runIDSchema(),
				"project_id": projectIDSchema(),
				"reason": {
					Type: "string", MaxLength: 500,
					Description: "Optional. Why the run is being cancelled, for the server log.",
				},
			},
		},
		Handler: func(ctx context.Context, call *Call, args map[string]any) (any, *Fault) {
			if fault := p.checkAssertion(args); fault != nil {
				return nil, fault
			}
			id, _ := args["run_id"].(string)

			run, fault := store.RequestCancel(ctx, call.Caller, p.ID, id)
			if fault != nil {
				return nil, fault
			}
			return map[string]any{
				"kind":   "cancellation_requested",
				"run_id": run.ID,
				"status": run.Status,
				"phase":  run.Phase,
				"note": "The run was asked to stop. It tears down its environment before " +
					"settling, so poll get_rehearsal_run until the status is cancelled.",
			}, nil
		},
	}
}
