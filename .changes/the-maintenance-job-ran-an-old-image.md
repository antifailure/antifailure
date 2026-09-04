# fixed

Continuous deployment now moves the maintenance job to the same tested image
digest as the serving control plane, after both health checks pass, and reads
the job back before reporting success.

Previously only the application and bootstrap job moved. The scheduled process
that creates event partitions stayed on the image from the last Terraform
apply, so production served v1.1.0 while maintenance still ran v1.0.0.
Both Terraform defaults now name the published v1.1.1 image, so an apply repairs
that existing job rather than keeping its old release.

A revision whose private address cannot be read now refuses promotion instead
of skipping the candidate health check and asking customers to test it first.
