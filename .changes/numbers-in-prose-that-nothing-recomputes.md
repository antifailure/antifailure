# fixed

Seven figures in the documentation had drifted from the code.

The manifest size limit read 256 KiB against `MaxSize = 1 << 20`. The stability page
said the Helm chart is 1.0.0, where `Chart.yaml` has moved past it. The production
page counted twelve alert rules against ten. The oracle page said "nine others"
against fourteen ignored headers. The goldens page said an unset `max_age` means
nothing is refreshed, where it becomes 168h. The route table listed both webhook
routes twice, and its own test compares a set of paths, so it could not see the
duplicate. The operations page said every alert rule links to a runbook section,
where six of ten do. `AF-RUN-010` was documented as saying 2.0 GiB is required,
where the code fills that slot with the words "the state directory".
