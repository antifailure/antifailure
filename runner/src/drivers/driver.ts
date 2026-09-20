// The surface driver abstraction: what "run this against a surface" means, so a
// workflow can target a browser, a terminal, a desktop app, an iOS app or an
// Android app, and the machinery around it, sign in, the planner, the live
// stream and the verdict, reads the same across all five.
//
// The design carries across every surface: drive the accessibility
// representation, not selectors (see runner/src/browser.ts). A browser exposes
// an accessibility tree; a terminal's rendered cells are its tree; a macOS app
// exposes AXUIElement; an iOS app exposes its own accessibility tree; an
// Android view hierarchy is the tree TalkBack reads. So a workflow that reads
// as a sentence, "press Continue", "expect Welcome", ports from surface to
// surface, and only the driver underneath changes.
//
// Today web, terminal and ios are available, and the terminal one drives a
// full screen program through a real pseudo terminal, which is where that
// design stops being a claim: a curses program's rendered grid of cells IS the
// tree, and matching against the bytes it wrote would be matching against the
// HTML. Desktop is scaffolded and has no implementation: its driver conforms
// to this interface and FAILS LOUDLY rather than silently passing, so a job
// that targets it is refused with a clear reason instead of returning a green
// verdict that tested nothing. Android is implemented and NOT available, which
// is the interesting case and the reason the rest of this paragraph exists.
//
// `available` IS A CLAIM THAT A DRIVER HAS BEEN DRIVEN, never that its code
// exists. Android's code is written, typechecked and unit tested against the
// tree shape UiAutomator2 produces, and no run has ever driven an application
// with it, because the emulator on the machine it was written on could not
// finish booting. So it stays false and a job asking for it is refused, with a
// summary saying exactly that.
//
// That is not caution for its own sake. Driving a real iOS app for the first
// time found three defects that every unit test had passed over: a text field
// reporting its placeholder as its value, the software keyboard appearing as
// application controls, and a recording that produced no file when it was
// stopped promptly. An implementation that has never met a device should be
// assumed to have defects of the same kind, and `available: true` is how that
// assumption reaches a customer as a promise.
//
// A surface also needs an external Appium server, and the honest place to say
// no about THAT is the run itself: runMobile asks whether the server is
// listening before it drives anything and BLOCKS every workflow when it is
// not, so a missing tool is reported as a missing tool rather than as a broken
// application.

import type { Surface } from '../live.ts';

export type { Surface };

/** NotImplementedError is thrown by a surface whose driver is not built yet. It
 *  is deliberately loud: a run that asked for a surface we cannot drive must
 *  fail, never pass. The message names the surface and what it will take. */
export class NotImplementedError extends Error {
  readonly surface: Surface;
  constructor(surface: Surface, detail: string) {
    super(`the ${surface} surface driver is not implemented yet: ${detail}`);
    this.name = 'NotImplementedError';
    this.surface = surface;
  }
}

/** SurfaceDriver describes one surface: which it is, whether it can drive a run
 *  today, and a one line summary of what it does or what it is waiting on. The
 *  run behavior itself lives in each driver's own module (web.ts, terminal.ts),
 *  because a browser run and a terminal run share nothing but this description.
 */
export interface SurfaceDriver {
  readonly surface: Surface;
  /** available is false for a scaffolded driver. A caller checks this and
   *  refuses the run loudly rather than pretending it ran. */
  readonly available: boolean;
  /** summary is shown to a person: what the driver does, or for a scaffolded
   *  one, what building it requires. */
  readonly summary: string;
}

// The registry. Every surface appears here exactly once, so the set of surfaces
// is one list rather than a switch repeated in five places. web, terminal, ios
// and android are available; desktop is declared and not yet available.
const drivers: Record<Surface, SurfaceDriver> = {
  web: {
    surface: 'web',
    available: true,
    summary: 'Drives a browser through its accessibility tree with Playwright.',
  },
  terminal: {
    surface: 'terminal',
    available: true,
    summary: 'Drives a command line program, through a pipe or, for one that draws a screen, a real pseudo terminal: sends keystrokes, reads the rendered screen, asserts on it.',
  },
  desktop: {
    surface: 'desktop',
    available: false,
    summary: 'Will drive native and Electron apps through the macOS accessibility API (AXUIElement), captured with ScreenCaptureKit. Needs a macOS runner pool and per-app accessibility grants.',
  },
  ios: {
    surface: 'ios',
    available: true,
    summary: 'Drives an iOS app on the simulator through its accessibility tree, with Appium\'s XCUITest driver. Needs Xcode and an Appium server with the xcuitest driver installed.',
  },
  android: {
    surface: 'android',
    available: false,
    summary: 'Implemented against Appium\'s UiAutomator2 driver, and NOT yet proven: no run has driven an application with it end to end, so it is refused rather than claimed. Flip this to true in the same commit as the run that proves it.',
  },
};

/** driverFor returns the driver description for a surface. */
export function driverFor(surface: Surface): SurfaceDriver {
  return drivers[surface];
}

/** surfaces lists every surface the abstraction knows, available or not, so a
 *  command can print the roadmap and a test can walk all of them. */
export function surfaces(): readonly Surface[] {
  return ['web', 'terminal', 'desktop', 'ios', 'android'];
}

/** assertAvailable throws NotImplementedError for a scaffolded surface. This is
 *  the single gate a job goes through before a surface is driven, so a desktop
 *  job fails loudly here rather than reaching a driver that would return an
 *  empty, misleadingly green result. */
export function assertAvailable(surface: Surface): void {
  const driver = driverFor(surface);
  if (!driver || !driver.available) {
    throw new NotImplementedError(surface, driver ? driver.summary : 'unknown surface');
  }
}
