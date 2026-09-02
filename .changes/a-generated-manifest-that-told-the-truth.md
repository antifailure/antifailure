# fixed

`af init` wrote a header saying "Every value here came from a file: a package
manifest, a Dockerfile, a compose file, or a dependency list", and directly
beneath it two personas at `example.test` and a `sign-up` workflow describing a
form. None of those came from anything: `defaultPersonas` returns the same two
accounts for every repository and `suggestedWorkflows` emits `sign-up` whatever
the dependencies say. So a first time reader was told the file described their
repository, ran the two commands the tool printed, and the second failed on a
users table their JSON API does not have.

The mechanism built to prevent exactly this could not see it. `assumed` was fed
only through `resolveQuestions`, and personas and workflows never become
questions, so the only guess `af init` ever disclosed was `database.present`.
It is now seeded from what the draft holds before any question is asked, so a
value nothing asked about can still be disclosed.

The header says what is true, the two guessed blocks carry a comment where they
appear rather than only in a summary printed once, and the rendered bytes are
still parsed before they are written, so a note that broke the document would
fail the command rather than reach a file.

They are still written, and deliberately. Nothing in a repository proves the
absence of authentication: the auth detector recognises five frameworks, so a
hand rolled sign in produces no finding and looks exactly like no sign in at
all. Dropping the personas on that evidence would trade a loud failure for a
silent wrong answer.
