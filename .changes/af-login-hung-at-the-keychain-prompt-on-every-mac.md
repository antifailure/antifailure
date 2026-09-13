# fixed

On a Mac, `af login` could not sign anyone in, and `af model set` could store
a broken key, since v1.3.0.

In a terminal, `af login` and `af model set` hung at "password data for new
item:" and stored nothing. The keychain write fed the value to the `security`
command's password prompt on stdin, and `security` reads a prompted password
from the terminal whenever there is one.

Without a terminal, the same write kept only the first 128 bytes of the value
and reported success. The credential `af login` stores is longer than that, so
`af login` said "Signed in" and every later command failed with AF-SEC-006,
"unexpected end of JSON input". `af model set` did the same to any key longer
than 128 bytes, which includes OpenAI project keys (`sk-proj-`), so every model
call with that key was then refused by the provider. Anthropic keys are shorter
than 128 bytes and were stored whole.

The write now goes through `security`'s interactive command stream, which reads
stdin with or without a terminal and keeps the value off the process listing.
Every write is read back and compared, so a value the keychain did not keep
exactly is an error rather than a success. Values of any bytes, line breaks
included, are stored whole up to about two kilobytes; a longer value goes to
the fallback store and the command says where it went. Reading a keychain value
holding a tab or a non-ASCII character no longer returns it as hex.

To recover, upgrade and run the same command again: `af login` for a
credential, `af model set` for a key. The new write replaces the damaged entry,
so nothing needs deleting first.
