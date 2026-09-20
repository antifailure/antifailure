// The surface driver abstraction: what "run this against a surface" means, so a
// workflow can target a browser, a terminal, a desktop app, or an iOS app, and
// the machinery around it, sign in, the planner, the live stream and the
// verdict, reads the same across all four.
//
// The design carries across every surface: drive the accessibility
// representation, not selectors (see runner/src/browser.ts). A browser exposes
// an accessibility tree; a terminal's rendered cells are its tree; a macOS app
// exposes AXUIElement; an iOS app exposes its own accessibility tree. So a
// workflow that reads as a sentence, "press Continue", "expect Welcome", ports
// from surface to surface, and only the driver underneath changes.
//
// Today web and terminal are implemented, and the terminal one drives a full
// screen program through a real pseudo terminal, which is where that design
// stops being a claim: a curses program's rendered grid of cells IS the tree,
// and matching against the bytes it wrote would be matching against the HTML.
// Desktop and iOS are defined here and scaffolded: their drivers conform to
// this interface and FAIL LOUDLY rather than silently passing, so a job that
// targets them is refused with a clear reason instead of returning a green
// verdict that tested nothing.

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
// is one list rather than a switch repeated in five places. web and terminal
// are available; desktop and ios are declared and not yet available.
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
    available: false,
    summary: 'Will drive an app through XCUITest against the simulator, then a device farm. Needs the Xcode toolchain and simulator provisioning.',
  },
};

/** driverFor returns the driver description for a surface. */
export function driverFor(surface: Surface): SurfaceDriver {
  return drivers[surface];
}

/** surfaces lists every surface the abstraction knows, available or not, so a
 *  command can print the roadmap and a test can walk all of them. */
export function surfaces(): readonly Surface[] {
  return ['web', 'terminal', 'desktop', 'ios'];
}

/** assertAvailable throws NotImplementedError for a scaffolded surface. This is
 *  the single gate a job goes through before a surface is driven, so a desktop
 *  or ios job fails loudly here rather than reaching a driver that would return
 *  an empty, misleadingly green result. */
export function assertAvailable(surface: Surface): void {
  const driver = driverFor(surface);
  if (!driver || !driver.available) {
    throw new NotImplementedError(surface, driver ? driver.summary : 'unknown surface');
  }
}
