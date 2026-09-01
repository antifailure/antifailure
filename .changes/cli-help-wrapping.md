# fixed

Help text now fits the terminal it is printed on. Every command's description,
its flag table and its list of subcommands were printed at whatever width they
were written at, so at 40 columns every help page in the tool ran past the
margin and the terminal broke it mid word, and at 200 it was a ribbon down the
left of an empty screen.

`af status` says which branch it is reporting on, not only the environment
identifier, which is that branch with the punctuation taken out and a hash on
the end.
