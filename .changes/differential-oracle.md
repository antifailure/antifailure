# added

`af oracle` runs a change beside the version it replaces. It brings a second
environment up from a baseline revision, branches the same golden for both so
they start from identical rows, sends both the same requests in the same order,
and reports every difference in what came back and in what ended up in the
database. Responses and database contents are compared completely; events,
outbound effects, traces and query plans are not compared at all, because two
comparisons done properly are worth more than six done shallowly.

Values that no two runs can agree on are normalised before they are compared:
two timestamps within an hour, two UUIDs, two numbers within a relative
tolerance. Sequence identifiers and numeric epochs are compared exactly on
purpose, and everything the comparison declined to look at is printed on every
run, defaults included.
