# fixed

Nothing checked that the repository index detection is built on is sorted, and
two things rest on it. `Exists` binary searches the slice, so an index left in
walk order does not fail loudly: it answers "no" about a file that is indexed.
And the analyzer that turns a directory of numbered SQL files into a migrate
command reads that index in order, which is the order a database replays the
files in.

Every fixture in the suite put its files in one directory, where a walk and a
sort produce the identical list, so removing the sort turned nothing red. The
new fixture is the minimal pair where the two disagree, a file and a directory
whose names differ only after the dot, and removing the sort now fails three
assertions: the index order itself, and each of the two lookups.
