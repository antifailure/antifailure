package mcp

import (
	"context"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/review"
)

// review_change runs the static, model-backed code reviewer against the current
// change and returns the concrete correctness defects it found on the lines the
// change ADDED. It reads the diff, not the twin, so it opens no session, brings
// up no environment and touches no database: it is the fast "run it on my
// change, get bugs back" lane, the same reviewer the rehearsal runs but without
// waiting for a rehearsal.
//
// It is deliberately allowed to report a file:line location, which
// read_security_findings is NOT, and the reason is the boundary. A security
// finding names a route or a table in a copy of production, so it must never
// carry the offending body or row. A review finding names a line in the
// caller's OWN changed code, which the caller wrote and is looking at, so the
// location is the useful part and there is no production value to leak. What it
// still refuses to carry is anything shaped like a captured body, response or
// row: this reads a diff, and a diff has no such thing.

// reviewMeta carries the facts the projection needs to be honest about a review
// that produced no findings. A run with no findings has three very different
// causes and a reader must be able to tell them apart: the change touched no
// code at all, there was code but no model key to review it with, or the model
// ran and found nothing. Only the last is a clean bill.
type reviewMeta struct {
	// TouchedCode is false for a docs-only or configuration-only change, which
	// routes no reviewer and is reported as "nothing to review" rather than as
	// a clean pass.
	TouchedCode bool
	// HadKey is false when no model key resolved. With code present, that is the
	// honest skip: an LLM reviewer with no model to call did not run, and the
	// absence is a note, never a fabricated clean pass.
	HadKey bool
	// FilesReviewed is how many code files the reviewer read, so a reader can
	// weigh an empty review against how much was actually looked at.
	FilesReviewed int
}

// reviewRunner runs the code reviewer against a branch and returns its findings
// beside the meta the projection needs. serve.go satisfies it with a closure
// that builds an orchestrator and calls review.Review; a test satisfies it with
// a fake that returns a canned result, so this tool is exercised without a
// model, a checkout or a network.
//
// An error is an infrastructure failure the reviewer could not get past: the
// project could not be built, or the diff could not be read at all. A gap in
// the reviewer itself, a missing key or a model that would not answer, is not
// an error here; it rides back as a note on the Result so the projection can
// report it as the honest non-verdict it is.
type reviewRunner func(ctx context.Context, branch string) (review.Result, reviewMeta, error)

// reviewFinding is one defect in the projection.
//
// Category is added beside the rule so a caller can group without parsing the
// rule: it is the segment after "review." in the rule, so review.error_handling
// is the error_handling category. Where is a file:line into the caller's own
// changed code, which is allowed precisely because it is the caller's code and
// not a copy of production. There is no field here for a body, a response or a
// row, and that absence is the contract.
type reviewFinding struct {
	Rule     string `json:"rule"`
	Category string `json:"category"`
	Level    string `json:"level"`
	Title    string `json:"title"`
	Detail   string `json:"detail,omitempty"`
	Fix      string `json:"fix,omitempty"`
	Where    string `json:"where,omitempty"`
}

// newReviewChangeTool builds review_change.
func newReviewChangeTool(p *Project, run reviewRunner) *Tool {
	return &Tool{
		Name:  "review_change",
		Title: "Review the change for bugs",
		// Read only: it reads the diff and calls a model to read it, and changes
		// nothing on this machine or in any environment. It is the same class as
		// plan_checks_for_change, which also reads the change and starts nothing.
		ReadOnly: true,
		Description: "Review the current change for concrete correctness defects and get them " +
			"back as findings, without waiting for a rehearsal. It reads the diff and shows a " +
			"model the changed files, then reports high-confidence bugs on the lines the change " +
			"ADDED: an off-by-one, a nil dereference on a new path, a dropped error, a boundary " +
			"the new code does not hold, a data shape assumed wrong, a new function nothing " +
			"calls. It reads the diff, NOT a copy of production, so it opens no session, builds " +
			"no image and needs no environment: run it mid-edit to ask 'is there a bug in what I " +
			"just wrote'. Each finding carries a rule, a category, a level, a bounded description, " +
			"a fix and a file:line into your own changed code. An empty review is the common, " +
			"correct outcome for a small clean change and is never invented into a defect. A " +
			"docs-only or configuration-only change touches no code and is reported as nothing to " +
			"review. With code present and no model key configured the review is SKIPPED and said " +
			"so, which is not a clean bill: describe_model_key says how to set one. The finding's " +
			"level is the project's own policy, warn unless the project raised it, so this advises " +
			"and does not block a merge on a model's say-so.",
		Input: &Schema{
			Type:     "object",
			Required: []string{"project_id"},
			Properties: map[string]*Schema{
				"project_id": projectIDSchema(),
				"branch": gitRefSchema("Optional. The head ref to review, defaulting to the " +
					"branch currently checked out. It narrows which change is read and reaches " +
					"nothing else: the repository, the base ref and every safety control are the " +
					"server's own and no argument here can point them elsewhere."),
			},
		},
		Handler: func(ctx context.Context, _ *Call, args map[string]any) (any, *Fault) {
			if fault := p.checkAssertion(args); fault != nil {
				return nil, fault
			}
			branch, _ := args["branch"].(string)

			res, meta, err := run(ctx, branch)
			if err != nil {
				// The diff could not be read at all, so nothing here says whether
				// the change has a bug. Reported as a refusal with a next step
				// rather than as an empty review a caller might read as clean,
				// the same way plan_checks_for_change refuses an unreadable diff.
				return nil, &Fault{
					Code: FaultSafetyUnavailable,
					Detail: "The change could not be read, so no review ran and this says " +
						"nothing about the change. The usual cause is a base ref this " +
						"checkout does not have, which happens in a shallow clone.",
					Retryable: true,
					wrapped:   err,
				}
			}
			return reviewProjection(res, meta), nil
		},
	}
}

