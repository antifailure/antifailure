import { serve } from '@hono/node-server'
import postgres from 'postgres'
import { createAdminPool, createPool, migrate } from '@antifailure/db'
import { createServer } from './src/server.ts'
import { systemClock } from './src/clock.ts'
import { FakeGitHub } from './src/auth/fakegithub.ts'
import { hashPassword } from './src/admin/session.ts'
import { findConsoleBuild } from './src/console/static.ts'
const url = process.env.AF_TEST_DATABASE_URL!
const port = Number(process.env.PORT ?? 8099)
const admin = postgres(url, { max: 4, onnotice: () => {} })
await migrate(admin)
await admin.unsafe(`ALTER ROLE antifailure_app LOGIN PASSWORD 'app-test-password'`)
await admin.unsafe(`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='antifailure_admin') THEN CREATE ROLE antifailure_admin NOLOGIN BYPASSRLS; ELSE ALTER ROLE antifailure_admin BYPASSRLS; END IF; END $$; ALTER ROLE antifailure_admin LOGIN PASSWORD 'admin-test-password';`)
const email = 'proof@antifailure.test'
const { hash, salt } = await hashPassword('proof-password-not-a-real-one')
await admin`DELETE FROM admin_users WHERE email = ${email}`
await admin`INSERT INTO admin_users (email, name, role, password_hash, password_salt, password_set_at) VALUES (${email}, 'Dana Okonkwo', 'owner', ${hash}, ${salt}, now())`
const u = new URL(url); u.username='antifailure_admin'; u.password='admin-test-password'
const a = new URL(url); a.username='antifailure_app'; a.password='app-test-password'
const { app } = createServer({
  pool: createPool({ url: a.toString(), max: 6 }),
  adminPool: createAdminPool({ url: u.toString(), max: 3 }),
  github: new FakeGitHub(systemClock), clock: systemClock,
  secureCookies: false, appBaseUrl: `http://localhost:${port}/`,
  signInAllowlist: null, sealingKey: null, githubWebhookSecret: null,
  consoleBuild: await findConsoleBuild(process.env.AF_CONSOLE_DIR),
  stripe: null, hostedRequiredPlan: null, operatorSetsPlan: false, githubApi: null,
})
serve({ fetch: app.fetch, port }); console.log('ready')
