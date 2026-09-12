# The four stacks, rehearsed against their own published files

This is the row that is judged by whether the product does the thing it is
being built to do, on somebody else's repository, with no edits to their
application. PostHog, ClickHouse and Supabase publish the file a twin would be
built from. Goliath Data publishes nothing, so it gets a shape and a question
list rather than a rehearsal, which is the ruling in `enterprise_plan.md` and is
not reopened here.

Everything below was measured on 2026-09-08 against `origin/main` at 53d62204.
The findings that would change once L1.1, L3.1, L4.4 or L6.1 land are marked.

## Where the files came from

Nothing here was hand written to resemble a stack. Each manifest is a
mechanical conversion of a file fetched from the project's own repository, by
`compose2af.py` beside this README, which records every compose key it could
not express rather than dropping it quietly.

| Stack | File | Services |
| --- | --- | --- |
| ClickHouse | `ClickHouse/examples`, `docker-compose-recipes/recipes/ch-1S_1K/docker-compose.yaml` | 2 |
| Supabase | `supabase/supabase`, `docker/docker-compose.yml` with `docker/.env.example` | 11 |
| PostHog | `PostHog/posthog`, `docker-compose.hobby.yml` merged with `docker-compose.base.yml` | 37 |
| Goliath Data | nothing is published | not rehearsable |

All four were fetched on 2026-09-08 from:

```
https://raw.githubusercontent.com/ClickHouse/examples/main/docker-compose-recipes/recipes/ch-1S_1K/docker-compose.yaml
https://raw.githubusercontent.com/supabase/supabase/master/docker/docker-compose.yml
https://raw.githubusercontent.com/supabase/supabase/master/docker/.env.example
https://raw.githubusercontent.com/PostHog/posthog/master/docker-compose.hobby.yml
https://raw.githubusercontent.com/PostHog/posthog/master/docker-compose.base.yml
https://raw.githubusercontent.com/PostHog/posthog/master/bin/deploy-hobby
```

The last one was read and not converted. It is the script that writes PostHog's
`.env`, and reading it is how F16 below is stated as fact rather than guessed.
PostHog's `.env.example` was deliberately NOT used: its own first line says it
is for local development of the repository only, it is not what the hobby
compose file reads, and it carries a private key that this report will not
carry.

Nothing was sent to any of those projects. No issue, discussion, pull request or
support ticket was opened anywhere. The fetches above are the whole of the
contact.

## The number

**Stacks that came up: 0 of 4. Services that reached ready: 0 of 50.**

One fidelity report was captured all the same, for the Goliath shape, and it is
where F0 below came from. `af fidelity` answers without a running environment
and reports every unmeasurable component as unmeasured rather than as a pass,
which is the behaviour the rest of this report leans on: it is why the zero
above is a zero and not a small number somebody could mistake for progress.

That number is about this machine and not about the three manifests, and the
distinction is the whole of the reporting here. All four manifests validate and
`af explain` renders every one of them. None reached a running environment,
because `af up` cannot create an environment at all on this machine tonight,
and none of the fifty services could have been started even if it could,
because their images cannot be fetched.

| Stack | Declared | Manifest valid | Reached ready | Why not |
| --- | --- | --- | --- | --- |
| ClickHouse ch-1S_1K | 2 | yes | 0 | `af up` killed at 25m32s on `building the egress proxy`, and neither of its two images is on this machine |
| Supabase self hosting | 11 | yes | 0 | same sidecar prerequisite, and none of its 11 images is on this machine |
| PostHog hobby | 37 | yes | 0 | same, and none of its images is on this machine |
| Goliath shape | 4 | yes | 0 | same |

Checked one at a time with `docker image inspect`, which answers in under a
second where `docker images` does not answer at all: `clickhouse-server`,
`clickhouse-keeper`, `supabase/postgres`, `postgrest`, `envoy`,
`hashicorp/http-echo`, `alpine:3` and `nginx:alpine` are all absent. So the
registry, not the sidecar, is the outer blocker: with the sidecar image seeded
by hand the stacks still could not start, because there is nothing to start
them from.

`af down` afterwards was clean: six resources removed, three compensated by the
journal, which is the half of the lifecycle that did work.

