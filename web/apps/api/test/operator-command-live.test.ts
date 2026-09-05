import { after, before, it } from 'node:test'
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import { adminUrl, available, startApi, type ApiHarness } from './harness.ts'

const hasDb = await available()
let api: ApiHarness
let ownsRootSlot = false
const email = 'operator-command-proof@example.test'
const password = 'test-only-command-passphrase'
const completionToken = '00000000-0000-4000-8000-000000000002'
before(async () => {
  if (!hasDb) return
  api = await startApi()
  api.clock.advance(Date.now() - api.clock.now().getTime())
  const roots = await api.admin`SELECT id FROM admin_users WHERE is_root`
  if (roots.length) throw new Error('This test requires an isolated database with no root operator. It never resets an existing root.')
  ownsRootSlot = true
})
after(async () => {
  if (api && ownsRootSlot) {
    await api.admin`ALTER TABLE admin_users DISABLE TRIGGER admin_root_is_permanent_del`
    try { await api.admin`DELETE FROM admin_users WHERE email = ${email}` }
    finally { await api.admin`ALTER TABLE admin_users ENABLE TRIGGER admin_root_is_permanent_del` }
  }
  if (api) await api.close()
})

it('the actual command creates a credential that reaches a protected operator endpoint', { skip: !hasDb }, async () => {
  const run = spawnSync(process.execPath, [fileURLToPath(new URL('../src/operator-cli.ts', import.meta.url)), 'init', '--email', email, '--name', 'Command Proof', '--completion-token', completionToken], {
    env: { ...process.env, AF_ADMIN_DATABASE_URL: adminUrl, AF_PUBLIC_URL: 'https://example.test' },
    input: `${password}\n`, encoding: 'utf8',
  })
  const signedIn = await api.fetch('/v1/admin/signin', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ email, password }) })
  const cookie = signedIn.headers.get('set-cookie')?.split(';')[0] ?? ''
  const me = await api.fetch('/trpc/admin.me', { headers: { cookie } })
  assert.deepEqual({ exit: run.status, signin: signedIn.status, protected: me.status, identity: (await me.text()).includes(email), passwordLeaked: `${run.stdout}${run.stderr}`.includes(password), completion: run.stdout.includes(`operator-init-complete:${completionToken}`) },
    { exit: 0, signin: 200, protected: 200, identity: true, passwordLeaked: false, completion: true })
})
