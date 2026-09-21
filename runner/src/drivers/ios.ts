// The iOS driver: drives an app on the iOS Simulator through its
// accessibility tree.
//
// An iOS app's accessibility tree is what VoiceOver reads, which is what these
// workflows are written against, so the workflow language carries over from
// the browser unchanged: "press Sign In", "expect Welcome back". The tree is
// read through Appium's XCUITest driver, which runs WebDriverAgent on the
// simulator and speaks the W3C WebDriver protocol.
//
// XCUITest through Appium rather than a hand written XCUITest bundle, and the
// reason is the product rather than convenience. A plain XCUITest target has to
// be COMPILED INTO the application under test: it needs the app's source, its
// scheme and its signing, and it produces a test bundle specific to that one
// app. Antifailure is handed an application to rehearse, frequently as a built
// artifact and frequently not the customer's own code. WebDriverAgent drives
// any installed bundle from outside, with no source and no project changes, so
// it is the only one of the two that can drive a customer's app at all. The
// cost is an Appium server as an external tool, which is the same shape as the
// engine binary the runner already shells out to.
//
// The recording deserves its own note; see stopRecording below.

import { spawn, execFile } from 'node:child_process';
import { once } from 'node:events';
import { stat } from 'node:fs/promises';
import { promisify } from 'node:util';
import { NotImplementedError, type SurfaceDriver } from './driver.ts';
import { fromIOS } from './axsource.ts';
import { isPlayable, xpathLiteral, type MobilePlatform, type Recorder } from './mobile.ts';
import type { AxLocation, AxNode } from './ax.ts';
import type { Locator, WebDriverSession } from './webdriver.ts';

const run = promisify(execFile);

export const ios: SurfaceDriver = {
  surface: 'ios',
  available: true,
  summary:
    'Drives an iOS app on the simulator through its accessibility tree, with Appium\'s ' +
    'XCUITest driver. Needs Xcode and an Appium server with the xcuitest driver installed.',
};

/** What an iOS run needs to know about the device and the app. */
export interface IOSTarget {
  /** The simulator's udid, from `xcrun simctl list devices`. */
  readonly udid: string;
  /** The built .app bundle to install. Absent drives an app already
   *  installed, which is what a run against a preloaded device does. */
  readonly app?: string;
  readonly bundleId: string;
  /** environment is what the application is launched with, on every launch.
   *
   *  AF_BASE_URL travels here: the address of the environment this run is
   *  rehearsing, for the reason the terminal and desktop drivers carry it. A
   *  phone client of a service has to be told which service to talk to, and
   *  one that is not can only reach its own configured backend, which under a
   *  rehearsal is either nothing or production. Absent launches the app as it
   *  was, which is what a run with no environment behind it should do. */
  readonly environment?: Readonly<Record<string, string>>;
  /** How long WebDriverAgent may take to come up. The FIRST session on a
   *  machine builds it with xcodebuild, which takes minutes; later sessions
   *  reuse the build and take seconds. The default here is generous for that
   *  reason, and a short one reads exactly like a broken driver. */
  readonly wdaLaunchTimeoutMs?: number;
}

const DEFAULT_WDA_TIMEOUT_MS = 480_000;

/** iosTargetFor is the target a phone run drives, built from the job's mobile
 *  block and the run's own address. One place, so the address cannot be
 *  forgotten by a second caller building a target of its own. */
export function iosTargetFor(
  udid: string, app: { readonly id: string; readonly app?: string }, baseURL?: string,
): IOSTarget {
  return {
    udid,
    bundleId: app.id,
    ...(app.app ? { app: app.app } : {}),
    ...(baseURL ? { environment: { AF_BASE_URL: baseURL } } : {}),
  };
}