### What the machine actually did, so the number is readable

- The Docker daemon answers `docker info` and `docker version`, and runs a
  cached image in under a second: `alpine`, `postgres:17`, `golang:1.25-alpine`
  and `busybox` all ran with `--pull=never` and exited 0.
- It cannot pull. `docker run hashicorp/http-echo:1.0` failed after 2m56s with
  `TLS handshake timeout` against `registry-1.docker.io`.
- It cannot enumerate. `docker ps` and `docker images` returned nothing inside
  180 seconds, against 236 containers and 115 running, at load average 30.
  Every container level observation in this report is therefore COULD-NOT-LOOK.
- Load average sat between 19 and 39 for the whole session, and `af doctor`
  reported `fail Docker daemon: the daemon did not respond` while the CLI was
  answering, which is the same condition seen from a shorter timeout.

## What the product got right, said before the findings

Three things worked exactly as they are meant to, and they are why the rest of
this report can be trusted at all.

**It refused to print a number it could not defend.** The ClickHouse report,
captured in full at `fidelity/clickhouse-1s-1k.txt`, ends with
`Nothing in this environment could be measured, so there is no score`, and then
names all eleven exclusions. A zero would have been readable as a bad result
rather than as an absent one.

**It noticed the stores the conversion could not declare.** F4 below says the
`datastores` block can only name a ClickHouse, so the ClickHouse recipe's two
services had to be written as ordinary services with an image. The report found
them anyway:

```
datastores     unmeasured (2 unmeasured)
  clickhouse   a clickhouse running clickhouse/clickhouse-server:latest that nothing in
               the manifest's datastores list names, so nobody chose a stance for it,
               nothing here copied anything into it, and nothing here can say whether it
               holds production's data or came up empty
```

That is the conversion gap being caught by the product rather than by this
report, which is the strongest form the finding could take. On PostHog it found
nine stores across 37 services, from the images alone, and eight of them are
right: ClickHouse, Postgres, Elasticsearch, Kafka, MinIO, Redis, SeaweedFS and
Valkey, each named with the image it is running and each saying that nobody
chose a stance for it. The full report is at `fidelity/posthog-hobby.txt`. F4
below is the list of engines the `datastores` block cannot declare, and this is
the same list arriving from the other direction with no help from the manifest.

**It refused a literal credential.** 29 Supabase values and 13 PostHog values
were rejected as credential shaped and had to move out of the committed
manifest. That refusal produced F10 below, which is a gap in where they move to
and not an argument against the refusal.

## Findings, each with the lane that owes it

The first one is the twin lying about itself, and it was found by running
`af fidelity`, which is the one part of the exercise this machine could
complete.

### F0. The fidelity report claimed a faithful provider substitution for a rule the sidecar refuses

This is the finding the row was told to hunt, and it turned out to live one
level up from where it was expected. The fear was that a wildcard would answer
200 and produce a twin that silently posts nowhere. The sidecar does not do
that: a capture whose rule does not name the host is refused with a 403, mock
with no fixture answers 404, and synth with no model key answers 403. Nothing
fabricates a success for a host that was merely swept up by a domain.

The REPORT does. `af fidelity` on a manifest carrying three capture rules gave
all three the same verdict:

```
third_party    substituted (3 substituted)
  *.zapier.com substituted  recorded into the inbox and answered with the provider's documented success shape
  api.resend.… substituted  recorded into the inbox and answered with the provider's documented success shape
  hooks.zapie… substituted  recorded into the inbox and answered with the provider's documented success shape
```

Three rules, three different run time behaviours, one sentence:

| Rule | What the sidecar does | What the report said |
| --- | --- | --- |
| `*.zapier.com` capture | 403, nothing recorded | substituted, answered with the provider's shape |
| `api.resend.com` capture | recorded, answered with Resend's own success body | substituted, answered with the provider's shape |
| `hooks.zapier.com` capture | recorded, answered `{}` | substituted, answered with the provider's shape |

Only the middle row is true. `hostComponent` in
`engine/internal/fidelity/build.go` matched `schema.ModeCapture` and set
`Substituted` with that sentence unconditionally, asking neither whether the
rule names the host nor whether this build has a handler for it.

