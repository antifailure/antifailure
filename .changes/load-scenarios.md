# added

`af load scenario` runs declared journeys. A scenario is an ordered list of
requests with waits between them, parallel blocks so the second submit arrives
while the first is still in flight, and assertions over what came back. Sessions
walk it at once and `start_after` lets one journey burst while another is
already running, which is the load that actually breaks things and which a flat
mix cannot express. It is deterministic per seed, it answers in the verdicts a
run already uses, and every step is checked against `safe_routes` before
anything is sent.
