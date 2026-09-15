# fixed

Four safety claims said more than the design does, and each is now shorter.

The firewall page said request and response redaction is mandatory. Nothing redacts
a request or a response, and the mode that forwards to a live endpoint does not
intercept, so it cannot. The transforms reference said the masking key differs
across goldens so a mapping could not be reversed; one key is generated and kept, on
purpose, because that is what makes two goldens comparable. Uniqueness was claimed
for format-preserving replacement, where seven of the twenty six transforms preserve
it and the format-preserving pair can collide. One page said tokens and sessions are
deleted, where a token becomes a keyed hash and only a live session is dropped.
