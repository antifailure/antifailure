// The public surface of the enterprise entitlement gate.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

export {
  FEATURES,
  isFeature,
  declare,
  sites,
  declared,
  licensed,
  licensedIn,
  requireFeature,
  refusal,
  Unlicensed,
  type Feature,
} from './features.ts'
