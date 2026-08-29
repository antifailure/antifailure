// A control plane on a real port, for conformance against a hosted provider.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// Keycloak can be driven entirely in-process, because the suite plays the part
// of the browser and hands the assertion straight to the routes. Entra ID and
// Okta cannot: they redirect a real browser to a real address, and Entra
// PUSHES SCIM at us rather than being polled. So those two need this process to
// be reachable from the internet, which is what `cloudflared tunnel --url`
// provides and what AF_PUBLIC_BASE_URL names.
//
// This is deliberately NOT web/apps/api/src/main.ts. That entry point requires
// GitHub OAuth credentials it is right to demand in production and that have no
// business being on a developer's machine for an SSO test. This registers the
// two extensions against a real database and serves them, and nothing else.
//
//   docker run -d --name af-cp-lane8 -p 55438:5432 \
//     -e POSTGRES_PASSWORD=test -e POSTGRES_DB=antifailure postgres:17-alpine
//   cloudflared tunnel --url http://localhost:8123        # prints a URL
//   AF_PUBLIC_BASE_URL=https://<that-url> \
//   AF_TEST_DATABASE_URL=postgres://postgres:test@127.0.0.1:55438/antifailure \
//     node ee/web/sso/test/live-server.ts
//
// It prints every value needed to configure the provider, and writes the SCIM
// bearer token to a 0600 file OUTSIDE the repository. The token is a
// credential: it is never printed in full, never committed, and never put in an
// artifact a scanner would find.

import postgres from 'postgres'
import { serve } from '@hono/node-server'
import { createHash, randomBytes, randomUUID } from 'node:crypto'
import { chmodSync, mkdirSync, writeFileSync } from 'node:fs'
import { homedir } from 'node:os'
import path from 'node:path'
import { createPool, migrate } from '@antifailure/db'
import { clearExtensions, createServer, registerExtension, setSignInPolicy, systemClock } from '@antifailure/api'
import { ssoExtension } from '../src/index.ts'
import { signInPolicy } from '../src/enforce.ts'
import { scimExtension } from '../../scim/src/index.ts'

function required(name: string): string {
  const value = process.env[name]
  if (!value) {
    console.error(`${name} is not set. This harness needs it.`)
    process.exit(2)
  }
  return value
}

const publicBaseUrl = required('AF_PUBLIC_BASE_URL').replace(/\/$/, '')
const adminUrl = required('AF_TEST_DATABASE_URL')
const port = Number(process.env.AF_PORT ?? 8123)

if (!publicBaseUrl.startsWith('https://')) {
  // The same refusal the product makes, for the same reason: a provider posting
  // an assertion or a SCIM token to a plain HTTP address puts both on the wire.
  console.error(`AF_PUBLIC_BASE_URL is ${publicBaseUrl}. It must be https.`)
  process.exit(2)
}

const admin = postgres(adminUrl, { max: 2, connect_timeout: 30, onnotice: () => {} })
await migrate(admin)
await admin.unsafe(`ALTER ROLE antifailure_app LOGIN PASSWORD 'app-test-password'`)

const appUrl = new URL(adminUrl)
appUrl.username = 'antifailure_app'
appUrl.password = 'app-test-password'
const pool = createPool({ url: appUrl.toString(), max: 5, connectTimeoutSeconds: 30 })

// Assembled at run time, like every other key in this package. Nothing here is
// committed and nothing here survives the process.
const encryptionKey = randomBytes(32)

clearExtensions()
setSignInPolicy(null)
registerExtension(
  ssoExtension({
    pool,
    clock: systemClock,
    baseUrl: publicBaseUrl,
    appBaseUrl: `${publicBaseUrl}/`,
    secureCookies: true,
    encryptionKey,
  }),
)
registerExtension(
  scimExtension({ pool, clock: systemClock, baseUrl: publicBaseUrl, defaultRole: 'member' }),
)
setSignInPolicy(signInPolicy(pool))

const { app } = createServer({
  pool,
  github: {} as never,
  clock: systemClock,
  secureCookies: true,
  appBaseUrl: `${publicBaseUrl}/`,
})

// One organisation, one connection placeholder, one SCIM token. The connection
// is left DISABLED and empty on purpose: the whole point is to fill it in from
// the provider's own metadata rather than from anything written here.
const slug = `live-${randomUUID().slice(0, 8)}`
const [org] = await admin<{ id: string }[]>`
  INSERT INTO organizations (slug, name) VALUES (${slug}, 'Conformance') RETURNING id`
const orgId = org!.id

const handle = randomBytes(32).toString('base64url')
const scimToken = `afs_${randomBytes(24).toString('base64url')}`
await admin`
  INSERT INTO scim_tokens (org_id, name, token_hash, prefix)
  VALUES (${orgId}, 'conformance', ${createHash('sha256').update(scimToken, 'utf8').digest()},
          ${scimToken.slice(0, 10)})`

// Outside the repository, 0600. A token in the tree is a token in every clone
// and every image built from it, and tools/scanrepo is right to fail on one.
const secretDir = path.join(homedir(), '.antifailure')
mkdirSync(secretDir, { recursive: true })
const secretFile = path.join(secretDir, `scim-token-${slug}`)
writeFileSync(secretFile, `${scimToken}\n`, { mode: 0o600 })
chmodSync(secretFile, 0o600)

serve({ fetch: app.fetch, port }, () => {
  console.log(`\nlistening on :${port}, published at ${publicBaseUrl}\n`)
  console.log(`organization   ${slug}  (${orgId})`)
  console.log(`sso handle     ${handle}`)
  console.log('')
  console.log('SAML, for the provider:')
  console.log(`  identifier (entity id)  ${publicBaseUrl}/sso/saml/${handle}/metadata`)
  console.log(`  reply url (ACS)         ${publicBaseUrl}/sso/saml/${handle}/acs`)
  console.log(`  our metadata            ${publicBaseUrl}/sso/saml/${handle}/metadata`)
  console.log('')
  console.log('OIDC, for the provider:')
  console.log(`  redirect uri            ${publicBaseUrl}/sso/oidc/${handle}/callback`)
  console.log('')
  console.log('SCIM, for the provider:')
  console.log(`  tenant url              ${publicBaseUrl}/scim/v2`)
  console.log(`  secret token            written to ${secretFile} (0600, outside the repo)`)
  console.log(`  token prefix            ${scimToken.slice(0, 10)}...  (full value only in that file)`)
  console.log('')
  console.log(`connection row is NOT created: fill it from the provider's own metadata.`)
})
