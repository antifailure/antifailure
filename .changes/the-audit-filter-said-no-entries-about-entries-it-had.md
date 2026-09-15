# fixed

The audit log's filter matched an action only when you typed the whole dotted
name exactly, so typing `billing` answered "No entries with that action" while
billing entries sat in the log. It now matches any action containing what you
typed, and a typed `%` or `_` stays a literal character rather than becoming a
wildcard.

The same box sent one request per keystroke, so a seven letter word was seven
requests and six discarded answers. It now waits for the typing to stop.

The filtered empty state told you to clear the filter and gave you nothing to
clear it with. It now carries the same "Clear the filter" control the operator
console already offers for the identical case.
