# fixed

The command line page ended in a table called "Terminals and tokens" with
roughly seven hundred rows in it, and the reader's own signed in terminal was
near the bottom. Almost every row was the same thing: a fifteen minute
credential issued to a GitHub Actions run, eight per run, three days deep, all
of them expired since the minute after they were issued. The page whose job is
to walk somebody through installing the command line ended in a wall of dead
machine credentials.

Two things caused it and both are fixed.

The table showed every row, ungrouped and unbounded. It now leads with
everything that can still act, in full and one row each, with the button that
takes it away. Nothing live is ever grouped, counted or folded away, because
this page is also how somebody notices a credential they did not expect.
Everything that has expired or been revoked is behind a disclosure with its
count on the label, and inside it the credentials from one Actions run are one
line carrying how many there were and how many of them were ever used.

And nothing had ever removed one. Expired workflow identities are now swept a
day after they die, which is ninety six times their own lifetime, so this
morning's runs are still on the screen and last week's are gone. The sweep
cannot reach a live credential, a revoked one, a person's signed in terminal or
an engine token pasted into a build machine: it runs as a role of its own whose
policy admits expired workflow identities and nothing else, decided on the
database's clock rather than on anything the application passes it. A revoked
credential is kept whatever its age, because the revocation is the record of it,
and the audit entry written when a credential is issued survives the sweep in
every case.
