// The Android driver: drives an app on an emulator or a connected device
// through its view hierarchy.
//
// An Android view hierarchy IS an accessibility tree: it is what TalkBack
// reads, and every attribute this driver reads, the content description, the
// text, whether a node is clickable or checkable, is an attribute TalkBack
// uses to decide what to announce. So the same workflow sentence that drives a
// web page and an iOS app drives an Android app, and only the spelling of the
// tree changes (see runner/src/drivers/axsource.ts).
//
// Appium's UiAutomator2 driver rather than raw `adb shell uiautomator dump`,
// for one reason that matters and one that follows from it. A dump gives the
// tree and no way to act on it: tapping by coordinate means reading a
// bounds attribute and computing a centre, which is exactly the selector
// coupling this runner exists to avoid, and it breaks on every animation. And
// once input goes through UiAutomator anyway, the session that provides it
// also provides the tree, so the dump adds a second source of truth that can
// disagree with the one being acted on.

import { spawn, execFile } from 'node:child_process';
import { once } from 'node:events';
import { access, stat } from 'node:fs/promises';
import { homedir } from 'node:os';
import { join } from 'node:path';
import { promisify } from 'node:util';
import { fromAndroid } from './axsource.ts';
import { isPlayable, xpathLiteral, type MobilePlatform, type Recorder } from './mobile.ts';
import type { SurfaceDriver } from './driver.ts';
import type { AxLocation, AxNode } from './ax.ts';
import type { Locator, WebDriverSession } from './webdriver.ts';

const run = promisify(execFile);

export const android: SurfaceDriver = {
  // false because no run has driven an application with this code end to end,
  // not because the code is missing. `available` is a claim that a driver HAS
  // BEEN DRIVEN, and the registry in driver.ts says the same thing; a test
  // compares the two, because this declaration disagreeing with the registry
  // is a claim nothing enforces until something reads this one.
  surface: 'android',
  available: false,
  summary:
    'Drives an Android app on an emulator or a connected device through its view hierarchy, ' +
    'with Appium\'s UiAutomator2 driver. Needs an Android SDK and an Appium server with the ' +
    'uiautomator2 driver installed.',
};

/** What an Android run needs to know about the device and the app. */
export interface AndroidTarget {
  /** The adb serial, "emulator-5554" or a device's own. */
  readonly serial: string;
  /** The built .apk to install. Absent drives an app already installed. */
  readonly app?: string;
  readonly appPackage: string;
  /** The launchable activity. Absent lets the driver resolve it from the
   *  package's manifest, which is what an apk installed out of band needs. */
  readonly appActivity?: string;
}

/** androidPlatform is everything the shared mobile loop needs for Android. */
export function androidPlatform(target: AndroidTarget): MobilePlatform {
  return {
    surface: 'android',

    capabilities(): Record<string, unknown> {
      return {
        platformName: 'Android',
        'appium:automationName': 'UiAutomator2',
        'appium:udid': target.serial,
        'appium:appPackage': target.appPackage,
        ...(target.appActivity ? { 'appium:appActivity': target.appActivity } : {}),
        ...(target.app ? { 'appium:app': target.app } : {}),
        'appium:noReset': true,
        'appium:newCommandTimeout': 300,
        // UiAutomator2's own helper apks have to be installed on the device
        // before a session can start, and on a cold emulator that is slower
        // than the default allows.
        'appium:uiautomator2ServerInstallTimeout': 120_000,
        'appium:uiautomator2ServerLaunchTimeout': 120_000,
        'appium:adbExecTimeout': 120_000,
      };
    },

    parse(source: string): AxNode {
      return fromAndroid(source);
    },

    locator(name: string): Locator {
      // XPath, matching exactly the two attributes fromAndroid builds a name
      // out of and in the same order of preference, so the element acted on is
      // the element that was read. See the same note on the iOS locator.
      const literal = xpathLiteral(name);
      return {
        using: 'xpath',
        value: `//*[@content-desc=${literal} or @text=${literal}]`,
      };
    },

    context(): AxLocation {
      // An app has no URL; the package name is the honest nearest thing.
      return { url: `android://${target.appPackage}`, title: target.appPackage };
    },

    async record(into: string): Promise<Recorder | undefined> {
      return startRecording(target.serial, `${into}.mp4`);
    },

    async restart(session: WebDriverSession): Promise<void> {
      // Same reason as the iOS driver: a session attaches to a running app,
      // so without this the second workflow starts on the first one's screen.
      // UiAutomator2 spells the argument `appId`, not `bundleId`.
      await session.execute('mobile: terminateApp', [{ appId: target.appPackage }]);
      await session.execute('mobile: activateApp', [{ appId: target.appPackage }]);
    },
  };
}

