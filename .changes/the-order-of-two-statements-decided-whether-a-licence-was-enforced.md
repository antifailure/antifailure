The enterprise binary's licence gate over the managed cloud providers now has a
test that says no when a provider is registered after it. The gate wraps what is
registered at the moment it runs, so a registration placed below it is served
ungated, and nothing else in the tree could tell the two orders apart.
