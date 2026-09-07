package env

import (
	"context"

	"github.com/antifailure/antifailure/engine/internal/invariant"
	"github.com/antifailure/antifailure/engine/internal/redact"
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

	// A read of the branch, so no lock. af test takes none either, and an
	// invariant check that waited on af down would hold up the teardown to
	// ask about data that is about to be gone.
	s, err := o.openReading(ctx)
	if err != nil {
		return nil, err
	}
	defer s.close()

	conn, err := connectSession(ctx, o, s)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	return InvariantResults(invariant.Run(ctx, conn, invs, invariant.Options{}),
		o.opts.Redactor), nil
}

// InvariantResults carries what the statements said into the shape the report
// and the JSON output read.
//
// Exported and separate from RunInvariants, which needs a live environment,
// so that a suite pointing a real Postgres at a real invariant can drive the
// production translation rather than writing its own beside the assertion. A
// translation written beside the assertion agrees with itself whatever the
// product does, which is the failure engine/internal/cli/saysno_test.go
// exists to keep out of the one report a customer reads.
//
// Held and Err stay apart here, the same as everywhere else: an invariant
// that could not be asked has not found anything.
func InvariantResults(summary invariant.Summary, red *redact.Redactor) []InvariantResult {
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
			res.Error = red.String(r.Err.Error())
		}
		out = append(out, res)
	}
	return out
}
