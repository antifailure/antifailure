// The macOS desktop driver: scaffolded, not implemented.
//
// It will drive native and Electron apps (Claude Code desktop, VS Code) through
// the macOS accessibility API (AXUIElement), which is the direct analogue of
// the browser's accessibility tree, so the workflow language carries over
// unchanged. Live view via ScreenCaptureKit into the same frame stream the web
// driver feeds. The reason it is not built here is infrastructure, not design:
// it needs a macOS runner pool the Linux runner does not provide, per-app
// accessibility (TCC) grants, and hardening around ScreenCaptureKit's stop path
// (a stop that loses the moov atom is a known trap).
//
// drive() throws rather than returning an empty result, so a job that targets
// the desktop is refused loudly instead of reported as a green run that tested
// nothing.

import { NotImplementedError, type SurfaceDriver } from './driver.ts';

export const desktop: SurfaceDriver = {
  surface: 'desktop',
  available: false,
  summary: 'Will drive native and Electron apps through AXUIElement, captured with ScreenCaptureKit. Needs a macOS runner pool and accessibility grants.',
};

/** drive is the entry the future implementation replaces. Today it fails loudly. */
export function drive(): never {
  throw new NotImplementedError('desktop', desktop.summary);
}
