# From a privileged action to your SIEM

Measured 2026-09-08 on darwin/arm64, over 200 entries per destination.

| Destination | Measured against | Median | p95 | Worst |
| --- | --- | --- | --- | --- |
| syslog over TLS | an in-process collector on loopback | 2µs | 5µs | 302µs |
| HTTPS webhook | an in-process HTTPS receiver on loopback | 158µs | 1.09ms | 7.59ms |
| object store drop | an in-process S3 API on loopback | 100µs | 477µs | 2.05ms |
| webhook, receiver down, into the dead letter file | an in-process HTTPS receiver answering 503 | 816.91ms | 834.01ms | 836.76ms |

## What this measures

The clock starts at the instant the action happened, which is the OccurredAt
the engine stamps on the entry in engine/internal/env/audit.go, and stops when
the RECEIVER has the entry rather than when the sink finished writing. A write
that returns while bytes are still in a kernel buffer has not put anything in
anybody's SIEM.

## What it does not measure, and how to get that number

With no receiver configured every figure above is loopback, so it is the delay
this product is responsible for and nothing else. The wide area time to a
hosted SIEM is your network and is not ours to quote. Point the harness at your
own collector and the number becomes the whole path, measured by you:

    AF_AUDIT_BENCHMARK_SYSLOG_ADDRESS=collector.internal:6514 \
      AF_AUDIT_BENCHMARK_SYSLOG_CA_FILE=/etc/ssl/collector-ca.pem \
      AF_AUDIT_BENCHMARK_WEBHOOK_URL=https://siem.example/ingest \
      just benchmark

Against a real receiver the syslog and webhook rows stop the clock when the
write returns rather than when the receiver has it, because a receiver we do
not control cannot tell us. Those two rows say so in the "measured against"
column. They are the weaker measurement and they are labelled rather than
quietly mixed in with the others.

## The row that matters during an outage

The last row is an entry the receiver refused, timed through the full retry and
into the dead letter file, with the real backoff rather than an injected one.
That is the delay somebody actually waits through while their SIEM restarts,
and it is the figure that decides whether a team leaves this turned on. The
entry is not lost during that window: it is on disk, in the same JSON the
receiver would have been given.
