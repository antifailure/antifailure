# fixed

The variable naming production was read from the shell and nowhere else, and
`af start` called that fine.

The secrets guide opens with `source_url_env: PRODUCTION_DATABASE_URL` as its
example and then lists the four places a value is looked up: this shell, `.env`,
the encrypted local store, and the system keyring. This one variable read the
first and none of the others. A project that put the production URL in `.env`,
beside the `STRIPE_SECRET_KEY` that `af up` finds there, was told the variable
held nothing. The two places a production credential actually belongs, the
encrypted store and the keyring, were unreachable for it, so
`af secret set PRODUCTION_DATABASE_URL` stored a value nothing would read.

It now goes through the same chain as every other name in the manifest, built
by the same constructor, so a command that says where a value will come from
cannot describe a different chain than the one that fetches it. The order is
unchanged and an export still beats a file.

The other half is `af start`. It reported

    ok    the database source          docker, so it comes from the daemon
                                       checked above

for a manifest that named production and a machine that did not hold it. That
sentence is true about the provider and says nothing about the variable, and
the reader was pointed at the next command. It now says which of the four
sources answered, or refuses to call the step finished:

    ok    the database source          docker, copying the database named by
                                       PRODUCTION_DATABASE_URL, found in .env

    fail  the database source          docker copies the database named by
                                       PRODUCTION_DATABASE_URL, and no
                                       configured source has it

AF-DB-016 said the variable held nothing "in this shell", which was accurate
about the old behaviour and would now be half the answer. It names the searched
sources instead.
