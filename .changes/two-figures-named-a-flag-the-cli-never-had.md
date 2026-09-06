# fixed

Two figures on the marketing site printed a command nobody could run. The
twins figure ended on `af env prune --before <cutoff>` and the load figure
opened with `af load --manifest load.yml`, and neither flag has ever existed:
the cutoff is `--older-than`, and `af load` reads the manifest in the working
directory rather than taking a path.

Both are the first command a reader would copy out of the page, and the shell
answers an invented flag with a usage error, which reads as the product being
broken rather than the page being wrong. The figures now show what the command
reference generated from the command tree says.
