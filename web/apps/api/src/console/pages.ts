// The console's pages.
//
// Every page reads through the same tenant transaction the API uses, so a page
// cannot see anything a procedure could not. There is no second query path with
// its own idea of who is asking, which is the mistake that turns row-level
// security into decoration.

import { sql } from 'drizzle-orm'
import type { Pool } from '@antifailure/db'
import { bare, bytes, chip, empty, html, page, raw, when, type Html, type Viewer } from './layout.ts'

// ---------------------------------------------------------------------------
// Signing in
// ---------------------------------------------------------------------------

export function signInPage(reason?: string): Html {
  return bare(
    { title: 'Sign in', description: 'Sign in to the Antifailure control plane.' },
    html`
      <div class="card">
        <div class="panel">
          <div class="panel-body">
            <h1>Sign in</h1>
            <p style="color:var(--ink-2);margin-top:8px">
              The control plane uses your GitHub account. You will see the organizations
              that have the Antifailure app installed, and nothing else.
            </p>
            ${reason ? html`<div class="notice bad"><span><strong>That did not work</strong>${reason}</span></div>` : ''}
            <a class="btn btn-primary btn-lg" href="/auth/github" style="width:100%;margin-top:18px">
              Continue with GitHub
            </a>
            <p class="hint" style="margin-top:16px">
              Sign-ups are closed while this is in development. If your account is not on
              the allowlist you will be turned away, and nothing about you is recorded.
            </p>
          </div>
        </div>
      </div>
    `,
  )
}

/** Signed in, and a member of nothing. A real state, and a common one. */
export function noOrganizationPage(viewer: Viewer): Html {
  return bare(
    { title: 'No organization', viewer },
    html`
      <div class="card">
        <div class="panel">
          <div class="panel-body">
            <h1>You are signed in</h1>
            <p style="color:var(--ink-2);margin-top:8px">
              Your account is not a member of an organization yet, so there is nothing to
              show you. This is not an error.
            </p>
            <p style="color:var(--ink-2)">
              Membership follows GitHub: you become a member of an organization when
              somebody installs the Antifailure app for it. Ask an owner to install it,
              then sign in again.
            </p>
            <form method="post" action="/console/signout" style="margin-top:18px">
              <input type="hidden" name="csrf" value="${viewer.csrfToken}" />
              <button class="btn" type="submit">Sign out</button>
            </form>
          </div>
        </div>
      </div>
    `,
  )
}

// ---------------------------------------------------------------------------
// Approving a terminal
// ---------------------------------------------------------------------------

export function devicePage(input: {
  viewer: Viewer
  code: string
  pending: { clientLabel: string; scopes: string[]; expiresAt: Date } | null
  error?: string
  approved?: boolean
  denied?: boolean
}): Html {
  const { viewer, pending } = input

  if (input.approved) {
    return bare(
      { title: 'Terminal approved', viewer },
      html`
        <div class="card">
          <div class="panel">
            <div class="panel-body" style="text-align:center">
              <div class="empty-mark" style="margin:0 auto 16px">
                ${raw(
                  '<svg width="20" height="20" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 10.5l4 4 8-9"/></svg>',
                )}
              </div>
              <h1>Approved</h1>
              <p style="color:var(--ink-2);margin:10px auto 0;max-width:36ch">
                The terminal has its token. You can close this tab and go back to it.
              </p>
            </div>
          </div>
        </div>
      `,
    )
  }

  if (input.denied) {
    return bare(
      { title: 'Terminal declined', viewer },
      html`
        <div class="card">
          <div class="panel">
            <div class="panel-body" style="text-align:center">
              <h1>Declined</h1>
              <p style="color:var(--ink-2);margin:10px auto 0;max-width:38ch">
                Nothing was granted. The terminal has been told to stop waiting.
              </p>
            </div>
          </div>
        </div>
      `,
    )
  }

  return bare(
    { title: 'Approve a terminal', viewer, description: 'Approve a device login for the Antifailure CLI.' },
    html`
      <div class="card">
        <div class="panel">
          <div class="panel-body">
            <h1>Approve a terminal</h1>
            <p style="color:var(--ink-2);margin-top:8px">
              A terminal is asking to sign in as you. Check the code below matches the one
              it printed.
            </p>

            ${input.error ? html`<div class="notice bad" style="margin-top:16px"><span><strong>That code did not work</strong>${input.error}</span></div>` : ''}

            ${
              pending
                ? html`
                    <div class="code-display">${input.code}</div>

                    <dl class="kv">
                      <dt>Terminal</dt>
                      <dd>${pending.clientLabel}</dd>
                      <dt>Organization</dt>
                      <dd>${viewer.organization ?? 'none'}</dd>
                      <dt>It will be able to</dt>
                      <dd>${pending.scopes.length ? pending.scopes.join(', ') : 'nothing: it asked only for scopes that do not exist'}</dd>
                      <dt>Expires</dt>
                      <dd>${when(pending.expiresAt)}</dd>
                    </dl>

                    <div class="notice warn" style="margin-top:18px">
                      <span>
                        <strong>Only approve a code you are reading off your own screen</strong>
                        If somebody sent you this code, they are asking for a token that acts as you.
                      </span>
                    </div>

                    <form method="post" action="/console/device" class="approve-row">
                      <input type="hidden" name="csrf" value="${viewer.csrfToken}" />
                      <input type="hidden" name="user_code" value="${input.code}" />
                      <button class="btn" type="submit" name="decision" value="deny">Decline</button>
                      <button class="btn btn-primary" type="submit" name="decision" value="approve">
                        Approve
                      </button>
                    </form>
                  `
                : html`
                    <form method="post" action="/console/device" style="margin-top:18px">
                      <input type="hidden" name="csrf" value="${viewer.csrfToken}" />
                      <input type="hidden" name="decision" value="lookup" />
                      <div class="field">
                        <label for="user_code">The code your terminal printed</label>
                        <input
                          class="code-input"
                          id="user_code"
                          name="user_code"
                          type="text"
                          value="${input.code}"
                          placeholder="XXXX-XXXX"
                          autocomplete="off"
                          autocapitalize="characters"
                          spellcheck="false"
                          maxlength="9"
                          required
                        />
                        <p class="hint">
                          Eight characters. It contains no O, 0, I, L or 1, so anything that
                          looks like one is something else.
                        </p>
                      </div>
                      <button class="btn btn-primary btn-lg" type="submit" style="width:100%">Continue</button>
                    </form>
                  `
            }
          </div>
        </div>
      </div>
    `,
  )
}

