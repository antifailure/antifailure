// Running the organization: what each role may actually do, and the orderings.
//
// Every test here calls the route the way the browser does, as a real role,
// against a real database. That is the point rather than a detail: a control
// hidden in the console and reachable by anybody who can type a URL is a
// security defect that looks like a feature, and the only way to know the
// difference is to call the route without the console in the way.
//
// The permission matrix already crosses every route with every role. What is
// here is what the matrix cannot say: that the refusal is the RIGHT refusal,
// that the route does what it claims when it is allowed, and that the flows
// that span two events behave in every order those events can arrive in.

import { after, before, beforeEach, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import {
  available,
  callProcedure,
  dropOrg,
  errorCode,
  seedOrg,
  signInAs,
  signInWithNoOrganization,
  startApi,
  type ApiHarness,
  type Org,
} from './harness.ts'

const hasDatabase = await available()

/** The body of a successful tRPC call. */
function data<T>(body: unknown): T {
  const b = body as { result?: { data?: T }; error?: { message?: string } }
  assert.ok(b.result, `expected a result, got: ${JSON.stringify(b.error ?? b).slice(0, 400)}`)
  return b.result.data as T
}

function message(body: unknown): string {
  return (body as { error?: { message?: string } }).error?.message ?? ''
}

describe(
  'running the organization',
  { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' },
  () => {
    let h: ApiHarness

    before(async () => {
      h = await startApi()
    })
    after(async () => {
      await h.close()
    })

    // -----------------------------------------------------------------------
    // The server decides, not the console
    // -----------------------------------------------------------------------

    describe('every control is enforced on the server, as each role', () => {
      let org: Org
      const sessions: Record<string, Awaited<ReturnType<typeof signInAs>>> = {}

      before(async () => {
        org = await seedOrg(h.admin, 'roles')
        for (const role of ['owner', 'admin', 'member', 'viewer'] as const) {
          sessions[role] = await signInAs(h, org, role)
        }
      })
      after(async () => {
        await dropOrg(h.admin, org.orgId)
      })

      /**
       * One table, read as a matrix, so a change to the role model breaks one
       * assertion per cell rather than one assertion for the whole idea.
       *
       * FORBIDDEN is the only refusal that counts as "the role was stopped".
       * A route that answers NOT_FOUND or BAD_REQUEST let the call through and
       * then declined for a reason about the input, which is a pass here: what
       * is being asserted is the gate, not the handler.
       */
      const cells: {
        route: string
        type: 'query' | 'mutation'
        input: unknown
        allowed: readonly string[]
      }[] = [
        {
          route: 'invitations.create',
          type: 'mutation',
          input: { email: 'gate@example.test', role: 'viewer' },
          allowed: ['owner', 'admin'],
        },
        { route: 'invitations.list', type: 'query', input: {}, allowed: ['owner', 'admin'] },
        {
          route: 'members.remove',
          type: 'mutation',
          input: { githubLogin: 'nobody-here' },
          allowed: ['owner', 'admin'],
        },
        {
          route: 'sessions.list',
          type: 'query',
          input: { includeRevoked: false },
          allowed: ['owner', 'admin'],
        },
        {
          route: 'sessions.revokeForPerson',
          type: 'mutation',
          input: { githubLogin: 'nobody-here' },
          allowed: ['owner', 'admin'],
        },
        {
          route: 'org.rename',
          type: 'mutation',
          input: { name: 'roles' },
          allowed: ['owner', 'admin'],
        },
        { route: 'exports.organization', type: 'mutation', input: {}, allowed: ['owner', 'admin'] },
        // Owner alone. Both decide something outside this product: one where a
        // bill goes, one whether the organization continues to exist.
        { route: 'org.billingContact', type: 'query', input: {}, allowed: ['owner'] },
        {
          route: 'org.setBillingContact',
          type: 'mutation',
          input: { email: 'finance@example.test' },
          allowed: ['owner'],
        },
        {
          route: 'deletion.request',
          type: 'mutation',
          input: { confirm: 'deliberately-not-the-slug' },
          allowed: ['owner'],
        },
        { route: 'deletion.cancel', type: 'mutation', input: {}, allowed: ['owner'] },
        // Every role, including viewer, because it is about the holder.
        {
          route: 'account.close',
          type: 'mutation',
          input: { confirm: 'deliberately-not-a-login' },
          allowed: ['owner', 'admin', 'member', 'viewer'],
        },
        // Owner only, and NOT because a member should be kept in the dark. It
        // was written under environments.view so every role saw the banner, and
        // the hosted plan gate took that away: environments.view is gated, so a
        // lapsed customer who had already asked for a deletion could not see
        // whether it was progressing. A member losing the banner is a bounded
        // loss. An owner unable to watch a deletion they can neither cancel nor
        // see is a trap. See HOSTED_GATE_EXEMPT.
        { route: 'deletion.status', type: 'query', input: {}, allowed: ['owner'] },
      ]

      for (const cell of cells) {
        for (const role of ['owner', 'admin', 'member', 'viewer'] as const) {
          const shouldAllow = cell.allowed.includes(role)
          it(`${role} ${shouldAllow ? 'may' : 'may not'} ${cell.route}`, async () => {
            const { body } = await callProcedure(
              h,
              sessions[role]!,
              cell.route,
              cell.type,
              cell.input,
            )
            const code = errorCode(body)
            if (shouldAllow) {
              assert.notEqual(
                code,
                'FORBIDDEN',
                `${role} should hold this and was refused: ${message(body)}`,
              )
            } else {
              assert.equal(
                code,
                'FORBIDDEN',
                `${role} reached ${cell.route} and should not have`,
              )
            }
          })
        }
      }

      it('a signed-out caller reaches none of them', async () => {
        for (const cell of cells) {
          const { body } = await callProcedure(h, null, cell.route, cell.type, cell.input)
          assert.equal(errorCode(body), 'UNAUTHORIZED', `${cell.route} answered a stranger`)
        }
      })
    })

    // -----------------------------------------------------------------------
    // Invitations
    // -----------------------------------------------------------------------

    describe('invitations', () => {
      let org: Org
      let owner: Awaited<ReturnType<typeof signInAs>>

      beforeEach(async () => {
        org = await seedOrg(h.admin, 'invites')
        owner = await signInAs(h, org, 'owner')
      })

      /** Sends one and returns the raw token out of the link. */
      async function invite(
        as: Awaited<ReturnType<typeof signInAs>>,
        email: string,
        role: 'owner' | 'admin' | 'member' | 'viewer' = 'member',
      ): Promise<{ token: string; id: string; link: string; emailed: boolean }> {
        const { body } = await callProcedure(h, as, 'invitations.create', 'mutation', {
          email,
          role,
        })
        const created = data<{ id: string; link: string; emailed: boolean }>(body)
        const token = new URL(created.link).searchParams.get('token')
        assert.ok(token, 'the invitation link carries no token')
        return { token, id: created.id, link: created.link, emailed: created.emailed }
      }

      it('an invited person joins with the role the invitation named', async () => {
        const { token } = await invite(owner, 'joiner@example.test', 'viewer')

        const looked = await h.fetch(`/auth/invitation?token=${encodeURIComponent(token)}`)
        assert.equal(looked.status, 200)
        const view = (await looked.json()) as { organization: string; role: string; state: string }
        assert.equal(view.role, 'viewer')
        assert.equal(view.state, 'open')

        const joiner = await signInWithNoOrganization(h)
        const accepted = await h.fetch('/auth/invitation/accept', {
          method: 'POST',
          headers: {
            'content-type': 'application/json',
            cookie: joiner.cookie,
            'x-antifailure-csrf': joiner.csrfToken,
          },
          body: JSON.stringify({ token }),
        })
        // The body is read once and asserted on, because reading it for the
        // failure message and then again for the value makes a passing run
        // throw "Body has already been read" instead of passing.
        const body = await accepted.text()
        assert.equal(accepted.status, 200, body)
        const result = JSON.parse(body) as { orgId: string; role: string; alreadyMember: boolean }
        assert.equal(result.orgId, org.orgId)
        assert.equal(result.role, 'viewer')
        assert.equal(result.alreadyMember, false)

        const [row] = await h.admin<{ role: string; source: string }[]>`
          SELECT role::text AS role, source FROM members
          WHERE org_id = ${org.orgId} AND user_id = ${joiner.userId}`
        assert.equal(row?.role, 'viewer')
        assert.equal(row?.source, 'invitation')
      })

      /**
       * The ordering that decides whether an invitation is a promise or a
       * favour: it was authorised when it was sent, so it stands afterwards.
       * Nothing in the acceptance path reads the inviter's membership, their
       * role, or their account, and this is what proves it rather than the
       * comment saying so.
       */
      it('is still good after the person who sent it has left', async () => {
        const inviter = await signInAs(h, org, 'admin', 'inviter')
        const [inviterRow] = await h.admin<{ github_login: string }[]>`
          SELECT github_login FROM users WHERE id = ${inviter.userId}`
        const { token } = await invite(inviter, 'orphan@example.test', 'member')

        const removed = await callProcedure(h, owner, 'members.remove', 'mutation', {
          githubLogin: inviterRow!.github_login,
        })
        assert.equal(data<{ removed: boolean }>(removed.body).removed, true)

        const looked = await h.fetch(`/auth/invitation?token=${encodeURIComponent(token)}`)
        const view = (await looked.json()) as { state: string; invitedBy: string }
        assert.equal(view.state, 'open')
        // The label was copied at send time, so the invitation still says who
        // sent it after the account that sent it is gone.
        assert.equal(view.invitedBy, 'inviter')

        const joiner = await signInWithNoOrganization(h)
        const accepted = await h.fetch('/auth/invitation/accept', {
          method: 'POST',
          headers: {
            'content-type': 'application/json',
            cookie: joiner.cookie,
            'x-antifailure-csrf': joiner.csrfToken,
          },
          body: JSON.stringify({ token }),
        })
        assert.equal(accepted.status, 200, await accepted.clone().text())
      })

      it('a second invitation to the same address is refused, and allowed again after a withdrawal', async () => {
        const first = await invite(owner, 'twice@example.test')
        const again = await callProcedure(h, owner, 'invitations.create', 'mutation', {
          email: 'twice@example.test',
          role: 'member',
        })
        assert.equal(errorCode(again.body), 'BAD_REQUEST')
        assert.match(message(again.body), /already has an invitation/)

        await callProcedure(h, owner, 'invitations.revoke', 'mutation', { id: first.id })
        const third = await callProcedure(h, owner, 'invitations.create', 'mutation', {
          email: 'twice@example.test',
          role: 'member',
        })
        assert.equal(errorCode(third.body), null, message(third.body))
      })

      it('the address is compared lower-cased, so one person cannot be invited twice', async () => {
        await invite(owner, 'Mixed.Case@Example.TEST')
        const again = await callProcedure(h, owner, 'invitations.create', 'mutation', {
          email: 'mixed.case@example.test',
          role: 'member',
        })
        assert.equal(errorCode(again.body), 'BAD_REQUEST')
      })

      it('clicking the link twice is success, and a third party is refused', async () => {
        const { token } = await invite(owner, 'double@example.test')
        const joiner = await signInWithNoOrganization(h)
        const accept = (session: { cookie: string; csrfToken: string }) =>
          h.fetch('/auth/invitation/accept', {
            method: 'POST',
            headers: {
              'content-type': 'application/json',
              cookie: session.cookie,
              'x-antifailure-csrf': session.csrfToken,
            },
            body: JSON.stringify({ token }),
          })

        const first = await accept(joiner)
        assert.equal(first.status, 200)
        assert.equal(((await first.json()) as { alreadyMember: boolean }).alreadyMember, false)

        const second = await accept(joiner)
        assert.equal(second.status, 200)
        assert.equal(((await second.json()) as { alreadyMember: boolean }).alreadyMember, true)

        const stranger = await signInWithNoOrganization(h, 'stranger')
        const third = await accept(stranger)
        assert.equal(third.status, 400)
        assert.match(((await third.json()) as { error: string }).error, /already been used/)
      })

      /**
       * The path that was never exercised, and that was broken.
       *
       * A second click on the same link returns earlier than the membership
       * insert, on `accepted_at`, so nothing here reached the insert with a row
       * already present until this test. What did reach it was an open
       * invitation for somebody a GitHub sync had added in the meantime, and
       * the code caught the duplicate key inside the transaction and carried
       * on. postgres.js records the first failed query of a transaction and
       * rethrows it after the callback returns, so the caller got the 23505
       * anyway and the whole transaction rolled back: the person was refused
       * AND the invitation was left open.
       */
      it('is taken up by somebody a sync added in the meantime, without failing', async () => {
        const { token } = await invite(owner, 'raced@example.test', 'admin')
        const joiner = await signInWithNoOrganization(h)
        // The sync, arriving between the invitation and the click.
        await h.admin`
          INSERT INTO members (org_id, user_id, role, source)
          VALUES (${org.orgId}, ${joiner.userId}, 'member', 'github')`

        const accepted = await h.fetch('/auth/invitation/accept', {
          method: 'POST',
          headers: {
            'content-type': 'application/json',
            cookie: joiner.cookie,
            'x-antifailure-csrf': joiner.csrfToken,
          },
          body: JSON.stringify({ token }),
        })
        const body = await accepted.text()
        assert.equal(accepted.status, 200, body)
        const result = JSON.parse(body) as { alreadyMember: boolean; role: string }
        assert.equal(result.alreadyMember, true)

        // The membership that was already there is the one that stands, so the
        // sync's role is not quietly overwritten by the invitation's.
        const [row] = await h.admin<{ role: string; source: string }[]>`
          SELECT role::text AS role, source FROM members
          WHERE org_id = ${org.orgId} AND user_id = ${joiner.userId}`
        assert.equal(row?.source, 'github')
        assert.equal(row?.role, 'member')

        // And the invitation is closed rather than left open, which is what the
        // rolled back transaction used to leave behind.
        const [invitation] = await h.admin<{ accepted_at: Date | null }[]>`
          SELECT accepted_at FROM invitations
          WHERE org_id = ${org.orgId} AND email = 'raced@example.test'`
        assert.ok(invitation?.accepted_at, 'the invitation was left open')
      })

      it('a withdrawn invitation cannot be taken up', async () => {
        const { token, id } = await invite(owner, 'withdrawn@example.test')
        await callProcedure(h, owner, 'invitations.revoke', 'mutation', { id })

        const joiner = await signInWithNoOrganization(h)
        const accepted = await h.fetch('/auth/invitation/accept', {
          method: 'POST',
          headers: {
            'content-type': 'application/json',
            cookie: joiner.cookie,
            'x-antifailure-csrf': joiner.csrfToken,
          },
          body: JSON.stringify({ token }),
        })
        assert.equal(accepted.status, 400)
        assert.match(((await accepted.json()) as { error: string }).error, /withdrawn/)
      })

      it('an expired invitation cannot be taken up', async () => {
        const { token } = await invite(owner, 'stale@example.test')
        // Fifteen days, against the injected clock. Nothing sleeps.
        h.clock.advance(15 * 24 * 60 * 60 * 1000)

        const looked = await h.fetch(`/auth/invitation?token=${encodeURIComponent(token)}`)
        assert.equal(((await looked.json()) as { state: string }).state, 'expired')

        const joiner = await signInWithNoOrganization(h)
        const accepted = await h.fetch('/auth/invitation/accept', {
          method: 'POST',
          headers: {
            'content-type': 'application/json',
            cookie: joiner.cookie,
            'x-antifailure-csrf': joiner.csrfToken,
          },
          body: JSON.stringify({ token }),
        })
        assert.equal(accepted.status, 400)
        assert.match(((await accepted.json()) as { error: string }).error, /expired/)
      })

      it('a token nobody was given reaches nothing, and says the same thing as a used one', async () => {
        const looked = await h.fetch('/auth/invitation?token=not-a-real-token')
        assert.equal(looked.status, 404)
        assert.match(((await looked.json()) as { error: string }).error, /not valid/)
      })

      it('sending it again invalidates the previous link', async () => {
        const first = await invite(owner, 'resent@example.test')
        const { body } = await callProcedure(h, owner, 'invitations.resend', 'mutation', {
          id: first.id,
        })
        const resent = data<{ link: string }>(body)
        const second = new URL(resent.link).searchParams.get('token')!
        assert.notEqual(second, first.token)

        const old = await h.fetch(`/auth/invitation?token=${encodeURIComponent(first.token)}`)
        assert.equal(old.status, 404, 'the previous link still works')
        const fresh = await h.fetch(`/auth/invitation?token=${encodeURIComponent(second)}`)
        assert.equal(fresh.status, 200)
      })

      it('the link comes back whether or not a message could be sent', async () => {
        const created = await invite(owner, 'mailed@example.test')
        // This harness configures a mailer, so the message is sent AND the link
        // is returned. A control plane with no AF_MAIL_FROM returns the link
        // with emailed false, which is what makes the invitation usable there.
        assert.equal(created.emailed, true)
        assert.match(created.link, /invite\?token=/)
        const sent = h.mailer.lastTo("mailed@example.test")
        assert.ok(sent, 'nothing was sent')
        assert.match(sent.text, /invited you to join/)
        assert.ok(sent.text.includes(created.token), 'the message does not carry the link')
      })

      it('somebody already in the organization is not invited again', async () => {
        const [existing] = await h.admin<{ email: string }[]>`
          SELECT email FROM users WHERE id = ${owner.userId}`
        const { body } = await callProcedure(h, owner, 'invitations.create', 'mutation', {
          email: existing!.email,
          role: 'member',
        })
        assert.equal(errorCode(body), 'BAD_REQUEST')
        assert.match(message(body), /already a member/)
      })

      it('something that is not an address is refused before a row is written', async () => {
        const { body } = await callProcedure(h, owner, 'invitations.create', 'mutation', {
          email: 'not an address',
          role: 'member',
        })
        assert.equal(errorCode(body), 'BAD_REQUEST')
        const [count] = await h.admin<{ n: string }[]>`
          SELECT count(*) AS n FROM invitations WHERE org_id = ${org.orgId}`
        assert.equal(Number(count!.n), 0)
      })
    })

    // -----------------------------------------------------------------------
    // Roles and sessions, in the orders they actually happen
    // -----------------------------------------------------------------------

    describe('roles and sessions', () => {
      let org: Org
      let owner: Awaited<ReturnType<typeof signInAs>>

      beforeEach(async () => {
        org = await seedOrg(h.admin, 'sessions')
        owner = await signInAs(h, org, 'owner')
      })

      /**
       * The role is re-read on every request rather than carried in the
       * session, so a demotion takes effect on the demoted person's NEXT
       * request rather than on their next sign-in, which may be never.
       */
      it('a role changed during an active session takes effect on the next request', async () => {
        const admin = await signInAs(h, org, 'admin', 'demoted')
        const [row] = await h.admin<{ github_login: string }[]>`
          SELECT github_login FROM users WHERE id = ${admin.userId}`

        const before = await callProcedure(h, admin, 'invitations.list', 'query', {})
        assert.equal(errorCode(before.body), null, 'an admin could not list invitations')

        await callProcedure(h, owner, 'members.setRole', 'mutation', {
          githubLogin: row!.github_login,
          role: 'viewer',
        })

        const after = await callProcedure(h, admin, 'invitations.list', 'query', {})
        assert.equal(
          errorCode(after.body),
          'FORBIDDEN',
          'the same session kept a permission the role no longer has',
        )
      })

      it('a revoked session stops working on the next request', async () => {
        const member = await signInAs(h, org, 'member', 'revoked')
        const before = await callProcedure(h, member, 'environments.list', 'query', { limit: 5 })
        assert.equal(errorCode(before.body), null)

        const listed = await callProcedure(h, owner, 'sessions.list', 'query', {
          includeRevoked: false,
        })
        const rows = data<{ id: string; person: string; isYou: boolean }[]>(listed.body)
        const theirs = rows.find((r) => !r.isYou && r.person.startsWith('revoked'))
        assert.ok(theirs, 'the session list did not show the other person')

        await callProcedure(h, owner, 'sessions.revoke', 'mutation', { id: theirs.id })

        const after = await callProcedure(h, member, 'environments.list', 'query', { limit: 5 })
        assert.equal(errorCode(after.body), 'UNAUTHORIZED')
      })

      it('the session list marks the reader’s own session and never carries a token', async () => {
        const { body } = await callProcedure(h, owner, 'sessions.list', 'query', {
          includeRevoked: false,
        })
        const rows = data<Record<string, unknown>[]>(body)
        assert.equal(rows.filter((r) => r.isYou === true).length, 1)
        for (const row of rows) {
          assert.ok(!('tokenHash' in row), 'the session list carries a token hash')
          assert.ok(!('token' in row), 'the session list carries a token')
        }
      })

      it('removing somebody signs them out in the same transaction', async () => {
        const member = await signInAs(h, org, 'member', 'departing')
        const [row] = await h.admin<{ github_login: string }[]>`
          SELECT github_login FROM users WHERE id = ${member.userId}`

        const removed = await callProcedure(h, owner, 'members.remove', 'mutation', {
          githubLogin: row!.github_login,
        })
        assert.equal(data<{ sessionsRevoked: number }>(removed.body).sessionsRevoked, 1)

        const after = await callProcedure(h, member, 'environments.list', 'query', { limit: 5 })
        assert.equal(errorCode(after.body), 'UNAUTHORIZED')
      })

      it('an administrator who removes themself gets a signed out session answer', async () => {
        const admin = await signInAs(h, org, 'admin', 'selfremove')
        const [row] = await h.admin<{ github_login: string }[]>`
          SELECT github_login FROM users WHERE id = ${admin.userId}`

        const removed = await callProcedure(h, admin, 'members.remove', 'mutation', {
          githubLogin: row!.github_login,
        })
        const refreshed = await h.fetch('/auth/session', { headers: { cookie: admin.cookie } })
        const session = (await refreshed.json()) as { signedIn: boolean }
        assert.deepEqual(
          [data<{ removed: boolean }>(removed.body).removed, session.signedIn],
          [true, false],
        )
      })

      it('the last owner cannot be removed or demoted', async () => {
        const [row] = await h.admin<{ github_login: string }[]>`
          SELECT github_login FROM users WHERE id = ${owner.userId}`
        const demoted = await callProcedure(h, owner, 'members.setRole', 'mutation', {
          githubLogin: row!.github_login,
          role: 'admin',
        })
        assert.equal(errorCode(demoted.body), 'BAD_REQUEST')
        const removed = await callProcedure(h, owner, 'members.remove', 'mutation', {
          githubLogin: row!.github_login,
        })
        assert.equal(errorCode(removed.body), 'BAD_REQUEST')
        assert.match(message(removed.body), /only owner/)
      })

      /**
       * Two administrators editing one member at the same instant.
       *
       * What is asserted is not which of them wins, because either is correct.
       * It is that the row ends up holding exactly one of the two values rather
       * than a mixture, and that neither call reports a failure the caller
       * would have to interpret.
       */
      it('two concurrent role edits on one member leave one of the two roles', async () => {
        const admin = await signInAs(h, org, 'admin', 'editor')
        const target = await signInAs(h, org, 'member', 'edited')
        const [row] = await h.admin<{ github_login: string }[]>`
          SELECT github_login FROM users WHERE id = ${target.userId}`

        const [a, b] = await Promise.all([
          callProcedure(h, owner, 'members.setRole', 'mutation', {
            githubLogin: row!.github_login,
            role: 'viewer',
          }),
          callProcedure(h, admin, 'members.setRole', 'mutation', {
            githubLogin: row!.github_login,
            role: 'admin',
          }),
        ])
        assert.equal(errorCode(a.body), null, message(a.body))
        assert.equal(errorCode(b.body), null, message(b.body))

        const [after] = await h.admin<{ role: string }[]>`
          SELECT role::text AS role FROM members
          WHERE org_id = ${org.orgId} AND user_id = ${target.userId}`
        assert.ok(
          after!.role === 'viewer' || after!.role === 'admin',
          `the member ended up as ${after!.role}, which is neither of the two writes`,
        )
      })

      it('signing one person out everywhere reports how many sessions went', async () => {
        const member = await signInAs(h, org, 'member', 'multi')
        await signInAs(h, org, 'member', 'unrelated')
        const [row] = await h.admin<{ github_login: string }[]>`
          SELECT github_login FROM users WHERE id = ${member.userId}`

        const { body } = await callProcedure(h, owner, 'sessions.revokeForPerson', 'mutation', {
          githubLogin: row!.github_login,
        })
        assert.equal(data<{ revoked: number }>(body).revoked, 1)

        // Zero is a real answer rather than a failure: somebody with no live
        // session is exactly who an administrator wants after pressing it.
        const again = await callProcedure(h, owner, 'sessions.revokeForPerson', 'mutation', {
          githubLogin: row!.github_login,
        })
        assert.equal(data<{ revoked: number }>(again.body).revoked, 0)
      })
    })

    // -----------------------------------------------------------------------
    // Settings and the billing contact
    // -----------------------------------------------------------------------

    describe('settings', () => {
      let org: Org
      let owner: Awaited<ReturnType<typeof signInAs>>

      beforeEach(async () => {
        org = await seedOrg(h.admin, 'settings')
        owner = await signInAs(h, org, 'owner')
      })

      it('renaming changes the name and deliberately leaves the slug alone', async () => {
        const { body } = await callProcedure(h, owner, 'org.rename', 'mutation', {
          name: 'A Different Name',
        })
        assert.equal(data<{ name: string }>(body).name, 'A Different Name')

        const settings = await callProcedure(h, owner, 'org.settings', 'query', {})
        const view = data<{ name: string; slug: string }>(settings.body)
        assert.equal(view.name, 'A Different Name')
        assert.equal(view.slug, org.slug, 'the slug moved, which breaks every link somebody has sent')
      })

      it('the billing contact is saved locally even when there is no Stripe customer', async () => {
        const { body } = await callProcedure(h, owner, 'org.setBillingContact', 'mutation', {
          email: 'Finance@Example.TEST',
          name: 'Accounts Payable',
        })
        const saved = data<{ email: string; pushedToStripe: boolean; note: string | null }>(body)
        assert.equal(saved.email, 'finance@example.test', 'the address was not lower-cased')
        assert.equal(saved.pushedToStripe, false)
        assert.match(saved.note ?? '', /no Stripe customer yet/)

        const read = await callProcedure(h, owner, 'org.billingContact', 'query', {})
        const view = data<{ contact: { email: string; name: string | null } | null }>(read.body)
        assert.equal(view.contact?.email, 'finance@example.test')
        assert.equal(view.contact?.name, 'Accounts Payable')
      })

      it('something that is not an address is refused', async () => {
        const { body } = await callProcedure(h, owner, 'org.setBillingContact', 'mutation', {
          email: 'finance at example dot test',
        })
        assert.equal(errorCode(body), 'BAD_REQUEST')
      })

      it('the settings a viewer can see do not include the billing contact', async () => {
        await callProcedure(h, owner, 'org.setBillingContact', 'mutation', {
          email: 'private@example.test',
        })
        const viewer = await signInAs(h, org, 'viewer')
        const { body } = await callProcedure(h, viewer, 'org.settings', 'query', {})
        const view = data<Record<string, unknown>>(body)
        assert.ok(!JSON.stringify(view).includes('private@example.test'))
      })
    })

    // -----------------------------------------------------------------------
    // The export
    // -----------------------------------------------------------------------

    describe('the export', () => {
      let org: Org
      let owner: Awaited<ReturnType<typeof signInAs>>

      before(async () => {
        org = await seedOrg(h.admin, 'export')
        owner = await signInAs(h, org, 'owner')
        await h.admin`
          INSERT INTO masking_rules (org_id, repository_id, table_name, column_name, transform, reason)
          VALUES (${org.orgId}, ${org.repoId}, 'users', 'email', 'email',
                  ${'the address a real person reads'})`
        await h.admin`
          INSERT INTO network_rules (org_id, repository_id, host, mode, note, paths, methods,
                                     rate_limit, credential, fixtures, webhook_path, approved_at)
          VALUES (${org.orgId}, ${org.repoId}, 'api.stripe.com', 'mock', 'payments',
                  ARRAY['/v1/charges'], ARRAY['POST'], '10/s', 'STRIPE_SANDBOX_KEY',
                  'fixtures/stripe.json', '/webhooks/stripe', now())`
        await h.admin`
          INSERT INTO engine_tokens (org_id, name, token_hash, prefix)
          VALUES (${org.orgId}, 'ci', ${Buffer.from('a-secret-hash')}, 'afe_ci')`
      })
      after(async () => {
        await dropOrg(h.admin, org.orgId)
      })

      it('names everything the way a person does, and carries no internal identifier', async () => {
        const { body } = await callProcedure(h, owner, 'exports.organization', 'mutation', {})
        const doc = data<Record<string, unknown>>(body)
        const text = JSON.stringify(doc)

        assert.ok(text.includes(org.repository), 'the repository is not named by its full name')
        assert.ok(text.includes(org.envId), 'the environment is not named by its env id')
        // The identifiers a dump would be full of. Their absence is the claim
        // the README makes, so it is asserted rather than described.
        assert.ok(!text.includes(org.orgId), 'the export carries the organization uuid')
        assert.ok(!text.includes(org.repoId), 'the export carries the repository uuid')
        assert.doesNotMatch(
          text,
          /"(id|orgId|org_id|repositoryId|repository_id|runId|environmentId)":/,
          'the export carries an internal identifier field',
        )
      })

      it('carries no secret material', async () => {
        const { body } = await callProcedure(h, owner, 'exports.organization', 'mutation', {})
        const text = JSON.stringify(data<Record<string, unknown>>(body))
        assert.ok(!text.includes('a-secret-hash'), 'the export carries an engine token hash')
        assert.ok(!text.includes('tokenHash'), 'the export carries a token hash field')
        // The bytes rather than the word: the document says, under
        // notIncluded, that key material is held as ciphertext and is not
        // here, so asserting on the word would fail against its own honesty.
        assert.ok(!text.includes('cipher'.concat('text":')), 'the export carries a ciphertext field')
        assert.ok(!text.includes('nonce'), 'the export carries a key nonce')
        assert.ok(text.includes('afe_ci'), 'the export does not name the token that exists')
      })

      it('produces a masking file the engine reads as it is', async () => {
        const { body } = await callProcedure(h, owner, 'exports.organization', 'mutation', {})
        const doc = data<{ files: Record<string, string> }>(body)
        const path = `repositories/${org.repository}/masking.yaml`
        const file = doc.files[path]
        assert.ok(file, `no ${path} in the export: ${Object.keys(doc.files).join(', ')}`)
        assert.match(file, /^rules:$/m)
        assert.match(file, /^ {2}- table: users$/m)
        assert.match(file, /^ {4}column: email$/m)
        assert.match(file, /^ {4}transform: email$/m)
        assert.match(file, /^ {4}why: "the address a real person reads"$/m)
      })

      it('produces an egress block in the shape antifailure.yaml takes', async () => {
        const { body } = await callProcedure(h, owner, 'exports.organization', 'mutation', {})
        const doc = data<{ files: Record<string, string> }>(body)
        const file = doc.files[`repositories/${org.repository}/egress.yaml`]
        assert.ok(file)
        assert.match(file, /^egress:$/m)
        assert.match(file, /^ {2}default: block$/m)
        assert.match(file, /^ {4}- host: api\.stripe\.com$/m)
        assert.match(file, /^ {6}mode: mock$/m)
        // Every key the manifest schema defines for an egress rule. A file
        // missing one is a policy that is quietly different from the one that
        // was enforced, and it still looks complete.
        // Quoted, because a value starting with a slash is not a plain YAML
        // scalar. The writer is right and this assertion was wrong: the same
        // file was parsed with the engine's own schema.Egress type with every
        // field arriving intact.
        assert.match(file, /^ {6}paths: \["\/v1\/charges"\]$/m)
        assert.match(file, /^ {6}methods: \[POST\]$/m)
        assert.match(file, /^ {6}rate_limit: "10\/s"$/m)
        // The NAME of the variable holding the credential, which is what the
        // schema defines and what the engine reads. No secret reaches this
        // control plane, so there is none here to leave out.
        assert.match(file, /^ {6}credential: STRIPE_SANDBOX_KEY$/m)
        assert.match(file, /^ {6}fixtures: fixtures\/stripe\.json$/m)
        assert.match(file, /^ {6}webhook_path: "\/webhooks\/stripe"$/m)
        assert.match(file, /^ {6}note: "payments"$/m)
      })

      /**
       * A note that fights the quoting.
       *
       * The writer here is thirty lines of string building rather than a YAML
       * library, which is the right trade for a value set this small and closed
       * and the wrong one if the escaping is guessed at. A comma would end a
       * flow sequence, a double quote would end the scalar, and a backslash
       * would eat the character after it.
       *
       * Both exported files were also parsed with the engine's own types,
       * `masking.Rule` through `masking.NewRuleSet` and `schema.Egress`, with
       * every field arriving intact. That is not repeated here because it needs
       * the Go toolchain; what is asserted here is the byte the parser then has
       * to read.
       */
      it('quotes a note that would otherwise break the file', async () => {
        const org2 = await seedOrg(h.admin, 'quoting')
        const owner2 = await signInAs(h, org2, 'owner')
        await h.admin`
          INSERT INTO network_rules (org_id, repository_id, host, mode, note, approved_at)
          VALUES (${org2.orgId}, ${org2.repoId}, 'api.example.test', 'capture',
                  ${'payments, a "quoted" word and a back\\slash'}, now())`
        try {
          const { body } = await callProcedure(h, owner2, 'exports.organization', 'mutation', {})
          const doc = data<{ files: Record<string, string> }>(body)
          const file = doc.files[`repositories/${org2.repository}/egress.yaml`]!
          assert.match(
            file,
            /^ {6}note: "payments, a \\"quoted\\" word and a back\\\\slash"$/m,
          )
        } finally {
          await dropOrg(h.admin, org2.orgId)
        }
      })

      it('says what it left out, and records that it was taken', async () => {
        const { body } = await callProcedure(h, owner, 'exports.organization', 'mutation', {})
        const doc = data<{ notIncluded: { what: string; why: string }[] }>(body)
        assert.ok(doc.notIncluded.length > 0)
        for (const entry of doc.notIncluded) {
          assert.ok(entry.what.length > 0 && entry.why.length > 0)
        }

        const [entry] = await h.admin<{ action: string }[]>`
          SELECT action FROM audit_entries WHERE org_id = ${org.orgId}
            AND action = 'organization.exported' ORDER BY seq DESC LIMIT 1`
        assert.equal(entry?.action, 'organization.exported')
      })
    })

    // -----------------------------------------------------------------------
    // Closing an account
    // -----------------------------------------------------------------------

    describe('closing your own account', () => {
      let org: Org

      beforeEach(async () => {
        org = await seedOrg(h.admin, 'closing')
      })

      it('the only owner is refused, and told what to do instead', async () => {
        const owner = await signInAs(h, org, 'owner')
        // The label the console shows, which is the name when there is one.
        // Confirming with the GitHub login instead is what a person who cannot
        // see their own login would never type.
        const [row] = await h.admin<{ name: string | null; github_login: string }[]>`
          SELECT name, github_login FROM users WHERE id = ${owner.userId}`
        const { body } = await callProcedure(h, owner, 'account.close', 'mutation', {
          confirm: row!.name ?? row!.github_login,
        })
        assert.equal(errorCode(body), 'BAD_REQUEST')
        assert.match(message(body), /only owner/)
      })

      it('a wrong confirmation writes nothing', async () => {
        await signInAs(h, org, 'owner')
        const member = await signInAs(h, org, 'member')
        const { body } = await callProcedure(h, member, 'account.close', 'mutation', {
          confirm: 'not-my-login',
        })
        assert.equal(errorCode(body), 'BAD_REQUEST')
        const [count] = await h.admin<{ n: string }[]>`
          SELECT count(*) AS n FROM members WHERE user_id = ${member.userId}`
        assert.equal(Number(count!.n), 1)
      })

      it('erases the personal data, removes the membership, and leaves the audit log intact', async () => {
        await signInAs(h, org, 'owner')
        const member = await signInAs(h, org, 'member', 'leaving')
        const [row] = await h.admin<{ name: string | null; github_login: string }[]>`
          SELECT name, github_login FROM users WHERE id = ${member.userId}`

        // Something they did, so there is an audit entry pointing at the row.
        await callProcedure(h, member, 'environments.teardown', 'mutation', { envId: org.envId })

        const { body } = await callProcedure(h, member, 'account.close', 'mutation', {
          confirm: row!.name ?? row!.github_login,
        })
        const closed = data<{ closed: boolean; sessionsRevoked: number; kept: string[] }>(body)
        assert.equal(closed.closed, true)
        assert.equal(closed.sessionsRevoked, 1)
        assert.ok(closed.kept.some((k) => /audit log/.test(k)))

        const [user] = await h.admin<{
          github_id: string | null
          github_login: string | null
          name: string | null
          email: string
          closed_at: Date | null
        }[]>`
          SELECT github_id, github_login, name, email, closed_at FROM users
          WHERE id = ${member.userId}`
        assert.equal(user!.github_id, null)
        assert.equal(user!.github_login, null)
        assert.equal(user!.name, null)
        assert.match(user!.email, /^closed-account\+/)
        assert.ok(user!.closed_at)

        const [countmemberships] = await h.admin<{ n: string }[]>`
          SELECT count(*) AS n FROM members WHERE user_id = ${member.userId}`
        assert.equal(Number(countmemberships!.n), 0)

        // The row is still there, because audit_entries points at it with NO
        // ACTION and an audit log whose subject can erase themselves from it is
        // not an audit log.
        const [countentries] = await h.admin<{ n: string }[]>`
          SELECT count(*) AS n FROM audit_entries
          WHERE org_id = ${org.orgId} AND actor_user_id = ${member.userId}`
        assert.ok(Number(countentries!.n) > 0, 'the audit entries went with the account')

        const after = await callProcedure(h, member, 'environments.list', 'query', { limit: 5 })
        assert.equal(errorCode(after.body), 'UNAUTHORIZED')
      })
    })
  },
)
