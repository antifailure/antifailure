// Package cli implements the command surface.
//
// The rule that shapes it: no business logic lives in a command handler.
// Handlers parse flags, call into engine packages, and render. That is what
// lets every behavior be tested without a process, and what keeps the command
// tree from becoming the place logic hides.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// Format is how output is rendered.
type Format string

const (
	// FormatText is human readable output, the default.
	FormatText Format = "text"
	// FormatJSON is a schema validated document per command, for scripts.
	FormatJSON Format = "json"
)

// Output renders command results.
//
// Every command supports both formats. Text output is stable for the same
// input, with no timestamps and no durations in the default rendering, so that
// snapshot tests and diffs are meaningful; timestamps live in the JSON form
// where a machine wants them.
type Output struct {
	Out    io.Writer
	Err    io.Writer
	Format Format
	// Color is whether to emit ANSI sequences. It is decided once, at the
	// command boundary, from the terminal, NO_COLOR, and TERM.
	Color bool
	// Quiet suppresses progress and non essential output.
	Quiet bool
	// Verbose adds detail, including stack traces on errors.
	Verbose bool
	// Width is how many columns a line may use.
	//
	// It is the terminal's real width when there is a terminal, and
	// defaultWidth when there is not, which is what keeps text output byte
	// stable for the same input: a pipe, a file, and a test buffer all have
	// no size, so they all render at the same width. Only a person watching a
	// real terminal sees the layout reflow, and they are the only one who
	// benefits from it.
	Width int
	// writeErr is the first failure writing to Out.
	//
	// Kept rather than returned, because a print helper that returned an
	// error would put an if at four hundred call sites and every one of them
	// would ignore it. Execute asks once, at the end, and a command that could
	// not write what it was asked to write does not report success. A full
	// disk or a closed pipe is exactly when a script needs to be told.
	writeErr error
}

// note records the first write failure and discards the rest. The first one
// is the one that explains what happened; the ones after it are consequences.
func (o *Output) note(_ int, err error) {
	if err != nil && o.writeErr == nil {
		o.writeErr = err
	}
}

// WriteErr reports the first failure writing to the output stream, if any.
//
// Named for what it reports rather than for the field, because Err is already
// the error stream and a command that confused the two would be reporting a
// failure to the thing that failed.
func (o *Output) WriteErr() error { return o.writeErr }

// The width a line may use when nothing better is known.
//
// Three numbers rather than one. defaultWidth is what a stream with no size
// gets, and it is 80 because that is what every captured log, pull request
// comment and terminal defaults to. minWidth is the floor: below it a table
// stacks and prose stops shrinking, because text reflowed into a four word
// column is not readable, it is just narrow. proseWidth is the measure prose
// stops growing at, because a sentence that runs the full width of a two
// hundred column terminal is a sentence the eye loses its place in; tables and
// status lines keep using the whole width, since a column of aligned values is
// scanned rather than read.
const (
	defaultWidth = 80
	minWidth     = 40
	maxWidth     = 200
	proseWidth   = 88
)

// NewOutput returns an Output configured from the environment.
func NewOutput(out, errW io.Writer) *Output {
	return &Output{Out: out, Err: errW, Format: FormatText, Color: false, Width: defaultWidth}
}

// DetectWidth decides how many columns output may use.
//
// The precedence mirrors DetectColor: an explicit override, then the terminal,
// then the default. AF_WIDTH exists for the same reason AF_FORCE_COLOR does,
// and is the only way to prove the narrow and wide renderings without
// allocating a pseudo terminal, which no test in this package does.
//
// A writer that is not a terminal gets defaultWidth rather than a guess. That
// is the load bearing half: it is what makes a piped run, a redirected run and
// a test render identical bytes.
func DetectWidth(w io.Writer, env func(string) string) int {
	if v := env("AF_WIDTH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return clampWidth(n)
		}
	}
	f, ok := w.(*os.File)
	if !ok {
		return defaultWidth
	}
	n, _, err := term.GetSize(int(f.Fd()))
	if err != nil || n <= 0 {
		return defaultWidth
	}
	return clampWidth(n)
}