/** sdkRoot finds the Android SDK.
 *
 *  Found rather than assumed, and the environment is asked first: a machine
 *  with the SDK somewhere else is ordinary, and a hardcoded path would make
 *  this driver work on exactly one laptop. */
export function sdkRoot(): string {
  return process.env['ANDROID_HOME']
    ?? process.env['ANDROID_SDK_ROOT']
    ?? join(homedir(), 'Library', 'Android', 'sdk');
}

/** adbPath is the adb binary, whether or not the SDK is on PATH.
 *
 *  A great many machines have the SDK installed and never put platform-tools
 *  on PATH, so resolving it here is the difference between this driver working
 *  out of the box and failing with "adb: command not found" at the first
 *  step. */
export function adbPath(): string {
  return join(sdkRoot(), 'platform-tools', 'adb');
}

/** toolsPresent answers whether the SDK pieces this driver needs are there,
 *  so a missing SDK is reported as a missing SDK rather than as a failing
 *  application. */
export async function toolsPresent(): Promise<boolean> {
  try {
    await access(adbPath());
    return true;
  } catch {
    return false;
  }
}

/** devices lists the attached devices adb can see, ready ones first. */
export async function devices(): Promise<readonly { serial: string; state: string }[]> {
  const { stdout } = await run(adbPath(), ['devices']);
  return stdout.split('\n')
    .slice(1)
    .map((line) => line.trim())
    .filter((line) => line.length > 0 && !line.startsWith('*'))
    .map((line) => {
      const [serial = '', state = ''] = line.split(/\s+/);
      return { serial, state };
    })
    .filter((d) => d.serial)
    .sort((a, b) => (a.state === 'device' ? -1 : 0) - (b.state === 'device' ? -1 : 0));
}

/** avds lists the emulator images this machine can boot. */
export async function avds(): Promise<readonly string[]> {
  const { stdout } = await run(join(sdkRoot(), 'emulator', 'emulator'), ['-list-avds']);
  return stdout.split('\n').map((l) => l.trim()).filter(Boolean);
}

/** bootEmulator starts an AVD headless and waits until it has finished
 *  booting, returning its adb serial.
 *
 *  READINESS IS THE PACKAGE MANAGER ANSWERING, not a property and not adb.
 *
 *  `adb wait-for-device` returns as soon as the daemon can see the device,
 *  which is long before the system is usable: the device reports `device`
 *  while `pm` still answers "Can't find service: package".
 *
 *  The traditional fix is to poll the `sys.boot_completed` property, and on
 *  this image THAT PROPERTY DOES NOT EXIST. Measured on an API 36 emulator
 *  (Android 16): `getprop sys.boot_completed` prints nothing and exits ZERO,
 *  and the name appears nowhere in a full `getprop` dump, while the device is
 *  running 313 processes and booting normally. A poll waiting for it to become
 *  "1" therefore waits forever and then reports a device that never booted,
 *  which is a false statement about a device that booted fine. An exit code of
 *  zero from a property that is absent is not a verdict.
 *
 *  So this waits for the capability it is about to use instead of for a proxy
 *  for it: the package manager listing packages. It cannot be absent on a
 *  ready device, it says no while the device is still coming up, and it is the
 *  exact service the very next step needs, because the next step installs an
 *  apk. */
