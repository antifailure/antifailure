# fixed

The manifest schema now publishes the duration pattern on
`database.volume.max_age` and `load.traffic.max_age`. The engine already refused
anything that was not a duration in both places, so the reference page was
telling readers that any string up to 32 characters would be accepted when it
would not.