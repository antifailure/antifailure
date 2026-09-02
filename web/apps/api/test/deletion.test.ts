// Deleting an organization, in every order the events can arrive in.
//
// The thing being tested is not that a DELETE works. It does, and there is a
// test below that measures it: a cascade from `organizations` removes every
// referencing row on the cluster, including the ones whose DELETE privilege was
// deliberately revoked, because referential integrity actions run with the
// table owner's privileges and bypass row-level security.
//
// What is tested is everything that has to happen BEFORE it, in order, and what
// happens when the process doing it stops halfway. A deletion that removed the
// row while Stripe kept billing the card would pass every test that only looked
// at the database.

import { after, afterEach, before, beforeEach, describe, it } from 'node:test'
import { readdir, readFile } from 'node:fs/promises'
import assert from 'node:assert/strict'
import {
  available,
  callProcedure,
  dropOrg,
  errorCode,
  seedOrg,
  signInAs,
  startApi,
  stripeAgainstMockPack,
  type ApiHarness,
  type Org,
  type SignedIn,
} from './harness.ts'
import type { Billing } from '../src/billing/index.ts'
import {
  advanceDeletion,
  stopWork,
  resumeDeletions,
  runToCompletion,
  type DeletionDeps,
  type DeletionView,
} from '../src/enterprise/deletion.ts'

const hasDatabase = await available()

function data<T>(body: unknown): T {
  const b = body as { result?: { data?: T }; error?: { message?: string } }
  assert.ok(b.result, `expected a result, got: ${JSON.stringify(b.error ?? b).slice(0, 500)}`)
  return b.result.data as T
}

function message(body: unknown): string {
  return (body as { error?: { message?: string } }).error?.message ?? ''
}

