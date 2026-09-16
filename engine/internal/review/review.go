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

// FileReader returns the whole new side of a changed file, so the reviewer can
// show the model the file around the lines a diff added rather than the added
// lines alone. It is an interface for the same reason Client is: the collector
// backs it with git, and a test hands the package a fake, so nothing here shells
// out or needs a checkout.
//
// A file the reader cannot produce, a deletion, a path absent at head, a diff
// read from a file with no checkout behind it, returns ok=false with no error,
// and the reviewer falls back to that file's added lines alone. An error is a
// real failure to read, which the reviewer records as a note and still falls
// back from, so a reader that cannot answer never blanks a review. A nil reader
// is the honest no context mode: every file is shown as its added lines, exactly
// as the reviewer behaved before whole file context existed.
type FileReader interface {
	FullFile(ctx context.Context, path string) (content string, ok bool, err error)
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
	// MaxFileBytes bounds the full new file content shown as context for one
	// changed file. A file whose head content exceeds it is not shown whole: its
	// added lines are shown with a window of surrounding context instead, and a
	// note records that the file was too large for full context. Zero disables
	// the per file bound, which sends every selected file whole.
	MaxFileBytes int
	// MaxContextBytes bounds the total rendered context across all reviewed
	// files. The files are taken highest signal first, most added lines first,
	// and once the budget is spent the rest are not sent to the model and a note
	// says so. The highest signal file is always sent even when it alone exceeds
	// the budget, because dropping the file a review is worth most on would be
	// the worse failure. Zero disables the total bound.
	MaxContextBytes int
	// ContextWindow is how many unchanged lines are shown on each side of an
	// added hunk in the per file fallback, when a file is too large to show
	// whole. Zero leaves only the added lines with no surrounding context.
	ContextWindow int
}