func clampWidth(n int) int {
	switch {
	case n < minWidth:
		return minWidth
	case n > maxWidth:
		return maxWidth
	default:
		return n
	}
}

// cells is the display width of a string, ignoring the escape sequences that
// colour it.
//
// Measuring with len would be wrong twice over. A styled cell carries nine
// bytes of escape sequence that occupy no columns, which is why a coloured
// table used to print its headers nine columns to the right of the values
// under them; and a multi byte character occupies one byte per byte and one or
// two columns, which is a different wrong answer in the other direction.
func cells(s string) int { return ansi.StringWidth(s) }

// Printf writes to the output stream in text mode, and nothing in JSON mode.
//
// Mixing prose into a JSON stream is how a script that pipes output into jq
// breaks, so the check is here rather than at every call site.
func (o *Output) Printf(format string, args ...any) {
	if o.Format == FormatJSON || o.Quiet {
		return
	}
	o.note(fmt.Fprintf(o.Out, format, args...))
}

// Println writes a line in text mode.
func (o *Output) Println(s string) {
	if o.Format == FormatJSON || o.Quiet {
		return
	}
	o.note(fmt.Fprintln(o.Out, s))
}

// Raw writes to the output stream regardless of format or quiet. It is for
// content the user asked for, such as a rendered manifest or a log line.
func (o *Output) Raw(s string) { o.note(fmt.Fprint(o.Out, s)) }

// JSON writes a document in JSON mode, and nothing in text mode.
func (o *Output) JSON(v any) error {
	if o.Format != FormatJSON {
		return nil
	}
	enc := json.NewEncoder(o.Out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		o.note(0, err)
		return fmt.Errorf("cli: encode output: %w", err)
	}
	return nil
}

// Style names a visual treatment. Styles are named by meaning rather than by
// colour so that the palette can change in one place.
type Style int

const (
	StylePlain Style = iota
	StyleGood
	StyleWarn
	StyleBad
	StyleDim
	StyleBold
	StyleAccent
)

var styleCodes = map[Style]string{
	StyleGood:   "\x1b[32m",
	StyleWarn:   "\x1b[33m",
	StyleBad:    "\x1b[31m",
	StyleDim:    "\x1b[2m",
	StyleBold:   "\x1b[1m",
	StyleAccent: "\x1b[36m",
}

// S styles a string, or returns it unchanged when colour is off.
func (o *Output) S(style Style, s string) string {
	if !o.Color || style == StylePlain {
		return s
	}
	code, ok := styleCodes[style]
	if !ok {
		return s
	}
	return code + s + "\x1b[0m"
}

// Symbols used in status output. They are ASCII rather than Unicode so that
// they render identically in every terminal, in CI logs, and in a pull request
// comment, which is where most people will actually read them.
const (
	SymbolOK      = "ok"
	SymbolFail    = "fail"
	SymbolWarn    = "warn"
	SymbolSkip    = "skip"
	SymbolPending = "..."
)

// StyleOf is the treatment a status symbol is rendered in.
//
// Exported so that a caller who needs the colour for something other than a
// status line asks for it here rather than picking one, which is how the same
// idea ends up green in one command and cyan in another.
func StyleOf(symbol string) Style {
	switch symbol {
	case SymbolOK:
		return StyleGood
	case SymbolFail:
		return StyleBad
	case SymbolWarn:
		return StyleWarn
	case SymbolSkip:
		return StyleDim
	default:
		return StylePlain
	}
}

// statusLabelWidth is how wide the label column of a status line is.
//
// Fixed rather than measured, because status lines are printed one at a time
// as work finishes and there is nothing to measure yet when the first one goes
// out. A label longer than this pushes its own detail right rather than
// shifting every other line, which is the lesser of the two ugly options.
const statusLabelWidth = 28

