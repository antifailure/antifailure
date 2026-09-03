# fixed

`tools/site/check-tls.sh` was committed without its executable bit, and both
the `deploy.yml` step and the `just tls` recipe invoke it directly as
`tools/site/check-tls.sh` rather than through `bash`. So the certificate check
that was added to watch two hostnames could not run at all. It exited 126,
"Permission denied", the first time a push to main reached the deploy job.

Every other tracked shell script in the tree was already 100755. This one was
100644 and nothing noticed, because the step that runs it only runs on a deploy
and never on a pull request.
