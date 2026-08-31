# added

`af model` brings your own model key without a control plane. There was one way
to give the engine a key, which was to export a variable, and no way at all to
find out what it would do with one: nothing said which key was configured, where
it came from, or whether it worked, and `af doctor` said nothing about the model.

`af model set` stores a key in the system keyring, or in the encrypted local
store on a machine without one, and never takes it as an argument.
`af model show` reports the provider, the model, the endpoint, the source and a
fingerprint, and never the key. `af model test` makes one cheap call and tells a
revoked key, an exhausted balance, an unknown model name, a rate limit, an
outage and an unreachable endpoint apart, because each has a different fix.
`af doctor` now reports the model key, and reports having none as a pass rather
than a warning, because running with the deterministic planner is a supported
mode.

A key resolves through the same chain and in the same precedence order as every
other secret, so an export still beats a stored key, and `af model show` says
when the key you stored is not the one runs will use. It now reaches the two
processes that spend it: the runner subprocess, which inherited only the
engine's own environment and so could never see a stored key, and the egress
sidecar's synth path, which read the environment directly and would have told
somebody who had stored a key to set the variable.

Custom endpoints are a first class path for a local model or a gateway, with
their own failure advice. A manifest's egress policy does not govern the
model call and does not have to name the provider, which is now covered by a
test so a refactor cannot quietly turn synth mode off under `default: block`.