What the correction is, and what it is not. The obvious repair is to call a
wildcard capture refused, and that would be wrong for half the hosts such a rule
matches. The sidecar's guard is `!known && !d.NamesHost()`, so a request under
`*.resend.com` IS captured, because this build has a Resend handler, and one
under `*.zapier.com` is refused, because it does not. Neither answer can be read
off the rule, so the rule is now reported `Unmeasured` with the reason, which is
the package's own word for a thing it could not determine and is exactly what
the mock branch of the same function already does with the same rule shape
through `PackReason`. It leaves the score and is named in the exclusions rather
than being counted as an answer nobody checked.

The predicate is a LEADING star and not any star, which is the same one the mock
branch uses and the same one the policy uses to decide `NamesHost`. An interior
star, as in `email.*.amazonaws.com`, pins the service label and the label count,
so it can only ever reach SES, the policy counts it as naming the host, and the
sidecar captures it. Widening to every pattern would trade one wrong answer for
a quieter one, and a fourth test holds that line.

For a company whose entire outbound path is a Zapier catch hook and two CRMs,
every line of the third party dimension would have been overstated, and the
document that exists to say what the twin does not reproduce would have been
the thing hiding it.

**Half of it is fixed in this branch.** Three tests pin it, and one of them
asserts that the component leaves the score AND is named in the exclusions,
because those are two separate claims: dropping out of the denominator is what
stops a guess being counted, and appearing in `Excluded` with a reason is what
stops it disappearing silently.

**The other half is not fixed and the reason is stated.** Whether a NAMED host,
or a host a wildcard happens to cover, is answered with the provider's own shape,
with the generic empty object, or with a 403, depends on the sidecar's handler
list, which lives in `package main` under
`engine/cmd/af-proxy` and cannot be imported by the report. Copying it in would
make a fifth copy of host matching beside the four this repository already
carries, in the Go engine, in the console's TypeScript, in the generated sidecar
sources and in the enterprise deny list, and those four drifting apart is the
reason a fifth is the wrong answer. The fix is one registry both read. See F14,
which is the same missing registry seen from the CLI.

Owed by: the fidelity lane for the classification, L0.3 for the registry.


### F1. There is no way to mount a file into a service, and all three stacks need one

`container.HostConfig` in `engine/internal/runtime/local/container.go` sets
neither `Binds` nor `Mounts`, and `schemas/manifest.v1.json` has no `volumes`
key on a service. So this is a manifest surface gap and a runtime gap at once,
and neither can be worked around from a manifest.

The three published files ask for 36 bind mounts of a file or a directory, and
13 named volumes:

| Stack | Bind mounts | Named volumes |
| --- | --- | --- |
| ClickHouse ch-1S_1K | 3 | 0 |
| Supabase | 18 | 2 |
| PostHog hobby | 15 | 11 |

What each one costs, concretely:

- **ClickHouse.** Both services lose their entire configuration. The server
  never receives the `<zookeeper>` block that points it at the keeper, and the
  keeper never receives `keeper_config.xml`. The containers would start on
  image defaults, which is a different topology from the recipe and would be
  reported as the recipe.
- **Supabase.** The `db` service loses all seven SQL files under
  `/docker-entrypoint-initdb.d`, which is where `roles.sql` creates
  `supabase_auth_admin`, `authenticator`, `anon` and `service_role`. Without
  them `auth`, `rest`, `realtime`, `storage` and `supavisor` cannot connect at
  all. `api-gw` loses `envoy.yaml`, so Envoy starts with no configuration.
- **PostHog.** ClickHouse loses `config.xml`, `users.xml`, the protobuf IDL and
  the init SQL; `web`, `plugins`, `livestream`, `feature-flags` and `cymbal`
  lose their shared directories.

Owed by: the runtime lane and whoever owns the manifest schema. This is the
single blocker that stops all three stacks, and no other finding can be
observed past it.

### F2. A manifest cannot say the application has no Postgres

`database` has no off switch. The ClickHouse recipe declares no database and
`af explain` fills in `provider docker, Postgres 17` anyway, then `af up`
spends its first minutes creating a golden for a store the twin does not have.
The twin has one component production does not, and the fidelity report counts
it.

