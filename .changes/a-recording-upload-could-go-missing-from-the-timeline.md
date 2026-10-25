# fixed

A run that uploaded its recordings could show fewer of them on its timeline
than it had sent. Each upload emits an `artifact.stored` event and the run then
flushes its events to the control plane, but the flush could happen while the
last of those events was still on its way to the part of the engine that sends
them, and that event was left behind. The flush now waits for every event
emitted before it, so the timeline lists every recording the run uploaded.