// DefaultCaps are generous enough that an ordinary change is reviewed whole and
// tight enough that a mechanical one does not spend a fortune. They are the
// collector's default; a caller may pass its own.
//
// The context bounds are the ones that make whole file review affordable: a file
// up to MaxFileBytes is shown entire, a larger one falls back to its added lines
// with ContextWindow lines of context on each side, and the whole change is held
// under MaxContextBytes of context so a sprawling pull request cannot run the
// bill up on files with little signal.
var DefaultCaps = Caps{
	MaxFiles: 40, MaxLines: 1500, MaxBytes: 60000,
	MaxFileBytes: 24000, MaxContextBytes: 120000, ContextWindow: 40,
}

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
	ctx context.Context, client Client, reader FileReader,
	files []change.File, level report.Level, caps Caps,
) (Result, error) {
	selected, notes := boundDiff(files, caps)
	if len(selected) == 0 {
		// Nothing with added lines to review. Not an error and not a finding:
		// a change that added no code lines (a pure deletion, a binary file, a
		// rename with no edits) has nothing for a line reviewer to read.
		return Result{Notes: notes}, nil
	}

	system := systemPrompt
	user, renderNotes, addedByFile := renderContext(ctx, reader, selected, caps)
	notes = append(notes, renderNotes...)

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

	findings := mapEntries(entries, level, addedByFile)
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

// renderContext turns the selected files into the view the model reads: each
// file with line numbers, the lines the change added marked, and the unchanged
// lines around them shown as context so a finding on an added line can be judged
// against the whole file rather than the added line alone.
//
// It returns the rendered text, any notes the rendering produced (a file too
// large for full context, a file whose context could not be read, a total budget
// that cut the change short), and the set of added line numbers per rendered
// file. That set is the drop rule's authority: only a finding on a line the
// change added survives, so widening the model's view to the whole file can
// never turn a pre existing bug into a reported one. A file that was not sent,
// because the total budget was spent before it, is absent from the set, so a
// finding the model somehow returns for it is dropped too.
func renderContext(
	ctx context.Context, reader FileReader, files []change.File, caps Caps,
) (string, []string, map[string]map[int]bool) {
	var b strings.Builder
	b.WriteString(contextLegend)
	var notes []string
	addedByFile := make(map[string]map[int]bool, len(files))
	total := 0
	sent := 0
	for i, f := range files {
		added := addedSet(f)
		text, note := renderFile(ctx, reader, f, added, caps)

		// The total context budget. The highest signal file is always sent, even
		// alone over budget, so a review is never cut to nothing; each further
		// file is sent only while the budget holds, and the rest are noted rather
		// than dropped in silence.
		if caps.MaxContextBytes > 0 && sent > 0 && total+len(text) > caps.MaxContextBytes {
			remaining := len(files) - i
			notes = append(notes, fmt.Sprintf(
				"the change's context did not fit the reviewer's budget, so it reviewed the %d "+
					"highest-signal files and did not send the remaining %d; a finding's absence on "+
					"an unsent file is not a clean bill", sent, remaining))
			break
		}

		b.WriteString(text)
		b.WriteByte('\n')
		total += len(text)
		sent++
		addedByFile[f.Path] = added
		if note != "" {
			notes = append(notes, note)
		}
	}
	return b.String(), notes, addedByFile
}

// contextLegend heads the model's input so it reads the markers the way the
// renderer writes them, and repeats the one rule the whole context feature turns
// on: report only on the added lines.
const contextLegend = "Each file below is shown with its line numbers. A line that begins with \"+ \" was ADDED by this change. A line that begins with two spaces is unchanged context, shown only so you can understand the added lines. Report defects ONLY on the added (\"+ \") lines; use the surrounding context to decide whether an added line is wrong.\n\n"

// renderFile renders one file for the model and returns any note the rendering
// produced. It shows the whole file when it fits the per file byte cap, falls
// back to the added lines with a window of context when the file is too large,
// and falls back again to the added lines alone when the file's content cannot
// be read at all. Each fallback is noted so "reviewed with less context" is
// never silent.
func renderFile(
	ctx context.Context, reader FileReader, f change.File, added map[int]bool, caps Caps,
) (string, string) {
	content, ok, note := fetchContent(ctx, reader, f)
	if !ok {
		return renderAddedOnly(f), note
	}
	if caps.MaxFileBytes > 0 && len(content) > caps.MaxFileBytes {
		return renderWindowed(f, content, added, caps.ContextWindow), fmt.Sprintf(
			"%s was too large to show the reviewer in full, so its added lines were shown with %d "+
				"lines of surrounding context on each side rather than the whole file", f.Path, caps.ContextWindow)
	}
	return renderWhole(f, content, added), ""
}

// fetchContent asks the reader for a file's head side content. A nil reader is
// the no context mode and produces no note. A reader that returns not ok, or an
// error, is a file whose context could not be read, which is noted so the
// narrower review is visible.
func fetchContent(ctx context.Context, reader FileReader, f change.File) (string, bool, string) {
	if reader == nil {
		return "", false, ""
	}
	content, ok, err := reader.FullFile(ctx, f.Path)
	if err != nil {
		return "", false, fmt.Sprintf(
			"the code reviewer could not read the full contents of %s, so it reviewed that file's "+
				"added lines without surrounding context: %s", f.Path, err.Error())
	}
	if !ok {
		return "", false, fmt.Sprintf(
			"the code reviewer could not read the full contents of %s, so it reviewed that file's "+
				"added lines without surrounding context", f.Path)
	}
	return content, true, ""
}

// renderWhole shows every line of the file, the added ones marked. The line
// number is the file's own, so a finding names a line the author can open, and
// the marker is what tells the model which lines it may report on.
func renderWhole(f change.File, content string, added map[int]bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "FILE: %s (%s)\n", f.Path, statusOf(f))
	if f.LinesTruncated {
		b.WriteString("  (this file added more lines than the reviewer marks; some added lines below are shown as context)\n")
	}
	for i, ln := range splitLines(content) {
		writeLine(&b, i+1, ln, added[i+1])
	}
	return b.String()
}

