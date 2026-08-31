# fixed

`af init` no longer reports one application as two services when the repository
is cloned into a directory whose name is not the package name. The Dockerfile
analyzer has nothing but the directory to name a service after and every
framework analyzer uses the package name, so the two disagreed, both claimed the
same port, and the manifest that produced was invalid. Nothing was written and
the error told the user to fix a line in a file that had never been created.

A Dockerfile now folds into the service it builds, keyed on the directory. Where
two services really do claim one port, `af init` says so with AF-DET-003 and
names what to change, instead of pointing at a file that does not exist.