export async function bootEmulator(
  avd: string,
  options: {
    readonly headless?: boolean;
    readonly timeoutMs?: number;
    /** Erase the device before booting. Off by default: see below. */
    readonly wipe?: boolean;
    /** Megabytes of guest RAM, overriding the AVD's own setting. */
    readonly memoryMb?: number;
  } = {},
): Promise<string> {
  const before = new Set((await devices()).map((d) => d.serial));
  const child = spawn(
    join(sdkRoot(), 'emulator', 'emulator'),
    [
      '-avd', avd,
      ...(options.headless === false ? [] : ['-no-window']),
      '-no-audio', '-no-boot-anim', '-no-snapshot',
      // Wiping is OFF by default, which is the opposite of what it should
      // obviously be and is the right answer for a reason worth writing down.
      //
      // A fresh device sounds strictly better, and it costs a full first boot
      // every time: the system has to rebuild everything it would otherwise
      // have kept. On a machine that is short of memory that is the difference
      // between an emulator that boots and one that never reports
      // sys.boot_completed at all, which is exactly what happened here. And it
      // buys very little, because the state a rehearsal must not inherit is
      // the APPLICATION's, and that is handled per workflow by restart() and
      // by reinstalling the apk, neither of which needs the device erased.
      ...(options.wipe ? ['-wipe-data'] : []),
      ...(options.memoryMb ? ['-memory', String(options.memoryMb)] : []),
    ],
    { stdio: ['ignore', 'ignore', 'ignore'], detached: true },
  );
  child.unref();

  const deadline = Date.now() + (options.timeoutMs ?? 300_000);
  let serial: string | undefined;
  while (Date.now() < deadline) {
    if (!serial) {
      serial = (await devices()).map((d) => d.serial).find((s) => !before.has(s));
    }
    if (serial) {
      if (await packageManagerReady(serial)) return serial;
    }
    await new Promise((resolve) => setTimeout(resolve, 2_000));
  }
  throw new Error(
    `the emulator ${avd} did not finish booting within ` +
    `${Math.round((options.timeoutMs ?? 300_000) / 1000)}s` +
    (serial
      ? ` (it appeared as ${serial}, but its package manager never answered, so nothing ` +
        `could have been installed on it)`
      : ' (it never appeared in "adb devices" at all)'),
  );
}

/** packageManagerReady answers whether the device can serve `pm` yet.
 *
 *  Counting real package lines rather than trusting the exit code. `adb shell`
 *  reports the exit status of ADB, not of the command on the device, so a
 *  device answering "Can't find service: package" exits ZERO with that
 *  sentence on stdout. Reading the status alone calls a device ready while the
 *  package manager is still absent, and the install that follows then fails
 *  for a reason that looks nothing like the cause. */
export async function packageManagerReady(serial: string): Promise<boolean> {
  return run(adbPath(), ['-s', serial, 'shell', 'pm', 'list', 'packages'], { timeout: 30_000 })
    .then(({ stdout }) => stdout.split('\n').some((line) => line.startsWith('package:')))
    .catch(() => false);
}

/** A refusal that means "ask again shortly", not "this will never work".
 *
 *  Both sentences below come from a device whose system services are still
 *  settling, and BOTH are emitted by a device that reports `device` to adb and
 *  answers a shell command. A boot is not a moment, it is a period during which
 *  services appear, and on a loaded host the package manager can answer once
 *  and be gone again when its process is restarted. Treating either as a
 *  permanent failure turns a device that is nearly ready into a run that
 *  reports it could not install. */
const STILL_SETTLING = /device is still booting|Can't find service: package|closed/i;

/** installApk installs, replacing any previous copy.
 *
 *  Retried, because the refusals above are timing rather than truth. The
 *  alternative is to wait longer before the first attempt, which cannot work:
 *  there is no instant at which a device becomes ready, only an interval over
 *  which it does, and the install itself is the only completely honest probe
 *  for whether the thing we need is possible yet. Anything else is a proxy that
 *  can disagree with it, and on this platform one already does: `pm
 *  install-create` answered Success while `adb install` was still refusing. */
export async function installApk(
  serial: string, apk: string, options: { readonly timeoutMs?: number } = {},
): Promise<void> {
  const deadline = Date.now() + (options.timeoutMs ?? 300_000);
  let last = '';
  for (;;) {
    try {
      // -r replaces, -t allows a test-only build, -g grants the manifest's
      // runtime permissions so a run is not stopped by a permission dialog
      // nobody wrote a workflow step for.
      await run(adbPath(), ['-s', serial, 'install', '-r', '-t', '-g', apk], { timeout: 180_000 });
      return;
    } catch (err) {
      last = err instanceof Error ? err.message : String(err);
      if (!STILL_SETTLING.test(last) || Date.now() >= deadline) {
        throw new Error(`could not install ${apk} on ${serial}: ${last}`);
      }
      await new Promise((resolve) => setTimeout(resolve, 5_000));
    }
  }
}

