# fixed

On Windows a process that only looked at a branch lock could leave it held for
the rest of the holder's life. Asking who holds a lock opened the lock file
without sharing delete, and Windows refuses to remove a file that any handle
has open that way, so a holder releasing its lock while somebody else was
reading it failed. Every caller drops that error, so the file stayed, it named
a process that was still running, and every later attempt to take the lock was
refused with AF-RUN-003 until that process exited. For `af mcp`, which can hold
locks for hours, that was a branch nobody could use until the server was
restarted. The required Windows check reached it from the replay store's own
test, which retires and imports one incident at the same time.

The lock is read sharing delete now, a release retries the moment another
process is still in the middle of a remove, and a waiter treats that moment as
the lock changing hands rather than as an error.

Taking a lock also published the lock file before writing its owner into it,
so a waiter that looked in between found an empty file, took it for a crash and
removed it. On Unix that could remove a lock a live process was still writing;
on Windows the remove was refused and the waiter failed instead of queueing.
The owner is written to a private file first and linked into place, so the lock
never exists without its contents.

And on every platform, a lock released between a waiter's attempt and its look
used to count as a reclaim, so after two of them the waiter reported "no such
file or directory" where it should have gone on waiting. It now tries again.
