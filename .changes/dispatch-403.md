# fixed

The console's **Ask for an environment**, **Run agents** and **Run load**
controls put GitHub's raw JSON on the screen when a dispatch was refused, and
the sentence beside it named three possible causes at once. A refused dispatch
now says which one it is: the App was not granted Actions write, the App was
never given that repository, there is no repository of that name it can see,
there is no workflow file at that path on the default branch, the branch does
not exist, the workflow declares no `workflow_dispatch` trigger, or it does not
declare the inputs the console sends. Each carries its own remedy and none of
them carries a status code or a JSON body.

The same check now runs when a repository is chosen rather than when the button
is pressed, so a missing permission is visible before the form is filled in.

The permission behaviour was documented backwards: a missing `actions: write`
is a `403 Resource not accessible by integration`, not the 404 the
documentation claimed, and it is checked before the workflow file is looked
for, so it hides a missing file behind it.
