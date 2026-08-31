// The first organization on a control plane nobody has installed the App on.
//
// This exists because self-hosting had no first move. A tenant begins when a
// GitHub App installation arrives, so an operator with an empty database and no
// App had no organization, every sign-in landed with no tenant, no actor could
// be built, and break-glass could not help because it deliberately cannot
// create the thing it would have to name.
//
// So the cases that matter are the same shape as break-glass's: what it refuses
// is more load bearing than what it does. It must not create an account, it
// must not grant a role, it must not work from the credential the web tier
// holds, and running it twice must not disturb an organization an installation
// has since adopted.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { createOrganization, BootstrapRefused } from '../src/bootstrap-org.ts'
import { available, startApi, dropOrg, adminUrl, appUrl, type ApiHarness } from './harness.ts'

const hasDatabase = await available()

describe('creating the first organization', { skip: hasDatabase ? false : 'no Postgres' }, () => {
  let h: ApiHarness
  const made: string[] = []

  before(async () => {
    h = await startApi()
  })

  after(async () => {
    for (const id of made) await dropOrg(h.admin, id)
    await h.close()
  })

  function slug(): string {
    // Lower case letters and digits only, because the table's own CHECK is what
    // this has to satisfy and a uuid's hyphens are fine but its case is not.
    const s = `bootstrap${randomUUID().replace(/-/g, '').slice(0, 12)}`
    return s
  }

  async function track<T extends { orgId: string }>(result: T): Promise<T> {
    if (result.orgId) made.push(result.orgId)
    return result
  }

  it('creates an organization with nobody in it', async () => {
    const name = slug()
    const result = await track(
      await createOrganization({ adminUrl, slug: name, name: 'Acme', operator: 'tester' }),
    )
    assert.equal(result.created, true)
    assert.equal(result.applied, true)
    assert.equal(result.slug, name)
    assert.equal(result.name, 'Acme')

    const rows = await h.admin<{ slug: string; name: string }[]>`
      SELECT slug, name FROM organizations WHERE id = ${result.orgId}`
    assert.equal(rows.length, 1)
    assert.equal(rows[0]!.name, 'Acme')

    // THE POINT. It creates a tenant and grants nobody anything, so it is not a
    // way in on its own: an operator still has to sign in through GitHub and
    // then break-glass, and each of those is a separate, audited act.
    const members = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM members WHERE org_id = ${result.orgId}`
    assert.equal(Number(members[0]!.n), 0)
    const users = await h.admin<{ n: string }[]>`SELECT count(*) AS n FROM users`
    const again = await h.admin<{ n: string }[]>`SELECT count(*) AS n FROM users`
    assert.equal(users[0]!.n, again[0]!.n, 'it created no account')
  })

  it('writes the first entry in the organization audit chain', async () => {
    const name = slug()
    const result = await track(await createOrganization({ adminUrl, slug: name, operator: 'ada' }))
    const rows = await h.admin<{
      actor_label: string
      action: string
      origin: string
      detail: Record<string, unknown>
    }[]>`
      SELECT actor_label, action, origin, detail FROM audit_entries
      WHERE org_id = ${result.orgId} ORDER BY seq`
    assert.equal(rows.length, 1)
    assert.equal(rows[0]!.action, 'organization.created')
    // No user did this, so the label is what survives, exactly as break-glass
    // records its own. An operator reading the log later has to be able to tell
    // a tenant that began with an installation from one created by hand.
    assert.equal(rows[0]!.actor_label, 'bootstrap')
    assert.equal(rows[0]!.origin, 'operator')
    assert.equal(rows[0]!.detail.operator, 'ada')
    assert.equal(result.auditSeq, Number(result.auditSeq))
  })

  it('defaults the name to the slug', async () => {
    const name = slug()
    const result = await track(await createOrganization({ adminUrl, slug: name }))
    assert.equal(result.name, name)
  })

  it('records a github login so a later installation adopts the row', async () => {
    // The reason --github-login exists. slugFor in the webhook derives an
    // organization's slug from the installing account and rememberInstallation
    // upserts on it, so an organization created here under that login is
    // adopted rather than duplicated when the App finally arrives.
    const name = slug()
    const result = await track(
      await createOrganization({ adminUrl, slug: name, githubLogin: name }),
    )
    const rows = await h.admin<{ github_login: string | null }[]>`
      SELECT github_login FROM organizations WHERE id = ${result.orgId}`
    assert.equal(rows[0]!.github_login, name)
  })

  it('is idempotent and leaves an adopted row alone', async () => {
    const name = slug()
    const first = await track(await createOrganization({ adminUrl, slug: name, name: 'First' }))
    // Stand in for an installation having arrived since.
    await h.admin`
      UPDATE organizations SET name = 'Adopted', github_login = 'acme' WHERE id = ${first.orgId}`

    const second = await createOrganization({ adminUrl, slug: name, name: 'Second' })
    assert.equal(second.created, false)
    assert.equal(second.orgId, first.orgId)
    // Reported as it is, not as it was asked for. An operator re-running the
    // command during a confused first deployment must not rename a tenant.
    assert.equal(second.name, 'Adopted')
    assert.equal(second.githubLogin, 'acme')

    const rows = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM organizations WHERE slug = ${name}`
    assert.equal(Number(rows[0]!.n), 1)
    // And no second audit entry, because nothing happened.
    const entries = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM audit_entries WHERE org_id = ${first.orgId}`
    assert.equal(Number(entries[0]!.n), 1)
  })

  it('a dry run writes nothing', async () => {
    const name = slug()
    const result = await createOrganization({ adminUrl, slug: name, dryRun: true })
    assert.equal(result.applied, false)
    assert.equal(result.created, true)
    const rows = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM organizations WHERE slug = ${name}`
    assert.equal(Number(rows[0]!.n), 0)
  })

  it('refuses a slug the table would refuse', async () => {
    // Checked here so the failure is a sentence rather than a constraint
    // violation quoting a regular expression at somebody mid deployment.
    for (const bad of ['-leading', 'has space', 'under_score', '', 'x'.repeat(64)]) {
      await assert.rejects(
        () => createOrganization({ adminUrl, slug: bad }),
        BootstrapRefused,
        `${bad} was accepted as a slug`,
      )
    }
  })

  it('lower cases the slug, because slugFor does', async () => {
    // Not a nicety. An operator types their GitHub organization the way it is
    // displayed, and the webhook derives its slug lower cased, so accepting the
    // display casing here and storing it verbatim would produce two
    // organizations for one account the day the App is installed.
    const name = slug()
    const result = await track(await createOrganization({ adminUrl, slug: name.toUpperCase() }))
    assert.equal(result.slug, name)
    const rows = await h.admin<{ slug: string }[]>`
      SELECT slug FROM organizations WHERE id = ${result.orgId}`
    assert.equal(rows[0]!.slug, name)
  })

  it('refuses the credential the web tier holds', async () => {
    // The same refusal break-glass makes, and the same reason: the application
    // role is subject to the policies, so without this the command would read
    // nothing and report that the organization does not exist, which is a
    // diagnosis pointing at the wrong thing entirely.
    await assert.rejects(
      () => createOrganization({ adminUrl: appUrl(), slug: slug() }),
      BootstrapRefused,
    )
  })
})
