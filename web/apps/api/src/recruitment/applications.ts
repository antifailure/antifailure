import { createHash } from 'node:crypto'
import { z } from 'zod'
import { sql } from 'drizzle-orm'
import type { Pool } from '@antifailure/db'
import type { Clock } from '../clock.ts'

export const applicationInput = z.object({
  submissionId: z.string().uuid(),
  name: z.string().trim().min(1, 'Tell us your name.').max(200),
  email: z.string().trim().toLowerCase().email('Enter a valid email address.').max(320),
  // With a message, because the route answers the first issue's message
  // straight to the applicant and zod's default here names the two internal
  // identifiers rather than telling somebody what to do about it.
  role: z.enum(['founding_engineer', 'founding_growth'], { error: 'Choose which role you are applying for.' }),
  projectUrl: z.string().trim().max(2000).refine((value) => {
    if (!value) return true
    try {
      const url = new URL(value)
      return (url.protocol === 'https:' || url.protocol === 'http:') && !url.username && !url.password
    } catch { return false }
  }, 'Use a public http or https link without credentials.'),
  why: z.string().trim().min(1, 'Tell us about your work and why this role.').max(4000),
  compensationAcknowledged: z.literal(true, { error: 'Confirm that you understand the current compensation.' }),
  website: z.literal('', { error: 'The hidden website field must be empty.' }).default(''),
}).strict()

export type ApplicationInput = z.infer<typeof applicationInput>

export async function recordApplication(pool: Pool, clock: Clock, input: ApplicationInput) {
  // Identical retries get one key. A changed payload gets a different key, so
  // a conflict never acknowledges answers that were not actually recorded.
  const digest = createHash('sha256').update(JSON.stringify([
    input.submissionId, input.name, input.email, input.role, input.projectUrl, input.why,
    input.compensationAcknowledged,
  ])).digest('hex')
  const id = `${digest.slice(0, 8)}-${digest.slice(8, 12)}-4${digest.slice(13, 16)}-a${digest.slice(17, 20)}-${digest.slice(20, 32)}`
  await pool.withoutTenant((db) => db.execute(sql`
    INSERT INTO recruitment_applications
      (id, name, email, role, project_url, why, compensation_acknowledged, created_at)
    VALUES (${id}::uuid, ${input.name}, ${input.email}, ${input.role}, ${input.projectUrl},
            ${input.why}, true, ${clock.now().toISOString()}::timestamptz)
    ON CONFLICT DO NOTHING`))
  return { id, submissionId: input.submissionId, recorded: true as const }
}
