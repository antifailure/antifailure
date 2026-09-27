# fixed

A terminal workflow could wait past its time budget while its emulator was
still parsing output. Active programs now return a blocked result when that
budget expires, including when they go quiet with unread output. Programs
that have exited still drain their complete output before their result is
judged.
