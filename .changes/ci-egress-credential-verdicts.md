# fixed

The proxy refuses live shaped credentials in decoded query parameters and
supported HTTP request bodies before forwarding to an allowed destination.
Requests that cannot be inspected are refused; streaming gRPC metadata remains
checked without buffering its message stream.

`af ci` reads complete egress decisions after load and before teardown, and
reports an incomplete log instead of treating missing evidence as clean. A
chaos setup error now follows `policy.chaos_unverified` and appears in the chaos
report. MCP masking errors say when rows were already committed, distinguish
plan refusals, and avoid retrying an interrupted rewrite automatically.