/** startRecording begins an on-device screen recording.
 *
 *  Returns undefined rather than throwing when recording will not start, for
 *  the same reason the iOS one does: a recording is evidence about a run, and
 *  a run that produced a verdict must not become a failure because the camera
 *  did not work. */
async function startRecording(serial: string, path: string): Promise<Recorder | undefined> {
  const remote = `/sdcard/af-${Date.now()}.mp4`;
  let child;
  try {
    child = spawn(
      adbPath(),
      // screenrecord refuses to run without a time limit on some builds, and
      // three minutes is comfortably past any workflow's budget.
      ['-s', serial, 'shell', 'screenrecord', '--time-limit', '180', remote],
      { stdio: ['ignore', 'ignore', 'pipe'] },
    );
  } catch {
    return undefined;
  }
  let failed = false;
  child.on('error', () => { failed = true; });
  // screenrecord needs a moment on the device before it is capturing, the
  // same as the simulator recorder does. Measured there: stopping at one
  // second produced no file at all. The device side cannot be polled as
  // cheaply as a local path, so the guarantee is made at the other end, by
  // holding the stop until the minimum window has passed.
  await new Promise((resolve) => setTimeout(resolve, 1_000));
  if (failed || child.exitCode !== null) {
    if (child.exitCode === null) child.kill('SIGKILL');
    return undefined;
  }
  const startedAt = Date.now();
  return { stop: () => stopRecording(serial, child, remote, path, startedAt) };
}

/** The shortest recording worth finalizing, for the reason above. */
const RECORDER_MINIMUM_MS = 2_500;

/** stopRecording finalizes the recording, pulls it, and proves it is playable.
 *
 *  THE STOP HAS TO REACH THE PROCESS ON THE DEVICE, which is the part that is
 *  easy to get wrong and impossible to see afterwards. Killing the local adb
 *  client does not stop `screenrecord`: the recorder is a separate process on
 *  the phone, and orphaning it leaves a file that is still being written while
 *  it is pulled. screenrecord also writes the mp4 index, the moov atom, only
 *  when it is interrupted CLEANLY, so a SIGKILL leaves a file of plausible
 *  size that no player will open.
 *
 *  So the signal is sent to the process ON THE DEVICE with pkill -INT, then
 *  the local client is waited on, then the file is pulled, and only then is it
 *  checked for its index. A file that fails that check is reported as no video
 *  rather than as a video, because a path to an unplayable file sends whoever
 *  is debugging a failure to their player instead of to the bug. */
async function stopRecording(
  serial: string, child: ReturnType<typeof spawn>, remote: string, path: string,
  startedAt: number,
): Promise<string | undefined> {
  try {
    const short = RECORDER_MINIMUM_MS - (Date.now() - startedAt);
    if (short > 0) await new Promise((resolve) => setTimeout(resolve, short));

    await run(adbPath(), ['-s', serial, 'shell', 'pkill', '-INT', '-f', 'screenrecord'])
      .catch(() => undefined);

    if (child.exitCode === null) {
      const exited = once(child, 'exit');
      const graced = await Promise.race([
        exited.then(() => true),
        new Promise<boolean>((resolve) => {
          const timer = setTimeout(() => resolve(false), 20_000);
          timer.unref?.();
        }),
      ]);
      if (!graced) {
        child.kill('SIGKILL');
        return undefined;
      }
    }
    // screenrecord finishes writing the index just after it is interrupted,
    // and pulling into that window is how a truncated file is produced.
    await new Promise((resolve) => setTimeout(resolve, 1_500));

    await run(adbPath(), ['-s', serial, 'pull', remote, path], { timeout: 120_000 });
    await run(adbPath(), ['-s', serial, 'shell', 'rm', '-f', remote]).catch(() => undefined);

    const info = await stat(path).catch(() => undefined);
    if (!info || info.size === 0) return undefined;
    return (await isPlayable(path)) ? path : undefined;
  } catch {
    return undefined;
  }
}
