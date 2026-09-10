Kubernetes workers could be reported as started when only their Pod objects
existed. A single worker also passed after a pod-list error or while its
containment init container was still pending.

Readiness now requires every ordinary init container to exit successfully and
the application's container to be running or successfully completed. The wait
honors the service's health timeout, including API requests, and preserves a
bounded early-crash observation for a newly started single worker. Restartable
init sidecars use their startup and readiness state and can stop after a
successful job. Init failures include the container's termination message.

Published ingress URLs are checked after pod readiness, so an ingress controller
still reconciling its backend no longer produces a ready environment. The check
retains authentication redirects without following them and refuses unavailable
routes by the declared timeout.