// Status prints a labelled status line.
//
// symbol must be one of the Symbol constants, unstyled. Passing a styled
// string was possible and was a bug in two places: the switch below no longer
// recognised it, so the line lost its colour meaning, and the padding counted
// the escape sequence as visible width, so the line lost its alignment too.
func (o *Output) Status(symbol, label, detail string) {
	if o.Format == FormatJSON || o.Quiet {
		return
	}
	line := "  " + o.padTo(o.S(StyleOf(symbol), symbol), 5) +
		" " + o.padTo(label, statusLabelWidth)
	if detail != "" {
		// The detail hangs under itself rather than running off the right
		// edge. A check that says what is wrong in a sentence the terminal
		// chopped in half has not said it.
		indent := 2 + 5 + 1 + statusLabelWidth
		if cells(label) > statusLabelWidth {
			indent = 2 + 5 + 1 + cells(label) + 1
		}
		line += " " + o.S(StyleDim, o.WrapTo(detail, indent, o.Width))
	}
	o.note(fmt.Fprintln(o.Out, strings.TrimRight(line, " ")))
}

// padTo right pads to a display width, ignoring escape sequences.
func (o *Output) padTo(s string, w int) string {
	if n := cells(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// Section prints a heading.
func (o *Output) Section(title string) {
	if o.Format == FormatJSON || o.Quiet {
		return
	}
	o.note(fmt.Fprintf(o.Out, "\n%s\n", o.S(StyleBold, title)))
}

// Column describes one column of a table.
//
// Alignment and shrinkability are declared rather than inferred. A column of
// numbers that is sometimes empty, or that carries a unit, defeats every rule
// for guessing, and a table that right aligns one column on Tuesday because
// that day's data happened to be all digits is worse than one that never right
// aligns at all.
type Column struct {
	// Title is the heading, in capitals. Capitals because that is what a
	// column heading looks like everywhere a person already reads tables in a
	// terminal, and because seven of the eight tables in this tree already
	// used them; the eighth used sentence case and looked like what it was, a
	// command written on a different day by somebody who did not look.
	Title string
	// Right aligns the column, for quantities compared down the page: counts,
	// sizes, durations, percentages. Left is right for anything a reader scans
	// for a prefix, which is names and identifiers.
	Right bool
	// Flex marks the column that gives up width when the table is too wide for
	// the terminal, and the only one that may be truncated.
	//
	// Declared rather than shared out evenly, because sharing the shortfall
	// across every column is how a sixty four column terminal ends up showing
	// "2026..." under CREATED and "447...." under SIZE: four columns ruined to
	// save one. In every table here there is one column that is prose or a
	// hash and can lose its tail, and several that are a date, a count or an
	// identifier and cannot. Naming which is which is the whole difference.
	Flex bool
}

// Col is a plain left aligned column that keeps its natural width.
func Col(title string) Column { return Column{Title: title} }

// Num is a right aligned column, for a quantity.
func Num(title string) Column { return Column{Title: title, Right: true} }

// Flex is the column that absorbs a terminal too narrow for the table.
func Flex(title string) Column { return Column{Title: title, Flex: true} }

// tableGap is the space between two columns.
const tableGap = 2

// tableFloor is the narrowest a flexible column may become before the table
// gives up on fitting and stacks instead. Eight columns is about the point at
// which a truncated value stops being a recognisable prefix of anything.
const tableFloor = 8

// Table renders aligned columns. Rows are rendered in the order given; the
// caller sorts, because only the caller knows what order is meaningful.
//
// Three things this has to get right, all of which it once got wrong:
//
// Widths are measured in display columns, not bytes. A cell styled green
// carries nine bytes that occupy no columns, and measuring those made every
// coloured table print its headings a full nine columns clear of the values
// beneath them. Nobody reading the source would see it; everybody running the
// command did.
//
// A table wider than the terminal is not a table. When the flexible columns
// cannot give up enough width, each row is stacked as a labelled block instead
// of being left to the terminal's own wrapping, which interleaves the halves
// of two rows and is unreadable at any width.
//
// Nothing is truncated silently. A shortened cell ends in an ellipsis, and
// only a column declared Flex is ever shortened at all.
func (o *Output) Table(cols []Column, rows [][]string) {
	if o.Format == FormatJSON || o.Quiet || len(rows) == 0 || len(cols) == 0 {
		return
	}
	widths := o.tableWidths(cols, rows)
	if widths == nil {
		o.stackRows(cols, rows)
		return
	}

	var head strings.Builder
	head.WriteString(tableIndent)
	for i, c := range cols {
		head.WriteString(alignCell(truncateCell(c.Title, widths[i]), widths[i], c.Right))
		if i < len(cols)-1 {
			head.WriteString(strings.Repeat(" ", tableGap))
		}
	}
	o.note(fmt.Fprintln(o.Out, o.S(StyleDim, strings.TrimRight(head.String(), " "))))

	for _, r := range rows {
		var b strings.Builder
		b.WriteString(tableIndent)
		for i, c := range cols {
			cell := ""
			if i < len(r) {
				cell = r[i]
			}
			b.WriteString(alignCell(truncateCell(cell, widths[i]), widths[i], c.Right))
			if i < len(cols)-1 {
				b.WriteString(strings.Repeat(" ", tableGap))
			}
		}
		o.note(fmt.Fprintln(o.Out, strings.TrimRight(b.String(), " ")))
	}
}

// tableIndent puts a table under its heading like every other block, rather
// than flush against the left margin while the status lines and key value
// blocks around it sit two columns in.
const tableIndent = "  "

// tableWidths sizes the columns, or returns nil when the table cannot be made
// to fit and should be stacked instead.
func (o *Output) tableWidths(cols []Column, rows [][]string) []int {
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = cells(c.Title)
	}
	for _, r := range rows {
		for i := range cols {
			if i < len(r) {
				if n := cells(r[i]); n > widths[i] {
					widths[i] = n
				}
			}
		}
	}

	avail := o.Width - len(tableIndent)
	for total(widths) > avail {
		// The widest flexible column gives up a column at a time, so that two
		// flexible columns narrow towards each other rather than one being
		// flattened while the other keeps its slack.
		widest, at := tableFloor, -1
		for i, c := range cols {
			if c.Flex && widths[i] > widest {
				widest, at = widths[i], i
			}
		}
		if at < 0 {
			return nil
		}
		widths[at]--
	}
	return widths
}

// total is the rendered width of a set of columns, gaps included.
func total(widths []int) int {
	n := 0
	for _, w := range widths {
		n += w
	}
	return n + tableGap*(len(widths)-1)
}

// stackRows renders one block per row, for a terminal too narrow for columns.
//
// The first column is the heading, because in every table in this tree it is
// the name or identifier the row is about, and a block of labelled values
// under the thing they describe reads at forty columns where a table does not.
func (o *Output) stackRows(cols []Column, rows [][]string) {
	label := 0
	for _, c := range cols[1:] {
		if n := cells(c.Title); n > label {
			label = n
		}
	}
	for i, r := range rows {
		if i > 0 {
			o.note(fmt.Fprintln(o.Out, ""))
		}
		head := ""
		if len(r) > 0 {
			head = r[0]
		}
		o.note(fmt.Fprintf(o.Out, "%s%s\n", tableIndent, head))
		for j, c := range cols[1:] {
			cell := ""
			if j+1 < len(r) {
				cell = r[j+1]
			}
			if strings.TrimSpace(ansi.Strip(cell)) == "" {
				continue
			}
			indent := len(tableIndent) + 2 + label + 2
			o.note(fmt.Fprintf(o.Out, "%s  %s  %s\n", tableIndent,
				o.S(StyleDim, o.padTo(c.Title, label)),
				o.WrapTo(cell, indent, o.Width)))
		}
	}
}

// alignCell pads a cell to a width on the side its column is aligned from.
func alignCell(s string, w int, right bool) string {
	gap := w - cells(s)
	if gap <= 0 {
		return s
	}
	if right {
		return strings.Repeat(" ", gap) + s
	}
	return s + strings.Repeat(" ", gap)
}

// truncateCell shortens a cell that no longer fits its column, marking that it
// did. A value silently cut off is a value a reader will copy and be wrong.
func truncateCell(s string, w int) string {
	if cells(s) <= w {
		return s
	}
	if w <= 3 {
		return ansi.Truncate(s, w, "")
	}
	return ansi.Truncate(s, w, "...")
}

func pad(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-len(s))
}

