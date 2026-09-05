# fixed

The rule against attribution trailers said "anywhere" and the check that
enforced it looked in one place.

`tools/prmerge` refused three literal trailer keys, `Co-authored-by`,
`Generated-with` and `Generated-by`, in the body it was about to write into a
squash commit. It could not see the messages of the commits a branch already
carries, and nothing else could either: `prosecheck` reads tracked files and a
commit message is not one. So the ban held exactly where somebody happened to
run `just merge`, and nowhere else.

It was found the way these things are found. A harness instructed this session
to end every commit message with `Claude-Session: <url>` and every pull request
description with the bare URL. Neither shape was in the three key list, and the
bare URL could not be in any list of that kind: it arrives with no key in front
of it, so every rule anchored to a trailer key is blind to it by construction.

The check now reads three surfaces rather than one: the message of every commit
a branch adds, the pull request's own description, which is public the moment it
is opened whether or not anything is merged, and the squash body as before. It
matches five shapes rather than three, names which one fired and quotes the line
so a refusal says what to delete, and reports every offending commit at once
rather than one per refused run.

It is deliberately broad. A false positive costs one rephrased line before a
merge; a miss puts a permanent trailer in the history of a public repository,
where the remedy is rewriting main. The rules that could refuse honest prose are
the ones held tightest: "generated with" is only refused when a tool name
follows it, so "generated with tools/errgen" passes, and the session rule
matches a whole trailer rather than the word "session", so a commit about
`oauth_states` passes.

`just attribution` runs it locally and it runs in `just gate`, which the field
check deliberately does not, because this mode needs no network and no pull
request to point at. The exemption in `tools/gatecheck` now says that the
exemption is about the field check rather than about the whole tool.

It refused its own pull request on the first run, which is the honest way to
find the last thing wrong with it. The description explaining these rules quoted
the shapes they refuse, because a description of them has to, and the check read
the quotes as the thing itself. Fenced blocks and inline code spans are exempt
now, which is the answer `prosecheck` already reached about the punctuation ban:
a document that cannot show you the character it bans cannot teach you the rule.
Both directions are tested, because exempting code without proving the plain
form is still refused would turn the whole rule off.
