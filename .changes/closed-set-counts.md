# fixed

The documentation said we catch six classes of unsafe migration. We catch
seventeen. Every rule added since that sentence was written landed in the code
and in the lint rules table and in no sentence that counts them, so the
reference, the verdicts page and the manifest schema all understated what the
migration lint does by eleven rules. Detection is thirteen analyzers, not
twelve, and nine migration tools are rehearsed, not seven.

A new gate, `just constcheck`, reads each of these sets out of the Go source
and fails the build when prose states a count that is not its real size. It
also reads the three documentation tables that are the reference for a set and
checks them row for row, because the page that lists all seventeen rules
correctly states no number anywhere, so a count rule alone would never reach
it.