Owed by: L2.1.

### F3. The engine's Postgres answers to `db`, and two of the three stacks have a service called `db`

`DatabaseAlias` is the literal string `db` at
`engine/internal/runtime/local/local.go:51`, and a service is attached to the
same inner network with `Aliases: []string{s.Name}` at `container.go:343`.
Nothing in `engine/internal/manifest/validate.go` refuses a service named `db`.
Supabase and PostHog both name one, and both resolve `db` from several other
services expecting their own Postgres.

The fix cannot be to refuse the name, because renaming the application's
service is an edit to their application and the whole claim is that there is
none. It has to be on the engine's side.

Owed by: the runtime lane with L2.1. NOT CONFIRMED BY EXPERIMENT: the alias
collision is read from the code, and no environment reached the point where two
containers claim one alias, because of F1 and the machine.

### F4. Only one datastore engine has a provider, and PostHog declares eight it does not have

`newDatastoreProvider` in `engine/internal/env/datastores.go` has a single case,
`clickhouse`, and refuses everything else by name with AF-MAN-002 and the
sentence `The engines it can provide are: clickhouse`. That refusal is the
right shape and it is the correct behaviour. It is also the whole gap.

PostHog's published file declares Postgres and ClickHouse, which this build
provides, and Kafka, Zookeeper, Redis, Valkey, Elasticsearch, MinIO, SeaweedFS
and Temporal, which it does not. Supabase declares no second store at all,
because everything it keeps is in its own Postgres.

Declaring any of those eight in the `datastores` block refuses the manifest, so
the only way to express them today is as ordinary services with an image, which
is what the converted PostHog manifest does. That works as far as starting a
container and it means none of them is masked, branched, verified or reported
on, which is the difference the `datastores` block exists to make.

Owed by: L4.1 through L4.4. This finding changes when they land.

### F5. `af explain` does not print `datastores` at all

`af explain -o json` carries the datastores array, including the `primary` entry
the database block normalizes into. The human output, which is the surface a
person reads and whose own help says it shows the effective configuration with
every default filled in, prints no line about them. A declared ClickHouse is
invisible in the document that exists to say what an environment will be.

Owed by: L4.4.

### F6. `command` in a manifest replaces the entrypoint, and in compose it does not

`engine/internal/runtime/local/container.go:300` sets `cfg.Cmd` to
`/bin/sh -c <command>` and then `cfg.Entrypoint = []string{}`. Compose sets CMD
and leaves ENTRYPOINT alone, so the image's entrypoint still runs and the
command is its arguments.

The difference is not cosmetic for these stacks. Supabase's `db` declares
`command: ["postgres", "-c", "config_file=..."]` against `supabase/postgres`,
whose entrypoint is the script that runs the init SQL. Converted faithfully,
the manifest skips that script. The same applies to `rest`, `functions` and
`supavisor` in Supabase and to `web`, `objectstorage`, `kafka-init` and
`temporal-django-worker` in PostHog. An image built `FROM scratch` has no
`/bin/sh` at all, so a command on one cannot run.

Owed by: the runtime lane.

### F7. There is no `entrypoint` key, and dropping it corrupts the command beside it

Five services across the three files declare one: Supabase's `api-gw`, and
PostHog's `proxy`, `objectstorage`, `seaweedfs` and `kafka-init`. There is no
manifest key for any of them, so the conversion drops them and records that it
did.

Dropping an entrypoint is not a missing feature on its own. It is a missing
feature that breaks the key next to it, and PostHog's `kafka-init` is the
clearest case in the three files:

```yaml
entrypoint: /bin/sh
command:
    - -c
    - |
        set -x
        until rpk topic list --brokers kafka:9092 2>/dev/null; do
        ...
```

The entrypoint is the shell and the command is its arguments. Converted, the
entrypoint is gone and the argv list is joined into one string, so the manifest
carries a command beginning with a literal `-c`, which the engine then wraps as
`/bin/sh -c "-c set -x ..."`. It parses, it validates, and it runs nothing. The
same shape appears on `objectstorage`, `seaweedfs`, `proxy` and Supabase's
`api-gw`.

