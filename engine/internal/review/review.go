// Package review is the static, model-backed code reviewer: it reads the lines a
// change ADDS and reports the concrete correctness defects a diff introduces.
//
// It is the complement to the rehearsal the rest of the product does. The twin
// proves what the change does when it runs; this reads what the change is, the
// way a senior reviewer reads a pull request, and it catches the class of bug
// that never reaches a running environment because no workflow happened to
// exercise it: an off-by-one, a nil dereference on a newly added path, an error
// that is checked and then dropped, a boundary the new code does not hold, a new
// function nothing calls, a data shape assumed wrong, an ordering hazard.
//
// Three things keep it honest rather than noisy.
//
// It reads ONLY the added lines. A reviewer that reported pre-existing problems
// would drown the one thing the author can act on now, and would make every
// unrelated change red. The diff handed to the model is the additions, with the
// line numbers they carry in the new file, so a finding can point at a line the
// author just wrote.
//
// It is told to prefer silence. An empty review is the common, correct outcome
// for a small clean change, and a model that invents a defect to look useful is
// the failure this feature would be judged by. The prompt asks for high
// confidence, concrete, actionable defects only, and the parser drops anything
// it cannot read rather than guessing at it.
//
// Its findings are advisory by default. A probabilistic reviewer must not fail a
// build on its own say-so, so the collector gives every finding the level the
// manifest's policy resolved (warn unless the project raised it), never a level
// this package chose. The level is a parameter here for exactly that reason.
package review

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/change"
	"github.com/antifailure/antifailure/engine/internal/report"
)

// Client sends one review to a model and returns its raw text completion.
//
// An interface, and the whole reason the package is testable without a network:
// the collector builds a real client that posts the diff to the user's own
// provider through the air gap guard, and a test hands the package a fake that
// returns a canned review. Nothing here makes an HTTP call; the client does.
type Client interface {
	// Complete sends the system and user prompts and returns the model's text.
	// An error is a fact about the call, not a verdict about the change, so the
	// collector records it as a note and emits no finding.
	Complete(ctx context.Context, system, user string) (string, error)
}

// Caps bound how much of a change is sent to the model.
//
// A model call is paid per token and a huge mechanical change (a generated file,
// a vendored dependency, a mass rename) would spend that budget on lines with no
// signal. So the diff is capped: the files with the most added lines are read
// first, because a file the change rewrote is where a review is worth most, and
// the rest are dropped with a note that says so. A note rather than a silent
// truncation, because "we reviewed part of this" and "we reviewed all of this
// and found nothing" are different claims and a reader must be able to tell them
// apart.
type Caps struct {
	// MaxFiles is how many changed files are reviewed, largest first.
	MaxFiles int
	// MaxLines is the ceiling on total added lines across those files.
	MaxLines int
	// MaxBytes is the ceiling on the total added-line bytes, a second bound so a
	// file of very long lines cannot blow the budget under a small line count.
	MaxBytes int
}

// DefaultCaps are generous enough that an ordinary change is reviewed whole and
// tight enough that a mechanical one does not spend a fortune. They are the
// collector's default; a caller may pass its own.
var DefaultCaps = Caps{MaxFiles: 40, MaxLines: 1500, MaxBytes: 60000}

// Result is what one review produced.
type Result struct {
	// Findings are the defects, already mapped to the report's shape, at the
	// level the caller passed. Deduplicated and ordered deterministically.
	Findings []report.Finding
	// Notes are facts about the review itself: the diff was capped, or the model
	// answered with something that could not be read. They belong on the run as
	// notes, never as findings, because they say nothing about the change.
	Notes []string
}

// The finding categories, which become the "review.<category>" rule namespace.
// A closed set, because the rule is what a person greps for and what a manifest
// key would raise the level of, so a model inventing a new category name would
// produce a finding nobody can configure. An entry whose category is none of
// these is mapped to correctness rather than dropped: a real defect with an odd
// label is still a real defect, and correctness is the honest superset.
const (
	categoryCorrectness   = "correctness"
	categoryEdgeCase      = "edge_case"
	categoryErrorHandling = "error_handling"
	categoryConcurrency   = "concurrency"
	categoryDataShape     = "data_shape"
	categoryDeadCode      = "dead_code"
	categorySecurity      = "security"
)

// knownCategories is the set a model may return, for normalisation.
var knownCategories = map[string]bool{
	categoryCorrectness:   true,
	categoryEdgeCase:      true,
	categoryErrorHandling: true,
	categoryConcurrency:   true,
	categoryDataShape:     true,
	categoryDeadCode:      true,
	categorySecurity:      true,
}

// Review reads the added lines of the changed files and returns the defects a
// model found in them, at the level the caller passed.
//
// It never returns an error for an empty change or an empty review: both are
// ordinary. It returns an error only when the model call itself failed, so the
// collector can tell "the reviewer ran and found nothing" from "the reviewer
// could not run", which are the two outcomes that must never look alike.
func Review(
	ctx context.Context, client Client, files []change.File, level report.Level, caps Caps,
) (Result, error) {
	selected, notes := boundDiff(files, caps)
	if len(selected) == 0 {
		// Nothing with added lines to review. Not an error and not a finding:
		// a change that added no code lines (a pure deletion, a binary file, a
		// rename with no edits) has nothing for a line reviewer to read.
		return Result{Notes: notes}, nil
	}

	system := systemPrompt
	user := renderDiff(selected)

	raw, err := client.Complete(ctx, system, user)
	if err != nil {
		return Result{Notes: notes}, err
	}

	entries, ok := parseEntries(raw)
	if !ok {
		// The response was not JSON we could read. Tolerant on the read
		// boundary: rather than blank the feature or trust a guess, the whole
		// response is discarded and the fact is noted, so a malformed answer
		// costs a note and not a false clean pass.
		notes = append(notes, "the code reviewer's model answered with something that was not the "+
			"expected JSON, so no review finding was produced from it")
		return Result{Notes: notes}, nil
	}

	findings := mapEntries(entries, level)
	return Result{Findings: findings, Notes: notes}, nil
}

