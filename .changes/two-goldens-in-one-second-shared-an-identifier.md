# fixed

A golden version identifier carried its timestamp to the second, and the hash
beside it is a digest of the masking rules rather than anything unique, so two
refreshes inside one second were handed one identifier. The second `CREATE
DATABASE` then failed with "already exists". Two test suites had already worked
around it by minting a random rules hash per call, which left the edge in place
for everyone else. The timestamp now carries microseconds, which is exactly the
twenty digits the MCP server already publishes as the widest it will accept.
