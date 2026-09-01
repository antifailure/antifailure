# fixed

A project with no production database refreshed a golden on every `af up`.

The path that creates an empty golden stamped `seedRulesHash(seed)`, which for
a project with no seed command is the literal string `empty`, while selection
asked for the digest of an empty rule set. Those two never matched, so the
golden made by one run was refused by the next and a new one was built each
time. Both paths now record the same project identity, so the second run
branches what the first one made.
