# changed

`af token list` lets STATE give up width alongside NAME when the terminal is
too narrow for the table. PREFIX now keeps its full width in every terminal,
because it is exactly what `af token rm` takes as an argument, and a shortened
prefix prints something that does not work when it is pasted back. STATE reads
`revoked 12 Mar` or `active`, so losing the date still leaves the word that
decides anything.