// boundDiff keeps only files that added lines, orders them by how much they
// added (most first, so the highest-signal files are reviewed when the budget is
// tight), and caps the total against the three limits. It returns the files to
// review and any note the capping produced.
func boundDiff(files []change.File, caps Caps) ([]change.File, []string) {
	withAdds := make([]change.File, 0, len(files))
	for _, f := range files {
		if f.Binary || len(f.AddedLines) == 0 {
			continue
		}
		withAdds = append(withAdds, f)
	}
	// Most-added first, and by path second so the order is deterministic when
	// two files added the same number of lines. A deterministic order is what
	// makes the whole reviewer testable and its output diffable run to run.
	sort.SliceStable(withAdds, func(i, j int) bool {
		if len(withAdds[i].AddedLines) != len(withAdds[j].AddedLines) {
			return len(withAdds[i].AddedLines) > len(withAdds[j].AddedLines)
		}
		return withAdds[i].Path < withAdds[j].Path
	})

	var (
		selected   []change.File
		lines      int
		bytesTotal int
		capped     bool
	)
	total := len(withAdds)
	for _, f := range withAdds {
		if caps.MaxFiles > 0 && len(selected) >= caps.MaxFiles {
			capped = true
			break
		}
		// Take the file whole only if it fits both remaining budgets. A file
		// that would overflow the line or byte budget is dropped rather than
		// half-read: a diff cut mid-file would hand the model a hunk with no
		// context and invite exactly the low-confidence noise this reviewer is
		// built to avoid.
		fileBytes := 0
		for _, al := range f.AddedLines {
			fileBytes += len(al.Text)
		}
		if caps.MaxLines > 0 && lines+len(f.AddedLines) > caps.MaxLines {
			capped = true
			continue
		}
		if caps.MaxBytes > 0 && bytesTotal+fileBytes > caps.MaxBytes {
			capped = true
			continue
		}
		selected = append(selected, f)
		lines += len(f.AddedLines)
		bytesTotal += fileBytes
	}

	var notes []string
	if capped && len(selected) < total {
		notes = append(notes, fmt.Sprintf(
			"the change was large, so the code reviewer read the %d highest-signal files of %d "+
				"and did not review the rest; a finding's absence on an unreviewed file is not a clean bill",
			len(selected), total))
	}
	return selected, notes
}

// renderDiff turns the selected files into the added-lines view the model reads.
// Every line carries the number it has in the new file, so a finding can name a
// line the author can open. Kept plain on purpose: a fenced or annotated format
// invites the model to answer in the same shape instead of the JSON asked for.
func renderDiff(files []change.File) string {
	var b strings.Builder
	for _, f := range files {
		status := string(f.Status)
		if status == "" {
			status = "modified"
		}
		fmt.Fprintf(&b, "FILE: %s (%s)\n", f.Path, status)
		if f.LinesTruncated {
			b.WriteString("  (the added lines below are a prefix; this file's diff was truncated)\n")
		}
		for _, al := range f.AddedLines {
			fmt.Fprintf(&b, "%d: %s\n", al.N, al.Text)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// mapEntries turns the model's parsed entries into findings at the caller's
// level, dropping the malformed and deduplicating the rest.
//
// Strict on what an entry must carry (a title and a file), tolerant of what it
// may get wrong (an unknown category becomes correctness, a missing or zero line
// drops the line from Where rather than the whole entry). One bad element must
// never blank the feature: a single unreadable entry is skipped and the rest
// stand, the same discipline the read boundary uses everywhere.
func mapEntries(entries []rawFinding, level report.Level) []report.Finding {
	seen := map[string]bool{}
	var out []report.Finding
	for _, e := range entries {
		title := strings.TrimSpace(e.Title)
		file := strings.TrimSpace(e.File)
		if title == "" || file == "" {
			// Nothing to point a reader at. Dropped, not guessed.
			continue
		}
		category := strings.ToLower(strings.TrimSpace(e.Category))
		if !knownCategories[category] {
			category = categoryCorrectness
		}
		rule := "review." + category

		where := file
		if e.Line > 0 {
			where = fmt.Sprintf("%s:%d", file, e.Line)
		}

		key := rule + "\x00" + where + "\x00" + strings.ToLower(title)
		if seen[key] {
			continue
		}
		seen[key] = true

		out = append(out, report.Finding{
			Rule:   rule,
			Level:  level,
			Title:  clip(title, 200),
			Detail: clip(strings.TrimSpace(e.Explanation), 800),
			Fix:    clip(strings.TrimSpace(e.SuggestedFix), 800),
			Count:  1,
			Where:  where,
		})
	}
	return out
}

// clip bounds a string a model produced, so one verbose finding cannot fill the
// report. Runes, not bytes, so a multibyte character is never cut in half.
func clip(s string, limit int) string {
	if limit <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return strings.TrimSpace(string(r[:limit])) + "..."
}
