# added

The analytics catalog, which is the whole vocabulary the store may contain: an
event name not in it is refused and counted, and so is a payload field it does
not declare. There is no free-text field kind, so a repository name or a query
string cannot reach the database even by mistake.

Milestones live in the organization facts table rather than in the stream.
"The first time this organization proved something" was an event until the
ordering tests showed two concurrent batches could both claim to be the first;
as a column set with LEAST it has no race and converges to the same date
whatever order events arrive in.
