package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/antifailure/antifailure/engine/internal/crossstore"
	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// af mask crossstore, which is the command the guarantee did not have.
//
// The check behind it has existed since the masking dialects landed and had
// zero production callers. So the sentence a customer was given, that the same
// person is masked identically in their Postgres and in their ClickHouse, was
// verified in our continuous integration on our fixtures, and on their stack
// it was a claim. This is the surface, and it follows af golden verify: one
// subject, a verdict, the coverage under it, and a non zero exit when the
// answer is no.

// CrossStoreJSON is the machine readable report.
type CrossStoreJSON struct {
	// OK is the whole verdict, and it is false both when a pair disagreed and
	// when there was nothing to compare. A reader scripting against this must
	// not have to know that the second is not a pass.
	OK        bool   `json:"ok"`
	Summary   string `json:"summary"`
	Checked   int    `json:"join_keys_checked"`
	Identical int    `json:"join_keys_identical"`
	// Percent is absent rather than zero when nothing was checked, because
	// zero percent and no comparison are different facts and a number that
	// renders as 0.0 for both is the defect this command was written against.
	Percent  *float64            `json:"percent"`
	Stores   []string            `json:"stores_read"`
	Unread   []crossstore.Unread `json:"stores_unread,omitempty"`
	NoSource []string            `json:"stores_without_source_url_env,omitempty"`
	Declared []string            `json:"stores_declared"`
	Tables   int                 `json:"tables"`
	Columns  int                 `json:"columns"`
	// SkippedTables names the tables a reader deliberately did not return. A
	// share of the join keys a reader felt like returning is a true answer to
	// a smaller question, and this is what tells a reader which it is.
	SkippedTables []string `json:"skipped_tables,omitempty"`
	// RowsRead is always zero, and it is a field rather than a sentence in the
	// description so that somebody deciding whether this is safe to point at
	// production can read the answer instead of trusting the prose.
	RowsRead   int                  `json:"rows_read"`
	RulesHash  string               `json:"rules_hash,omitempty"`
	Mismatches []CrossStorePairJSON `json:"mismatches,omitempty"`
}

// CrossStorePairJSON is one candidate join key that did not agree.
type CrossStorePairJSON struct {
	Key    string `json:"key"`
	A      string `json:"a"`
	B      string `json:"b"`
	Reason string `json:"reason"`
}

