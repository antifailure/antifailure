# added

Custom roles: an organisation can define a role, grant it permissions, assign it
to a member at a scope, and have that member's requests allowed or refused
accordingly.

The enterprise licence sold this, `LICENSING.md` and `ee/README.md` listed it,
and no organisation could have a custom role. Both halves of the mechanism
existed and neither was joined to the other. `permits()` in the community API has
asked an installed permission resolver on every request since it was written, and
`ee/web/rbac` has always carried a role model, a validator, a scope resolver and
a reviewable file format that answer in exactly that resolver's shape. Nothing
stored a model, so there was nothing to build a resolver from, and nothing
installed one outside that package's own tests.

What is new is the join: migration 0044 stores a model per organisation under row
level security, the enterprise edition mounts four routes that export, preview and
apply it as a reviewed YAML file, and its entry point installs the resolver. The
resolver reads the model on every request a built-in role would refuse, per
request rather than from a cache, for the reason `orgProcedure` already re-reads
the member's role: a model held in memory is a revocation that does not take
effect on the replica holding it.

`rbac` is now enforced rather than reported. It leaves the `unenforced` map in
`ee/engine/license/license.go`, which is empty as a result and deleted along with
the licensegen warning it fed and the checks that read it, because a warning that
cannot fire teaches whoever reads it that this state does not exist.

The four built-in roles are unchanged. A resolver is asked only where the
built-in table has already refused, so a custom role can widen a role and can
never narrow one. That was the documented rule and not the behaviour: `permits`
returned a resolver's `false` over the table's `true`, under a test named "a
resolver cannot take away what a built-in role grants" that asserted it had.

`members.list` now returns each member's `user_id`. A grant names a person by
that id, and no route an organisation can call answered it: the list stripped the
field from every row, while `nextCursor` carried the last row's id to the same
caller anyway. So the feature above could be used only by somebody with a
database console. The id rather than a GitHub login, because a member who signs
in through single sign-on can have no GitHub login at all.

Any member of an organisation can read these ids, viewers included, because
`members.list` needs only `environments.view`, which all four built-in roles
hold. A viewer already sees every member's login, name and role, and an id is
not a credential: writing a grant with one needs `members.manage`. The list is
bounded to the caller's own organisation by two row level security policies,
one on `members` and one on `users`, and either one alone holds. A test proves
that with no cursor and with a cursor forged from another organisation's member.