/** iosPlatform is everything the shared mobile loop needs for iOS. */
export function iosPlatform(target: IOSTarget): MobilePlatform {
  return {
    surface: 'ios',

    capabilities(): Record<string, unknown> {
      const wda = target.wdaLaunchTimeoutMs ?? DEFAULT_WDA_TIMEOUT_MS;
      return {
        platformName: 'iOS',
        'appium:automationName': 'XCUITest',
        'appium:udid': target.udid,
        'appium:bundleId': target.bundleId,
        ...(target.app ? { 'appium:app': target.app } : {}),
        // The simulator is already booted and the app already installed by
        // prepareSimulator, so resetting would only throw that away.
        'appium:noReset': true,
        'appium:wdaLaunchTimeout': wda,
        'appium:wdaConnectionTimeout': wda,
        // Without this a session that outlives one workflow's think time is
        // torn down underneath the next step.
        'appium:newCommandTimeout': 300,
      };
    },

    parse(source: string): AxNode {
      return fromIOS(source);
    },

    locator(name: string): Locator {
      // XPath rather than the faster accessibility id strategy, deliberately.
      // The element acted on has to be the element that was READ, and the
      // snapshot's name comes from `label` falling back to `name` (see
      // fromIOS). This expression matches exactly those two attributes, so a
      // control cannot be located by an attribute the snapshot never looked
      // at. An accessibility id lookup matches the identifier only, which is
      // a different string in any app that sets one.
      const literal = xpathLiteral(name);
      return { using: 'xpath', value: `//*[@label=${literal} or @name=${literal}]` };
    },

    context(): AxLocation {
      // An app has no URL. The bundle identifier is the honest nearest thing:
      // it names what is being driven, it is stable, and it is what a person
      // would use to find the app again.
      return { url: `ios://${target.bundleId}`, title: target.bundleId };
    },

    async record(into: string): Promise<Recorder | undefined> {
      return startRecording(target.udid, `${into}.mov`);
    },

    async restart(session: WebDriverSession): Promise<void> {
      // Terminate then activate, rather than relying on the session opening
      // the app. Appium ATTACHES to an already running application, so a
      // second workflow in the same run would otherwise inherit the first
      // one's screen. XCUITest spells the argument `bundleId`.
      await session.execute('mobile: terminateApp', [{ bundleId: target.bundleId }]);
      // LAUNCH with the environment, not activate, when there is one to give.
      // Activating an application that was just terminated starts it with no
      // environment at all, and this runs before every workflow, so an address
      // set anywhere else, a session capability included, would be discarded
      // here each time. XCUITest's launchApp takes `environment` by that name.
      if (target.environment && Object.keys(target.environment).length > 0) {
        await session.execute('mobile: launchApp', [
          { bundleId: target.bundleId, environment: { ...target.environment } },
        ]);
      } else {
        await session.execute('mobile: activateApp', [{ bundleId: target.bundleId }]);
      }
    },
  };
}

/** prepareSimulator boots the device and installs the app, so a run starts
 *  from a known state rather than from whatever the last run left.
 *
 *  Separate from the session on purpose: booting is slow, it is shared by
 *  every workflow in a run, and doing it once here keeps it out of the
 *  per workflow budget. */
export async function prepareSimulator(target: IOSTarget): Promise<void> {
  await run('xcrun', ['simctl', 'boot', target.udid]).catch((err: unknown) => {
    // Booting an already booted device answers "Unable to boot device in
    // current state: Booted", which is success for our purposes and the only
    // failure worth swallowing here.
    const text = err instanceof Error ? err.message : String(err);
    if (!/current state: Booted/i.test(text)) throw err;
  });
  // Waits for the device to finish starting rather than sleeping. A device
  // that answers simctl but has not finished booting installs an app and then
  // loses it.
  await run('xcrun', ['simctl', 'bootstatus', target.udid, '-b'], { timeout: 300_000 });
  if (target.app) {
    await run('xcrun', ['simctl', 'install', target.udid, target.app]);
  }
}

/** listSimulators returns the booted-or-bootable devices, newest runtime
 *  first, so a caller can pick one without hardcoding a udid. */
export async function listSimulators(): Promise<
  readonly { udid: string; name: string; runtime: string; state: string }[]
> {
  const { stdout } = await run('xcrun', ['simctl', 'list', 'devices', 'available', '--json']);
  const parsed = JSON.parse(stdout) as {
    devices?: Record<string, { udid: string; name: string; state: string }[]>;
  };
  const found: { udid: string; name: string; runtime: string; state: string }[] = [];
  for (const [runtime, devices] of Object.entries(parsed.devices ?? {})) {
    if (!runtime.includes('iOS')) continue;
    for (const device of devices ?? []) {
      found.push({ udid: device.udid, name: device.name, runtime, state: device.state });
    }
  }
  // A booted device first, then by runtime descending, so repeated runs reuse
  // the device that is already up rather than booting a second one.
  return found.sort((a, b) =>
    (a.state === 'Booted' ? -1 : 0) - (b.state === 'Booted' ? -1 : 0)
    || b.runtime.localeCompare(a.runtime));
}

/** startRecording begins a simulator screen recording.
 *
 *  Returns undefined rather than throwing when recording will not start: a
 *  recording is evidence about a run, and a run that produced a verdict must
 *  not be turned into a failure because the camera did not work. */
