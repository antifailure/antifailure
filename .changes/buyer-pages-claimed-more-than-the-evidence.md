# fixed

Several pages told a buyer more than the evidence behind them supports, and two
told a security reviewer less than is now true.

The pricing page's Enterprise card sold "Fleet management and premium
connectors" and "Governance, evidence retention, and residency". Fleet is the
vendor's own operator surface. "Premium connectors" appeared nowhere else in the
repository. The per plan retention number is read by nothing, because one
retention setting applies to every organization. And residency is real for
environments, through the `allowed_regions` policy, but not for the hosted
control plane, which runs in one Azure region. The card now sells residency for
environments, and single sign-on and SCIM in the enterprise edition, and the
paragraph above the cards names the control plane's region.

The database providers table said Aurora branch time is flat. Nobody has timed
an Aurora clone, and the provider's own benchmark prints those cells as
unmeasured. The row now says the time is expected and never timed, and a new
section says which providers are proved against their real service on every
pull request, which were proved by hand, and which only against a fake.

The licence catalogue said compliance packs cover SOC 2 and ISO 27001. The
command builds SOC 2 and HIPAA and nothing else. A test now holds the
catalogue's wording to the packs the build carries, in both directions.

`LICENSING.md` and the enterprise README listed billing, metering and support
tooling as enterprise features. The licence verifier records billing as a name
with nothing behind it, and operator support access is community code.
`LICENSING.md` also called every database provider MIT, and Aurora is not.

`SECURITY.md` said there is no adversarial containment suite. There are two, one
against a real Docker daemon and one against the sidecar. Neither reaches the
Kubernetes runtime, and the page now says that instead. It also said no release
carries a signature yet, when every release from v1.0.0 to v1.3.5 does.

Smaller corrections:

- The README's count of database conformance behaviours said 24, and the roster
  is 26.
- The README said all four platforms are checked for reproducibility in CI, and
  CI checks one.
- The AKS guide said Antifailure runs on AKS. The Kubernetes runtime has only
  run against k3s.
- The terms page, the privacy link and two documentation pages still pointed at
  the deleted service levels and subprocessor pages.
- The egress example named `localstack`, which is not a name the engine
  registers. The AWS surface registers as `aws`, answered by LocalStack's image.
- The Twins page said there is no automatic time to live, and every environment
  is created with one.
- `CONTRIBUTING.md` said TypeScript is formatted by Biome, which is not
  installed.
