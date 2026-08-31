# fixed

The command line now measures the terminal it is writing to. Text that used to
run past the right margin, or wrap wherever the terminal happened to run out of
room, now breaks at a word boundary with a hanging indent: error messages and
their next steps, doctor's remediations, and every status line's detail.

Tables no longer misalign when colour is on. Widths were measured in bytes, so
the nine bytes of escape sequence around a coloured cell counted as nine
columns and pushed every heading to the right of the values beneath it.

Numbers in tables are right aligned, one column per table absorbs a terminal
too narrow for the whole thing, and a table that still cannot fit is stacked as
one labelled block per row rather than being left to the terminal's own
wrapping.