// Error renders an error the way every command's failure is rendered: the
// code, what failed, and what to do about it.
//
// The next step is not optional and not a nicety. An error message that names
// the problem and stops is the difference between a user fixing something in
// thirty seconds and a user opening an issue.
func (o *Output) Error(err error) {
	if err == nil {
		return
	}
	// Writes here are not recorded and not checked, unlike the ones to the
	// output stream. This is the error path: if the error stream is broken
	// there is nowhere left to say so, and the exit code already carries the
	// failure to whatever is watching.
	var coded *aferrors.Error
	if !aferrors.As(err, &coded) {
		_, _ = fmt.Fprintf(o.Err, "%s %s\n",
			o.S(StyleBad, "Error:"), o.Wrap(err.Error(), len("Error: ")))
		return
	}
	// Every line hangs under the first word of the message rather than under
	// the code, so the sentence reads as one block with the code sitting out
	// to its left where the eye can find it. Before this, a message and a next
	// step ran to two hundred characters and the terminal broke them wherever
	// it happened to run out, mid word and hard against the left margin, which
	// is the rendering a user sees at exactly the moment they are already
	// stuck.
	code := string(coded.Code())
	_, _ = fmt.Fprintf(o.Err, "%s %s\n",
		o.S(StyleBad, code), o.Wrap(coded.Message(), cells(code)+1))
	if next := coded.NextStep(); next != "" {
		_, _ = fmt.Fprintf(o.Err, "  %s %s\n",
			o.S(StyleBold, "Next:"), o.Wrap(next, len("  Next: ")))
	}
	// The documentation link is never wrapped. Wrap splits on spaces and a URL
	// has none, so it runs long rather than breaking, which is the right way
	// round: a line that overhangs is ugly and a URL with a newline in it is
	// one somebody pastes and gets a 404 from.
	_, _ = fmt.Fprintf(o.Err, "  %s %s\n", o.S(StyleDim, "More:"), o.S(StyleDim, coded.DocsURL()))
	if o.Verbose {
		if cause := aferrors.Unwrap(coded); cause != nil {
			_, _ = fmt.Fprintf(o.Err, "  %s %s\n",
				o.S(StyleDim, "Cause:"), o.Wrap(cause.Error(), len("  Cause: ")))
		}
	}
}

