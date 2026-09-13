// The enterprise command line, run as a process, against the community one.
//
// What this proves is the edition seam at the one place a rotation meets it: the
// operator's command line. The enterprise edition registers its sealed table
// through a hook the community edition exposes, and that registration is worth
// nothing unless the process that re-seals actually runs it. So both command
// lines are spawned, the way the image's launcher and a source checkout run
// them, rather than their functions being called in this process.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { randomBytes, randomUUID } from 'node:crypto'
import { fileURLToPath } from 'node:url'
import postgres from 'postgres'
import { Keyring, seal } from '@antifailure/api'
import { migrate } from '@antifailure/db'
import { PURPOSE_PREFIX } from '@antifailure-ee/audit'
import { adminUrl, available } from './harness.ts'

const ENTERPRISE = fileURLToPath(new URL('../src/backup-cli.ts', import.meta.url))
const COMMUNITY = fileURLToPath(new URL('../../../../web/apps/api/src/backup-cli.ts', import.meta.url))

function run(entry: string, args: string[], env: Record<string, string> = {}) {
  const base: Record<string, string> = {}
  for (const [k, v] of Object.entries(process.env)) {
    if (v !== undefined && !k.startsWith('AF_PROVIDER_KEY_') && k !== 'AF_RESEAL_DATABASE_URL') base[k] = v
  }
  return spawnSync(process.execPath, [entry, ...args], {
    env: { ...base, NODE_OPTIONS: '--disable-warning=ExperimentalWarning', ...env },
    encoding: 'utf8',
    timeout: 120_000,
  })
}

describe('which tables each command line re-seals', () => {
  it('the enterprise command line lists the audit stream table and the community one does not', () => {
    const enterprise = run(ENTERPRISE, ['sealed-tables'])
    assert.equal(enterprise.status, 0, enterprise.stderr)
    const enterpriseTables = enterprise.stdout.trim().split('\n').map((l) => l.split('\t')[0])
    assert.ok(enterpriseTables.includes('audit_stream_destinations'), `enterprise lists: ${enterpriseTables.join(', ')}`)
    assert.ok(enterpriseTables.includes('provider_keys'))

    const community = run(COMMUNITY, ['sealed-tables'])
    assert.equal(community.status, 0, community.stderr)
    assert.deepEqual(community.stdout.trim().split('\n').map((l) => l.split('\t')[0]), ['provider_keys'])
  })
})

const hasDatabase = await available()

describe(
  'a rotation against a database holding an audit stream destination',
  { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' },
  () => {
    let admin: postgres.Sql
    const orgId = randomUUID()
    const key = randomBytes(32)

    before(async () => {
      admin = postgres(adminUrl, { max: 2, connect_timeout: 30, onnotice: () => {} })
      await migrate(admin)
      await admin`INSERT INTO organizations (id, slug, name) VALUES (${orgId}, ${`cli-${orgId.slice(0, 8)}`}, 'Command line')`
      const sealed = seal(Keyring.of(key), 'a-collector-token-for-the-command-line', {
        orgId, provider: `${PURPOSE_PREFIX}webhook`,
      })
      await admin`
        INSERT INTO audit_stream_destinations
          (org_id, kind, url, ciphertext, nonce, key_version, fingerprint, last4)
        VALUES (${orgId}, 'webhook', 'https://collector.example/ingest', ${sealed.ciphertext}, ${sealed.nonce},
                ${sealed.keyVersion}, ${sealed.fingerprint}, ${sealed.last4})`
    })

    after(async () => {
      await admin`DELETE FROM organizations WHERE id = ${orgId}`
      await admin.end({ timeout: 5 })
    })

    it('the community command line refuses by name, and the enterprise one opens the table', () => {
      const env = { AF_PROVIDER_KEY_SECRET: key.toString('base64'), AF_RESEAL_DATABASE_URL: adminUrl }

      const community = run(COMMUNITY, ['reseal', '--check'], env)
      assert.equal(community.status, 2, `community exit ${community.status}: ${community.stdout}${community.stderr}`)
      assert.match(community.stderr, /audit_stream_destinations/)
      assert.match(community.stderr, /backup-cli\.mjs reseal/)

      // Other suites share this database and seal rows under their own keys, so
      // the enterprise run may report rows it cannot open (exit 3). What it must
      // not do is refuse (exit 2), and it must have read the audit stream table.
      const enterprise = run(ENTERPRISE, ['reseal', '--check'], env)
      assert.notEqual(enterprise.status, 2, `the enterprise command line refused: ${enterprise.stderr}`)
      assert.match(enterprise.stdout, /^audit_stream_destinations\s/m, enterprise.stdout)
    })
  },
)
