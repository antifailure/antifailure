// Package textwrap breaks a line of prose to fit a terminal.
//
// It exists as its own package because two renderers need the same rules and
// neither can import the other: the command layer wraps everything a command
// prints, and internal/manifest renders the effective configuration as a
// string that the command layer only prints. Two copies of a wrapping rule
// eventually disagree about what may be broken, and the whole value of the
// rules below is that they are the same everywhere.
package textwrap

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// DefaultWidth is what a caller with no terminal to measure gets: eighty,
// because that is what a captured log, a pull request comment and a terminal
// with no size all default to, and because a stream with no size getting a
// fixed answer is what keeps text output byte stable for the same input.
const DefaultWidth = 80

// MinWidth is the narrowest terminal this wraps for. Below it, text stops
// shrinking: prose reflowed into a four word column is not readable, it is
// just narrow.
const MinWidth = 40

// minAvail is the least room a line is given after its indent, so that a
// deeply indented value still gets a usable column rather than one word.
const minAvail = 20

// Wrap breaks s to fit width, indenting every line after the first.
//
// Two things are never broken, for the same reason: a line that overhangs the
// right margin is ugly, and a token with a newline in the middle of it is
// wrong the moment somebody copies it.
//
// A word is never split, which is what keeps a URL or a host name intact.
//
// A single quoted run is never split either, which is what keeps a command
// intact: every next step in the error catalogue names what to run as 'af
// something', and a reader who copies "af net explain GET" off the end of one
// line and pastes it gets a usage error out of the message that was meant to
// rescue them. A run starts only at a token whose first byte is a quote, which
// is what stops an apostrophe inside a word from opening one, and is abandoned
// if nothing closes it within a few tokens, which is what stops an unbalanced
// quote from gluing a paragraph into one unbreakable line.
func Wrap(s string, indent, width int) string {
	if width < MinWidth {
		width = MinWidth
	}
	avail := width - indent
	if avail < minAvail {
		avail = minAvail
	}
	words := tokens(s)
	if len(words) == 0 {
		return ""
	}
	var b strings.Builder
	pad := strings.Repeat(" ", indent)
	line := 0
	for i, w := range words {
		n := Cells(w)
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

// Cells is the display width of a string, ignoring the escape sequences that
// colour it.
//
// Measuring with len would be wrong twice over: a styled run carries bytes
// that occupy no columns, and a multi byte character occupies one byte per
// byte and one or two columns, which are two different wrong answers in
// opposite directions.
func Cells(s string) int { return ansi.StringWidth(s) }

// tokens splits text into the units a line break may fall between.
func tokens(s string) []string {
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
