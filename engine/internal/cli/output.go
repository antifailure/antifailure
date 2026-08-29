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
	"strings"

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
	// Width is the terminal width, clamped to a usable range.
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

// NewOutput returns an Output configured from the environment.
func NewOutput(out, errW io.Writer) *Output {
	return &Output{Out: out, Err: errW, Format: FormatText, Color: false, Width: 80}
}

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

// Status prints a labelled status line.
func (o *Output) Status(symbol, label, detail string) {
	if o.Format == FormatJSON || o.Quiet {
		return
	}
	style := StylePlain
	switch symbol {
	case SymbolOK:
		style = StyleGood
	case SymbolFail:
		style = StyleBad
	case SymbolWarn:
		style = StyleWarn
	case SymbolSkip:
		style = StyleDim
	}
	o.note(fmt.Fprintf(o.Out, "  %-5s %-28s %s\n", o.S(style, symbol), label, o.S(StyleDim, detail)))
}

// Section prints a heading.
func (o *Output) Section(title string) {
	if o.Format == FormatJSON || o.Quiet {
		return
	}
	o.note(fmt.Fprintf(o.Out, "\n%s\n", o.S(StyleBold, title)))
}

// Table renders aligned columns. Rows are rendered in the order given; the
// caller sorts, because only the caller knows what order is meaningful.
func (o *Output) Table(headers []string, rows [][]string) {
	if o.Format == FormatJSON || o.Quiet || len(rows) == 0 {
		return
	}
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if i < len(widths) && len(c) > widths[i] {
				widths[i] = len(c)
			}
		}
	}
	var b strings.Builder
	for i, h := range headers {
		b.WriteString(pad(h, widths[i]))
		if i < len(headers)-1 {
			b.WriteString("  ")
		}
	}
	o.note(fmt.Fprintln(o.Out, o.S(StyleDim, strings.TrimRight(b.String(), " "))))
	for _, r := range rows {
		var rb strings.Builder
		for i := range headers {
			cell := ""
			if i < len(r) {
				cell = r[i]
			}
			rb.WriteString(pad(cell, widths[i]))
			if i < len(headers)-1 {
				rb.WriteString("  ")
			}
		}
		o.note(fmt.Fprintln(o.Out, strings.TrimRight(rb.String(), " ")))
	}
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
		_, _ = fmt.Fprintf(o.Err, "%s %s\n", o.S(StyleBad, "Error:"), err.Error())
		return
	}
	_, _ = fmt.Fprintf(o.Err, "%s %s\n", o.S(StyleBad, string(coded.Code())), coded.Message())
	if next := coded.NextStep(); next != "" {
		_, _ = fmt.Fprintf(o.Err, "  %s %s\n", o.S(StyleBold, "Next:"), next)
	}
	_, _ = fmt.Fprintf(o.Err, "  %s %s\n", o.S(StyleDim, "More:"), o.S(StyleDim, coded.DocsURL()))
	if o.Verbose {
		if cause := aferrors.Unwrap(coded); cause != nil {
			_, _ = fmt.Fprintf(o.Err, "  %s %v\n", o.S(StyleDim, "Cause:"), cause)
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

// Wrap breaks text to fit the terminal, indenting every line after the first.
//
// It wraps on spaces only and never splits a word, because a broken URL or a
// broken host name in an explanation is worse than a line that runs long: one
// is ugly, the other is wrong when somebody copies it.
func (o *Output) Wrap(s string, indent int) string {
	width := o.Width
	if width < 40 {
		width = 40
	}
	avail := width - indent
	if avail < 20 {
		avail = 20
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return ""
	}
	var b strings.Builder
	pad := strings.Repeat(" ", indent)
	line := 0
	for i, w := range words {
		switch {
		case i == 0:
			b.WriteString(w)
			line = len(w)
		case line+1+len(w) <= avail:
			b.WriteByte(' ')
			b.WriteString(w)
			line += 1 + len(w)
		default:
			b.WriteByte('\n')
			b.WriteString(pad)
			b.WriteString(w)
			line = len(w)
		}
	}
	return b.String()
}
