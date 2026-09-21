#!/usr/bin/env node
// af-runner reads a job on standard input and writes results to standard
// output, as JSON.
//
// A subprocess with a JSON boundary rather than a library, because the engine
// is Go and the browser automation that works is TypeScript, and pretending
// otherwise would mean either a worse browser driver or a foreign function
// interface. The boundary is one document in and one document out, which is
// also the boundary a person can drive by hand when they want to.

import { readFileSync } from 'node:fs';
import { mkdirSync } from 'node:fs';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { run, type Job, type WorkflowResult } from './execute.ts';
import { explore, type Exploration, type Goal } from './explore.ts';
import { accessProbe, type AccessObjectDoc } from './access.ts';
import { CommandInbox } from './inbox.ts';
import { exitCodeFor } from './verdict.ts';
import { callModel, fromEnvironment, type ModelConfig } from './model.ts';
import { cassetteFromEnvironment } from './cassette.ts';
import { emit } from './emit.ts';
import { nullSink, socketSink, type LiveSink } from './live.ts';
import { assertAvailable, type Surface } from './drivers/driver.ts';
import { runTerminal, type TerminalWorkflow } from './drivers/terminal.ts';
import { runDesktop, type DesktopApp } from './drivers/desktop.ts';
import { runMobile, type MobilePlatform } from './drivers/mobile.ts';
import { iosPlatform, listSimulators, prepareSimulator } from './drivers/ios.ts';
import {
  androidPlatform, avds, bootEmulator, devices, installApk, toolsPresent,
} from './drivers/android.ts';
import type { Persona } from './login.ts';
import type { Workflow } from './workflow.ts';
import type { ResolvedDiversity } from './personality.ts';

const exec = promisify(execFile);

/** The document the engine sends. */
interface JobDocument {
  readonly base_url: string;
  readonly artifacts: string;
  /** workflows and personas are optional on the way in even though the engine
   *  always sends both now.
   *
   *  A nil Go slice marshals as null, and `af explore` builds a document with
   *  no workflows in it, so every exploration ever run reached here as
   *  {"workflows": null} and died on `doc.workflows.length` before the browser
   *  opened. The engine sends [] as of the same change that added this, which
   *  is the strict half; this is the tolerant half, and it is what keeps a
   *  runner working against an engine that predates the fix. One malformed
   *  field must not take a whole run with it. */
  readonly workflows?: readonly Workflow[] | null;
  readonly personas?: readonly Persona[] | null;
  /** diversity is the resolved per-agent personality plan. Absent means one
   *  neutral agent per workflow, which is what every run did before the engine
   *  learned to send this. */
  readonly diversity?: ResolvedDiversity;
  /** goals are exploratory runs. Present for 'af explore', absent for
   *  'af test'. One entry point rather than two binaries, because the browser,
   *  the sign in and the evidence capture are the same in both and a second
   *  main is a second place for them to drift. */
  readonly goals?: readonly Goal[];
  /** accessProbes are the declared ownership-scoped objects the access-probe
   *  pass reaches as each persona for the authenticated authorization
   *  differential. Optional and tolerant like the lists above: a nil Go slice
   *  arrives as null or is absent, and either means no access probing, which is
   *  every run that has not declared access fixtures. */
  readonly accessProbes?: readonly AccessObjectDoc[] | null;
  /** af is the path to the engine binary, used to read the inbox. Absent
   *  means no inbox, and a workflow needing one is blocked rather than
   *  failed. */
  readonly af?: string;
  readonly work_dir?: string;
  readonly attempts?: number;
  readonly headless?: boolean;
  /** surface names what this run drives. Absent means 'web', which is every
   *  run the engine sends today. A run that names a surface whose driver is not
   *  built (desktop) is refused loudly rather than reported as green. */
  readonly surface?: Surface;
  /** terminal are the command line workflows this run drives.
   *
   *  Sent alongside `workflows` rather than instead of them. A manifest may
   *  declare both, and a run that carried only one of the two lists would have
   *  to be two runs against one environment, which is two reports, two
   *  verdicts and two chances for them to disagree about the same change.
   *  Tolerant like the lists above: absent, null and empty all mean none. */
  readonly terminal?: readonly TerminalWorkflow[] | null;
  /** desktop says which application a desktop-surface run drives, and how to
   *  reach it: an Electron binary and its arguments, or a macOS application by
   *  name with a bundle to launch. Present only when surface is 'desktop', and
   *  REQUIRED then, because there is no default application the way there is a
   *  default base_url. A desktop run without it is blocked and says so. */
  readonly desktop?: DesktopApp;
  /** mobile is the device and application an ios or android run drives.
   *  Present only when surface is one of those two. Absent is a legal
   *  document and blocks the run with a reason rather than failing it, which
   *  is the same rule every other missing prerequisite follows here. */
  readonly mobile?: MobileDoc;
  /** live is the path to a local socket the engine is listening on, present
   *  only when somebody is watching this run. Absent means no watcher, which is
   *  the ordinary case: the sink becomes a no-op and the run is unchanged. The
   *  frames this streams never leave the machine the engine relays them from,
   *  and never reach the control plane. */
  readonly live?: string;
}