async function startRecording(udid: string, path: string): Promise<Recorder | undefined> {
  let child;
  try {
    child = spawn(
      'xcrun',
      ['simctl', 'io', udid, 'recordVideo', '--codec', 'h264', '--force', path],
      { stdio: ['ignore', 'ignore', 'pipe'] },
    );
  } catch {
    return undefined;
  }
  let failed = false;
  child.on('error', () => { failed = true; });

  // WAIT FOR THE RECORDER TO ACTUALLY BE RECORDING, by waiting for the file to
  // exist, rather than sleeping a fixed moment and hoping.
  //
  // Measured on an iPhone 17 Pro simulator: recordVideo takes appreciably
  // longer than a second to create its output file, and stopping it before
  // then leaves NO FILE AT ALL. Stopping at one second produced nothing;
  // three, six and ten seconds each produced a playable file. The earlier
  // version slept 500ms and returned a recorder regardless, so every workflow
  // short enough to finish quickly, which is most of them, silently produced
  // no video while the long ones did. A capability that works only for slow
  // runs is worse than one that is absent, because the gap looks like chance.
  const appeared = await waitFor(
    async () => (await stat(path).catch(() => undefined)) !== undefined,
    RECORDER_START_TIMEOUT_MS,
  );
  if (failed || child.exitCode !== null || !appeared) {
    if (child.exitCode === null) child.kill('SIGKILL');
    return undefined;
  }
  const startedAt = Date.now();
  return { stop: () => stopRecording(child, path, startedAt) };
}

/** How long to give the recorder to start before giving up on it. */
const RECORDER_START_TIMEOUT_MS = 10_000;

/** The shortest recording worth finalizing. Below this the recorder has the
 *  file open and no frames in it, and stopping produces an empty container. */
const RECORDER_MINIMUM_MS = 1_500;

/** waitFor polls a condition until it holds or the budget runs out. Polled
 *  rather than slept, so a fast machine is not made to wait for a slow one. */
async function waitFor(ready: () => Promise<boolean>, timeoutMs: number): Promise<boolean> {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    if (await ready()) return true;
    if (Date.now() >= deadline) return false;
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
}

/** stopRecording finalizes the recording and proves the file is playable.
 *
 *  THE STOP PATH IS THE WHOLE PROBLEM, and it is worth spelling out because
 *  the broken version looks identical to the working one. `simctl io
 *  recordVideo` streams samples into the file as it goes and writes the moov
 *  atom, the index without which nothing can play the file, only when it is
 *  asked to stop CLEANLY. Send it SIGKILL, or let the process exit take it
 *  down, and what is left is a file of the expected size, with the expected
 *  name, at the expected path, that no player will open. Every check short of
 *  opening it passes.
 *
 *  So: SIGINT, then wait for it to exit on its own, and escalate only after a
 *  grace period, because a recording that will not stop cleanly is still worth
 *  more than a hung run. Then READ THE FILE and confirm the moov atom is
 *  actually in it, and report no video rather than a broken one if it is not.
 *  A path to an unplayable file is worse than no path: it is evidence that is
 *  not evidence, and somebody debugging a failure spends their time on the
 *  player rather than on the bug. */
async function stopRecording(
  child: ReturnType<typeof spawn>, path: string, startedAt: number,
): Promise<string | undefined> {
  try {
    // A recording that has only just started has the file open and nothing in
    // it. Finishing the minimum window costs a moment on the very shortest
    // workflows and is the difference between a video and an empty container.
    const short = RECORDER_MINIMUM_MS - (Date.now() - startedAt);
    if (short > 0) await new Promise((resolve) => setTimeout(resolve, short));

    if (child.exitCode === null) {
      child.kill('SIGINT');
      const exited = once(child, 'exit');
      const graced = await Promise.race([
        exited.then(() => true),
        new Promise<boolean>((resolve) => {
          const timer = setTimeout(() => resolve(false), 20_000);
          timer.unref?.();
        }),
      ]);
      if (!graced) {
        // It would not stop cleanly, so the file cannot be trusted. Killed so
        // the run can finish, and no video is reported.
        child.kill('SIGKILL');
        return undefined;
      }
    }
    const info = await stat(path).catch(() => undefined);
    if (!info || info.size === 0) return undefined;
    return (await isPlayable(path)) ? path : undefined;
  } catch {
    return undefined;
  }
}

/** drive refuses a run this driver cannot serve.
 *
 *  Kept because the surface abstraction promises it, and because "the Appium
 *  server is not running" has to be a loud refusal rather than a green run.
 *  The real entry is iosPlatform together with runMobile. */
export function drive(): never {
  throw new NotImplementedError(
    'ios',
    'drive() is not the entry point for iOS: build a platform with iosPlatform() and pass it ' +
    'to runMobile(), which is what runner/src/main.ts does.',
  );
}
