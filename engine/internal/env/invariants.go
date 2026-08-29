package env

import (
	"context"

	"github.com/antifailure/antifailure/engine/internal/invariant"
)

// InvariantResult is one invariant's outcome, in the shape the report and the
// JSON output carry.
//
// Error is a string rather than an error because this crosses a JSON boundary.
// It is the filled message, so a reader sees "AF-AGT-011 Invariant sneaky is
// not read only" rather than a wrapped chain nobody asked for.
type InvariantResult struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Held        bool       `json:"held"`
	Columns     []string   `json:"columns,omitempty"`
	Rows        [][]string `json:"rows,omitempty"`
	More        bool       `json:"more,omitempty"`
	Error       string     `json:"error,omitempty"`
	DurationMs  int64      `json:"durationMs"`
}

// Violated reports whether this invariant was shown to be broken.
func (r InvariantResult) Violated() bool { return r.Error == "" && !r.Held }

// RunInvariants asks every invariant the manifest declares of the
// environment's own database.
//
// Against the environment's database rather than a branch of it, which is the
// opposite of what insights does and is deliberate. A migration rehearsal has
// to run somewhere the migrations have not already been applied, so it needs
// its own branch. An invariant is a question about the data the workflows just
// touched, so a branch would answer a question about different data: the whole
// point is to catch a flow that appeared to succeed while corrupting the rows
// it wrote. Reading them is safe because the transaction is READ ONLY, which
// the database enforces rather than this package promising it.
func (o *Orchestrator) RunInvariants(ctx context.Context) ([]InvariantResult, error) {
	invs := o.opts.Manifest.Invariants
	if len(invs) == 0 {
		return nil, nil
	}

	s, err := o.open(ctx, "af test invariants")
	if err != nil {
		return nil, err
	}
	defer s.close()

	conn, err := connectSession(ctx, o, s)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	summary := invariant.Run(ctx, conn, invs, invariant.Options{})
	out := make([]InvariantResult, 0, len(summary.Results))
	for _, r := range summary.Results {
		res := InvariantResult{
			Name:        r.Name,
			Description: r.Description,
			Held:        r.Held,
			Columns:     r.Columns,
			Rows:        r.Rows,
			More:        r.More,
			DurationMs:  r.Duration.Milliseconds(),
		}
		if r.Err != nil {
			res.Error = o.opts.Redactor.String(r.Err.Error())
		}
		out = append(out, res)
	}
	return out, nil
}
