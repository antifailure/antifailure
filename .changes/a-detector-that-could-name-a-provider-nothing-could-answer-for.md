# added

The detector and the sidecar are two lists about the same providers and nothing
held them together. `engine/internal/detect` decides that a host gets egress
mode `capture`; `engine/cmd/af-proxy` decides what a captured request is
answered with. A provider named in the first with no handler in the second is
answered by the generic fallback, which returns 200 and an empty object, so an
application reading a provider specific id or status out of the response gets
nothing.

The two lists agree today, seven providers for seven handlers. This adds the
check that holds them there.
