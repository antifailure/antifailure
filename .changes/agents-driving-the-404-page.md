# fixed

The agents could not sign in to an application whose sign-in form is not at
`/login`. The runner navigated straight there, and the `signInPath` option that
looked like a way to change it had no caller anywhere, so the path was in effect
hardcoded. It now searches for the form, starting with the workflow's own start
path, which is where an application that answers every protected route with its
sign-in screen actually shows it, and a run that finds no form anywhere reports
which addresses it tried rather than the regular expression it gave up on.

Two more, found underneath it. A field whose label carries its own hint text has
one accessible name made of both, so the anchored patterns the runner matches
fields by could not see it; the exact name is still preferred and the looser one
is now the fallback. And the vocabulary for the button that asks for a sign-in
link did not include "Send a sign-in link".

In the console, a `Field`'s hint and error moved out of the `<label>` and became
its description. They were part of the field's name, so every screen reader
announced the sign-in field as "Email address We send a link that signs you in.
No password."

Each of the three would have blocked every workflow on its own. All six
workflows in this repository's own Dogfood run had been blocked by them since
the console was folded into the API.