// ---------------------------------------------------------------------------
// Environments
// ---------------------------------------------------------------------------

interface EnvRow extends Record<string, unknown> {
  env_id: string
  repository: string
  branch: string | null
  pull_request: number | null
  state: string
  preview_url: string | null
  runtime: string | null
  created_at: Date | string
  updated_at: Date | string
}

export async function environmentsPage(pool: Pool, viewer: Viewer, orgId: string): Promise<Html> {
  const rows = await pool.withTenant({ orgId }, async (db) =>
    db.execute<EnvRow>(sql`
      SELECT e.env_id, r.full_name AS repository, e.branch, e.pull_request, e.state::text AS state,
             e.preview_url, e.runtime, e.created_at, e.updated_at
      FROM environments e JOIN repositories r ON r.id = e.repository_id
      ORDER BY e.created_at DESC LIMIT 100`),
  )

  const live = rows.filter((r) => r.state !== 'torn_down').length

  return page(
    { title: 'Environments', current: 'environments', viewer, environmentLabel: 'staging' },
    html`
      <div class="page">
        <div class="page-head">
          <div class="eyebrow">Delivery</div>
          <h1>Environments</h1>
          <p>Every environment this organization has created, newest first.</p>
        </div>

        <div class="panel">
          <div class="panel-head">
            <h2>${rows.length} recorded, ${live} not torn down</h2>
          </div>
          ${
            rows.length === 0
              ? empty(
                  'No environments yet',
                  'An environment appears here the first time af up runs against a repository connected to this organization. Nothing is wrong.',
                  html`<a class="btn" href="https://antifailure.dev/docs/getting-started/quickstart">Read the quickstart</a>`,
                )
              : html`
                  <div class="scroll-x">
                    <table>
                      <thead>
                        <tr>
                          <th scope="col">Environment</th>
                          <th scope="col">Repository</th>
                          <th scope="col">Branch</th>
                          <th scope="col">State</th>
                          <th scope="col">Created</th>
                        </tr>
                      </thead>
                      <tbody>
                        ${rows.map(
                          (r) => html`
                            <tr>
                              <td class="mono"><a href="/environments/${encodeURIComponent(r.env_id)}">${r.env_id}</a></td>
                              <td>${r.repository}</td>
                              <td>
                                ${r.branch ?? '—'}
                                ${r.pull_request ? html`<span class="chip neutral">#${r.pull_request}</span>` : ''}
                              </td>
                              <td>${chip(r.state)}</td>
                              <td class="when">${when(r.created_at)}</td>
                            </tr>
                          `,
                        )}
                      </tbody>
                    </table>
                  </div>
                `
          }
        </div>
      </div>
    `,
  )
}

export async function environmentPage(
  pool: Pool,
  viewer: Viewer,
  orgId: string,
  envId: string,
): Promise<Html | null> {
  return pool.withTenant({ orgId }, async (db) => {
    const rows = await db.execute<EnvRow & { golden_version: string | null }>(sql`
      SELECT e.env_id, r.full_name AS repository, e.branch, e.pull_request, e.state::text AS state,
             e.preview_url, e.runtime, e.golden_version, e.created_at, e.updated_at
      FROM environments e JOIN repositories r ON r.id = e.repository_id
      WHERE e.env_id = ${envId}`)
    const env = rows[0]
    if (!env) return null

    const events = await db.execute<{ type: string; sequence: string; occurred_at: Date | string }>(sql`
      SELECT type, sequence, occurred_at FROM events
      WHERE env_id = ${envId} ORDER BY sequence DESC LIMIT 40`)

    return page(
      { title: env.env_id, current: 'environments', viewer, environmentLabel: 'staging' },
      html`
        <div class="page">
          <div class="page-head">
            <div class="eyebrow"><a href="/environments">Environments</a></div>
            <h1 class="mono">${env.env_id}</h1>
          </div>

          <div class="panel">
            <div class="panel-body">
              <dl class="kv">
                <dt>Repository</dt><dd>${env.repository}</dd>
                <dt>Branch</dt><dd>${env.branch ?? '—'}</dd>
                <dt>State</dt><dd>${chip(env.state)}</dd>
                <dt>Runtime</dt><dd>${env.runtime ?? '—'}</dd>
                <dt>Golden</dt><dd class="mono">${env.golden_version ?? '—'}</dd>
                <dt>Preview</dt>
                <dd>${env.preview_url ? html`<a href="${env.preview_url}">${env.preview_url}</a>` : '—'}</dd>
                <dt>Created</dt><dd>${when(env.created_at)}</dd>
                <dt>Updated</dt><dd>${when(env.updated_at)}</dd>
              </dl>
            </div>
          </div>

          <div class="panel">
            <div class="panel-head"><h2>Events</h2></div>
            ${
              events.length === 0
                ? empty('No events', 'The engine sends events as it works. None have arrived for this environment.')
                : html`
                    <div class="scroll-x">
                      <table>
                        <thead>
                          <tr>
                            <th scope="col" class="num">Seq</th>
                            <th scope="col">Type</th>
                            <th scope="col">Occurred</th>
                          </tr>
                        </thead>
                        <tbody>
                          ${events.map(
                            (e) => html`
                              <tr>
                                <td class="num">${e.sequence}</td>
                                <td class="mono">${e.type}</td>
                                <td class="when">${when(e.occurred_at)}</td>
                              </tr>
                            `,
                          )}
                        </tbody>
                      </table>
                    </div>
                  `
            }
          </div>
        </div>
      `,
    )
  })
}

// ---------------------------------------------------------------------------
// Runs and their artifacts
// ---------------------------------------------------------------------------

export async function runsPage(pool: Pool, viewer: Viewer, orgId: string): Promise<Html> {
  const rows = await pool.withTenant({ orgId }, async (db) =>
    db.execute<{
      id: string
      env_id: string | null
      state: string
      started_at: Date | string | null
      finished_at: Date | string | null
      verdicts: string
      artifacts: string
    }>(sql`
      SELECT ru.id, e.env_id, ru.state::text AS state, ru.started_at, ru.finished_at,
             (SELECT count(*) FROM verdicts v WHERE v.run_id = ru.id) AS verdicts,
             (SELECT count(*) FROM artifacts a WHERE a.run_id = ru.id) AS artifacts
      FROM runs ru LEFT JOIN environments e ON e.id = ru.environment_id
      ORDER BY ru.started_at DESC NULLS LAST LIMIT 100`),
  )

  return page(
    { title: 'Runs', current: 'runs', viewer, environmentLabel: 'staging' },
    html`
      <div class="page">
        <div class="page-head">
          <div class="eyebrow">Delivery</div>
          <h1>Runs</h1>
          <p>What the agents did, and what they left behind.</p>
        </div>
        <div class="panel">
          <div class="panel-head"><h2>${rows.length} runs</h2></div>
          ${
            rows.length === 0
              ? empty(
                  'No runs yet',
                  'A run appears when af test drives your workflows. Each one carries verdicts and the artifacts that back them: a video, a trace, and steps to reproduce a failure.',
                )
              : html`
                  <div class="scroll-x">
                    <table>
                      <thead>
                        <tr>
                          <th scope="col">Run</th>
                          <th scope="col">Environment</th>
                          <th scope="col">State</th>
                          <th scope="col" class="num">Verdicts</th>
                          <th scope="col" class="num">Artifacts</th>
                          <th scope="col">Started</th>
                        </tr>
                      </thead>
                      <tbody>
                        ${rows.map(
                          (r) => html`
                            <tr>
                              <td class="mono"><a href="/runs/${r.id}">${r.id.slice(0, 8)}</a></td>
                              <td class="mono">${r.env_id ?? '—'}</td>
                              <td>${chip(r.state)}</td>
                              <td class="num">${r.verdicts}</td>
                              <td class="num">${r.artifacts}</td>
                              <td class="when">${when(r.started_at)}</td>
                            </tr>
                          `,
                        )}
                      </tbody>
                    </table>
                  </div>
                `
          }
        </div>
      </div>
    `,
  )
}

export async function runPage(pool: Pool, viewer: Viewer, orgId: string, runId: string): Promise<Html | null> {
  return pool.withTenant({ orgId }, async (db) => {
    const runs = await db.execute<{
      id: string
      state: string
      env_id: string | null
      started_at: Date | string | null
      finished_at: Date | string | null
    }>(sql`
      SELECT ru.id, ru.state::text AS state, e.env_id, ru.started_at, ru.finished_at
      FROM runs ru LEFT JOIN environments e ON e.id = ru.environment_id
      WHERE ru.id = ${runId}::uuid`)
    const run = runs[0]
    if (!run) return null

    // The columns verdicts actually has. It has `value` and `summary`; this
    // query asked for `outcome` and `detail`, which meant this page answered
    // 500 for any run that existed. It answered correctly for a run that did
    // NOT exist, because the query above returns nothing first and the handler
    // returns not-found before reaching here, which is how it passed a smoke
    // test on an empty console for as long as the console was empty.
    //
    // persona, steps and duration come with it, because a verdict without them
    // is a row that says "fail" and cannot say what failed or how far it got.
    const verdicts = await db.execute<{
      workflow: string
      persona: string | null
      value: string
      summary: string | null
      steps: number
      duration_ms: number | null
    }>(sql`
      SELECT workflow, persona, value::text AS value, summary, steps, duration_ms
      FROM verdicts WHERE run_id = ${runId}::uuid ORDER BY workflow`)

    // Likewise: artifacts holds storage_key and size_bytes, not path and bytes.
    //
    // `retained` is read because a row whose bytes retention removed still
    // exists on purpose, so the timeline can say "not retained" rather than
    // rendering a gap that looks like a bug. Dropping it from the query would
    // have made this page do the exact thing the schema comment says not to.
    const artifacts = await db.execute<{
      id: string
      kind: string
      step: number | null
      storage_key: string
      content_type: string | null
      size_bytes: string | null
      retained: boolean
      created_at: Date | string
    }>(sql`
      SELECT id, kind, step, storage_key, content_type, size_bytes, retained, created_at
      FROM artifacts WHERE run_id = ${runId}::uuid ORDER BY step NULLS LAST, created_at`)

    return page(
      { title: `Run ${run.id.slice(0, 8)}`, current: 'runs', viewer, environmentLabel: 'staging' },
      html`
        <div class="page">
          <div class="page-head">
            <div class="eyebrow"><a href="/runs">Runs</a></div>
            <h1 class="mono">${run.id.slice(0, 8)}</h1>
            <p>${chip(run.state)} in <span class="mono">${run.env_id ?? 'an environment that is gone'}</span></p>
          </div>

          <div class="panel">
            <div class="panel-head"><h2>Verdicts</h2></div>
            ${
              verdicts.length === 0
                ? empty('No verdicts', 'This run recorded no verdicts. A run that finished with none usually means it was blocked before any workflow started.')
                : html`
                    <div class="scroll-x">
                      <table>
                        <thead>
                          <tr>
                            <th scope="col">Workflow</th><th scope="col">Persona</th>
                            <th scope="col">Outcome</th>
                            <th scope="col" class="num">Steps</th>
                            <th scope="col" class="num">Took</th>
                            <th scope="col">Summary</th>
                          </tr>
                        </thead>
                        <tbody>
                          ${verdicts.map(
                            (v) => html`
                              <tr>
                                <td>${v.workflow}</td>
                                <td style="color:var(--ink-3)">${v.persona ?? 'anybody'}</td>
                                <td>${chip(v.value)}</td>
                                <td class="num">${v.steps}</td>
                                <td class="num">${v.duration_ms === null ? '—' : `${(v.duration_ms / 1000).toFixed(1)}s`}</td>
                                <td style="color:var(--ink-3)">${v.summary ?? '—'}</td>
                              </tr>
                            `,
                          )}
                        </tbody>
                      </table>
                    </div>
                  `
            }
          </div>

          <div class="panel">
            <div class="panel-head"><h2>Artifacts</h2></div>
            ${
              artifacts.length === 0
                ? empty(
                    'No artifacts',
                    'Videos, traces and reproduction steps are recorded here. A run with none produced no evidence, which is worth knowing on its own.',
                  )
                : html`
                    <div class="scroll-x">
                      <table>
                        <thead>
                          <tr>
                            <th scope="col" class="num">Step</th>
                            <th scope="col">Kind</th><th scope="col">Stored at</th>
                            <th scope="col" class="num">Size</th>
                            <th scope="col">Recorded</th>
                          </tr>
                        </thead>
                        <tbody>
                          ${artifacts.map(
                            (a) => html`
                              <tr>
                                <td class="num">${a.step ?? '—'}</td>
                                <td>${chip(a.kind)}</td>
                                <td class="mono">${a.storage_key}</td>
                                <td class="num">${
                                  a.retained
                                    ? bytes(a.size_bytes)
                                    : html`<span style="color:var(--ink-3)">not retained</span>`
                                }</td>
                                <td class="when">${when(a.created_at)}</td>
                              </tr>
                            `,
                          )}
                        </tbody>
                      </table>
                    </div>
                  `
            }
          </div>
        </div>
      `,
    )
  })
}

// ---------------------------------------------------------------------------
// Masking attestations
// ---------------------------------------------------------------------------

export async function maskingPage(pool: Pool, viewer: Viewer, orgId: string): Promise<Html> {
  // The columns are the ones golden_versions actually has. It had none of the
  // ones this query first named -- no state, no verified_at, no row_count --
  // so the page answered 500 for every organization, including one with no
  // goldens at all, where it should have rendered its empty state.
  //
  // What replaced them comes out of the attestation itself, which is the point
  // of the page: how many tables and columns a scanner read back, when it
  // finished, and how many findings it signed for. Read in SQL rather than
  // parsed here, because jsonb is what the column is and a hand-written cast on
  // the way out is another chance to be wrong about a shape.
  const goldens = await pool.withTenant({ orgId }, async (db) =>
    db.execute<{
      version: string
      verified: boolean
      finished_at: string | null
      tables: number | null
      columns: number | null
      rows_sampled: string | null
      findings: number | null
      scanner: string | null
      created_at: Date | string
    }>(sql`
      SELECT version, verified, created_at,
             attestation #>> '{report,finished_at}'                  AS finished_at,
             (attestation #>> '{report,tables}')::int                AS tables,
             (attestation #>> '{report,columns}')::int               AS columns,
             attestation #>> '{report,rows_sampled}'                 AS rows_sampled,
             coalesce(jsonb_array_length(attestation #> '{report,findings}'), 0) AS findings,
             attestation #>> '{report,scanner}'                      AS scanner
      FROM golden_versions ORDER BY created_at DESC LIMIT 60`),
  )

  const verified = goldens.filter((g) => g.verified).length

  return page(
    { title: 'Masking', current: 'masking', viewer, environmentLabel: 'staging' },
    html`
      <div class="page">
        <div class="page-head">
          <div class="eyebrow">Evidence</div>
          <h1>Masking attestations</h1>
          <p>
            Every golden, and whether a scanner read back every column of every table and
            signed for it. A golden that is not verified cannot be branched from, and that
            is enforced in code rather than in a checklist.
          </p>
        </div>

        <div class="grid grid-3" style="margin-bottom:18px">
          <div class="panel stat">
            <div class="k">Goldens</div>
            <div class="v">${goldens.length}</div>
          </div>
          <div class="panel stat">
            <div class="k">Verified</div>
            <div class="v">${verified}</div>
            <div class="sub">signed by the scanner</div>
          </div>
          <div class="panel stat">
            <div class="k">Unverified</div>
            <div class="v">${goldens.length - verified}</div>
            <div class="sub">cannot be branched</div>
          </div>
        </div>

        <div class="panel">
          <div class="panel-head"><h2>History</h2></div>
          ${
            goldens.length === 0
              ? empty(
                  'No goldens yet',
                  'A golden is a masked copy of production data. One appears here after af golden refresh runs: copy, mask, verify, publish.',
                )
              : html`
                  <div class="scroll-x">
                    <table>
                      <thead>
                        <tr>
                          <th scope="col">Version</th><th scope="col">State</th>
                          <th scope="col" class="num">Tables</th>
                          <th scope="col" class="num">Columns</th>
                          <th scope="col" class="num">Rows read</th>
                          <th scope="col" class="num">Findings</th>
                          <th scope="col">Scanned</th><th scope="col">Created</th>
                        </tr>
                      </thead>
                      <tbody>
                        ${goldens.map(
                          (g) => html`
                            <tr>
                              <td class="mono">${g.version}</td>
                              <td>${chip(g.verified ? 'verified' : 'unverified')}</td>
                              <td class="num">${g.tables ?? '—'}</td>
                              <td class="num">${g.columns ?? '—'}</td>
                              <td class="num">${g.rows_sampled ?? '—'}</td>
                              <td class="num">${g.findings ?? '—'}</td>
                              <td class="when">${when(g.finished_at)}</td>
                              <td class="when">${when(g.created_at)}</td>
                            </tr>
                          `,
                        )}
                      </tbody>
                    </table>
                  </div>
                `
          }
        </div>
      </div>
    `,
  )
}

// ---------------------------------------------------------------------------
// Network decisions
// ---------------------------------------------------------------------------

export async function networkPage(pool: Pool, viewer: Viewer, orgId: string): Promise<Html> {
  const rules = await pool.withTenant({ orgId }, async (db) =>
    // network_rules holds `paths text[]`, not a path_prefix, and naming a column
    // that does not exist made this page a 500 rather than a page with nothing
    // on it. Methods are shown too: a rule that allows GET and a rule that
    // allows POST to the same host are different policies, and a table that
    // showed only the host would render them identically.
    db.execute<{
      host: string
      mode: string
      position: number
      paths: string[] | null
      methods: string[] | null
      repository: string | null
    }>(sql`
      SELECT n.host, n.mode::text AS mode, n.position, n.paths, n.methods,
             r.full_name AS repository
      FROM network_rules n LEFT JOIN repositories r ON r.id = n.repository_id
      ORDER BY n.position ASC LIMIT 200`),
  )

  return page(
    { title: 'Network', current: 'network', viewer, environmentLabel: 'staging' },
    html`
      <div class="page">
        <div class="page-head">
          <div class="eyebrow">Evidence</div>
          <h1>Network policy</h1>
          <p>
            The effective egress policy, in the order that decides. Every environment sits
            on a network with no route to the internet; the sidecar is the only thing on
            both, so these are enforcement rather than a request.
          </p>
        </div>

        <div class="panel">
          <div class="panel-head"><h2>${rules.length} rules</h2></div>
          ${
            rules.length === 0
              ? empty(
                  'No rules recorded',
                  'Rules come from the manifest in each repository. Until one is connected, the default applies: nothing leaves.',
                  html`<a class="btn" href="https://antifailure.dev/docs/concepts/egress">How egress works</a>`,
                )
              : html`
                  <div class="scroll-x">
                    <table>
                      <thead>
                        <tr>
                          <th scope="col" class="num">#</th><th scope="col">Host</th>
                          <th scope="col">Paths</th><th scope="col">Methods</th>
                          <th scope="col">Mode</th><th scope="col">Repository</th>
                        </tr>
                      </thead>
                      <tbody>
                        ${rules.map(
                          (r) => html`
                            <tr>
                              <td class="num">${r.position}</td>
                              <td class="mono">${r.host}</td>
                              <td class="mono" style="color:var(--ink-3)">${r.paths?.length ? r.paths.join(' ') : '*'}</td>
                              <td class="mono" style="color:var(--ink-3)">${r.methods?.length ? r.methods.join(' ') : 'any'}</td>
                              <td>${chip(r.mode.toLowerCase())}</td>
                              <td>${r.repository ?? html`<span style="color:var(--ink-3)">all</span>`}</td>
                            </tr>
                          `,
                        )}
                      </tbody>
                    </table>
                  </div>
                `
          }
        </div>
      </div>
    `,
  )
}

// ---------------------------------------------------------------------------
// Audit
// ---------------------------------------------------------------------------

export async function auditPage(pool: Pool, viewer: Viewer, orgId: string): Promise<Html> {
  const entries = await pool.withTenant({ orgId }, async (db) =>
    db.execute<{
      seq: string
      actor_label: string
      action: string
      target_type: string
      target_id: string | null
      origin: string
      occurred_at: Date | string
    }>(sql`
      SELECT seq, actor_label, action, target_type, target_id, origin, occurred_at
      FROM audit_entries ORDER BY seq DESC LIMIT 100`),
  )

  return page(
    { title: 'Audit log', current: 'audit', viewer, environmentLabel: 'staging' },
    html`
      <div class="page">
        <div class="page-head">
          <div class="eyebrow">Evidence</div>
          <h1>Audit log</h1>
          <p>
            Append only, and enforced by the database rather than by this application: the
            role serving this page has INSERT and SELECT on this table and nothing else.
            Each entry carries the hash of the one before it.
          </p>
        </div>
        <div class="panel">
          <div class="panel-head"><h2>${entries.length} most recent</h2></div>
          ${
            entries.length === 0
              ? empty('Nothing recorded yet', 'Actions that change something are written here as they happen.')
              : html`
                  <div class="scroll-x">
                    <table>
                      <thead>
                        <tr>
                          <th scope="col" class="num">#</th><th scope="col">Who</th>
                          <th scope="col">Action</th><th scope="col">Target</th>
                          <th scope="col">From</th><th scope="col">When</th>
                        </tr>
                      </thead>
                      <tbody>
                        ${entries.map(
                          (e) => html`
                            <tr>
                              <td class="num">${e.seq}</td>
                              <td>${e.actor_label}</td>
                              <td class="mono">${e.action}</td>
                              <td class="mono" style="color:var(--ink-3)">${e.target_id ?? e.target_type}</td>
                              <td>${chip(e.origin)}</td>
                              <td class="when">${when(e.occurred_at)}</td>
                            </tr>
                          `,
                        )}
                      </tbody>
                    </table>
                  </div>
                `
          }
        </div>
      </div>
    `,
  )
}

// ---------------------------------------------------------------------------
// Members
// ---------------------------------------------------------------------------

export async function membersPage(pool: Pool, viewer: Viewer, orgId: string): Promise<Html> {
  const members = await pool.withTenant({ orgId }, async (db) =>
    db.execute<{ github_login: string; name: string | null; role: string; source: string; created_at: Date | string }>(sql`
      SELECT u.github_login, u.name, m.role, m.source, m.created_at
      FROM members m JOIN users u ON u.id = m.user_id ORDER BY m.created_at ASC`),
  )

  return page(
    { title: 'Members', current: 'members', viewer, environmentLabel: 'staging' },
    html`
      <div class="page">
        <div class="page-head">
          <div class="eyebrow">Organization</div>
          <h1>Members</h1>
          <p>
            Membership follows GitHub. Somebody removed from the GitHub organization loses
            access on the next sync, and nobody has to remember to remove them here too.
          </p>
        </div>
        <div class="panel">
          <div class="panel-head"><h2>${members.length} members</h2></div>
          <div class="scroll-x">
            <table>
              <thead>
                <tr>
                  <th scope="col">Account</th><th scope="col">Name</th>
                  <th scope="col">Role</th><th scope="col">Source</th><th scope="col">Since</th>
                </tr>
              </thead>
              <tbody>
                ${members.map(
                  (m) => html`
                    <tr>
                      <td class="mono">${m.github_login}</td>
                      <td>${m.name ?? '—'}</td>
                      <td>${chip(m.role)}</td>
                      <td>${chip(m.source)}</td>
                      <td class="when">${when(m.created_at)}</td>
                    </tr>
                  `,
                )}
              </tbody>
            </table>
          </div>
        </div>
      </div>
    `,
  )
}

// ---------------------------------------------------------------------------
// Provider keys
// ---------------------------------------------------------------------------

/**
 * Bring your own key, and the budget that bounds it.
 *
 * The key is never rendered. It cannot be: nothing on this page reads the
 * ciphertext, and the three things it does show -- provider, last four,
 * fingerprint -- are the columns stored beside it precisely so that a screen
 * has no reason to ask for the secret.
 *
 * The budget is on the same page rather than behind a tab, because a key with
 * no cap is the shape of an unbounded bill on somebody else's card, and putting
 * the cap one click away is how it ends up unset.
 */
export function keysPage(input: {
  viewer: Viewer
  keys: { provider: string; last4: string; fingerprint: string; createdAt: Date; rotatedAt: Date | null }[]
  budgets: { provider: string; capUsd: number; spentUsd: number; remainingUsd: number; period: string }[]
  sealingConfigured: boolean
  /** Whether this viewer may change anything here. A member who cannot is
   *  shown the same state and no forms, rather than forms that would be
   *  refused: a control that cannot work is worse than no control. */
  mayManage: boolean
  notice?: { tone: 'ok' | 'bad' | 'warn'; title: string; body: string }
}): Html {
  const { viewer } = input
  const byProvider = (p: string) => input.keys.find((k) => k.provider === p) ?? null
  const budgetFor = (p: string) => input.budgets.find((b) => b.provider === p) ?? null

  const providers: { id: string; name: string; hint: string }[] = [
    { id: 'anthropic', name: 'Anthropic', hint: 'Starts with sk-ant-. Used for the agents that drive your workflows.' },
    { id: 'openai', name: 'OpenAI', hint: 'Starts with sk-. Used when a workflow names an OpenAI model.' },
  ]

  return page(
    { title: 'Provider keys', current: 'keys', viewer, environmentLabel: 'staging' },
    html`
      <div class="page">
        <div class="page-head">
          <div class="eyebrow">Organization</div>
          <h1>Provider keys</h1>
          <p>
            Your own Anthropic and OpenAI keys, sealed with a secret that is not in this
            database. They are never shown again after you save them, including to us: the
            last four characters and a fingerprint are all that any screen can read.
          </p>
        </div>

        ${
          input.notice
            ? html`<div class="notice ${input.notice.tone}">
                <span><strong>${input.notice.title}</strong>${input.notice.body}</span>
              </div>`
            : ''
        }

        ${
          input.mayManage
            ? ''
            : html`<div class="notice warn">
                <span>
                  <strong>You can see these, and you cannot change them</strong>
                  Storing, rotating and capping a key is for owners and admins. What is
                  shown here is a last four and a fingerprint, never a key, so it is safe
                  to read and enough to tell whether a run was refused for want of one.
                </span>
              </div>`
        }

        ${
          input.sealingConfigured
            ? ''
            : html`<div class="notice bad">
                <span>
                  <strong>Keys cannot be stored on this installation</strong>
                  AF_PROVIDER_KEY_SECRET is not set, so there is nothing to seal them with.
                  Until it is, saving a key is refused rather than stored in the clear.
                </span>
              </div>`
        }

        <div class="grid grid-2">
          ${providers.map((p) => {
            const key = byProvider(p.id)
            const budget = budgetFor(p.id)
            const spentPct = budget && budget.capUsd > 0 ? Math.min(100, (budget.spentUsd / budget.capUsd) * 100) : 0
            return html`
              <div class="panel">
                <div class="panel-head">
                  <h2>${p.name}</h2>
                  ${key ? chip('active') : html`<span class="chip neutral">not set</span>`}
                </div>
                <div class="panel-body">
                  ${
                    key
                      ? html`
                          <dl class="kv" style="margin-bottom:18px">
                            <dt>Key</dt>
                            <dd class="mono">••••••••${key.last4}</dd>
                            <dt>Fingerprint</dt>
                            <dd class="mono" style="color:var(--ink-3)">${key.fingerprint}</dd>
                            <dt>Stored</dt>
                            <dd>${when(key.createdAt)}</dd>
                            ${key.rotatedAt ? html`<dt>Rotated</dt><dd>${when(key.rotatedAt)}</dd>` : ''}
                          </dl>
                        `
                      : html`
                          <p class="hint" style="margin-bottom:18px">
                            No key stored. Runs that need ${p.name} are refused with a message
                            saying so, rather than falling back to a key of ours.
                          </p>
                        `
                  }

                  ${input.mayManage ? html`
                  <form method="post" action="/console/keys">
                    <input type="hidden" name="csrf" value="${viewer.csrfToken}" />
                    <input type="hidden" name="provider" value="${p.id}" />
                    <div class="field">
                      <label for="key-${p.id}">${key ? `Replace the ${p.name} key` : `${p.name} API key`}</label>
                      <input
                        id="key-${p.id}"
                        name="key"
                        type="password"
                        autocomplete="off"
                        spellcheck="false"
                        placeholder="${key ? 'Paste a new key to rotate' : 'Paste your key'}"
                      />
                      <p class="hint">${p.hint}</p>
                    </div>
                    <div class="row">
                      <button class="btn btn-primary" type="submit" name="action" value="save"
                        ${input.sealingConfigured ? raw('') : raw('disabled')}>
                        ${key ? 'Rotate' : 'Save'}
                      </button>
                      ${
                        key
                          ? html`<button class="btn btn-danger" type="submit" name="action" value="revoke">
                              Remove
                            </button>`
                          : ''
                      }
                    </div>
                  </form>` : ''}
                </div>

                <div class="panel-head" style="border-top:1px solid var(--hairline-soft);border-bottom:0">
                  <h2>Monthly budget</h2>
                  <span class="chip neutral">${budget ? budget.period.slice(0, 7) : 'not set'}</span>
                </div>
                <div class="panel-body">
                  ${
                    budget
                      ? html`
                          <div class="stat" style="padding:0 0 12px">
                            <div class="v">${budget.spentUsd.toFixed(2)}<span style="color:var(--ink-3);font-size:15px;font-weight:500"> of ${budget.capUsd.toFixed(2)} USD</span></div>
                            <div class="sub">${budget.remainingUsd.toFixed(2)} USD left this month</div>
                          </div>
                          <div style="height:6px;border-radius:3px;background:var(--surface-sunk);overflow:hidden">
                            <div style="height:100%;width:${spentPct.toFixed(1)}%;background:${spentPct >= 100 ? 'var(--danger)' : 'var(--accent)'}"></div>
                          </div>
                        `
                      : html`
                          <p class="hint" style="margin-bottom:14px">
                            No budget, and that means nothing may be spent. A missing cap is read
                            as zero rather than as unlimited, because the alternative on somebody
                            else's key is an unbounded bill.
                          </p>
                        `
                  }
                  ${input.mayManage ? html`
                  <form method="post" action="/console/keys" class="row" style="margin-top:14px">
                    <input type="hidden" name="csrf" value="${viewer.csrfToken}" />
                    <input type="hidden" name="provider" value="${p.id}" />
                    <input type="hidden" name="action" value="budget" />
                    <div class="field" style="flex:1;margin-bottom:0">
                      <label for="cap-${p.id}">Cap, USD per month</label>
                      <input id="cap-${p.id}" name="cap" type="number" min="0" step="1"
                             value="${budget ? String(budget.capUsd) : ''}" placeholder="50" />
                    </div>
                    <button class="btn" type="submit">Set</button>
                  </form>` : ''}
                </div>
              </div>
            `
          })}
        </div>

        <div class="panel">
          <div class="panel-head"><h2>What happens to a key here</h2></div>
          <div class="panel-body">
            <p style="color:var(--ink-2)">
              It is sealed with AES-256-GCM under a secret held outside this database, bound
              to this organization and this provider. A row copied to another tenant does not
              open. A row edited by one bit does not open.
            </p>
            <p style="color:var(--ink-2)">
              The only code that ever holds the plaintext is the code putting it in a request
              to the provider. It is not in an event, an artifact, a log line, or a support
              bundle, and the budget above is checked before it is decrypted, so a run with no
              allowance never causes the key to exist in memory at all.
            </p>
            <p style="color:var(--ink-2)">
              Rotating stores the new key and revokes the old one in the same transaction. The
              old fingerprint stays in the audit log so it is possible to say which key was in
              use when, without either key being readable.
            </p>
            <p style="color:var(--ink-2)">
              The same thing is reachable from a terminal with
              <span class="mono">af provider</span>, which needs a token that asked for the
              scope: <span class="mono">af login --scope providers.write</span>. There is no
              command, and no scope, that reads a key back.
            </p>
          </div>
        </div>
      </div>
    `,
  )
}
