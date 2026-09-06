# changed

The hosted control plane scales on a declared concurrency instead of the platform default.

Production ran its first release on six replicas and a scale rule nobody had
written: Azure Container Apps adds a replica at ten concurrent requests when
no rule is declared, so the whole product could hold sixty requests in flight
before queuing, and a slow reader holds one for as long as a response streams.
The number is now declared in Terraform, forty per replica in production with
a ceiling of twelve replicas, and the connection arithmetic beside it shows
the database still has five times the headroom the app can use.