func newMaskCrossStoreCommand(e *Env) *cobra.Command {
	var branch string
	cmd := &cobra.Command{
		Use:   "crossstore",
		Short: "Check that one person masks to the same person in every store",
		Long: strings.TrimSpace(`
Determinism inside one store has been enforced since the beginning, by the key
derivation. Across two stores it was a property of the construction that
nothing checked, and a property nothing checks is a property you have somebody's
word for.

The failure it exists to catch is silent. An empty ClickHouse beside a masked
Postgres is a twin that is visibly incomplete and somebody notices within a
minute of opening a chart. One identity masked into two different fake people
is a twin that is confidently wrong: every join across the two stores returns
nothing or returns the wrong person, every report built on it is plausible, and
nothing anywhere says so.

It reads schemas and no rows. The check masks its own probe values through both
stores' rules and compares the outputs, so what it needs from a store is the
catalog, which is why it is safe to point at production. Every store it reads
is named, every store it could not read is named with the reason, and a run
that reached one store reports that it proved nothing rather than reporting a
hundred percent of one.

Each datastore says where its schema is read from with source_url_env, which
names an environment variable and never the connection string. The primary
takes that from database.source_url_env and does not repeat it.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := orchestrator(e, branch, false)
			if err != nil {
				return err
			}
			res, err := o.CrossStoreCheck(cmd.Context())
			if err != nil {
				return err
			}
			return reportCrossStore(e, res)
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "",
		"Branch context to use, defaulting to the checked out one")
	return cmd
}

// reportCrossStore prints the report and turns its verdict into an exit code.
func reportCrossStore(e *Env, res *env.CrossStoreResult) error {
	r := res.Report
	doc := CrossStoreJSON{
		OK: r.OK(), Summary: strings.TrimSpace(r.Summary()),
		Checked: r.Cross.Checked, Identical: r.Cross.Identical,
		Stores: r.Read, Unread: r.Unread, NoSource: res.WithoutSource,
		Declared: res.Declared, Tables: r.Tables, Columns: r.Columns,
		SkippedTables: r.SkippedTables, RulesHash: r.RulesHash,
	}
	if r.Cross.Checked > 0 {
		pct := r.Cross.Percent()
		doc.Percent = &pct
	}
	for _, p := range r.Cross.Mismatches() {
		doc.Mismatches = append(doc.Mismatches, CrossStorePairJSON{
			Key: p.Key, A: p.A.String(), B: p.B.String(), Reason: p.Reason,
		})
	}

	if e.Out.Format == FormatJSON {
		if err := e.Out.JSON(doc); err != nil {
			return err
		}
		// Wrapped only when there IS a failure. silent(nil) is a non nil
		// error carrying exit code zero, so returning it unconditionally
		// would make a run that passed look like a run that did not, in the
		// one output format nobody reads by eye.
		if failure := crossStoreFailure(res); failure != nil {
			return silent(failure)
		}
		return nil
	}

	e.Out.Section("Cross store masking")
	if r.OK() {
		e.Out.Status(e.Out.S(StyleGood, SymbolOK), "identical",
			fmt.Sprintf("%d of %d join keys across %s",
				r.Cross.Identical, r.Cross.Checked, strings.Join(r.Read, " and ")))
	} else {
		e.Out.Status(e.Out.S(StyleBad, SymbolFail), "not verified",
			strings.TrimSpace(firstLineOf(r.Summary())))
	}
	for _, p := range r.Cross.Mismatches() {
		e.Out.Printf("  %s %s and %s: %s\n",
			e.Out.S(StyleBad, SymbolFail), p.A, p.B, p.Reason)
	}
	printCrossStoreCoverage(e, res)
	return crossStoreFailure(res)
}

// printCrossStoreCoverage says what was NOT compared, after the verdict.
//
// Printed on an identical result as well as a failed one, which is the point.
// A percentage over two of five stores is a true number about the wrong
// question, and this is the only thing that tells a reader which it is.
func printCrossStoreCoverage(e *Env, res *env.CrossStoreResult) {
	r := res.Report
	e.Out.Printf("  %d tables and %d columns read, and no rows.\n", r.Tables, r.Columns)
	for _, u := range r.Unread {
		e.Out.Printf("  %s %s (%s) could not be read: %s\n",
			e.Out.S(StyleWarn, SymbolWarn), u.Store, u.Engine, u.Why)
	}
	for _, name := range res.WithoutSource {
		e.Out.Printf("  %s %s names no source_url_env, so its schema was never read.\n",
			e.Out.S(StyleWarn, SymbolWarn), name)
	}
	for _, skip := range r.SkippedTables {
		e.Out.Printf("  %s %s was not compared.\n", e.Out.S(StyleWarn, SymbolWarn), skip)
	}
}

// crossStoreFailure is the exit code for a report that is not a pass.
//
// TWO codes over THREE ways of not passing, and the grouping is the point. A
// pair that disagreed is a statement about the data. Nothing having been
// compared, and a store having been left unread, are both statements about
// what could be reached. Reporting the second kind as the first would tell
// somebody their stores disagree when the truth is that nobody looked, and
// this repository has already paid for that conflation once.
//
// Nothing compared at all is checked FIRST, because a report with no pairs in
// it has no mismatches either, so testing for mismatches first would return
// nil for exactly the run that proved nothing.
//
// The unread store is checked LAST, after the mismatches, because a measured
// disagreement is the more specific finding and the one somebody can act on
// immediately. It still refuses: every pair the check could compare agreeing
// says nothing about the store it never opened.
func crossStoreFailure(res *env.CrossStoreResult) error {
	r := res.Report
	if len(r.Read) < 2 || r.Cross.Checked == 0 {
		return aferrors.Coded(aferrors.AFMSK015, "detail", strings.TrimSpace(firstLineOf(r.Summary())))
	}
	if mismatches := r.Cross.Mismatches(); len(mismatches) > 0 {
		p := mismatches[0]
		return aferrors.Coded(aferrors.AFMSK014,
			"detail", fmt.Sprintf("%s and %s: %s", p.A, p.B, p.Reason))
	}
	if len(r.Unread) > 0 {
		u := r.Unread[0]
		return aferrors.Coded(aferrors.AFMSK015, "detail", fmt.Sprintf(
			"%d of %d join keys agreed across %s, and %s could not be read: %s",
			r.Cross.Identical, r.Cross.Checked, strings.Join(r.Read, " and "), u.Store, u.Why))
	}
	return nil
}

// firstLineOf takes the verdict line out of a multi line summary.
func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
