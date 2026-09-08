# security

The credential gate said a tree was clean when it had read no files.

`no credentials in the tree` is a required context on every pull request, and
`tools/scanrepo` printed one sentence for two different outcomes. Its walk
swallowed the callback error, the stat error and the read error, each with a
bare `return nil`, and nothing counted what had been examined, so a root that
does not exist, a directory the scan could not enter, and a genuinely clean
repository all produced "no live credentials in the tree" and exit 0. A
credential inside a directory that would not open was not found, and not
finding it was reported as its absence.

Its own suite pinned the gap as correct. A case called
`TestAMissingRootIsNotASilentPass` asserted the silent pass: it required a nil
error and no findings from a root that is not there, and closed with a comment
saying scanrepo trusts its argument. So the test list carried a green tick
under a name promising the hole was closed.

There are three answers now. A credential was found, the tree was read and is
clean, or the scan could not look. The last one is refused and says which
paths went unread, and the pass now reports how many files it read. The
wording is the one fourteen other checks in this repository already use for
this, which is the point: the idiom existed and the credential gate was the
tool that did not use it.
