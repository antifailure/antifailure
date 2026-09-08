// Single sign-on: SAML 2.0 and OIDC, per organization.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// install() is the whole public surface, and it is one call on purpose. Single
// sign-on is not finished when its routes exist: an organization that has
// required it also needs GitHub sign-in to stop being a way in, and those are
// two extension points. Registering one and not the other leaves a feature that
// looks complete and enforces nothing, which is exactly the failure this
// repository keeps hitting. Making them one call means they cannot be
// half-installed.

import { registerExtension, setSignInPolicy } from '@antifailure/api'
import { ssoExtension, type SsoOptions } from './routes.ts'
import { signInPolicy } from './enforce.ts'
import { keyFromEnv } from './secrets.ts'

export function install(options: SsoOptions): void {
  // The key is read here, at startup, rather than on the first login that
  // needs it. A control plane that starts happily and fails the first time
  // somebody signs in has failed in production instead of at deploy time,
  // which is the rule main.ts already applies to every other secret.
  const encryptionKey = options.encryptionKey ?? keyFromEnv()

  registerExtension(ssoExtension({ ...options, encryptionKey }))
  // The clock is handed to the policy rather than left to default, for the
  // same reason every other date in this package comes from it: the
  // entitlement the policy now reads has an expiry, and an expiry compared
  // against a different clock from the one the rest of the request uses is a
  // grant that lapses at a moment nothing else in the system agrees on.
  setSignInPolicy(signInPolicy(options.pool, () => options.clock.now()))
}

export { ssoExtension, type SsoOptions, LOGIN_TTL_MS } from './routes.ts'

export {
  verifyResponse,
  SignatureRefused,
  type VerifiedAssertion,
  type VerifyOptions,
} from './saml/verify.ts'
export {
  readAssertion,
  statusMessage,
  AssertionRefused,
  type AssertionFacts,
  type AssertionExpectations,
} from './saml/response.ts'
export {
  buildAuthnRequest,
  parseIdentityProviderMetadata,
  serviceProviderMetadata,
  samlUrls,
  MetadataRefused,
  type IdentityProviderMetadata,
  type ServiceProviderMetadata,
} from './saml/request.ts'
export { parseXml, MalformedXml, certificateToPem } from './saml/xml.ts'

export {
  authorizationUrl,
  beginLogin,
  codeChallenge,
  completeLogin,
  discover,
  exchangeCode,
  fetchJwks,
  DiscoveryRefused,
  type ProviderEndpoints,
  type LoginSecrets,
} from './oidc/flow.ts'
export { verifyIdToken, TokenRefused, type Jwk, type JwtClaims } from './oidc/jwt.ts'

export {
  connectionByHandle,
  connectionByEntityId,
  connectionById,
  connectionSecrets,
  consumeLoginState,
  enabledConnections,
  memberByEmail,
  rememberAssertion,
  routeForDomain,
  saveLoginState,
  seatsUsed,
  sweepOrg,
  type Connection,
  type LoginState,
  type Role,
} from './store.ts'

export { provision, roleFromGroups, verifiedDomain, ProvisioningRefused } from './provision.ts'

export {
  enforce,
  relax,
  isEnforced,
  signInPolicy,
  spendRecoveryCode,
  generateCode,
  hashCode,
  BreakGlassRefused,
  RECOVERY_CODE_COUNT,
} from './enforce.ts'

export { keyFromEnv, seal, open, SecretUnavailable } from './secrets.ts'
