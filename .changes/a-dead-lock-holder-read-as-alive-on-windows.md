# fixed

On Windows, a lock whose holder had died could read as still held, so `af`
waited on an owner that no longer existed instead of reclaiming the lock.
Liveness asked only whether the holder's process could be opened, and Windows
keeps an exited process openable for as long as anything holds a handle to it,
an antivirus scan included. It now asks whether the process has ended, which an exit code cannot answer.