// renderWindowed shows the added lines of a file too large to show whole, each
// with window unchanged lines on either side, and a gap marker where lines were
// left out. The added markers still come from the diff, so the model sees which
// lines it may report on even in the trimmed view.
func renderWindowed(f change.File, content string, added map[int]bool, window int) string {
	lines := splitLines(content)
	n := len(lines)
	show := make([]bool, n+1)
	for a := range added {
		if a < 1 || a > n {
			continue
		}
		lo, hi := a-window, a+window
		if lo < 1 {
			lo = 1
		}
		if hi > n {
			hi = n
		}
		for k := lo; k <= hi; k++ {
			show[k] = true
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "FILE: %s (%s)\n", f.Path, statusOf(f))
	prev := false
	for i := 1; i <= n; i++ {
		if !show[i] {
			if prev {
				b.WriteString("  ...\n")
			}
			prev = false
			continue
		}
		writeLine(&b, i, lines[i-1], added[i])
		prev = true
	}
	return b.String()
}

// renderAddedOnly shows a file's added lines alone, the view the reviewer had
// before whole file context, used when a file's content cannot be read. The
// added lines carry the same marker so the model's rule does not change with the
// available context.
func renderAddedOnly(f change.File) string {
	var b strings.Builder
	fmt.Fprintf(&b, "FILE: %s (%s)\n", f.Path, statusOf(f))
	if f.LinesTruncated {
		b.WriteString("  (the added lines below are a prefix; this file's diff was truncated)\n")
	}
	for _, al := range f.AddedLines {
		writeLine(&b, al.N, al.Text, true)
	}
	return b.String()
}

// writeLine writes one numbered line with the marker that says whether the
// change added it: "+ " for an added line, two spaces for context.
func writeLine(b *strings.Builder, n int, text string, isAdded bool) {
	marker := "  "
	if isAdded {
		marker = "+ "
	}
	fmt.Fprintf(b, "%s%d: %s\n", marker, n, text)
}

// addedSet is the set of new file line numbers a change added to a file, which
// marks the rendered lines and is the drop rule's authority for which lines a
// finding may sit on.
func addedSet(f change.File) map[int]bool {
	set := make(map[int]bool, len(f.AddedLines))
	for _, al := range f.AddedLines {
		set[al.N] = true
	}
	return set
}

// statusOf is the file's status, defaulting to modified when the diff carried
// none, so the model always sees what happened to the path.
func statusOf(f change.File) string {
	if f.Status == "" {
		return "modified"
	}
	return string(f.Status)
}

// splitLines splits file content into its lines, dropping the empty trailing
// element a final newline produces so the last real line keeps its own number.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// mapEntries turns the model's parsed entries into findings at the caller's
// level, dropping the malformed and deduplicating the rest.
//
// Strict on what an entry must carry (a title and a file), tolerant of what it
// may get wrong (an unknown category becomes correctness, a missing or zero line
// drops the line from Where rather than the whole entry). One bad element must
// never blank the feature: a single unreadable entry is skipped and the rest
// stand, the same discipline the read boundary uses everywhere.
//
// addedByFile is the drop rule that keeps whole file context honest. The model
// now sees unchanged code around the added lines, so it could report a pre
// existing defect the change did not introduce. Only a line the change added is
// eligible for a finding: an entry on a file that was not sent is dropped, and
// an entry on a line the change did not add, a context line, is dropped, so
// widening the model's view can never widen what it may flag. An entry with no
// line stays a file level finding, because it names no context line to have been
// read off.
func mapEntries(entries []rawFinding, level report.Level, addedByFile map[string]map[int]bool) []report.Finding {
	seen := map[string]bool{}
	var out []report.Finding
	for _, e := range entries {
		title := strings.TrimSpace(e.Title)
		file := strings.TrimSpace(e.File)
		if title == "" || file == "" {
			// Nothing to point a reader at. Dropped, not guessed.
			continue
		}
		added, sent := addedByFile[file]
		if !sent {
			// A file the reviewer did not send the model. A finding on it cannot
			// be trusted, so it is dropped rather than reported on code the model
			// was never shown.
			continue
		}
		line := int(e.Line)
		if line > 0 && !added[line] {
			// The finding sits on a context line the change did not add. Dropped:
			// the reviewer reports only what the change introduced, and the extra
			// context is there to judge the added lines, not to be flagged itself.
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
