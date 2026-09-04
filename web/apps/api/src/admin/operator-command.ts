import { bootstrapOperator, type BootstrapOperatorInput, type BootstrapOperatorResult } from './bootstrap.ts'
import { operatorInput } from './operator-input.ts'

export interface OperatorCommandOptions {
  adminUrl?: string
  publicUrl?: string
  email?: string
  name?: string
  password?: string
  prompt?: typeof operatorInput
  bootstrap?: (input: BootstrapOperatorInput) => Promise<BootstrapOperatorResult>
}

/** Uses the credential already present in the runtime, never a URL argument. */
export async function initializeOperator(options: OperatorCommandOptions): Promise<string> {
  if (!options.adminUrl) {
    throw new Error('This runtime has no AF_ADMIN_DATABASE_URL. Enable its operator database credential before creating an operator.')
  }
  let destination = '/admin'
  if (options.publicUrl) {
    try {
      const url = new URL(options.publicUrl)
      if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password) throw new Error()
      destination = new URL('/admin', url).href
    } catch {
      throw new Error('AF_PUBLIC_URL must be an HTTP or HTTPS address without credentials. Nothing was written.')
    }
  }
  const prompt = options.prompt ?? operatorInput
  const email = options.email ?? await prompt('Operator email: ', false)
  const name = options.name ?? await prompt('Display name: ', false)
  const password = options.password ?? await prompt('Password, at least 12 characters: ', true)
  if (options.password === undefined) {
    const confirmation = await prompt('Confirm password: ', true)
    if (password !== confirmation) throw new Error('The passwords did not match. Nothing was written.')
  }
  await (options.bootstrap ?? bootstrapOperator)({
    adminUrl: options.adminUrl,
    email,
    name,
    password,
    operator: 'operator init',
  })
  return `The first operator is ready. Sign in at ${destination}`
}
