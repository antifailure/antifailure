# fixed

The Details link on a pull request check opened the runs list instead of the run.

The check the App posts on a pull request linked to the console with only
the commit in the address, and the runs page reads a run id and nothing
else, so every click from GitHub landed on the generic list and the reader
had to find their run by eye. The link now names the pull request, and the
runs page opens the newest run for it, or says plainly that none has
reported yet and shows the list.

The console also had no error boundary at all. One uncaught exception on
any page blanked the whole window with nothing to read and nothing to
press. Every page now falls back to a sentence, a reference to quote and a
Try again, and the shell stays up around it.

The website said the hosted control plane was invitation only, that Team
was bought by booking a call, and that there was no status page. Signups
are open, Team is bought from the console with a card at the price the
console shows, and the status page is linked from the footer and named on
the service levels page.
