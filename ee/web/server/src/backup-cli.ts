#!/usr/bin/env node
// The control plane's operator command line, as the enterprise edition runs it.
//
// It is the community command line with one thing done first: every table the
// enterprise edition seals values into is registered with the re-sealing tool.
// Without that, `reseal` in an enterprise deployment would not know the audit
// stream's collector credentials exist. It does not skip them silently, it
// refuses to run, but a rotation that cannot run is still a rotation nobody can
// perform, and this file is what lets it run.
//
// The registration is a side effect before an import on purpose. The community
// command line dispatches on its arguments when it is loaded, so the tables have
// to be registered before it is, and both modules resolve the re-sealing tool to
// the same file, so they share one registry.

import { registerSealedDestinations } from '@antifailure-ee/audit'

registerSealedDestinations()

await import('@antifailure/api/backup-cli')
