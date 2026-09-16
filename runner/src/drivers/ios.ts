// The iOS driver: scaffolded, not implemented.
//
// It will drive an app through its accessibility tree with XCUITest (or Appium)
// against the simulator first and a device farm later, with simulator capture
// (simctl io recordVideo and screenshots) into the same frame stream the web
// driver feeds. The workflow language is the same: an app's accessibility tree
// is what a screen reader reads, which is what these workflows are written
// against. It is last because it is the highest effort (Xcode toolchain,
// simulator and device provisioning, signing) and the narrowest near-term
// audience.
//
// drive() throws rather than returning an empty result, so a job that targets
// iOS is refused loudly instead of reported as a green run that tested nothing.

import { NotImplementedError, type SurfaceDriver } from './driver.ts';

export const ios: SurfaceDriver = {
  surface: 'ios',
  available: false,
  summary: 'Will drive an app through XCUITest against the simulator, then a device farm. Needs the Xcode toolchain and simulator provisioning.',
};

/** drive is the entry the future implementation replaces. Today it fails loudly. */
export function drive(): never {
  throw new NotImplementedError('ios', ios.summary);
}
