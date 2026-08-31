# added

`load.source: otel` reads the traffic mix out of an OpenTelemetry trace export
on disk, in OTLP/JSON, either one document or the one per line a collector's
file exporter writes. Only server spans become traffic, both the current and
the pre-1.21 attribute names are read, and because a span carries a duration
the shape arrives with production's own p95 for each route in it. That is the
baseline `p95_increase` compares against, and until now nothing could provide
one: a combined format log line has no duration, so the threshold was real and
unreachable.