describe(
  'deleting an organization',
  { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' },
  () => {
    let h: ApiHarness
    let billing: Billing
    let deps: DeletionDeps

    before(async () => {
      const stripe = await stripeAgainstMockPack()
      billing = stripe.billing
      h = await startApi({ stripe: billing })
      deps = {
        pool: h.pool,
        clock: h.clock,
        github: h.github,
        stripe: billing,
        // Silent. Every failure this suite provokes is asserted on the record
        // rather than read out of a log, and a suite that printed them would
        // make a passing run look like a broken one.
        log: () => {},
      }
    })
    after(async () => {
      await h.close()
    })

    let org: Org
    let owner: SignedIn

    beforeEach(async () => {
      org = await seedOrg(h.admin, 'deleting')
      owner = await signInAs(h, org, 'owner')
    })

    /**
     * Each test takes its organization with it.
     *
     * Not tidiness. `resumeDeletions` acts on every due record in the database,
     * so a record left behind by one test is picked up by the next test's
     * sweep, and the counters it returns become facts about the whole database
     * rather than about the thing under test. Leaving the subscription rows
     * behind is worse: the mock pack hands out identifiers from a counter, and
     * two tests that both create one collide on
     * `subscriptions_stripe_subscription_id_key`.
     */
    afterEach(async () => {
      await dropOrg(h.admin, org.orgId)
    })

    async function request(
      confirm = org.slug,
      reason?: string,
    ): Promise<{ deletion: DeletionView; exportUrl: string }> {
      const { body } = await callProcedure(h, owner, 'deletion.request', 'mutation', {
        confirm,
        ...(reason ? { reason } : {}),
      })
      return data(body)
    }

    async function record(): Promise<Record<string, unknown>> {
      const [row] = await h.admin<Record<string, unknown>[]>`
        SELECT * FROM organization_deletions WHERE org_id = ${org.orgId}
        ORDER BY requested_at DESC LIMIT 1`
      return row!
    }

    async function orgExists(): Promise<boolean> {
      const [count] = await h.admin<{ n: string }[]>`
        SELECT count(*) AS n FROM organizations WHERE id = ${org.orgId}`
      return Number(count!.n) > 0
    }

    /** A live subscription at Stripe and a row here that knows about it. */
    async function subscribe(periodEndMs: number): Promise<string> {
      const customerId = `cus_del_${org.slug}`
      await h.admin`
        INSERT INTO billing_customers (org_id, stripe_customer_id) VALUES (${org.orgId}, ${customerId})`
      const created = await billing.config.fetch!(
        new URL('/v1/subscriptions', 'https://api.stripe.com'),
        {
          method: 'POST',
          headers: { 'content-type': 'application/x-www-form-urlencoded' },
          body: new URLSearchParams({
            customer: customerId,
            'items[0][price]': 'price_enterprise_afmock',
            'items[0][quantity]': '3',
          }).toString(),
        },
      )
      const atStripe = (await created.json()) as { id: string }
      // The mock pack mints identifiers from a counter that starts at zero for
      // each pack instance, and a pack instance lives for one run of this file.
      // So a run against a database that still holds rows from an earlier run
      // collides on `subscriptions_stripe_subscription_id_key` at its first
      // subscription, which reads as a bug in the code under test. Clearing the
      // one row makes the fixture independent of what was left behind.
      await h.admin`
        DELETE FROM subscriptions WHERE stripe_subscription_id = ${atStripe.id}`
      await h.admin`
        INSERT INTO subscriptions (
          org_id, stripe_subscription_id, stripe_customer_id, plan, status, quantity,
          current_period_start, current_period_end, last_event_at)
        VALUES (${org.orgId}, ${atStripe.id}, ${customerId}, 'enterprise', 'active', 3,
                ${h.clock.now()}, ${new Date(periodEndMs)}, ${h.clock.now()})`
      await h.admin`
        UPDATE organizations SET plan = 'enterprise' WHERE id = ${org.orgId}`
      return atStripe.id
    }

    // -----------------------------------------------------------------------
    // The request itself
    // -----------------------------------------------------------------------

    it('refuses a wrong confirmation and writes nothing', async () => {
      const { body } = await callProcedure(h, owner, 'deletion.request', 'mutation', {
        confirm: 'not-the-slug',
      })
      assert.equal(errorCode(body), 'BAD_REQUEST')
      assert.match(message(body), new RegExp(`Type ${org.slug} to confirm`))

      const [count] = await h.admin<{ n: string }[]>`
        SELECT count(*) AS n FROM organization_deletions WHERE org_id = ${org.orgId}`
      assert.equal(Number(count!.n), 0)
      assert.ok(await orgExists())
    })

    it('refuses a second deletion while one is live', async () => {
      // A subscription, so the first request stops at the wait rather than
      // finishing. With nothing to wait for it runs all the way to the purge
      // inside the request, which revokes the caller's own session, and the
      // second call is then UNAUTHORIZED rather than a refusal about the
      // deletion. That is correct behaviour and it is not what this asserts.
      await subscribe(h.clock.now().getTime() + 10 * 24 * 60 * 60 * 1000)
      await request()
      const again = await callProcedure(h, owner, 'deletion.request', 'mutation', {
        confirm: org.slug,
      })
      assert.equal(errorCode(again.body), 'BAD_REQUEST')
      assert.match(message(again.body), /already being deleted/)
    })

    // -----------------------------------------------------------------------
    // The happy path, and the cascade at the end of it
    // -----------------------------------------------------------------------

    it('runs to the purge in one request when there is nothing to wait for', async () => {
      const { deletion, exportUrl } = await request(org.slug, 'we are done')
      assert.equal(deletion.step, 'done', `stopped at ${deletion.step}`)
      assert.equal(await orgExists(), false, 'the organization row is still there')

      const row = await record()
      assert.ok(row.work_stopped_at)
      assert.ok(row.subscription_cancelled_at)
      assert.equal(row.subscription_id, null, 'a subscription was recorded where there was none')
      assert.ok(row.credentials_revoked_at)
      assert.ok(row.exported_at)
      assert.ok(row.purged_at)
      assert.equal(row.reason, 'we are done')

      // The record survives the row it is about, which is the whole reason it
      // has no foreign key.
      assert.equal(row.org_slug, org.slug)

      // And so does the export, reachable by the link the requester was handed
      // at request time and by nothing else, because there is no membership
      // left to authorise anything.
      const token = new URL(exportUrl).searchParams.get('token')!
      const download = await h.fetch(`/exports/deletion?token=${encodeURIComponent(token)}`)
      assert.equal(download.status, 200)
      assert.match(download.headers.get('content-disposition') ?? '', /attachment; filename=/)
      const doc = (await download.json()) as { organization: { slug: string }; files: Record<string, string> }
      assert.equal(doc.organization.slug, org.slug)
      assert.ok(doc.files['README.md'])
    })

    /**
     * The loaded gun the purge is pointing at everything else.
     *
     * A cascade from `organizations` removes rows whose DELETE privilege is
     * explicitly revoked, `audit_entries` and every billing table among them,
     * because referential integrity actions run with the table owner's
     * privileges and bypass row level security. The test below measures that,
     * because the deletion needs it.
     *
     * The consequence is that ANY other statement that removes an
     * organizations row silently destroys that organization's billing history
     * and its append only audit log. There is exactly one place in the shipped
     * code that may do it, and this is what keeps it that way: a second one
     * added anywhere under src/ fails here rather than being found afterwards.
     *
     * Deliberately a source check rather than a behavioural one. The behaviour
     * is correct wherever the statement is; what must not happen is a second
     * caller existing at all, and there is no request that can reveal one.
     */
    it('nothing but the deletion machine deletes an organization', async () => {
      const root = new URL('../src/', import.meta.url)
      const found: string[] = []
      const walk = async (dir: URL): Promise<void> => {
        for (const entry of await readdir(dir, { withFileTypes: true })) {
          const child = new URL(entry.name + (entry.isDirectory() ? '/' : ''), dir)
          if (entry.isDirectory()) {
            await walk(child)
            continue
          }
          if (!entry.name.endsWith('.ts')) continue
          const body = await readFile(child, 'utf8')
          // The verb, then anything, then the noun, across newlines, because
          // SQL wraps and a line oriented grep cannot see
          // `DELETE\n  FROM organizations`.
          if (/\bdelete\s+from\b[\s\S]{0,120}?\borganizations\b/i.test(body)) {
            found.push(child.pathname.slice(root.pathname.length))
          }
        }
      }
      await walk(root)
      assert.deepEqual(
        found.sort(),
        ['enterprise/deletion.ts'],
        'something other than the deletion state machine deletes an organization row, and a ' +
          'cascade from that row removes the audit log and every billing record with it',
      )
    })

    /**
     * The same guard, for the table whose comment used to claim a guarantee.
     *
     * `audit_entries.actor_user_id` references `users` with ON DELETE SET NULL,
     * not NO ACTION, measured with pg_get_constraintdef rather than read off a
     * catalog letter. So deleting a person is not refused by the database: it
     * succeeds and nulls the actor on every entry they ever wrote, silently and
     * all at once. `account.close` erases the personal fields and never deletes,
     * which is right, and nothing enforced that but the absence of a second
     * caller. This is the enforcement.
     */
    it('nothing deletes a person', async () => {
      const root = new URL('../src/', import.meta.url)
      const found: string[] = []
      const walk = async (dir: URL): Promise<void> => {
        for (const entry of await readdir(dir, { withFileTypes: true })) {
          const child = new URL(entry.name + (entry.isDirectory() ? '/' : ''), dir)
          if (entry.isDirectory()) {
            await walk(child)
            continue
          }
          if (!entry.name.endsWith('.ts')) continue
          const body = await readFile(child, 'utf8')
          if (/\bdelete\s+from\b[\s\S]{0,120}?\busers\b/i.test(body)) {
            found.push(child.pathname.slice(root.pathname.length))
          }
        }
      }
      await walk(root)
      assert.deepEqual(
        found,
        [],
        'something deletes a user row, and the foreign key from audit_entries is ON DELETE SET ' +
          'NULL, so it nulls the actor on every entry that person ever wrote',
      )
    })

    it('the purge takes the rows a bare DELETE would leave behind', async () => {
      await h.admin`
        INSERT INTO audit_entries (org_id, actor_label, action, target_type, origin, entry_hash)
        VALUES (${org.orgId}, 'fixture', 'x.y', 'organization', 'web', 'deadbeef')`
      await h.admin`
        INSERT INTO billing_customers (org_id, stripe_customer_id)
        VALUES (${org.orgId}, ${`cus_cascade_${org.slug}`})`

      await request()

      // Both tables explicitly REVOKE DELETE from the application role, and
      // both are empty afterwards. Referential integrity actions run with the
      // table owner's privileges and bypass row-level security, so the append
      // only audit log and the never-deleted billing rows both go with the
      // organization. That is what a deletion has to do, and it is worth
      // asserting rather than assuming, because it also means any other code
      // path that ever deletes an organizations row destroys billing history.
      const [countaudits] = await h.admin<{ n: string }[]>`
        SELECT count(*) AS n FROM audit_entries WHERE org_id = ${org.orgId}`
      const [countcustomers] = await h.admin<{ n: string }[]>`
        SELECT count(*) AS n FROM billing_customers WHERE org_id = ${org.orgId}`
      const [countmembers] = await h.admin<{ n: string }[]>`
        SELECT count(*) AS n FROM members WHERE org_id = ${org.orgId}`
      assert.equal(Number(countaudits!.n), 0)
      assert.equal(Number(countcustomers!.n), 0)
      assert.equal(Number(countmembers!.n), 0)
    })

    // -----------------------------------------------------------------------
    // Step 1, against work that is running
    // -----------------------------------------------------------------------

    it('stops running work and closes the door behind itself', async () => {
      await h.admin`
        UPDATE environments SET state = 'running' WHERE org_id = ${org.orgId}`
      const [env] = await h.admin<{ id: string }[]>`
        SELECT id FROM environments WHERE org_id = ${org.orgId} LIMIT 1`
      await h.admin`
        INSERT INTO runs (org_id, environment_id, kind, state)
        VALUES (${org.orgId}, ${env!.id}, 'agent', 'running')`

      // A subscription, so the deletion stops at the wait and the rows can be
      // read before the purge removes them.
      await subscribe(h.clock.now().getTime() + 20 * 24 * 60 * 60 * 1000)
      const { deletion } = await request()
      assert.equal(deletion.step, 'await_entitlement_end')
      assert.equal(deletion.stoppedWork?.environments, 1)
      assert.equal(deletion.stoppedWork?.runs, 1)

      const [state] = await h.admin<{ state: string }[]>`
        SELECT state::text AS state FROM environments WHERE org_id = ${org.orgId}`
      assert.equal(state!.state, 'torn_down')
      const [run] = await h.admin<{ state: string }[]>`
        SELECT state::text AS state FROM runs WHERE org_id = ${org.orgId} LIMIT 1`
      assert.equal(run!.state, 'cancelled')

      // Suspended, so nothing new is created behind the deletion. Creating an
      // environment during the wait would leave containers running in somebody's
      // CI when the purge arrived.
      const [suspended] = await h.admin<{ suspended_reason: string | null }[]>`
        SELECT suspended_reason FROM organizations WHERE id = ${org.orgId}`
      assert.equal(suspended!.suspended_reason, 'deletion requested')
    })

    // -----------------------------------------------------------------------
    // Step 2 and the wait
    // -----------------------------------------------------------------------

    it('cancels the subscription, waits out the period, and only then revokes', async () => {
      const endsAt = h.clock.now().getTime() + 20 * 24 * 60 * 60 * 1000
      const subscriptionId = await subscribe(endsAt)
      await h.admin`
        INSERT INTO engine_tokens (org_id, name, token_hash, prefix)
        VALUES (${org.orgId}, 'ci', ${Buffer.from(`tok-${org.slug}`)}, 'afe_ci')`

      const { deletion } = await request()
      assert.equal(deletion.step, 'await_entitlement_end')
      assert.equal(deletion.cancelledSubscription?.subscription, subscriptionId)
      assert.ok(deletion.waitingUntil, 'nothing to wait for was recorded')

      // Cancelled at Stripe, at the end of the period, and NOT immediately.
      // Somebody who has paid for the month keeps the month.
      const atStripe = await billing.client.getSubscription(subscriptionId)
      assert.equal(atStripe?.cancelAtPeriodEnd, true)

      // Nothing has been revoked and nothing has been deleted while they are
      // still entitled. This is the assertion that would fail if the order were
      // wrong.
      const [token] = await h.admin<{ revoked_at: Date | null }[]>`
        SELECT revoked_at FROM engine_tokens WHERE org_id = ${org.orgId}`
      assert.equal(token!.revoked_at, null, 'credentials went while the customer was still paying')
      assert.ok(await orgExists())

      // A sweep while the wait is on leaves THIS organization alone. The
      // assertion is on the organization rather than on the sweep's counters:
      // other tests in this file leave records behind that the sweep also picks
      // up, and a count is a fact about the whole database.
      await resumeDeletions(deps)
      assert.ok(await orgExists())
      const held = await record()
      assert.equal(held.credentials_revoked_at, null)

      // Past the end of the period.
      h.clock.advance(new Date(deletion.waitingUntil!).getTime() - h.clock.now().getTime() + 1000)
      await resumeDeletions(deps)
      assert.equal(await orgExists(), false)
      const finished = await record()
      assert.ok(finished.purged_at)
      assert.equal(finished.engine_tokens_revoked, 1)
    })

    /**
     * The ordering that makes the wait a guess rather than a fact: the
     * cancellation reads the period end, and a `customer.subscription.updated`
     * that was in flight at that moment lands afterwards and moves it.
     *
     * The stored value would then be too early, and the deletion would revoke
     * credentials on an organization that is still entitled. So the wait
     * re-reads the subscription every time it is examined and keeps the later
     * of the two.
     */
    it('a Stripe update that lands after the cancellation moves the wait, and does not shorten it', async () => {
      const first = h.clock.now().getTime() + 5 * 24 * 60 * 60 * 1000
      const subscriptionId = await subscribe(first)
      const { deletion } = await request()
      assert.equal(deletion.step, 'await_entitlement_end')
      const recorded = new Date(deletion.waitingUntil!).getTime()

      // The delivery lands: the period now ends ten days later than the
      // cancellation was told.
      const extended = recorded + 10 * 24 * 60 * 60 * 1000
      await h.admin`
        UPDATE subscriptions SET current_period_end = ${new Date(extended)}
        WHERE org_id = ${org.orgId} AND stripe_subscription_id = ${subscriptionId}`

      // Past the ORIGINAL end. Without the re-read this is where the deletion
      // would revoke and purge.
      h.clock.advance(recorded - h.clock.now().getTime() + 1000)
      await resumeDeletions(deps)
      assert.ok(await orgExists(), 'the deletion purged while the customer was still entitled')

      // Read from the record rather than through the route. The clock has just
      // moved five days, which is well past the twelve hour idle timeout, so
      // every session issued before it has stopped resolving. That is correct
      // behaviour and it is a trap for any test that moves a fake clock by
      // days and then makes a request.
      const still = await record()
      assert.equal(still.credentials_revoked_at, null)
      assert.equal(new Date(String(still.entitlement_ends_at)).getTime(), extended)

      // And a delivery that SHORTENS the period is ignored, because being late
      // costs a day and being early breaks something somebody paid for.
      await h.admin`
        UPDATE subscriptions SET current_period_end = ${new Date(recorded)}
        WHERE org_id = ${org.orgId} AND stripe_subscription_id = ${subscriptionId}`
      await resumeDeletions(deps)
      const unchanged = await record()
      assert.equal(new Date(String(unchanged.entitlement_ends_at)).getTime(), extended)
      assert.equal(unchanged.credentials_revoked_at, null)
    })

    it('a control plane with a live subscription and no Stripe refuses to go on', async () => {
      await subscribe(h.clock.now().getTime() + 10 * 24 * 60 * 60 * 1000)
      const withoutStripe: DeletionDeps = { ...deps, stripe: null }

      // The request itself goes through the router, which has Stripe. The
      // resumer here is the one that does not, which is the real shape of the
      // failure: a deployment that lost its Stripe configuration between the
      // request and the sweep.
      await h.admin`
        INSERT INTO organization_deletions (org_id, org_slug, org_name, requested_by_label)
        VALUES (${org.orgId}, ${org.slug}, 'deleting', 'the test')`
      await assert.rejects(
        () => runToCompletion(withoutStripe, org.orgId),
        /not configured to talk to Stripe/,
      )

      assert.ok(await orgExists(), 'the organization was purged with a live subscription')
      const row = await record()
      assert.equal(row.subscription_cancelled_at, null)
      assert.equal(row.last_error_step, 'cancel_subscription')
      assert.match(String(row.last_error_message), /AF_STRIPE_SECRET_KEY/)
      assert.equal(Number(row.attempts), 1)
    })

    // -----------------------------------------------------------------------
    // Step 4
    // -----------------------------------------------------------------------

    it('revokes every credential and uninstalls the App', async () => {
      await h.admin`
        INSERT INTO engine_tokens (org_id, name, token_hash, prefix)
        VALUES (${org.orgId}, 'ci', ${Buffer.from(`tok-${org.slug}`)}, 'afe_ci')`
      await h.admin`
        INSERT INTO provider_keys (org_id, provider, ciphertext, nonce, fingerprint, last4)
        VALUES (${org.orgId}, 'anthropic', ${Buffer.from('cipher')}, ${Buffer.from('nonce')},
                'fp', '1234')`
      await h.admin`
        INSERT INTO github_installations (org_id, installation_id, account_login, account_type)
        VALUES (${org.orgId}, 98765, ${org.slug}, 'Organization')`
      h.github.addInstallation(98765)

      const { deletion } = await request()
      assert.equal(deletion.step, 'done')
      assert.equal(deletion.revokedCredentials?.engineTokens, 1)
      assert.equal(deletion.revokedCredentials?.providerKeys, 1)
      assert.ok((deletion.revokedCredentials?.sessions ?? 0) >= 1)
      assert.equal(deletion.revokedCredentials?.installations, 1)

      // The one that is not in this database. Marking the row suspended and
      // leaving the App installed would leave it able to dispatch workflows in
      // the customer's repositories after we told them the organization was
      // gone.
      assert.deepEqual(h.github.revoked.slice(-1), [98765])
    })

    /**
     * The interruption, and the re-entry.
     *
     * GitHub refuses the uninstall, so the deletion stops at revocation with
     * the failure recorded. Nothing that already happened is undone and nothing
     * later happens. Then GitHub answers, the deletion is re-entered, and it
     * finishes without repeating the steps it had already done.
     */
    it('an interrupted deletion records where it stopped and is safe to re-enter', async () => {
      await h.admin`
        INSERT INTO github_installations (org_id, installation_id, account_login, account_type)
        VALUES (${org.orgId}, 4242, ${org.slug}, 'Organization')`
      await h.admin`
        UPDATE environments SET state = 'running' WHERE org_id = ${org.orgId}`
      h.github.addInstallation(4242)
      h.github.refuseRevocations('GitHub is having a moment')

      const { body } = await callProcedure(h, owner, 'deletion.request', 'mutation', {
        confirm: org.slug,
      })
      // The request advances as far as it can and then surfaces the failure.
      assert.equal(errorCode(body), 'INTERNAL_SERVER_ERROR')

      const stopped = await record()
      assert.ok(stopped.work_stopped_at, 'the step before the failure was rolled back')
      assert.equal(stopped.environments_stopped, 1)
      assert.ok(stopped.subscription_cancelled_at)
      assert.equal(stopped.credentials_revoked_at, null)
      assert.equal(stopped.exported_at, null)
      assert.equal(stopped.purged_at, null)
      assert.equal(stopped.last_error_step, 'revoke_credentials')
      assert.match(String(stopped.last_error_message), /having a moment/)
      assert.ok(await orgExists())

      // A sweep while it is failing does not advance it and does not lose it.
      // The back-off in deletions_due holds it back for a minute per attempt,
      // so the clock has to move for it to be offered again at all.
      h.clock.advance(2 * 60 * 1000)
      await resumeDeletions(deps).catch(() => {})
      assert.ok(await orgExists())
      const stillStopped = await record()
      assert.equal(stillStopped.credentials_revoked_at, null)
      assert.equal(
        stillStopped.environments_stopped,
        1,
        'the first step ran again and recounted',
      )

      // GitHub answers.
      h.github.refuseRevocations(null)
      h.clock.advance(5 * 60 * 1000)
      const resumed = await resumeDeletions(deps)
      assert.equal(resumed.advanced, 1)
      assert.equal(await orgExists(), false)

      const finished = await record()
      assert.ok(finished.purged_at)
      // The counts from the first attempt are the ones on the record. A resumed
      // deletion that redid step one would have found nothing running and
      // written a zero over the one.
      assert.equal(finished.environments_stopped, 1)
      assert.equal(finished.installations_revoked, 1)
      // Cleared when a step finally succeeds, so a finished deletion does not
      // read as a failed one forever.
      assert.equal(finished.last_error_at, null)
      assert.equal(Number(finished.attempts), 0)
    })

    it('two resumers arriving at once do not both do the same step', async () => {
      await subscribe(h.clock.now().getTime() + 10 * 24 * 60 * 60 * 1000)
      await h.admin`
        UPDATE environments SET state = 'running' WHERE org_id = ${org.orgId}`
      await h.admin`
        INSERT INTO organization_deletions (org_id, org_slug, org_name, requested_by_label)
        VALUES (${org.orgId}, ${org.slug}, 'deleting', 'the test')`

      // Both calls are aimed at the SAME step, which is the thing being
      // tested, rather than at the step machine, which is not.
      //
      // This used to fire two advanceDeletion calls through Promise.all and
      // assert that exactly one reported progress. Promise.all does not
      // guarantee overlap: it starts both and waits, and on a loaded runner
      // the first can finish before the second reads its step. Then the second
      // reads the NEXT step and advances that instead, and both report
      // progress honestly. Instrumented and serialized on purpose, that is
      // exactly what happens: the first advances stop_work, the second
      // advances cancel_subscription, both return moved, and the assertion
      // reported 2 !== 1 against a product that had done nothing wrong. It
      // took green pull requests down at random for as long as it stood.
      //
      // So the invariant is asserted where it lives. stopWork claims the
      // record before it does anything, and the promise is that a second
      // caller finds it claimed and stops. That is true whether the two
      // overlap or serialize, which is what makes this deterministic without
      // making it weaker.
      const [a, b] = await Promise.all([
        stopWork(deps, org.orgId),
        stopWork(deps, org.orgId),
      ])
      assert.equal([a, b].filter(Boolean).length, 1,
        'two callers both reported doing the same step, so the claim did not hold')

      // And the step happened once, with the count recorded by the caller that
      // actually did it. If the claim moved but the work did not, or the work
      // ran under a caller whose counts were then thrown away, this is where
      // it shows.
      const row = await record()
      assert.equal(row.environments_stopped, 1)
      assert.equal(row.runs_cancelled, 0)
      const [env] = await h.admin<{ n: string }[]>`
        SELECT count(*)::text AS n FROM environments
        WHERE org_id = ${org.orgId} AND state = 'torn_down'`
      assert.equal(env!.n, '1', 'the environment was not torn down exactly once')
    })

    // -----------------------------------------------------------------------
    // Calling it off
    // -----------------------------------------------------------------------

    it('calling it off lifts the suspension it applied', async () => {
      await subscribe(h.clock.now().getTime() + 10 * 24 * 60 * 60 * 1000)
      await request()
      const { body } = await callProcedure(h, owner, 'deletion.cancel', 'mutation', {})
      const cancelled = data<{ cancelled: boolean; resumedOrganization: boolean }>(body)
      assert.equal(cancelled.cancelled, true)
      assert.equal(cancelled.resumedOrganization, true)

      const [row] = await h.admin<{ suspended_at: Date | null }[]>`
        SELECT suspended_at FROM organizations WHERE id = ${org.orgId}`
      assert.equal(row!.suspended_at, null)

      // And it stops moving. A sweep afterwards must not pick this one up,
      // whatever it does with the records other tests left behind.
      h.clock.advance(30 * 24 * 60 * 60 * 1000)
      await resumeDeletions(deps)
      assert.ok(await orgExists())
      const stopped = await record()
      assert.ok(stopped.cancelled_at)
      assert.equal(stopped.credentials_revoked_at, null)
    })

    it('calling it off does not lift a suspension somebody else applied', async () => {
      await subscribe(h.clock.now().getTime() + 10 * 24 * 60 * 60 * 1000)
      await request()
      // An incident, after the deletion was requested and before it was called
      // off. The organization must stay suspended.
      await h.admin`
        UPDATE organizations SET suspended_reason = 'an incident', suspended_by = 'somebody else'
        WHERE id = ${org.orgId}`

      const { body } = await callProcedure(h, owner, 'deletion.cancel', 'mutation', {})
      assert.equal(data<{ resumedOrganization: boolean }>(body).resumedOrganization, false)
      const [row] = await h.admin<{ suspended_at: Date | null; suspended_reason: string | null }[]>`
        SELECT suspended_at, suspended_reason FROM organizations WHERE id = ${org.orgId}`
      assert.ok(row!.suspended_at)
      assert.equal(row!.suspended_reason, 'an incident')
    })

    it('there is nothing to call off once it has finished', async () => {
      await request()
      const { body } = await callProcedure(h, owner, 'deletion.cancel', 'mutation', {})
      // The session was revoked and the organization is gone, so the caller is
      // no longer an actor at all. Either refusal is the right one; what must
      // not happen is a cancellation succeeding after the purge.
      assert.ok(['UNAUTHORIZED', 'BAD_REQUEST'].includes(errorCode(body) ?? ''))
      const row = await record()
      assert.ok(row.purged_at)
      assert.equal(row.cancelled_at, null)
    })

    // -----------------------------------------------------------------------
    // The export, during and after
    // -----------------------------------------------------------------------

    it('an export can still be taken while a deletion is waiting', async () => {
      await subscribe(h.clock.now().getTime() + 10 * 24 * 60 * 60 * 1000)
      await request()
      const { body } = await callProcedure(h, owner, 'exports.organization', 'mutation', {})
      const doc = data<{ organization: { slug: string } }>(body)
      assert.equal(doc.organization.slug, org.slug)
    })

    it('the held export is destroyed when its window ends, and the record stays', async () => {
      const { exportUrl } = await request()
      const token = new URL(exportUrl).searchParams.get('token')!

      const before = await h.fetch(`/exports/deletion?token=${encodeURIComponent(token)}`)
      assert.equal(before.status, 200)

      h.clock.advance(8 * 24 * 60 * 60 * 1000)
      const swept = await resumeDeletions(deps)
      assert.ok(swept.exportsDestroyed >= 1, 'the sweep destroyed nothing')

      const after = await h.fetch(`/exports/deletion?token=${encodeURIComponent(token)}`)
      assert.equal(after.status, 404)
      assert.match(((await after.json()) as { error: string }).error, /destroyed/)

      const [row] = await h.admin<{ destroyed_at: Date | null; size_bytes: string }[]>`
        SELECT e.destroyed_at, e.size_bytes FROM organization_deletion_exports e
        JOIN organization_deletions d ON d.id = e.deletion_id WHERE d.org_id = ${org.orgId}`
      assert.ok(row!.destroyed_at, 'the row went instead of the document')
      assert.equal(Number(row!.size_bytes), 0)
    })

    it('a download link nobody was given reaches nothing', async () => {
      await request()
      const response = await h.fetch('/exports/deletion?token=not-a-real-token')
      assert.equal(response.status, 404)
    })

    it('the export records that it was downloaded', async () => {
      const { exportUrl } = await request()
      const token = new URL(exportUrl).searchParams.get('token')!
      await h.fetch(`/exports/deletion?token=${encodeURIComponent(token)}`)
      await h.fetch(`/exports/deletion?token=${encodeURIComponent(token)}`)
      const [row] = await h.admin<{ download_count: number }[]>`
        SELECT e.download_count FROM organization_deletion_exports e
        JOIN organization_deletions d ON d.id = e.deletion_id WHERE d.org_id = ${org.orgId}`
      assert.equal(row!.download_count, 2)
    })

    it('the held copy can be destroyed early, before the purge', async () => {
      await subscribe(h.clock.now().getTime() + 10 * 24 * 60 * 60 * 1000)
      const { exportUrl } = await request()
      const token = new URL(exportUrl).searchParams.get('token')!
      const { body } = await callProcedure(h, owner, 'deletion.destroyExport', 'mutation', {})
      assert.equal(data<{ destroyed: boolean }>(body).destroyed, true)
      const after = await h.fetch(`/exports/deletion?token=${encodeURIComponent(token)}`)
      assert.equal(after.status, 404)
    })

    // -----------------------------------------------------------------------
    // What everybody else sees
    // -----------------------------------------------------------------------

    // The banner every role sees comes from org.settings, not from
    // deletion.status. deletion.status moved to organization.delete when the
    // hosted plan gate landed, because environments.view is gated and a lapsed
    // owner has to be able to watch a deletion they cannot cancel. The property
    // this test has always been about, that an organization is not deleted out
    // from under the people using it, is unchanged and is asserted where the
    // console actually reads it.
    it('every role can see that the organization is being deleted', async () => {
      await subscribe(h.clock.now().getTime() + 10 * 24 * 60 * 60 * 1000)
      await request(org.slug, 'moving to another vendor')
      for (const role of ['admin', 'member', 'viewer'] as const) {
        const session = await signInAs(h, org, role)
        const { body } = await callProcedure(h, session, 'org.settings', 'query', {})
        const view = data<{ deletion: DeletionView | null }>(body).deletion
        assert.ok(view, `${role} cannot see the deletion`)
        assert.equal(view.step, 'await_entitlement_end')
        assert.equal(view.reason, 'moving to another vendor')
      }
    })

    it('deletion.status is the owner\'s, because the plan gate reaches the other route', async () => {
      await subscribe(h.clock.now().getTime() + 10 * 24 * 60 * 60 * 1000)
      await request(org.slug, 'moving to another vendor')
      const { body } = await callProcedure(h, owner, 'deletion.status', 'query', {})
      const view = data<{ deletion: DeletionView | null }>(body).deletion
      assert.ok(view)
      assert.equal(view.step, 'await_entitlement_end')
      for (const role of ['admin', 'member', 'viewer'] as const) {
        const session = await signInAs(h, org, role)
        const refused = await callProcedure(h, session, 'deletion.status', 'query', {})
        assert.match(
          JSON.stringify(refused.body),
          /organization\.delete permission/,
          `${role} was not refused deletion.status`,
        )
      }
    })

    it('nothing is reported as deleting when nothing is', async () => {
      const { body } = await callProcedure(h, owner, 'deletion.status', 'query', {})
      assert.equal(data<{ deletion: DeletionView | null }>(body).deletion, null)
    })

    // Nothing to sweep up here any more: every test drops its own organization
    // in afterEach, which is what keeps one test's leftover record out of the
    // next test's sweep.
  },
)
