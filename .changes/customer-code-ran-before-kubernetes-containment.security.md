The Kubernetes containment preflight used a customer's shell and tools. Missing
utilities could report success without sending a probe, and a successful
preflight in one pod did not establish network policy for a newly scheduled
application pod.

The engine's own proxy image now runs the preflight and a mandatory init
container in every service, replica, migration and datastore stance pod. It
requires a reachable sidecar before and after eight independent direct probes,
and three consecutive denied rounds before releasing customer code. The probes
cover public TCP and UDP DNS, IPv4 metadata, AWS and Google IPv6 metadata, and
the actual Kubernetes API service address. Missing configuration, incomplete
probes and the ninety second deadline all refuse startup. No metadata payload
or credential is fetched.

Socket tests exercise TCP, UDP and IPv6, and an isolated cluster test exercises
the first customer command in migrations, replicas and replacement pods. These
finite reachability checks do not certify every CNI or detect a deliberately
silent one-way UDP receiver, and they cannot prevent an operator changing policy
after startup. The cluster still needs an enforcing CNI.

Generated Kubernetes containers now explicitly select the runtime's default
seccomp profile. Customer image users are preserved. A namespace enforcing
Restricted Pod Security still requires a non-root image and an admission
configuration that sets `runAsNonRoot`; this change does not declare full
Restricted compatibility or force a different user onto an application.