/** The device and application a mobile run drives.
 *
 *  Every field is optional except the application's identifier, because a
 *  machine with exactly one simulator or one attached device is the ordinary
 *  case and making somebody paste a udid into a manifest to describe it would
 *  be ceremony. What cannot be guessed is which app to drive, so that is
 *  required and its absence is refused rather than defaulted.
 */
interface MobileDoc {
  /** The simulator udid or the adb serial. Absent picks the booted device,
   *  or the newest available one. */
  readonly device?: string;
  /** The built .app or .apk to install. Absent drives an application that is
   *  already installed. */
  readonly app?: string;
  /** The iOS bundle identifier, or the Android package name. */
  readonly id: string;
  /** The Android launchable activity. Ignored on iOS. */
  readonly activity?: string;
  /** The Android emulator image to boot when no device is attached. Ignored on
   *  iOS. Absent with exactly one image installed boots that one, because a
   *  machine with a single emulator does not need to be told which. */
  readonly avd?: string;
  /** Where the Appium server is listening. */
  readonly server?: string;
}

/** Where an Appium server listens unless a job says otherwise. The port is
 *  Appium's own default, so a server started with no arguments is found. */
const DEFAULT_APPIUM_SERVER = 'http://127.0.0.1:4723';

/** sdkHint names where the Android SDK was looked for, so a refusal points at
 *  a path somebody can check rather than at a generic phrase. */
function sdkHint(): string {
  return process.env['ANDROID_HOME'] ?? process.env['ANDROID_SDK_ROOT']
    ?? '~/Library/Android/sdk';
}

/** mobilePlatformFor prepares the device and returns the platform the shared
 *  mobile loop drives.
 *
 *  Preparation happens HERE rather than inside the loop because it is per run
 *  rather than per workflow: booting a simulator and installing an app is slow
 *  and shared, and doing it once keeps it out of every workflow's time budget.
 *
 *  Everything it cannot do is thrown with a reason. A throw out of main is
 *  reported as the runner's own failure and the run is BLOCKED, which is
 *  exactly right: a device that would not boot is not evidence about the
 *  application.
 */
async function mobilePlatformFor(
  surface: 'ios' | 'android', doc: MobileDoc | undefined,
): Promise<MobilePlatform> {
  if (!doc?.id) {
    throw new Error(
      `a ${surface} run needs a mobile.id naming the ` +
      `${surface === 'ios' ? 'bundle identifier' : 'package name'} of the application to drive`,
    );
  }

  if (surface === 'ios') {
    const udid = doc.device ?? (await listSimulators())[0]?.udid;
    if (!udid) {
      throw new Error(
        'no iOS simulator is available on this machine. Install one with Xcode, or name a ' +
        'device with mobile.device. "xcrun simctl list devices available" lists them.',
      );
    }
    const target = { udid, bundleId: doc.id, ...(doc.app ? { app: doc.app } : {}) };
    await prepareSimulator(target);
    return iosPlatform(target);
  }

  // Whether the SDK is here at all, asked before anything is driven, so a
  // missing toolchain is reported as a missing toolchain rather than as a
  // device that would not answer.
  if (!await toolsPresent()) {
    throw new Error(
      `no Android SDK platform-tools found under ${sdkHint()}. Install the SDK, or set ` +
      'ANDROID_HOME to where it lives.',
    );
  }

  let serial = doc.device ?? (await devices()).find((d) => d.state === 'device')?.serial;
  if (!serial) {
    // Nothing attached, so boot one. Named by the job, or the only one
    // installed: a machine with a single emulator image does not need to be
    // told which to use, and a machine with several cannot be guessed at.
    const available = await avds();
    const avd = doc.avd ?? (available.length === 1 ? available[0] : undefined);
    if (!avd) {
      throw new Error(
        'no Android device or emulator is attached' +
        (available.length === 0
          ? ', and this machine has no emulator images to boot. Create one with avdmanager.'
          : `, and this machine has several images to choose from (${available.join(', ')}). ` +
            'Name one with mobile.avd, or attach a device.'),
      );
    }
    serial = await bootEmulator(avd);
  }
  if (doc.app) await installApk(serial, doc.app);
  return androidPlatform({
    serial,
    appPackage: doc.id,
    ...(doc.activity ? { appActivity: doc.activity } : {}),
    ...(doc.app ? { app: doc.app } : {}),
  });
}