Owed by: whoever owns the manifest schema, with the runtime lane, because the
key and the semantics in F6 are one decision.

### F8. Two thirds of the declared readiness checks can be expressed, and the third that cannot is the one that matters

The manifest offers `health_path` and `health_timeout`, and `waitReady` in
`engine/internal/runtime/local/container.go:707` is more capable than the schema
suggests. A service with a port and no `health_path` is ready when a TCP
connect succeeds, so a bare TCP check IS expressible. A service with a port and
a `health_path` is ready when an HTTP GET returns any status at all, 500
included, which is deliberate and documented in the code.

The three files declare 20 healthchecks between them:

| What the check is | Count | Expressible |
| --- | --- | --- |
| an HTTP GET on a path | 9 | yes, as `health_path` |
| a command inside the container | 8 | no |
| a bare TCP connect | 2 | yes, as a port with no `health_path` |
| an HTTP GET that needs an Authorization header | 1 | no |

So nine of the twenty cannot be written down, and the eight commands are the
ones the project chose deliberately: `pg_isready -U postgres` on Supabase's
`db`, `postgrest --ready`, `imgproxy health`. Supabase's Postgres accepts a
connection on 5432 while it is still running its init scripts, which is exactly
what `pg_isready` exists to distinguish and exactly what a TCP connect cannot.

Worse, and this is the number the row was told to count: a service with NO port
is ready when its container is running.

```go
if s.Port <= 0 || hostPort == 0 {
	// Nothing to poll from here. A worker is ready when it is running, and
	// asking for more would mean inventing a protocol the application does
	// not speak.
	return r.confirmStillRunning(ctx, s, id)
}
```

That reasoning is right for a worker and wrong for these stacks, because a
compose file uses the absence of a published port to mean `only other services
reach this`, not `this is a worker`. Of PostHog's 37 services, 32 publish no
port; of Supabase's 11, nine do not. Under the current model, `ready` for 41 of
the 50 declared services across the three stacks would mean `the container was
created and has not exited yet`.

Owed by: the runtime lane with whoever owns the manifest schema.

### F9. A service may publish one port

Supabase's `supavisor` publishes 5432 and 6543. PostHog's `proxy`,
`objectstorage` and `seaweedfs` publish two or three each. Whichever port the
conversion picks, the others are unreachable from outside the environment, and
because readiness is measured on the published port, F8 above decides which of
them the environment waits on.

Owed by: whoever owns the manifest schema, with the runtime lane.

### F10. Environment values are looked up by name in one flat namespace

`secrets.Chain.Lookup` takes a name and asks each source in turn. There is no
service scope. Supabase's compose gives `DATABASE_URL` one value in `storage`
and a different one in `supavisor`, and both are credentials, so neither can be
a literal in the manifest and both resolve through the same name. One of the
two is wrong, and nothing says which.

The refusal that forces this is correct and should stay: the manifest refuses a
literal value shaped like a credential, which is what moved 29 Supabase values
and 13 PostHog values out of the file. The gap is that the place they move to
cannot tell two services apart.

Owed by: the secrets lane.

### F11. There is no `env_file`

Four PostHog services read one. Every variable in it has to be enumerated in
the manifest, which is what makes the converted PostHog manifest 799 lines for
37 services.

Owed by: whoever owns the manifest schema.

### F12. The sidecar image has no pull path, and building it needs the network

`ensureProxyImage` inspects for the image and, failing that, builds it: a
`golang:1.25-alpine` stage running `go build` with `GOFLAGS=-mod=mod`, which
fetches the module graph from inside the container. There is no pull, no
prebuilt artifact and no way to seed it. The tag is content addressed off the
sidecar sources, so every engine version pays this once per machine.

Tonight it paid nothing and returned nothing: one progress line,
`building the egress proxy (once per version)`, for 25 minutes and 32 seconds
until the run was killed. There is no timeout on that step and no output from
it, so a user has no way to tell a slow build from a hung one.