// reviewProjection turns the reviewer's result into the tool's document.
//
// The three empty-review causes are kept distinct: a change that touched no code
// is reported as nothing to review, a change with code but no key is reported as
// skipped with the reason, and a change the model reviewed is reported with its
// findings, worst first. Every note the reviewer produced rides along in all
// three, because a cap or a file it could not read is a fact about what was
// looked at and true whatever the verdict.
func reviewProjection(res review.Result, meta reviewMeta) any {
	const boundaryNote = "This reads the diff, not a copy of production: it shows a model the " +
		"changed files and reports on the lines the change added. A location is a file:line " +
		"into your own changed code. No captured request body, response or row is read or " +
		"returned, because a diff carries none."

	notes := make([]string, 0, len(res.Notes)+1)
	for _, n := range res.Notes {
		notes = append(notes, safeText(n, 400))
	}

	if !meta.TouchedCode {
		// A docs-only or configuration-only change routed no reviewer. Not a
		// clean pass and not a fault: there was simply no code for a line
		// reviewer to read, and saying so is different from saying nothing found.
		return map[string]any{
			"kind":           "review_findings",
			"summary":        "This change touches no code surfaces, so there was nothing for the code reviewer to read.",
			"findings":       []reviewFinding{},
			"totals":         map[string]int{"fail": 0, "warn": 0},
			"by_category":    map[string]map[string]int{},
			"notes":          notes,
			"files_reviewed": 0,
			"reviewed":       false,
			"boundary_note":  boundaryNote,
		}
	}

	if !meta.HadKey {
		// There is code to review and no model to review it with. Named rather
		// than silent, because "skipped, no key" and "ran, found nothing" are
		// different facts and only one of them is a clean bill. The wording
		// matches the CLI's so the fix a caller is told is the same one.
		notes = append(notes, "code review skipped: no model key configured. Set one with "+
			"'af model set' to review the change's added lines for correctness defects.")
		return map[string]any{
			"kind":           "review_findings",
			"summary":        "There is code in this change, but no model key is configured, so the code review was skipped. Set a key with 'af model set'; describe_model_key says where one can go.",
			"findings":       []reviewFinding{},
			"totals":         map[string]int{"fail": 0, "warn": 0},
			"by_category":    map[string]map[string]int{},
			"notes":          notes,
			"files_reviewed": meta.FilesReviewed,
			"reviewed":       false,
			"boundary_note":  boundaryNote,
		}
	}

	kept := make([]reviewFinding, 0, len(res.Findings))
	totals := map[string]int{"fail": 0, "warn": 0}
	byCategory := map[string]map[string]int{}
	for _, f := range res.Findings {
		level := string(f.Level)
		// A finding the policy silenced is dropped, never returned: reporting it
		// would put back exactly what the manifest turned off, the same rule the
		// security projection follows.
		if f.Level == report.LevelIgnore || level == "" {
			continue
		}
		category := reviewCategoryOf(f.Rule)
		if _, ok := totals[level]; !ok {
			totals[level] = 0
		}
		totals[level]++
		if byCategory[category] == nil {
			byCategory[category] = map[string]int{"fail": 0, "warn": 0}
		}
		byCategory[category][level]++
		kept = append(kept, reviewFinding{
			Rule:     f.Rule,
			Category: category,
			Level:    level,
			Title:    safeText(f.Title, 200),
			Detail:   safeText(f.Detail, 800),
			Fix:      safeText(f.Fix, 800),
			Where:    safeText(f.Where, 400),
		})
	}
	// Worst first, then stable, so the first findings a reader sees are the ones
	// that would decide a verdict. Stable on the reviewer's own deterministic
	// order, which it already sorted by file and line.
	sort.SliceStable(kept, func(i, j int) bool {
		return levelRank(kept[i].Level) < levelRank(kept[j].Level)
	})

	return map[string]any{
		"kind":           "review_findings",
		"summary":        reviewSummary(len(kept), totals),
		"findings":       kept,
		"totals":         totals,
		"by_category":    byCategory,
		"notes":          notes,
		"files_reviewed": meta.FilesReviewed,
		"reviewed":       true,
		"boundary_note":  boundaryNote,
	}
}

// reviewSummary is one sentence a person could read aloud.
func reviewSummary(shown int, totals map[string]int) string {
	if shown == 0 {
		return "The code reviewer read the change and reported no defects on the added lines. " +
			"An empty review is the common outcome for a small clean change; a note says if any " +
			"file was too large to review in full."
	}
	return plural(shown, "code review finding", "code review findings") + ": " +
		plural(totals["fail"], "fail", "fail") + ", " +
		plural(totals["warn"], "warn", "warn") + ". Each names a file and line in the change."
}

// reviewCategoryOf reads the category out of a "review.<category>" rule, so a
// caller groups by category without parsing the rule itself. A rule not in that
// namespace, which the reviewer does not produce, reports an empty category
// rather than a misleading one.
func reviewCategoryOf(rule string) string {
	const prefix = "review."
	if strings.HasPrefix(rule, prefix) {
		return rule[len(prefix):]
	}
	return ""
}
