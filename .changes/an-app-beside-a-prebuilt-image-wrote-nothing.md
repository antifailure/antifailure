# fixed

`af init` refused the ordinary compose file: an application built from the
repository beside an image pulled from a registry, such as a helper, a mail
catcher or any container nobody builds. It stopped with AF-DET-004 asking
what command starts `web`, or with AF-DET-005 and three services claiming
port 3000 once a command was given, and wrote no manifest. Detection placed
the image at the repository root, because a service with no build context
looks exactly like one built from the root, so compose appeared to declare
two services in one directory and the application was never joined to its
own Dockerfile. With no build service beside it, the image was folded into
the application and disappeared from the manifest instead.

An image nobody builds is now written as its own service with
`build: {strategy: image, image: <reference>}`: a web service when compose
publishes a port for it, and a worker when it publishes none, carrying its
compose `command` when there is one. The application beside it is one
service again, and the summary names the image each such service runs.