// ErrorJSON is the machine readable form of an error, so that a script gets
// the same information the text form carries.
type ErrorJSON struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	NextStep  string `json:"next_step"`
	Docs      string `json:"docs"`
	Retryable bool   `json:"retryable"`
	ExitCode  int    `json:"exit_code"`
	Cause     string `json:"cause,omitempty"`
}

// ErrorDocument converts an error into its JSON form.
func ErrorDocument(err error) ErrorJSON {
	var coded *aferrors.Error
	if !aferrors.As(err, &coded) {
		return ErrorJSON{
			Code: "AF-GEN-000", Message: err.Error(),
			NextStep: "This is an unclassified failure. Please report it with the command you ran.",
			Docs:     "https://antifailure.dev/docs/reference/errors",
			ExitCode: int(aferrors.ExitFailure),
		}
	}
	doc := ErrorJSON{
		Code: string(coded.Code()), Message: coded.Message(), NextStep: coded.NextStep(),
		Docs: coded.DocsURL(), Retryable: coded.Retryable(), ExitCode: int(coded.ExitCode()),
	}
	if cause := aferrors.Unwrap(coded); cause != nil {
		doc.Cause = cause.Error()
	}
	return doc
}

// DetectColor decides whether to emit ANSI sequences.
//
// The precedence is the one users expect and every tool should follow:
// NO_COLOR wins over everything, then a dumb terminal, then whether the stream
// is a terminal at all. Getting this wrong fills CI logs with escape codes.
func DetectColor(w io.Writer, env func(string) string) bool {
	if env("NO_COLOR") != "" {
		return false
	}
	if env("AF_FORCE_COLOR") != "" {
		return true
	}
	if term := env("TERM"); term == "dumb" || term == "" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// wrapTokens splits text into the units a line break may fall between.
//
// Words, except that a single quoted run is one unit. Wrap already refuses to
// break a word, because a URL or a host name with a newline in it is not ugly,
// it is wrong once somebody copies it. A quoted command is wrong in exactly
// the same way and for exactly the same reason: every next step in the error
// catalogue says what to run as 'af something', and a reader who copies
// "af net explain GET" off the first line and pastes it gets a usage error
// out of the message that was supposed to rescue them.
//
// A run starts at a token whose first byte is a quote, which is what keeps an
// apostrophe inside a word from opening one, and it is abandoned if nothing
// closes it within a few tokens, which is what keeps an unbalanced quote from
// gluing the rest of a paragraph into one unbreakable line.
func wrapTokens(s string) []string {
	const maxQuotedRun = 12
	fields := strings.Fields(s)
	out := make([]string, 0, len(fields))
	for i := 0; i < len(fields); i++ {
		if !strings.HasPrefix(fields[i], "'") {
			out = append(out, fields[i])
			continue
		}
		end := -1
		for j := i; j < len(fields) && j < i+maxQuotedRun; j++ {
			if j > i && strings.Contains(fields[j], "'") {
				end = j
				break
			}
		}
		if end < 0 {
			out = append(out, fields[i])
			continue
		}
		out = append(out, strings.Join(fields[i:end+1], " "))
		i = end
	}
	return out
}

// SortedKeys returns a map's keys in order, so that rendering never depends on
// map iteration order. A snapshot test of command output would otherwise flake.
func SortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Wrap breaks prose to fit the terminal, indenting every line after the first.
//
// It wraps on spaces only and never splits a word, because a broken URL or a
// broken host name in an explanation is worse than a line that runs long: one
// is ugly, the other is wrong when somebody copies it.
//
// Prose stops widening at proseWidth even when the terminal is wider. A table
// is scanned down its columns and wants every column it can get; a sentence is
// read left to right and a two hundred character line loses the eye on the
// return sweep.
func (o *Output) Wrap(s string, indent int) string {
	width := o.Width
	if width > proseWidth {
		width = proseWidth
	}
	return o.WrapTo(s, indent, width)
}

// WrapTo wraps to an explicit width, for the callers that want the whole
// terminal rather than the prose measure.
func (o *Output) WrapTo(s string, indent, width int) string {
	if width < minWidth {
		width = minWidth
	}
	avail := width - indent
	if avail < 20 {
		avail = 20
	}
	words := wrapTokens(s)
	if len(words) == 0 {
		return ""
	}
	var b strings.Builder
	pad := strings.Repeat(" ", indent)
	line := 0
	for i, w := range words {
		n := cells(w)
		switch {
		case i == 0:
			b.WriteString(w)
			line = n
		case line+1+n <= avail:
			b.WriteByte(' ')
			b.WriteString(w)
			line += 1 + n
		default:
			b.WriteByte('\n')
			b.WriteString(pad)
			b.WriteString(w)
			line = n
		}
	}
	return b.String()
}