The contrast is the measurement. The same sidecar, compiled on the host where
the module cache is already warm and packaged into the same `FROM scratch`
image under the engine's own content addressed tag, took 237.5 seconds
end to end on this machine, of which the Go build was a few seconds and the
rest was the daemon exporting and unpacking a 10.9 MB layer under load. The
container build did not finish in 1532. The difference is entirely the module
fetch, and it is a fetch of this repository's own dependency graph over a
network that was refusing TLS handshakes.

That image was seeded by hand for this session, which is disclosed here rather
than left implicit: it is why the `db` alias experiment below could run at all,
and it means nothing in this report is evidence about the build step.

There is a second cold dependency behind the same door, and it is worse in one
respect. `ensureIngressImage` builds `antifailure/ingress:socat-1` from
`FROM alpine:3.20` and `RUN apk add --no-cache socat`, so it needs both a
registry pull and a package fetch. It is skipped when no service publishes a
port, which is the only reason the experiment below could run.

Owed by: the runtime lane and whoever owns releases. This is the finding that
turned a rehearsal into a refusal, so it is the one to fix first if the row is
re-run.

### F13. A domain wildcard with `mode: allow` was accepted, and explained as a clean ALLOW

Re-verified on 2026-09-11 against main at 67fcf2d7, by running it, and it
held. `host: "*"` was refused outside block by the egress validator in
`engine/internal/manifest/validate.go`. `host: "*.zapier.com"` with
`mode: allow` was accepted, and `af net explain` rendered it as a clean ALLOW
with no other rule matching and no caution:

```
POST https://hooks.zapier.com/hooks/catch/1234/abcd

  ALLOW

  The rule for *.zapier.com decided allow because the host ends in .zapier.com.

  No other rule matches this request.
```

For a delivery path that posts to a customer's CRM through a Zapier catch hook,
that is a rehearsal firing somebody's real automation at real contacts.

The matcher was never wrong. The rule does reach every name under
`zapier.com`, at any depth, and not the apex. What was missing was anything
saying so, and three neighbours of the same shape were worse: `*.com` and
`hooks.*.com` were accepted in allow and reached names anybody registered, and
`egress.default: sandbox` was accepted while a sandbox request with no
credential leaves untouched, which made it the refused `default: allow` under a
safer word.

Closed on the branch `w-wildcard-egress-caution`. The breadth is stated
wherever a rule is explained, a star standing where the owner's name goes is
refused outside block, and a sandbox default is refused.

### F14. `af net explain` cannot tell the two capture rules apart, and the sidecar can

The same blind spot as F0, seen from the CLI rather than from the report, and
this is the half that answers the question the row was actually sent to ask.

**No wildcard answers 200.** The sidecar refuses a capture whose rule does not
name the host. `Decision.NamesHost()` is false for a leading wildcard and for
match all, and `capture` in `engine/cmd/af-proxy/capture.go` refuses on
`!known && !d.NamesHost()` with a 403 and an explanation. Mock with no matching
fixture answers 404. Synth with no model key answers 403. Nothing fabricates a
success for a host that was merely swept up by a domain. Four tests in
`engine/cmd/af-proxy/crm_capture_test.go` pin exactly this for
`hooks.zapier.com` and `api.hubapi.com`, and they run through the real policy
engine and the real capture handler with no fake between them.

**But the instrument that is supposed to say so cannot.** `af net explain`
gives the identical verdict for both rules:

```
  CAPTURE
  The rule for *.zapier.com decided capture because the host ends in .zapier.com.

  CAPTURE
  The rule for hooks.zapier.com decided capture because the host matches exactly.
```

One of those is answered and recorded; the other is refused at run time. A
person checking their egress policy before a demo reads the first and believes
their Zapier deliveries are being captured into the inbox.

This half cannot be fixed by looking at the rule alone, which is why F0 stopped
at saying it did not know. The report can say a wildcard is undetermined; the
CLI is being asked what will happen to one specific request, and answering that
needs the handler list. `*.resend.com` covering `api.resend.com` is captured and
`*.zapier.com` covering `hooks.zapier.com` is refused, and the difference is
entirely in that list.

The fix is not to duplicate the sidecar's handler list into the CLI. That list
already exists in the Go engine, in the console's TypeScript, in the generated
sidecar sources and in the enterprise deny list, and a fifth copy is how these
four drift. It is to move the host to provider matching into one package both
the sidecar and `af net explain` consult.

