# added

The deletion wording on the legal pages is now gated against the constraint
that would have to enforce it. The strong claim, that a row cannot be removed,
is permitted only where the foreign key is NO ACTION or RESTRICT. A migration
comment asserted that guarantee about a constraint that is ON DELETE SET NULL,
and it was one review away from becoming published legal text.
