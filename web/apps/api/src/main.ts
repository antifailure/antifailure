#!/usr/bin/env node
// The community control plane, which is this file and one call.
//
// It used to be six hundred lines and it is now three, because the six hundred
// moved to boot.ts unchanged and both editions call them. Keeping THIS path is
// not sentiment: deploy/docker/control-plane.Dockerfile runs
// `node apps/api/src/main.ts`, package.json names it as a bin and as `npm
// start`, and web/apps/api/test/hosted.test.ts spawns it to watch it refuse to
// start. A rename would have been a silent change to what a released image
// executes, for no gain.
//
// There is no hook. The community edition registers nothing, and the absence
// is what boot.ts prints at startup rather than something a reader has to
// infer from this file being short.

import { startControlPlane } from './boot.ts'

await startControlPlane()
