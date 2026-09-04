#!/usr/bin/env node
import { parseArgs } from 'node:util'
import { initializeOperator } from './admin/operator-command.ts'

try {
  const { values, positionals } = parseArgs({
    allowPositionals: true,
    options: { email: { type: 'string' }, name: { type: 'string' }, 'completion-token': { type: 'string' } },
  })
  if (positionals.length !== 1 || positionals[0] !== 'init' || Boolean(values.email) !== Boolean(values.name)) {
    throw new Error('Run af-operator init. For automation, supply both email and name and pipe the password on standard input.')
  }
  if (values['completion-token'] && !/^[0-9a-f-]{36}$/.test(values['completion-token'])) {
    throw new Error('The completion token is not a valid setup token.')
  }
  let password: string | undefined
  if (values.email !== undefined) {
    if (process.stdin.isTTY) throw new Error('Automation reads the password from standard input, never an argument. Omit email and name for interactive setup.')
    const chunks: Buffer[] = []
    let size = 0
    for await (const chunk of process.stdin) {
      const bytes = Buffer.from(chunk)
      size += bytes.length
      if (size > 2048) throw new Error('The password input exceeds the 2048 byte limit.')
      chunks.push(bytes)
    }
    password = Buffer.concat(chunks).toString('utf8').replace(/\r?\n$/, '')
  }
  console.log(await initializeOperator({
    adminUrl: process.env.AF_ADMIN_DATABASE_URL,
    publicUrl: process.env.AF_PUBLIC_URL,
    email: values.email,
    name: values.name,
    password,
  }))
  if (values['completion-token']) console.log(`operator-init-complete:${values['completion-token']}`)
} catch (error) {
  // Argument errors may contain the supplied value. Do not echo them.
  if (error instanceof TypeError && 'code' in error && String(error.code).startsWith('ERR_PARSE_ARGS')) {
    console.error('Unrecognized arguments. Run af-operator init; a password is never a command argument.')
  } else {
    console.error(error instanceof Error ? error.message : 'Operator setup failed.')
  }
  process.exitCode = 1
}