Owed by: L0.3.

### F15. There is no Zapier capture handler, so a named Zapier host is answered with the wrong shape

`captureHandlerFor` has entries for Resend, SendGrid, Postmark, Mailgun,
Twilio, SES and Slack. A host somebody named that is not in that list gets
`genericCapture`, which records the whole body and answers 200 with `{}`.

Zapier's catch hook answers
`{"status": "success", "attempt": ..., "id": ..., "request_id": ...}`, and a
client that checks `status` reads `{}` as a failure. The message is captured
either way, so this is a wrong shape rather than a lost message, and the fourth
test in `crm_capture_test.go` states it as a limit rather than leaving a reader
to assume it was checked.

Owed by: L0.3.

### F15a. `af fidelity` printed its whole report and then did not exit

Every one of the four runs printed the complete report, including the closing
list of exclusions, and was then killed by a 400 second timeout with exit 124.
The output is not truncated, so the hang is after the last line is written.

COULD-NOT-LOOK as to the cause. The likely candidate is a Docker client call
that does not return while the daemon is unresponsive, which is the same
condition every other Docker observation in this report ran into, and it cannot
be told apart from a real defect without a machine where the daemon answers. It
is written down rather than left out because a command that prints its answer
and then hangs is indistinguishable, in a script, from one that never answered.

Owed by: nobody yet. Re-run it on a machine with a responsive daemon before
attributing it.

### F16. A published compose file is half a deployment, and the conversion gets the half that is published

Two things a manifest needs are simply not in a compose file, and neither is a
defect in anything. They decide what `convert and run` can mean, so they belong
here rather than in a footnote.

**The egress policy.** Compose says nothing about which hosts a service reaches,
so all three converted manifests carry `egress.default: block` and no rules,
which is the only honest default. The first run of any of them refuses every
outbound call the application makes, and the loop is to read `af net log` and
name what shows up. That loop compounds with F8: a service that never becomes
ready because of a blocked call at startup publishes no port, so it is reported
ready right up until it exits.

**The environment.** PostHog's `docker-compose.hobby.yml` is not self contained.
`bin/deploy-hobby` writes the `.env` beside it, generating `POSTHOG_SECRET`,
`ENCRYPTION_SALT_KEYS` and `BROWSERLESS_SECRET` from `/dev/urandom` and
`openssl rand` at install time, and taking `DOMAIN`, `TLS_BLOCK`,
`REGISTRY_URL`, `CADDY_TLS_BLOCK`, `CADDY_HOST` and `POSTHOG_APP_TAG` from
prompts or the environment. Converted from the compose file alone, every one of
those resolves empty, which is why `SITE_URL` in the converted manifest reads
`https://` and nothing else.

That is not a gap to close. Those three are secrets, and the engine already
refuses a literal credential in a committed manifest, so they were always going
to come from outside it. What it means is that the conversion produces a
manifest that is correct about the services and incomplete about the deployment,
and a reader should know which half they are holding. The Supabase conversion is
better off only because Supabase publishes a `.env.example` beside its compose
file, and that is what was used.

Owed by: nobody. It is a property of the input format, and it belongs in the
onboarding documentation rather than in a lane.

### F17. The datastore detector called PostgREST a Postgres, on Supabase's own file

The Supabase report, captured in full at `fidelity/supabase-selfhost.txt`, says
this:

```
datastores     unmeasured (3 unmeasured)
  db           a postgres running supabase/postgres:17.6.1.136 that nothing in the
               manifest's datastores list names ...
  meta         a postgres running supabase/postgres-meta:v0.96.6 that nothing in the
               manifest's datastores list names ...
  rest         a postgres running postgrest/postgrest:v14.12 that nothing in the
               manifest's datastores list names ...
```

One of those three is a Postgres. `postgres-meta` is a stateless HTTP API over
somebody else's Postgres and `postgrest` is a stateless HTTP API over somebody
else's Postgres. Neither holds anything, so neither can hold production's data
and neither needs a stance.

