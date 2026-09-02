---
title: Watching a run
description: The live dashboard, what each pane means, and what you get where there is no terminal.
sidebar:
  order: 3
---

`af up` prints a handful of lines and then a summary. That is the right amount
of output when a run takes twenty seconds and works. It is the wrong amount
when a build is slow, a service will not become ready, or a request is being
refused by the egress policy and you want to see which one.

`af up --hud` runs the same lifecycle and draws it instead.

```sh
af up --hud
```

<!-- frame:start -->

```
antifailure  pr-482  up 14s                                                            2/2 ready
▸ SERVICES ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
✓ web                               http://127.0.0.1:41273
✓ worker                            running


NETWORK ────────────────────────────────────────────────────────────────────────────────────────
allow 0   deny 0   mock 0   record 0

DATABASE ───────────────────────────────────────────────────────────────────────────────────────
branched
gv_20260101_ab12cd  verified
AGENTS ─────────────────────────────────────────────────────────────────────────────────────────
no agents running



LOG ────────────────────────────────────────────────────────────────────────────────────────────
00:00:13 service.ready web is running kind=web service=web state=running url=http://127.0.0.1:4…
00:00:14 service.ready worker is running kind=worker service=worker state=running
00:00:15 env.ready pr-482 is ready proxied=true url=http://127.0.0.1:41273
```

<!-- frame:end -->

## What each pane shows

**Services** is one row per service, with its URL once it has one. The count in
the header is ready over total, which is the number to watch: a run that sits
at `1/3 ready` is waiting on a health check, not on a build.

**Network** is the egress ledger for this environment: how many requests the
policy allowed, refused, answered from a mock pack, and recorded, and the host
of the most recent refusal. It reads zero in the frame above, and that is
accurate rather than a placeholder: the counts come from `egress.decision`
events, and in this release the proxy records its decisions for `af net
explain` without publishing them to the event stream. A refusal is still there
to be read, with `af net explain`, and it does not yet appear here.

The prose above says what the pane is for. When the decisions reach the
stream, a service that appears to hang on startup, because its first outbound
call was refused, will show up here rather than only in its own logs.

**Database** is the golden this environment branched from, whether that golden
passed verification, and the phase of any masking run in flight.

**Agents** is empty during `af up` and fills in when an agent run is attached.

**Log** is the event stream itself, newest last. Every pane above is a summary
of it, so when a summary does not say enough, the line that produced it is here.

## Keys

| Key | What it does |
| --- | --- |
| `tab`, `→`, `l` | Focus the next pane |
| `shift+tab`, `←`, `h` | Focus the previous pane |
| `↓`, `j` / `↑`, `k` | Scroll the focused pane |
| `home`, `g` | Back to the top of the focused pane |
| `q`, `esc`, `ctrl+c` | Quit |

The dashboard uses the alternate screen, so quitting gives back the terminal
you had before it started, scrollback intact.

## Where there is no terminal

`--hud` in a pipeline, a CI job, or anywhere else that stdout is not a terminal
does not refuse and does not draw a frame. It writes one line per significant
event instead, and a summary at the end. A Bubble Tea frame redrawn into a
build log produces a file of cursor escapes and no information, so the flag
means "show me the events" and the display is chosen from what is actually on
the other end of the stream.

`--hud` and `--format json` together are refused. They are two answers to one
question about a single stream, and picking either one silently throws away
what you asked for.

## The stream underneath

The dashboard is a subscriber, not a special case. Everything it draws is on
the event bus, which is the same stream the NDJSON sink writes to
`.antifailure/logs/<environment>.ndjson`. Nothing is computed for the display
that is not also available to a script reading that file.

That also means the dashboard is honest about gaps. A pane that stays empty is
a pane whose events nothing is emitting yet, not a pane that is broken.
