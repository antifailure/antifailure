package cli

import (
	"fmt"
	"strings"
)

// The shared vocabulary every command renders through.
//
// The reason this file exists: each command in this tree was written on its
// own day and rendered on its own terms, and the result was eight tables in
// two casings, five spellings of "here is what to do next" at three different
// indents, two empty states that named no way out of being empty, and prose
// advisories that overflowed the terminal because nothing wrapped them. None
// of those is a bug in a command. They are what a command surface looks like
// when there is no vocabulary, and patching them one command at a time would
// leave the ninth command free to invent a ninth shape.
//
// So: a heading, a key value block, a status line, a table, a hint, an empty
// state and a note. Everything a command has to say fits one of those. A
// command that needs a shape this file does not have should add it here.

// blockIndent is how far a block of content sits inside its heading. Two
// columns, everywhere, so that headings are the only thing on the left margin
// and the eye can find them by scanning one column.
const blockIndent = "  "

// blockGap separates a label from its value.
const blockGap = 2

// Block is a key value list: a label column and a value column, sized to the
// block's own longest label.
//
// Sized per block rather than globally, because a run of related facts reads
// as a unit and a label column padded to fit the longest label anywhere in the
// command leaves a valley of whitespace in every short block.
type Block struct {
	o    *Output
	rows [][2]string
}

// Block starts a key value list.
func (o *Output) Block() *Block { return &Block{o: o} }

// Add appends a label and its value.
func (b *Block) Add(label, value string) *Block {
	b.rows = append(b.rows, [2]string{label, value})
	return b
}

// Addf appends a formatted value.
func (b *Block) Addf(label, format string, args ...any) *Block {
	return b.Add(label, fmt.Sprintf(format, args...))
}

// AddIf appends only when the condition holds, so that a caller assembling a
// block from optional facts does not need an if around every line.
func (b *Block) AddIf(cond bool, label, value string) *Block {
	if cond {
		b.Add(label, value)
	}
	return b
}

// Flush renders the block.
//
// Values wrap under themselves rather than under the label, so a long one
// stays in its own column and the labels remain a readable list down the left.
func (b *Block) Flush() {
	o := b.o
	if o.Format == FormatJSON || o.Quiet || len(b.rows) == 0 {
		return
	}
	label := 0
	for _, r := range b.rows {
		if n := cells(r[0]); n > label {
			label = n
		}
	}
	indent := len(blockIndent) + label + blockGap
	for _, r := range b.rows {
		o.note(fmt.Fprintf(o.Out, "%s%s%s%s\n",
			blockIndent,
			o.padTo(r[0], label),
			strings.Repeat(" ", blockGap),
			o.Wrap(r[1], indent)))
	}
}

// Subject names what the command is acting on and where, under its heading.
//
// The context line is the terminal's version of knowing where you are. A
// command that prints an environment identifier alone leaves the reader to
// work out which branch produced it, and the identifier is that branch with
// the punctuation taken out and a hash on the end, which is not something
// anybody can check against the branch they think they are on. Running on the
// wrong branch is one of the two most common ways to be confused by this tool.
//
// Both halves are inputs rather than observations, so this stays byte stable.
func (o *Output) Subject(title, context string) {
	if o.Format == FormatJSON || o.Quiet {
		return
	}
	o.note(fmt.Fprintf(o.Out, "\n%s\n", o.S(StyleBold, title)))
	if context != "" {
		o.note(fmt.Fprintf(o.Out, "%s\n", o.S(StyleDim, o.Wrap(context, 0))))
	}
}

// Hint says what to do next, and is the only shape in which this tree says it.
//
// text is the prose and command is what to run, separated so that the command
// is always the emphasised half and always the part a reader can pick out and
// copy. There were five shapes of this line before: two indents, one with a
// leading blank line and one without, and none of them distinguishing the
// prose from the command, which is the one distinction that matters when
// somebody is scanning for the thing to type.
func (o *Output) Hint(text, command string) {
	if o.Format == FormatJSON || o.Quiet {
		return
	}
	if command == "" {
		o.note(fmt.Fprintf(o.Out, "%s%s\n", blockIndent, o.Wrap(text, len(blockIndent))))
		return
	}
	// The command is placed rather than wrapped into the prose, because Wrap
	// breaks on spaces and would put "af webhook list" on one line and
	// "stripe" on the next, which is a command a reader copies and gets a
	// usage error from. It goes on the same line when it fits and on its own
	// line indented under the prose when it does not, and it is never broken.
	line := blockIndent + o.Wrap(text+":", len(blockIndent))
	last := line
	if i := strings.LastIndexByte(line, '\n'); i >= 0 {
		last = line[i+1:]
	}
	if cells(last)+1+cells(command) <= o.Width {
		o.note(fmt.Fprintf(o.Out, "%s %s\n", line, o.S(StyleBold, command)))
		return
	}
	o.note(fmt.Fprintf(o.Out, "%s\n%s  %s\n", line, blockIndent, o.S(StyleBold, command)))
}

// Empty says there is nothing to show, and what would put something there.
//
// The second half is not decoration. An empty state that names no way out is
// indistinguishable from a broken command: "The store is empty" and "Nothing
// has been sent yet" both used to end there, and a reader who did not already
// know how a store gets filled learned nothing from either.
//
// command may be empty when there genuinely is nothing to do, as when a policy
// declares no rules on purpose. Nothing else may leave it empty.
func (o *Output) Empty(what, hint, command string) {
	if o.Format == FormatJSON || o.Quiet {
		return
	}
	o.note(fmt.Fprintf(o.Out, "%s%s\n", blockIndent, o.Wrap(what, len(blockIndent))))
	if hint != "" {
		o.Hint(hint, command)
	}
}

// Note is an aside under a block: wrapped, indented, and styled by what it is.
//
// StyleDim for something worth knowing, StyleWarn for something worth
// checking. These were bare Printf calls with a hardcoded indent and no
// wrapping, which is why the one under af init's egress table ran off the
// right of an eighty column terminal.
func (o *Output) Note(style Style, text string) {
	if o.Format == FormatJSON || o.Quiet {
		return
	}
	o.note(fmt.Fprintf(o.Out, "\n%s%s\n",
		blockIndent, o.S(style, o.Wrap(text, len(blockIndent)))))
}
