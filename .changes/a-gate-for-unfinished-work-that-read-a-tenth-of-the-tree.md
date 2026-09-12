# fixed

`tools/docs/forbidden.sh` has refused the marker words and the two word promise
since it was written, and it reads six paths, all of them Markdown:
`docs/src/content/docs`, `examples`, and four files at the repository root. That
is 300 of the 2875 tracked files. So a note to the author in any Go file, any
SQL migration, any Terraform stack, any workflow, the justfile, the api's or the
runner's TypeScript, the console, the marketing site, or any of the seventy
gates in `tools` passed every check this repository runs, and nothing said so.
The one instrument with an opinion about unfinished work was pointed at a tenth
of the tree.

`tools/defercheck` asks the same question of the whole git index. Five rules: a
note to the author, a promise instead of a capability, work that calls itself
short lived, work handed to a later change, and the one no scan for words can
reach, a test that skips without saying why. That last rule is the reason this
is worth more than a grep. A marker is the weakest form of deferred work because
somebody wrote it down; a disabled test is the strongest, because it reports a
skip, the suite stays green, and the count in the summary reads as a pass.

Three suites were in that state. `admincontrols`, `adminfleet` and `adminhealth`
each registered one empty test with the skip option set to the bare literal
true, which states no condition, so a reader could not tell a suite somebody
turned off from one whose database was absent, and the summary said one skipped
test whatever the file held. Each now names what was measured and the variable
that fixes it, and says that the whole suite did not run rather than that one
test did not.

The rules are narrow where the tree earned it, and the exemptions say why rather
than where. A row quotes a fragment of the line it excuses, so it covers that
line and nothing else: the row for the sentence in `ci.yml` about another
project's parser does not cover the next marker somebody writes in that
workflow. There are twelve rows and four reasons among them, none of which is
that a file is allowed to be unfinished. Three quote another project's own words,
one is the shape of an error code, seven are a sibling gate's rule and its
fixtures, and one is a fixture for the tool that finds pages like it. The tool's
own marker words are assembled from fragments instead, so it is subject to itself
rather than exempt from itself. That was tested the hard way: the first commit of
the tool and its exemptions was refused by the tool, four times, for a comment
spelling the empty reason skip it forbids and two rows quoting the very phrases
they excuse. Neither file had been read before, because an untracked file is not
in the index this gate reads.

It also says what it did not check, and that is how it found
`runner/src/cassette.ts`. The cassette key is hashed with a NUL separator,
because no provider name, model name or prompt can contain one, and the byte was
written as the character rather than as an escape. That made the file BINARY to
git and to every text tool here at once: `git grep -I`, `tools/prosecheck` and
`tools/docs/forbidden.sh` all skip a file holding a NUL, so a tracked TypeScript
source was invisible to all of them and nothing reported it. The escape produces
the identical byte at run time, proven against `String.fromCharCode(0)`, so
every recorded key is unchanged, and the runner's 138 tests pass with none
skipped. A tracked file that cannot be read is now a failure unless its name
says it is an image or a font, in which case it is named in the report rather
than counted as checked.

One capability was defined and never read. `FAILED_STATES` in
`web/apps/api/src/admin/operations.ts` exists, in its own comment's words, so
that nobody counts only `failed` and forgets `timed_out` and `abandoned`. Nothing
read it. The failure groups query typed the three states out again inline, so a
fourth terminal state added to the constant would have been missing from the
operator's failure list with every check green. The query now takes its list from
the constant, which drizzle binds as three parameters. Narrowing the constant to
`failed` alone turns the three state test red, one group where it wants three,
and restoring it turns it green, against a Postgres of its own.

And one promise reached a user. `AF-RUN-001` says a command is not available in
this version, and its next step was "See the roadmap for when it lands". There
is no roadmap page in the documentation and never has been, so the one thing the
sentence asks the reader to do cannot be done. It now names `af --help` for what
the binary carries, `af version` for which build it is, and `af update` for
replacing it, all of which exist.

Every assertion in the gate's own tests was watched failing against the line it
exists to catch and passing once the bytes were restored, nineteen cells in all
counting the query above, and one marker was planted in each of eleven language
families in the real tree and named by file and line. The cases that must PASS are as important as
the ones that must fail and there are seven of them, every one a real line from
this repository: an explained skip in both languages, the conditional skip this
repository already writes, a sentence about rows not yet written inside a
transaction, the reaper's own use of the word deferred, an error's next step, a
digest whose letters spell a marker, and a button that offers to skip a step.
A gate that fired on any of those would be answered by making the product worse.
