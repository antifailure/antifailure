# added

A switch on the privacy page that turns this site's page counting off, and a
section that says what leaves the browser and what never does.

The subprocessor page already told readers a preference would be remembered if
they switched measurement off. The only way to switch it off was a query
parameter documented in one source comment, so the promise had no reachable
mechanism behind it.

The control renders the reason rather than a position. Four different things
can stop a reader being counted and only one of them is the switch: a browser
sending Global Privacy Control is not counted whatever the switch says. Where
the decision is not the reader's, there is no switch at all, only the state.
