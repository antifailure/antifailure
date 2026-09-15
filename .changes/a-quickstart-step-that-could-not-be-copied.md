# fixed

Four things on the quickstart path said something the software does not do.

`af net explain` was printed with no method, so the documented line answered with a
usage error. The `af start` sample was missing the masking rules row. The page said
only a real failure exits non zero, where a run that never reached a verdict exits
on the configuration problem that stopped it. `concepts/load` said a blocked
scenario still exits non-zero, where `af ci` exits non-zero only on a failure.

`af explain` printed `masking      masking.yaml` for a project with no such file.
The path is a default and the engine reads its built in rules when the file is not
there.

The three getting-started pages are 75 lines shorter, with every command, flag,
path, version minimum and error code untouched.