/** The document the engine reads back. */
interface ResultDocument {
  readonly results: readonly WorkflowResult[];
  readonly explorations: readonly Exploration[];
  readonly passed: number;
  readonly failed: number;
  readonly flaky: number;
  readonly blocked: number;
  readonly unverified: number;
}

async function main(): Promise<number> {
  const raw = readFileSync(0, 'utf8');
  if (!raw.trim()) {
    process.stderr.write('af-runner: expected a job document on standard input\n');
    return 2;
  }
  const doc = JSON.parse(raw) as JobDocument;
  mkdirSync(doc.artifacts, { recursive: true });

  // The live channel, if the engine gave us a socket to reach a watcher on.
  // Best effort throughout: a socket that will not connect degrades to the
  // no-op sink and the run is exactly what it would have been.
  const live: LiveSink = doc.live ? socketSink(doc.live) : nullSink();
  live.hello(doc.work_dir ?? doc.artifacts);

  // A cassette, if one is configured, and the model configuration that goes
  // with it. The two interact in one way worth spelling out: in replay mode
  // there does not have to be a key at all. That is the whole point. A
  // scheduled run reads answers off disk, reaches no network, and costs
  // nothing, and it must not silently become a deterministic run just because
  // nobody set ANTHROPIC_API_KEY on the schedule.
  const cassette = cassetteFromEnvironment(process.env);
  let model = fromEnvironment(process.env);
  if (!model && cassette?.mode === 'replay') {
    model = replayOnlyConfig(process.env);
  }

  // In replay the network is not merely unused, it is unreachable: a miss
  // throws rather than falling through to this, and this exists so that a
  // future edit which removes that guard fails loudly instead of spending.
  const complete = cassette
    ? cassette.wrap(
        cassette.mode === 'record'
          ? callModel
          : () => {
              throw new Error(
                'a replaying cassette tried to call the model, which it must never do',
              );
            },
      )
    : undefined;

  if (model && doc.workflows?.length) {
    const how = cassette
      ? `${cassette.mode === 'record' ? 'recording' : 'replaying'} ${cassette.size()} answers in ${cassette.dir}`
      : 'live';
    process.stderr.write(
      `af-runner: reading pages with ${model.provider}/${model.model}, ${how}\n`,
    );
  }

  // Normalised once, here, rather than at each of the four places these are
  // read. Three of the four were already safe and the fourth was not, which is
  // what a per-site guard buys: three correct lines and one outage.
  const workflows = doc.workflows ?? [];
  const personas = doc.personas ?? [];

  const job: Job = {
    baseURL: doc.base_url,
    artifacts: doc.artifacts,
    workflows,
    personas,
    ...(doc.attempts === undefined ? {} : { attempts: doc.attempts }),
    // Read from this process's environment rather than sent in the job, so a
    // key never passes through a file the engine wrote or a document anybody
    // logged.
    ...(model ? { model } : {}),
    ...(complete ? { complete } : {}),
    ...(doc.diversity ? { diversity: doc.diversity } : {}),
    live,
    ...(doc.headless === undefined ? {} : { headless: doc.headless }),
    ...(doc.af
      ? {
          inbox: new CommandInbox(async (args) => {
            const { stdout } = await exec(doc.af!, [...args], {
              cwd: doc.work_dir ?? process.cwd(),
              maxBuffer: 32 * 1024 * 1024,
            });
            return stdout;
          }),
        }
      : {}),
  };

  // Tolerant about both lists, because this is a boundary and one bad or
  // absent field must not take out the whole run. It used to read
  // doc.workflows.length directly, and the engine sends null for that field on
  // every exploration, so af explore died here with a TypeError before it
  // reached the goals it was given. A caller that sends no workflows means no
  // workflows, which is a legal document and not a fault.
  // Which surface this run drives, meaning WHAT IS OPENED. Web is the default;
  // the engine sends `terminal` when the manifest's terminal workflows are all
  // there is to run, and `ios` or `android` when the workflows drive an
  // application on a device. In each of the three non web cases no browser is
  // started, no goal is explored and no access object is probed, because none
  // of the three means anything without a page. Desktop is declared and not
  // built, and a run that asks for it is refused here rather than returning an
  // empty, misleadingly green result.
  const surface: Surface = doc.surface ?? 'web';
  let results: WorkflowResult[] = [];
  let explorations: Exploration[] = [];
  // Whether anything in this file actually DRIVES this surface.
  //
  // Tracked rather than assumed, and this variable is the whole reason the
  // desktop surface did not ship as a silent green nothing. assertAvailable
  // was the only thing failing a run that named an unbuilt surface. The day a
  // driver becomes available that line stops throwing, and if nothing takes
  // its place `results` stays the empty array it is initialised to and the run
  // exits zero having driven nothing: a loud, correct refusal converted into a
  // quiet pass. web and terminal are driven below, so they start true; every
  // other surface has to say it was driven, and the check after the dispatch
  // is what stops the driver registry's flag from lying about work nobody
  // does. Adding a member to Surface and marking it available, without adding
  // a branch below, now fails the run instead of passing it.
  let driven = surface === 'web' || surface === 'terminal';
  if (!driven) {
    // ios: throws NotImplementedError, which main's catch reports as the
    // runner's own failure with a clear reason.
    assertAvailable(surface);
  }
  // The terminal workflows run whenever the engine sent any, whatever the
  // surface says. The surface decides whether a BROWSER is opened, and those
  // are two different questions: a manifest with a checkout workflow and a
  // deploy command declares both, and one run has to answer for both of them.
  const terminalWorkflows = doc.terminal ?? [];
  if (terminalWorkflows.length > 0) {
    results = await runTerminal({
      workflows: terminalWorkflows,
      live,
      ...(doc.work_dir ? { cwd: doc.work_dir } : {}),
      // Where the environment this run is rehearsing actually is. Without it a
      // command line tool under test would talk to whatever the developer's
      // shell points at, which is either nothing or, far worse, production.
      env: { AF_BASE_URL: doc.base_url },
    });
  }
  if (surface === 'desktop') {
    // The refusal and the dispatch are the same branch on purpose: there is no
    // state in which the registry says desktop is available and nothing runs.
    if (!doc.desktop) {
      throw new Error(
        'this run asks for the desktop surface and names no application to drive. ' +
        'Send a `desktop` block saying which application: an Electron binary and its ' +
        'arguments, or a macOS application by name. There is no default the way there ' +
        'is a default base_url.',
      );
    }
    // Accumulated rather than assigned, exactly as the web branch below does
    // it, so a manifest that declares both desktop workflows and a deploy
    // command keeps both sets of results instead of the later one erasing the
    // earlier.
    results = [...results, ...await runDesktop({
      app: doc.desktop,
      // Where the environment is, for an application that talks to one. The
      // terminal branch above sends the same address for the same reason.
      baseURL: doc.base_url,
      workflows,
      live,
      ...(doc.attempts === undefined ? {} : { attempts: doc.attempts }),
      ...(model ? { model } : {}),
      ...(complete ? { complete } : {}),
    })];
    driven = true;
  }
  if (surface === 'ios' || surface === 'android') {
    // The refusal and the dispatch are the same branch, for the reason the
    // desktop one gives just above: there is no state in which the registry
    // says a mobile surface is available and nothing runs.
    //
    // Refused BEFORE a device is touched, because mobilePlatformFor boots a
    // simulator and installs an application, and spending a minute of
    // somebody's machine on a run that is about to be refused is its own small
    // failure. runMobile refuses an empty run too, which is the guarantee
    // every other caller gets.
    if (workflows.length === 0) {
      throw new Error(
        `the ${surface} surface was given no workflows to drive, so there is nothing to judge. ` +
        `A run that drives nothing is refused rather than reported as passing, because zero ` +
        `failures out of zero workflows is not evidence about the application.`,
      );
    }
    // Accumulated rather than assigned, the same way the desktop branch does
    // it, so a run that also declared a deploy command keeps both sets of
    // results instead of the later one erasing the earlier.
    results = [...results, ...await runMobile({
      platform: await mobilePlatformFor(surface, doc.mobile),
      workflows,
      serverURL: doc.mobile?.server ?? DEFAULT_APPIUM_SERVER,
      artifacts: doc.artifacts,
      live,
    })];
    driven = true;
  }
  if (surface === 'web') {
    results = [...results, ...(workflows.length > 0 ? await run(job) : [])];
    explorations = doc.goals?.length
      ? await explore({
          baseURL: doc.base_url,
          artifacts: doc.artifacts,
          goals: doc.goals,
          personas,
          live,
          ...(job.inbox ? { inbox: job.inbox } : {}),
          ...(doc.headless === undefined ? {} : { headless: doc.headless }),
        })
      : [];
    // The access-probe pass runs when the engine declared access fixtures,
    // independent of goals: it reaches each declared object as every persona so
    // the authenticated authorization differential has observations to assess.
    // Its result rides the same explorations channel, carrying observations
    // rather than a goal's findings.
    if (doc.accessProbes?.length) {
      explorations = [
        ...explorations,
        await accessProbe({
          baseURL: doc.base_url,
          artifacts: doc.artifacts,
          objects: doc.accessProbes,
          personas,
          ...(job.inbox ? { inbox: job.inbox } : {}),
          ...(doc.headless === undefined ? {} : { headless: doc.headless }),
        }),
      ];
    }
  }


  if (cassette) {
    process.stderr.write(
      `af-runner: cassette ${cassette.mode}: ${cassette.read} replayed, ${cassette.written} recorded\n`,
    );
  }
  const counted = { passed: 0, failed: 0, flaky: 0, blocked: 0, unverified: 0 };
  for (const verdict of [
    ...results.map((r) => r.outcome.verdict),
    ...explorations.map((e) => e.outcome.verdict),
  ]) {
    switch (verdict) {
      case 'pass': counted.passed++; break;
      case 'fail': counted.failed++; break;
      case 'flaky': counted.flaky++; break;
      case 'blocked': counted.blocked++; break;
      case 'unverified': counted.unverified++; break;
    }
  }
  // The driver registry said this surface was available and nothing above
  // drove it. That combination is the exact defect the surface abstraction
  // exists to prevent, and it is INVISIBLE without this line: the run would
  // emit an empty result list, count zero of everything, exit zero, and read
  // as a clean rehearsal that tested the change against nothing at all.
  // Refused here rather than reported, because a verdict nobody produced is
  // worse than a run that failed.
  if (!driven) {
    throw new Error(
      `the ${surface} surface is marked available in the driver registry and nothing in ` +
      `main.ts drives it. An available driver with no call site returns an empty result ` +
      `and exits zero, which reads as a clean run that tested nothing. Add the dispatch ` +
      `beside the others above, or mark the surface unavailable until it has one.`,
    );
  }

  // The run is over. Tell any watcher the final tally and drain the socket
  // before the process exits, so the last frames and the done event make it
  // out rather than being lost with the connection.
  live.done(counted);
  await live.close();

  const out: ResultDocument = { results, explorations, ...counted };
  // Awaited, not fired and forgotten: the document must reach the operating
  // system before this function returns, because its caller exits the process
  // the moment it does, and process.exit truncates a document still draining.
  await emit(out);
  return exitCodeFor([
    ...results.map((r) => r.outcome),
    ...explorations.map((e) => e.outcome),
  ]);
}

/** The model to replay as, when there is no key to read one from.
 *
 * A replaying run never sends a request, so the key is not merely unused, it
 * is not needed. The provider and the model name still are: they are part of
 * the key a recording is filed under, so replaying as the wrong model finds
 * nothing rather than finding the wrong thing.
 */
function replayOnlyConfig(env: Record<string, string | undefined>): ModelConfig {
  const provider = env.AF_MODEL_PROVIDER === 'openai' ? 'openai' : 'anthropic';
  return {
    provider,
    // Deliberately empty. Nothing reads it in replay, and a placeholder that
    // looked like a key would be the kind of string that ends up in a log.
    apiKey: '',
    model: env.AF_MODEL ?? (provider === 'anthropic' ? 'claude-sonnet-5' : 'gpt-4.1'),
  };
}

main().then(
  (code) => process.exit(code),
  (err: unknown) => {
    // A crash here is the runner's own, and it says so. The engine reports it
    // as blocked rather than as a failing test, because a runner that could
    // not start is not evidence about the application.
    process.stderr.write(
      `af-runner: ${err instanceof Error ? (err.stack ?? err.message) : String(err)}\n`,
    );
    process.exit(1);
  },
);
