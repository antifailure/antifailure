# fixed

Organisation policy now evaluates the manifest's egress default, so a repository
cannot evade the deny list, the permitted modes or the synth approval
requirement by writing no egress rules at all.

`egress: {default: synth}` with an empty rule list is a valid manifest. The
community validator refuses an `allow`, an `emulate` and a `sandbox` default and
accepts that one, the policy engine gives every host no rule names the default
mode, and the sidecar's synth path then asks a model provider to invent a
response for each of them. `EgressHosts` and `EgressModes`, which the engine
builds from the explicit rules, are both empty for such a manifest, and
`policyenforce` read only those. So `denied_hosts`, `allowed_modes` and
`synth_requires_approval` all found nothing to refuse, and three controls an
organisation had set were evaded by one line naming no host.

The default is now read as the mode every unnamed host is given. A default that
reaches the host it was asked about, meaning `allow`, `sandbox` or `synth`,
cannot honour a deny list, because the list is written as patterns precisely so
that it covers hosts nobody has thought of yet. A default the organisation does
not permit is refused the way a rule using that mode already was. A synth default
needs the same approval a synth rule needs.

`capture`, `mock` and `emulate` answer without contacting the host, so a default
of one of those breaches no deny list, and `block` is the engine's own floor
rather than an author's choice, so an organisation naming only `capture` does not
have to name it. Both are asserted, because the refusals this adds would
otherwise be free to grow into refusing most of the product's own examples.

The core manifest default is unchanged: empty still means block, and the
validator's refusals are untouched. `engine/pkg/extension` documented this blind
spot in the past tense while only `ee/engine/airgapped` had been fixed, and that
comment now says which readers evaluate the field and what each does with it.