The mechanism is in `engineOfImage` at
`engine/internal/fidelity/datastores.go:378`. The registry prefix is stripped,
which is right, and the remaining base name is tested with
`strings.Contains(base, token)`. The token is `postgres`, and `postgrest` is
`postgres` with a `t` on the end, so it matches. `postgres-meta` matches for the
same reason.

Two things follow, and the second is the one that costs something.

The false positives are noise in the one list a reader is supposed to take
seriously. The exclusion list is where this report says what it could not
measure, and three entries where there is one store is how a list like that
stops being read.

And the same substring rule will do this to other stacks. Anything named after
the store it talks to is a candidate: a `redis-exporter`, a `kafka-ui`, a
`mongo-express`. Each would be reported as an unmeasured store of the kind it
merely monitors.

PostHog's report carries the third variant of the same thing. It lists
`kafka-init` as a Kafka, and `kafka-init` is a run once job that creates topics
using the Redpanda image and exits. It is a client of the broker in the next
row, not a second broker.

Not fixed here, deliberately. A word boundary rule stops `postgrest` and does
not stop `postgres-meta` or `kafka-init`, so the correct fix is a decision
about how to tell a store from a client of a store, and that decision belongs
to whoever owns this table rather than to a lane passing through. Two signals
the table does not use are sitting in the same manifest: a service that other
services `depends_on` is more likely to be the store, and a service that exits
is not one. The token `supabase` in the same
row is worth looking at in the same pass: the base name is taken after the last
slash, so `supabase/postgres` reaches the table as `postgres` and the
`supabase` token can only ever fire on an image actually named something like
`supabase-postgres`.

Owed by: L4.4.

## How to re-run it

`compose2af.py` and `report.py` beside this README are the whole conversion.
They need PyYAML and nothing else.

```
python3 compose2af.py docker-compose.yml --env .env.example \
    --name supabase-selfhost -o supabase-selfhost/antifailure.yaml
python3 report.py
```

The converter is deliberately dull. It refuses to guess: every compose key it
has no manifest home for is printed with the services that declare it, and a
value shaped like a credential is moved to a `.env` beside the manifest rather
than written into a file that gets committed, because the engine refuses that
and is right to. The one place it makes a choice is `depends_on`, where compose
allows a map with conditions and the manifest takes a list of names, so the
conditions are dropped and the names kept.

Where two services give one variable name two different values, it says so and
keeps the last, because `.env` is one flat namespace and there is nothing else
it could do. That happened once, for `DATABASE_URL` across Supabase's `storage`
and `supavisor`, and it is F10 below.

## What was proved and what was not

Proved: the conversions, from the projects' own published files. The manifests
validate and `af explain` renders them. The engine's refusals of a literal
credential, of a datastore engine it has no provider for, and of a match all
rule outside block mode. The sidecar's behaviour for a Zapier and a CRM
hostname under a wildcard and under an exact rule, by test. The teardown.

Not proved, and not to be reported as anything else: that any of these stacks
runs. No container of any of them was created. The `db` alias collision is read
from the code and was never observed, and so is the entrypoint difference in F6
and the absence of bind mounts in F1.

The fidelity reports that were captured are reports on an environment that does
not exist. Every service, the database and the datastore come back `unmeasured`
with the reason, which is the honest answer and is why F0 is about the third
party dimension alone: that dimension is computed from the manifest and does not
need anything running, so it is the one part of the report that said something
substantive, and what it said was wrong.

## The mutation table

Every test in this branch was mutation tested: the production line it guards was
broken, the suite re-run, and the cell classified on the `=== RUN` count rather
than on the exit code, because a break that stops the package compiling exits
non zero having run nothing and reads exactly like a catch. Restores were from a
file snapshot rather than with `git checkout`, because the worktree is shared
state.

The full table is reported in this lane's handover. One cell is worth writing
down here, because it is the defect the discipline exists to catch.

The first attempt at the cell for `TestCapture_ANamedZapierHostIsRecordedAndAnswered`
replaced the guard `if !known && !d.NamesHost() {` with `if true {`. That
removed the last use of `known`, so the package stopped compiling: exit 1, zero
tests run, and a summary that would read as a caught mutation to anything
counting exit codes. It was redone as `if !known && !d.NamesHost() || true {`,
which keeps `known` in use and actually runs the suite.
